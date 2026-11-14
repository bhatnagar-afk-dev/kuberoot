//go:build integration

package browser

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/kubeconfig"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/describe/kubectl"
	"kuberoot.in/kuberoot/internal/resources/object"
	"kuberoot.in/kuberoot/internal/resources/view"
)

const itTimeout = 60 * time.Second

func connect(t *testing.T) cluster.Session {
	t.Helper()
	name := os.Getenv("KUBEROOT_IT_CONTEXT")
	if name == "" {
		t.Skip("KUBEROOT_IT_CONTEXT not set")
	}
	home, _ := os.UserHomeDir()
	st := kubeconfig.NewStore("kuberoot-integration")
	st.SetSources(kubeconfig.DefaultSources(os.Getenv("KUBECONFIG"), home, runtime.GOOS))
	var id string
	for _, c := range st.Contexts() {
		if c.Name == name {
			id = c.ID
		}
	}
	if id == "" {
		t.Fatalf("context %q not found", name)
	}
	m := cluster.NewManager(st, cluster.Options{ConnectTimeout: 30 * time.Second})
	t.Cleanup(m.Close)
	if s, err := m.Connect(context.Background(), id); err != nil {
		t.Fatalf("Connect: %v (%+v)", err, s)
	}
	s, err := m.Session(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// sessionConns always returns one session.
type sessionConns struct{ s cluster.Session }

func (c sessionConns) Session(string) (cluster.Session, error) { return c.s, nil }

// chanSink delivers row batches to a channel.
type chanSink chan resources.RowBatch

func (c chanSink) Send(b resources.RowBatch) { c <- b }
func (chanSink) Status(resources.ViewStatus) {}

func waitRow(t *testing.T, batches chanSink, name string) {
	t.Helper()
	deadline := time.After(itTimeout)
	for {
		select {
		case b := <-batches:
			for _, r := range b.Upserts {
				if r.Name == name {
					return
				}
			}
		case <-deadline:
			t.Fatalf("event %s never arrived", name)
		}
	}
}

func event(ns, name, reason string, about corev1.ObjectReference) *corev1.Event {
	now := metav1.Now()
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: ns},
		InvolvedObject: about,
		Reason:         reason,
		Message:        "integration test",
		Type:           corev1.EventTypeNormal,
		Source:         corev1.EventSource{Component: "kuberoot-it"},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
	}
}

func TestIntegrationObjectDetails(t *testing.T) {
	s := connect(t)
	kc, err := kubernetes.NewForConfig(s.Config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ns := "kuberoot-it-" + rand.String(6)
	if _, err := kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kc.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })

	cm, err := kc.CoreV1().ConfigMaps(ns).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "settings"}, Data: map[string]string{"mode": "fast"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kc.CoreV1().Secrets(ns).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db"}, Data: map[string][]byte{"password": []byte("hunter2")},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	about := corev1.ObjectReference{Kind: "ConfigMap", APIVersion: "v1", Namespace: ns, Name: cm.Name, UID: cm.UID}
	other := corev1.ObjectReference{Kind: "ConfigMap", APIVersion: "v1", Namespace: ns, Name: "other", UID: "other-uid"}
	for _, e := range []*corev1.Event{event(ns, "first", "Created", about), event(ns, "unrelated", "Other", other)} {
		if _, err := kc.CoreV1().Events(ns).Create(ctx, e, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	cmType := resources.ResourceType{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
	ref := resources.ObjectRef{ContextID: s.ContextID, Type: cmType, Namespace: ns, Name: cm.Name, UID: string(cm.UID)}

	t.Run("events", func(t *testing.T) {
		informers := cache.New(cache.Options{})
		t.Cleanup(informers.Close)
		batches := make(chanSink, 100)
		b := New(sessionConns{s}, informers, func(resources.ViewRequest) resources.Sink { return batches }, view.Options{})
		t.Cleanup(b.CloseAll)
		if err := b.OpenEvents("e1", ref); err != nil {
			t.Fatal(err)
		}
		reset := <-batches
		if !reset.Reset || len(reset.Upserts) != 1 || reset.Upserts[0].Name != "first" {
			t.Fatalf("reset = %+v, want only the configmap's event", reset)
		}
		if _, err := kc.CoreV1().Events(ns).Create(ctx, event(ns, "second", "Updated", about), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		waitRow(t, batches, "second")
	})

	objects := NewObjects(sessionConns{s}, kubectl.Describer{}, object.Reader{})
	t.Run("describe", func(t *testing.T) {
		out, err := objects.Describe(ref)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"settings", "mode", "fast", "Created"} {
			if !strings.Contains(out, want) {
				t.Errorf("describe output lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("yaml", func(t *testing.T) {
		secret := resources.ObjectRef{ContextID: s.ContextID, Namespace: ns, Name: "db",
			Type: resources.ResourceType{Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true}}
		out, err := objects.YAML(secret, resources.YAMLOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "password: '<hidden: 7 bytes>'") || strings.Contains(out, "aHVudGVyMg") || strings.Contains(out, "managedFields") {
			t.Errorf("masked YAML:\n%s", out)
		}
		out, err = objects.YAML(secret, resources.YAMLOptions{RevealSecrets: true, ManagedFields: true})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "aHVudGVyMg==") || !strings.Contains(out, "managedFields") {
			t.Errorf("revealed YAML:\n%s", out)
		}
	})
}
