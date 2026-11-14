// Package object fetches single objects and renders them for the detail
// panel.
package object

import (
	"context"
	"encoding/base64"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

// lastApplied is the annotation `kubectl apply` stores the applied object
// in; on a Secret it holds the values in plain text.
const lastApplied = "kubectl.kubernetes.io/last-applied-configuration"

// Reader implements resources.ObjectReader with the session's dynamic
// client.
type Reader struct{}

// YAML fetches the object and renders it as YAML. Unless opts say otherwise,
// managedFields are dropped and Secret values are replaced by their sizes.
func (Reader) YAML(ctx context.Context, s cluster.Session, ref resources.ObjectRef, opts resources.YAMLOptions) (string, error) {
	r := s.Dynamic.Resource(ref.Type.GVR())
	var obj *unstructured.Unstructured
	var err error
	if ref.Type.Namespaced {
		obj, err = r.Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	} else {
		obj, err = r.Get(ctx, ref.Name, metav1.GetOptions{})
	}
	if err != nil {
		return "", err
	}
	return Render(obj, isSecret(ref.Type), opts)
}

// Render renders obj as YAML. secret marks a core/v1 Secret, whose values
// are masked unless opts.RevealSecrets is set. obj is modified.
func Render(obj *unstructured.Unstructured, secret bool, opts resources.YAMLOptions) (string, error) {
	if !opts.ManagedFields {
		unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	}
	if secret && !opts.RevealSecrets {
		maskSecret(obj.Object)
	}
	out, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", fmt.Errorf("render yaml: %w", err)
	}
	return string(out), nil
}

func isSecret(t resources.ResourceType) bool {
	return t.Group == "" && t.Resource == "secrets"
}

// maskSecret replaces the values in data (base64) and stringData (plain),
// and the last-applied annotation, with their sizes in bytes.
func maskSecret(obj map[string]any) {
	if data, ok := obj["data"].(map[string]any); ok {
		for k, v := range data {
			s, _ := v.(string)
			n := len(s)
			if b, err := base64.StdEncoding.DecodeString(s); err == nil {
				n = len(b)
			}
			data[k] = hidden(n)
		}
	}
	if data, ok := obj["stringData"].(map[string]any); ok {
		for k, v := range data {
			s, _ := v.(string)
			data[k] = hidden(len(s))
		}
	}
	if ann, ok, _ := unstructured.NestedMap(obj, "metadata", "annotations"); ok {
		if v, ok := ann[lastApplied].(string); ok {
			ann[lastApplied] = hidden(len(v))
			_ = unstructured.SetNestedMap(obj, ann, "metadata", "annotations")
		}
	}
}

func hidden(n int) string { return fmt.Sprintf("<hidden: %d bytes>", n) }
