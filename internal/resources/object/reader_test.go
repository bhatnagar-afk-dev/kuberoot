package object

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	secrets = resources.ResourceType{Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true}
	cms     = resources.ResourceType{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
	nodes   = resources.ResourceType{Version: "v1", Resource: "nodes", Kind: "Node"}
)

func obj(kind, ns, name string, fields map[string]any) *unstructured.Unstructured {
	meta := map[string]any{
		"name":              name,
		"uid":               "uid-" + name,
		"resourceVersion":   "7",
		"creationTimestamp": "2026-11-01T10:00:00Z",
		"annotations":       map[string]any{"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"password":"aHVudGVyMg=="}}`},
		"managedFields":     []any{map[string]any{"manager": "kubectl", "operation": "Apply"}},
	}
	if ns != "" {
		meta["namespace"] = ns
	}
	o := map[string]any{"apiVersion": "v1", "kind": kind, "metadata": meta}
	for k, v := range fields {
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

func secret() *unstructured.Unstructured {
	return obj("Secret", "web", "db", map[string]any{
		"type":       "Opaque",
		"data":       map[string]any{"password": "aHVudGVyMg==", "user": "YWRtaW4="},
		"stringData": map[string]any{"note": "plain text"},
	})
}

func session(objs ...runtime.Object) cluster.Session {
	sch := runtime.NewScheme()
	lists := map[schema.GroupVersionResource]string{
		secrets.GVR(): "SecretList", cms.GVR(): "ConfigMapList", nodes.GVR(): "NodeList",
	}
	return cluster.Session{Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(sch, lists, objs...)}
}

func TestYAML(t *testing.T) {
	s := session(
		secret(),
		obj("ConfigMap", "web", "settings", map[string]any{"data": map[string]any{"mode": "fast"}}),
		obj("Node", "", "node-1", map[string]any{"spec": map[string]any{"podCIDR": "10.0.0.0/24"}}),
	)
	tests := []struct {
		golden string
		ref    resources.ObjectRef
		opts   resources.YAMLOptions
	}{
		{"secret-masked", resources.ObjectRef{Type: secrets, Namespace: "web", Name: "db"}, resources.YAMLOptions{}},
		{"secret-revealed", resources.ObjectRef{Type: secrets, Namespace: "web", Name: "db"}, resources.YAMLOptions{RevealSecrets: true}},
		{"configmap", resources.ObjectRef{Type: cms, Namespace: "web", Name: "settings"}, resources.YAMLOptions{}},
		{"configmap-managed-fields", resources.ObjectRef{Type: cms, Namespace: "web", Name: "settings"}, resources.YAMLOptions{ManagedFields: true}},
		{"node", resources.ObjectRef{Type: nodes, Name: "node-1"}, resources.YAMLOptions{}},
	}
	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			got, err := Reader{}.YAML(context.Background(), s, tt.ref, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", tt.golden+".yaml")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("YAML mismatch (run with -update to rewrite)\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestYAMLNotFound(t *testing.T) {
	_, err := Reader{}.YAML(context.Background(), session(), resources.ObjectRef{Type: cms, Namespace: "web", Name: "gone"}, resources.YAMLOptions{})
	if err == nil {
		t.Fatal("YAML of a missing object succeeded")
	}
}

func TestMaskSecretWithoutValues(t *testing.T) {
	o := obj("Secret", "web", "empty", nil)
	unstructured.RemoveNestedField(o.Object, "metadata", "annotations")
	if _, err := Render(o, true, resources.YAMLOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestMaskSecretNotBase64(t *testing.T) {
	o := obj("Secret", "web", "odd", map[string]any{"data": map[string]any{"k": "not base64!"}})
	maskSecret(o.Object)
	if got := o.Object["data"].(map[string]any)["k"]; got != "<hidden: 11 bytes>" {
		t.Errorf("masked value = %v", got)
	}
}
