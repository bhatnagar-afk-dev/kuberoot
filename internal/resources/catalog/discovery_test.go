package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

var (
	listWatch = metav1.Verbs{"get", "list", "watch"}
	core      = []metav1.APIResource{
		{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: listWatch},
		{Name: "pods/log", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"get"}},
		{Name: "nodes", Kind: "Node", Verbs: listWatch},
		{Name: "bindings", Kind: "Binding", Namespaced: true, Verbs: metav1.Verbs{"create"}},
	}
	apps = []metav1.APIResource{
		{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: listWatch},
	}
	widgets = []metav1.APIResource{
		{Name: "widgets", Kind: "Widget", Namespaced: true, Verbs: listWatch},
	}
)

// fakeDiscovery serves legacy (non-aggregated) discovery documents.
type fakeDiscovery struct {
	mu       sync.Mutex
	groups   map[string][]metav1.APIResource // "group/version" -> resources; "" group is core
	broken   map[string]bool                 // group versions that answer 503
	requests int
}

func newFakeDiscovery(t *testing.T) (*fakeDiscovery, cluster.Session) {
	t.Helper()
	f := &fakeDiscovery{
		groups: map[string][]metav1.APIResource{"/v1": core, "apps/v1": apps},
		broken: map[string]bool{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	cfg := &rest.Config{Host: srv.URL}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f, cluster.Session{ContextID: "c1", Ctx: t.Context(), Config: cfg, Discovery: memory.NewMemCacheClient(disc)}
}

func (f *fakeDiscovery) set(gv string, rs []metav1.APIResource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rs == nil {
		delete(f.groups, gv)
	} else {
		f.groups[gv] = rs
	}
}

func (f *fakeDiscovery) setBroken(gv string, broken bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broken[gv] = broken
}

func (f *fakeDiscovery) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *fakeDiscovery) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch p := r.URL.Path; p {
	case "/api":
		write(metav1.APIVersions{TypeMeta: metav1.TypeMeta{Kind: "APIVersions"}, Versions: []string{"v1"}})
	case "/apis":
		list := metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList"}}
		for gv := range f.groups {
			g, v, _ := strings.Cut(gv, "/")
			if g == "" {
				continue
			}
			ver := metav1.GroupVersionForDiscovery{GroupVersion: gv, Version: v}
			list.Groups = append(list.Groups, metav1.APIGroup{Name: g, Versions: []metav1.GroupVersionForDiscovery{ver}, PreferredVersion: ver})
		}
		write(list)
	default:
		gv := strings.TrimPrefix(strings.TrimPrefix(p, "/apis/"), "/api/")
		if strings.HasPrefix(p, "/api/") {
			gv = "/" + gv
		}
		rs, ok := f.groups[gv]
		switch {
		case f.broken[gv]:
			w.WriteHeader(http.StatusServiceUnavailable)
			write(metav1.Status{Status: metav1.StatusFailure, Code: http.StatusServiceUnavailable, Reason: metav1.StatusReasonServiceUnavailable})
		case !ok:
			w.WriteHeader(http.StatusNotFound)
			write(metav1.Status{Status: metav1.StatusFailure, Code: http.StatusNotFound, Reason: metav1.StatusReasonNotFound})
		default:
			write(metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList"}, GroupVersion: strings.TrimPrefix(gv, "/"), APIResources: rs})
		}
	}
}

func names(types []resources.ResourceType) []string {
	var out []string
	for _, t := range types {
		out = append(out, t.Group+"/"+t.Resource)
	}
	return out
}

func newCatalog(t *testing.T, s cluster.Session) *Discovery {
	t.Helper()
	d, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestResourceTypesFiltersAndSorts(t *testing.T) {
	_, s := newFakeDiscovery(t)
	types, err := newCatalog(t, s).ResourceTypes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(types), []string{"/nodes", "/pods", "apps/deployments"}; !slices.Equal(got, want) {
		t.Errorf("types = %v, want %v", got, want)
	}
	pods := types[1]
	if pods.Version != "v1" || pods.Kind != "Pod" || !pods.Namespaced {
		t.Errorf("pods = %+v", pods)
	}
	if types[2].Version != "v1" || types[2].Group != "apps" {
		t.Errorf("deployments = %+v", types[2])
	}
}

func TestResourceTypesUsesCache(t *testing.T) {
	f, s := newFakeDiscovery(t)
	c := newCatalog(t, s)
	if _, err := c.ResourceTypes(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := f.count()
	if _, err := c.ResourceTypes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.count() != n {
		t.Errorf("second call made %d requests, want 0", f.count()-n)
	}
}

func TestPartialFailureKeepsWorkingGroups(t *testing.T) {
	f, s := newFakeDiscovery(t)
	f.set("metrics.k8s.io/v1beta1", []metav1.APIResource{{Name: "pods", Kind: "PodMetrics", Namespaced: true, Verbs: listWatch}})
	f.setBroken("metrics.k8s.io/v1beta1", true)

	types, err := newCatalog(t, s).ResourceTypes(context.Background())
	var pe *resources.PartialDiscoveryError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want PartialDiscoveryError", err)
	}
	if !slices.Equal(pe.GroupVersions, []string{"metrics.k8s.io/v1beta1"}) {
		t.Errorf("failed = %v", pe.GroupVersions)
	}
	if got, want := names(types), []string{"/nodes", "/pods", "apps/deployments"}; !slices.Equal(got, want) {
		t.Errorf("types = %v, want %v", got, want)
	}
}

func TestChangedAndRefresh(t *testing.T) {
	f, s := newFakeDiscovery(t)
	c := newCatalog(t, s)
	ctx := context.Background()

	if changed, err := c.Changed(ctx); err != nil || changed {
		t.Fatalf("Changed before listing = %v, %v; want false", changed, err)
	}
	if _, err := c.ResourceTypes(ctx); err != nil {
		t.Fatal(err)
	}
	if changed, err := c.Changed(ctx); err != nil || changed {
		t.Fatalf("Changed with no change = %v, %v; want false", changed, err)
	}

	f.set("example.com/v1", widgets)
	if changed, err := c.Changed(ctx); err != nil || !changed {
		t.Fatalf("Changed after adding a CRD = %v, %v; want true", changed, err)
	}
	// Changed doesn't update the cache.
	types, _ := c.ResourceTypes(ctx)
	if slices.Contains(names(types), "example.com/widgets") {
		t.Error("ResourceTypes after Changed includes the new type; the cache was updated")
	}
	if changed, _ := c.Changed(ctx); !changed {
		t.Error("Changed is false again before Refresh")
	}

	types, err := c.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names(types), "example.com/widgets") {
		t.Errorf("Refresh = %v, want widgets", names(types))
	}
	if changed, err := c.Changed(ctx); err != nil || changed {
		t.Errorf("Changed after Refresh = %v, %v; want false", changed, err)
	}

	f.set("example.com/v1", nil)
	if changed, _ := c.Changed(ctx); !changed {
		t.Error("Changed after removing a CRD = false, want true")
	}
}

func TestChangedSeesFailingGroup(t *testing.T) {
	f, s := newFakeDiscovery(t)
	f.set("example.com/v1", widgets)
	c := newCatalog(t, s)
	ctx := context.Background()
	if _, err := c.ResourceTypes(ctx); err != nil {
		t.Fatal(err)
	}
	f.setBroken("example.com/v1", true)
	if changed, err := c.Changed(ctx); err != nil || !changed {
		t.Errorf("Changed when a group fails = %v, %v; want true", changed, err)
	}
}

func TestChangedSeesRecoveredGroup(t *testing.T) {
	f, s := newFakeDiscovery(t)
	f.set("metrics.k8s.io/v1beta1", []metav1.APIResource{{Name: "pods", Kind: "PodMetrics", Namespaced: true, Verbs: listWatch}})
	f.setBroken("metrics.k8s.io/v1beta1", true)
	c := newCatalog(t, s)
	ctx := context.Background()
	if _, err := c.ResourceTypes(ctx); err == nil {
		t.Fatal("want a partial discovery error while the group is down")
	}
	if changed, err := c.Changed(ctx); err != nil || changed {
		t.Fatalf("Changed while still down = %v, %v; want false", changed, err)
	}
	f.setBroken("metrics.k8s.io/v1beta1", false)
	if changed, err := c.Changed(ctx); err != nil || !changed {
		t.Fatalf("Changed after the group recovered = %v, %v; want true", changed, err)
	}
	types, err := c.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh after recovery = %v, want no partial error", err)
	}
	if !slices.Contains(names(types), "metrics.k8s.io/pods") {
		t.Errorf("Refresh = %v, want the recovered group's types", names(types))
	}
}
