//go:build integration

package view

import (
	"context"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/kubeconfig"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
)

const itTimeout = 60 * time.Second

// chanSink delivers a view's output to channels.
type chanSink struct {
	batches  chan resources.RowBatch
	statuses chan resources.ViewStatus
}

func newChanSink() *chanSink {
	return &chanSink{batches: make(chan resources.RowBatch, 100), statuses: make(chan resources.ViewStatus, 100)}
}

func (s *chanSink) Send(b resources.RowBatch)      { s.batches <- b }
func (s *chanSink) Status(st resources.ViewStatus) { s.statuses <- st }
func (s *chanSink) nextBatch(t *testing.T) resources.RowBatch {
	t.Helper()
	select {
	case b := <-s.batches:
		return b
	case <-time.After(itTimeout):
		t.Fatal("no batch received")
		return resources.RowBatch{}
	}
}

func (s *chanSink) waitStatus(t *testing.T, phase resources.ViewPhase) resources.ViewStatus {
	t.Helper()
	deadline := time.After(itTimeout)
	for {
		select {
		case st := <-s.statuses:
			if st.Phase == phase {
				return st
			}
		case <-deadline:
			t.Fatalf("view never reached phase %s", phase)
		}
	}
}

// waitBatch returns the first batch that satisfies ok.
func (s *chanSink) waitBatch(t *testing.T, what string, ok func(resources.RowBatch) bool) resources.RowBatch {
	t.Helper()
	deadline := time.After(itTimeout)
	for {
		select {
		case b := <-s.batches:
			if ok(b) {
				return b
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

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

func namespace(t *testing.T, kc kubernetes.Interface) string {
	t.Helper()
	ns := "kuberoot-it-" + rand.String(6)
	ctx := context.Background()
	if _, err := kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kc.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })
	return ns
}

var (
	crdGVR    = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	widgetGVR = schema.GroupVersionResource{Group: "it.kuberoot.in", Version: "v1", Resource: "widgets"}
	widgets   = resources.ResourceType{Group: "it.kuberoot.in", Version: "v1", Resource: "widgets", Kind: "Widget", Namespaced: true}
)

func installWidgetCRD(t *testing.T, s cluster.Session) {
	t.Helper()
	ctx := context.Background()
	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "widgets.it.kuberoot.in"},
		"spec": map[string]any{
			"group": "it.kuberoot.in",
			"scope": "Namespaced",
			"names": map[string]any{"plural": "widgets", "singular": "widget", "kind": "Widget"},
			"versions": []any{map[string]any{
				"name": "v1", "served": true, "storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					"type": "object",
					"properties": map[string]any{"spec": map[string]any{
						"type": "object", "properties": map[string]any{"color": map[string]any{"type": "string"}},
					}},
				}},
				"additionalPrinterColumns": []any{map[string]any{"name": "Color", "type": "string", "jsonPath": ".spec.color"}},
			}},
		},
	}}
	_, err := s.Dynamic.Resource(crdGVR).Create(ctx, crd, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	deadline := time.Now().Add(itTimeout)
	for {
		got, err := s.Dynamic.Resource(crdGVR).Get(ctx, "widgets.it.kuberoot.in", metav1.GetOptions{})
		if err == nil && established(got) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("CRD not established")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func established(crd *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(crd.Object, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == "Established" && m["status"] == "True" {
			return true
		}
	}
	return false
}

func widget(ns, name, color string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "it.kuberoot.in/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"color": color},
	}}
}

func cell(b resources.RowBatch, cols []resources.Column, name, column string) any {
	i := slices.IndexFunc(cols, func(c resources.Column) bool { return c.Name == column })
	for _, r := range b.Upserts {
		if r.Name == name && i >= 0 && i < len(r.Cells) {
			return r.Cells[i]
		}
	}
	return nil
}

// TestIntegrationCRDView opens a live view of a custom resource and checks
// its printer column, updates, and deletes against a real API server.
func TestIntegrationCRDView(t *testing.T) {
	s := connect(t)
	kc := kubernetes.NewForConfigOrDie(s.Config)
	ns := namespace(t, kc)
	installWidgetCRD(t, s)
	ctx := context.Background()
	w := s.Dynamic.Resource(widgetGVR).Namespace(ns)
	if _, err := w.Create(ctx, widget(ns, "w1", "red"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	informers := cache.New(cache.Options{})
	defer informers.Close()
	sink := newChanSink()
	v, err := Open(s, informers, Request{ViewID: "it", Type: widgets, Namespaces: []string{ns}}, sink, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	sink.waitStatus(t, resources.ViewReady)
	reset := sink.nextBatch(t)
	if !reset.Reset || len(reset.Upserts) != 1 {
		t.Fatalf("reset = %+v", reset)
	}
	cols := reset.Columns
	if got := cell(reset, cols, "w1", "Color"); got != "red" {
		t.Fatalf("Color of w1 = %v; columns %+v", got, cols)
	}
	if r := reset.Upserts[0]; r.UID == "" || r.Namespace != ns || r.Created.IsZero() {
		t.Errorf("row = %+v", r)
	}
	t.Logf("columns: %+v", cols)

	if _, err := w.Create(ctx, widget(ns, "w2", "green"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	sink.waitBatch(t, "w2 added", func(b resources.RowBatch) bool { return cell(b, cols, "w2", "Color") == "green" })

	patch := []byte(`{"spec":{"color":"blue"}}`)
	if _, err := w.Patch(ctx, "w1", types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	sink.waitBatch(t, "w1 updated", func(b resources.RowBatch) bool { return cell(b, cols, "w1", "Color") == "blue" })

	w2, _ := w.Get(ctx, "w2", metav1.GetOptions{})
	if err := w.Delete(ctx, "w2", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	sink.waitBatch(t, "w2 deleted", func(b resources.RowBatch) bool { return slices.Contains(b.Deletes, string(w2.GetUID())) })
}

// TestIntegrationPartialForbidden lists configmaps as a ServiceAccount that
// may read only one of the two requested namespaces.
func TestIntegrationPartialForbidden(t *testing.T) {
	admin := connect(t)
	kc := kubernetes.NewForConfigOrDie(admin.Config)
	ns := namespace(t, kc)
	ctx := context.Background()

	if _, err := kc.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "reader"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.RbacV1().Roles(ns).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "reader"},
		Rules:      []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"list", "watch"}}},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "reader"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "reader"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "reader", Namespace: ns}},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	tok, err := kc.CoreV1().ServiceAccounts(ns).CreateToken(ctx, "reader", &authnv1.TokenRequest{}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	cfg := rest.AnonymousClientConfig(admin.Config)
	cfg.BearerToken = tok.Status.Token
	s := cluster.Session{ContextID: "reader", Ctx: admin.Ctx, Config: cfg}

	informers := cache.New(cache.Options{})
	defer informers.Close()
	sink := newChanSink()
	configmaps := resources.ResourceType{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
	v, err := Open(s, informers, Request{ViewID: "it", Type: configmaps, Namespaces: []string{ns, "kube-system"}}, sink, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	st := sink.waitStatus(t, resources.ViewReady)
	if !slices.Equal(st.Forbidden, []string{"kube-system"}) {
		t.Fatalf("status = %+v, want kube-system forbidden", st)
	}
	reset := sink.nextBatch(t)
	// Every namespace has the kube-root-ca.crt configmap.
	if !reset.Reset || len(reset.Upserts) == 0 || reset.Upserts[0].Namespace != ns {
		t.Fatalf("reset = %+v, want rows from %s only", reset, ns)
	}
	t.Logf("message: %s", st.Message)
}
