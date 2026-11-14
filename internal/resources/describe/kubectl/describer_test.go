package kubectl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

var (
	configMaps = resources.ResourceType{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
	widgets    = resources.ResourceType{Group: "example.com", Version: "v1", Resource: "widgets", Kind: "Widget", Namespaced: true}
)

// resettable adds Reset to a static mapper.
type resettable struct{ *meta.DefaultRESTMapper }

func (resettable) Reset() {}

// fakeServer serves one ConfigMap, one Widget and one event for each.
func fakeServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	objects := map[string]any{
		"/api/v1/namespaces/web/configmaps/settings": map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "settings", "namespace": "web", "uid": "cm-uid"},
			"data":     map[string]any{"mode": "fast"},
		},
		"/apis/example.com/v1/namespaces/web/widgets/w1": map[string]any{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]any{"name": "w1", "namespace": "web", "uid": "w1-uid"},
			"spec":     map[string]any{"color": "blue"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/namespaces/web/events" {
			sel := r.URL.Query().Get("fieldSelector")
			reason := "Unrelated"
			if strings.Contains(sel, "involvedObject.name=settings") {
				reason = "ConfigChanged"
			} else if strings.Contains(sel, "involvedObject.name=w1") {
				reason = "Painted"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "v1", "kind": "EventList", "metadata": map[string]any{},
				"items": []any{map[string]any{
					"metadata": map[string]any{"name": "e1", "namespace": "web"},
					"type":     "Normal", "reason": reason, "message": "it happened", "count": 1,
					"source": map[string]any{"component": "test"},
				}},
			})
			return
		}
		o, ok := objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "NotFound", "code": 404,
				"message": "not found",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(o)
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func session(srv *httptest.Server) cluster.Session {
	m := meta.NewDefaultRESTMapper(nil)
	m.Add(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}, meta.RESTScopeNamespace)
	return cluster.Session{Config: &rest.Config{Host: srv.URL}, Mapper: resettable{m}}
}

func TestDescribeBuiltIn(t *testing.T) {
	srv, _ := fakeServer(t)
	out, err := Describer{}.Describe(context.Background(), session(srv), resources.ObjectRef{Type: configMaps, Namespace: "web", Name: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Name:", "settings", "mode", "fast", "ConfigChanged"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDescribeCustomResource(t *testing.T) {
	srv, _ := fakeServer(t)
	out, err := Describer{}.Describe(context.Background(), session(srv), resources.ObjectRef{Type: widgets, Namespace: "web", Name: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Name:", "w1", "Color:", "blue", "Painted"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDescribeNotFound(t *testing.T) {
	srv, _ := fakeServer(t)
	_, err := Describer{}.Describe(context.Background(), session(srv), resources.ObjectRef{Type: configMaps, Namespace: "web", Name: "gone"})
	if err == nil {
		t.Fatal("Describe of a missing object succeeded")
	}
}

func TestDescribeUnmappedType(t *testing.T) {
	srv, _ := fakeServer(t)
	gadgets := resources.ResourceType{Group: "example.com", Version: "v1", Resource: "gadgets", Kind: "Gadget", Namespaced: true}
	if _, err := (Describer{}).Describe(context.Background(), session(srv), resources.ObjectRef{Type: gadgets, Namespace: "web", Name: "g"}); err == nil {
		t.Fatal("Describe of an unknown type succeeded")
	}
}

func TestDescribeCancelled(t *testing.T) {
	srv, paths := fakeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Describer{}).Describe(ctx, session(srv), resources.ObjectRef{Type: configMaps, Namespace: "web", Name: "settings"}); err == nil {
		t.Fatal("Describe with a cancelled context succeeded")
	}
	if len(*paths) != 0 {
		t.Errorf("requests made: %v", *paths)
	}
}
