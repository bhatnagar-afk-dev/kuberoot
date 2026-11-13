//go:build integration

package catalog

import (
	"context"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/kubeconfig"
	"kuberoot.in/kuberoot/internal/resources"
)

const itTimeout = 60 * time.Second

var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

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

// eventually polls cond until it is true or the test times out.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(itTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestIntegrationCatalogSeesNewCRD(t *testing.T) {
	s := connect(t)
	ctx := context.Background()
	c, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResourceTypes(ctx); err != nil {
		t.Fatal(err)
	}

	group := "c" + rand.String(6) + ".it.kuberoot.in"
	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "gadgets." + group},
		"spec": map[string]any{
			"group": group,
			"scope": "Namespaced",
			"names": map[string]any{"plural": "gadgets", "singular": "gadget", "kind": "Gadget"},
			"versions": []any{map[string]any{
				"name": "v1", "served": true, "storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					"type": "object", "x-kubernetes-preserve-unknown-fields": true,
				}},
			}},
		},
	}}
	if _, err := s.Dynamic.Resource(crdGVR).Create(ctx, crd, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			_ = s.Dynamic.Resource(crdGVR).Delete(context.Background(), "gadgets."+group, metav1.DeleteOptions{})
		}
	})

	eventually(t, "Changed after creating a CRD", func() bool {
		changed, err := c.Changed(ctx)
		return err == nil && changed
	})
	types, err := c.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(types, func(rt resources.ResourceType) bool { return rt.Group == group && rt.Resource == "gadgets" }) {
		t.Fatalf("Refresh did not list %s/gadgets", group)
	}
	if changed, err := c.Changed(ctx); err != nil || changed {
		t.Errorf("Changed right after Refresh = %v, %v; want false", changed, err)
	}

	if err := s.Dynamic.Resource(crdGVR).Delete(ctx, "gadgets."+group, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	deleted = true
	eventually(t, "Changed after deleting the CRD", func() bool {
		changed, err := c.Changed(ctx)
		return err == nil && changed
	})
}
