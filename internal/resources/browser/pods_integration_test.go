//go:build integration

package browser

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/view"
	"kuberoot.in/kuberoot/internal/resources/workload"
)

const pauseImage = "registry.k8s.io/pause:3.9"

func deployment(name string, replicas int32, lbls map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: lbls},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: lbls},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: pauseImage}}},
			},
		},
	}
}

// rowSet mirrors a view's rows by name, from its batches.
type rowSet map[string]bool

func (r rowSet) apply(b resources.RowBatch, uidName map[string]string) {
	if b.Reset {
		clear(r)
	}
	for _, uid := range b.Deletes {
		delete(r, uidName[uid])
	}
	for _, row := range b.Upserts {
		uidName[row.UID] = row.Name
		r[row.Name] = true
	}
}

// watchRows applies batches until done reports true, failing if check ever
// reports a problem.
func watchRows(t *testing.T, batches chanSink, check func(rowSet) string, done func(rowSet) bool) rowSet {
	t.Helper()
	rows, uids := rowSet{}, map[string]string{}
	deadline := time.After(itTimeout)
	for {
		select {
		case b := <-batches:
			rows.apply(b, uids)
			if msg := check(rows); msg != "" {
				t.Fatalf("%s; rows = %v", msg, rows)
			}
			if done(rows) {
				return rows
			}
		case <-deadline:
			t.Fatalf("timed out; rows = %v", rows)
		}
	}
}

func count(rows rowSet, prefix string) int {
	n := 0
	for name := range rows {
		if strings.HasPrefix(name, prefix) {
			n++
		}
	}
	return n
}

func TestIntegrationPods(t *testing.T) {
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

	// web's selector (app=web) also matches web-canary's pods and a
	// hand-made pod. Only a Service on app=web may show them all.
	web, err := kc.AppsV1().Deployments(ns).Create(ctx, deployment("web", 2, map[string]string{"app": "web"}), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kc.AppsV1().Deployments(ns).Create(ctx, deployment("web-canary", 1, map[string]string{"app": "web", "track": "canary"}), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "handmade", Labels: map[string]string{"app": "web"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: pauseImage}}},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.CoreV1().Services(ns).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "web"},
			Ports:    []corev1.ServicePort{{Port: 80}},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	informers := cache.New(cache.Options{})
	t.Cleanup(informers.Close)
	sinks := map[string]chanSink{"svc": make(chanSink, 100), "web": make(chanSink, 100)}
	b := New(sessionConns{s}, informers, workload.Matcher{Subscriber: informers},
		func(r resources.ViewRequest) resources.Sink { return sinks[r.ViewID] }, view.Options{})
	t.Cleanup(b.CloseAll)

	services := resources.ResourceType{Version: "v1", Resource: "services", Kind: "Service", Namespaced: true}
	deployments := resources.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}

	t.Run("service shows every selected pod", func(t *testing.T) {
		if err := b.OpenPods("svc", resources.ObjectRef{ContextID: s.ContextID, Type: services, Namespace: ns, Name: "web"}); err != nil {
			t.Fatal(err)
		}
		watchRows(t, sinks["svc"], func(rowSet) string { return "" }, func(r rowSet) bool {
			return count(r, "web-canary-") == 1 && r["handmade"] && len(r) == 4
		})
	})

	foreign := func(r rowSet) string {
		if count(r, "web-canary-") > 0 || r["handmade"] {
			return "the deployment shows a pod it does not own"
		}
		return ""
	}

	t.Run("deployment shows only its own pods", func(t *testing.T) {
		if err := b.OpenPods("web", resources.ObjectRef{ContextID: s.ContextID, Type: deployments, Namespace: ns, Name: "web", UID: string(web.UID)}); err != nil {
			t.Fatal(err)
		}
		rows := watchRows(t, sinks["web"], foreign, func(r rowSet) bool { return len(r) == 2 })

		// A rollout creates a second ReplicaSet; its pods must appear too.
		before := map[string]bool{}
		for name := range rows {
			before[templateHash(name)] = true
		}
		patch := []byte(`{"spec":{"template":{"metadata":{"annotations":{"kuberoot.in/restart":"1"}}}}}`)
		if _, err := kc.AppsV1().Deployments(ns).Patch(ctx, "web", types.StrategicMergePatchType, patch, metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
		watchRows(t, sinks["web"], foreign, func(r rowSet) bool {
			for name := range r {
				if !before[templateHash(name)] {
					return true
				}
			}
			return false
		})
	})

	t.Run("service without selector", func(t *testing.T) {
		if _, err := kc.CoreV1().Services(ns).Create(ctx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "manual"},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		err := b.OpenPods("manual", resources.ObjectRef{ContextID: s.ContextID, Type: services, Namespace: ns, Name: "manual"})
		if err == nil || err.Error() != "this service does not select pods" {
			t.Errorf("err = %v", err)
		}
	})
}

// templateHash returns the pod-template-hash part of a Deployment pod's
// name ("web-<hash>-<suffix>").
func templateHash(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return ""
	}
	return parts[len(parts)-2]
}
