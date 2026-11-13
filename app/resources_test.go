package app

import (
	"slices"
	"testing"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

var podsType = resources.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}

func newTestResourceService(t *testing.T) *ResourceService {
	t.Helper()
	cs, _, _ := newTestService(t)
	s := NewResourceService(cs)
	s.start()
	t.Cleanup(func() { _ = s.ServiceShutdown() })
	return s
}

func TestResourceOpenNotConnected(t *testing.T) {
	s := newTestResourceService(t)
	id := s.clusters.store.Contexts()[0].ID
	err := s.Open(resources.ViewRequest{ViewID: "v1", ContextID: id, Type: podsType})
	if err == nil || err.Error() != cluster.ErrNotConnected.Error() {
		t.Fatalf("err = %v, want %v", err, cluster.ErrNotConnected)
	}
	s.Close("v1") // never opened: ignored
}

func TestResourceOpenBeforeClusterStartup(t *testing.T) {
	s := NewResourceService(&ClusterService{})
	s.start()
	t.Cleanup(func() { _ = s.ServiceShutdown() })
	if err := s.Open(resources.ViewRequest{ViewID: "v1", ContextID: "x", Type: podsType}); err == nil {
		t.Fatal("Open succeeded without a cluster manager")
	}
}

func TestEventSink(t *testing.T) {
	type event struct {
		name string
		data any
	}
	var got []event
	sink := eventSink{emit: func(name string, data any) { got = append(got, event{name, data}) }}

	batch := resources.RowBatch{ViewID: "v1", Reset: true}
	status := resources.ViewStatus{ViewID: "v1", Phase: resources.ViewReady}
	sink.Status(status)
	sink.Send(batch)

	if len(got) != 2 {
		t.Fatalf("events = %+v", got)
	}
	if got[0].name != EventResourceStatus || got[0].data.(resources.ViewStatus).ViewID != "v1" {
		t.Errorf("status event = %+v", got[0])
	}
	if got[1].name != EventResourceRows || !got[1].data.(resources.RowBatch).Reset {
		t.Errorf("rows event = %+v", got[1])
	}
}

func TestEventSinkNotFoundAsksForCheck(t *testing.T) {
	checks := 0
	sink := eventSink{emit: func(string, any) {}, notFound: func() { checks++ }}
	sink.Status(resources.ViewStatus{ViewID: "v1", Phase: resources.ViewError})
	sink.Status(resources.ViewStatus{ViewID: "v1", Phase: resources.ViewError, Reason: resources.ReasonNotFound})
	if checks != 1 {
		t.Errorf("checks = %d, want 1", checks)
	}
}

func TestNavigationNotConnected(t *testing.T) {
	s := newTestResourceService(t)
	id := s.clusters.store.Contexts()[0].ID
	if _, err := s.Navigation(id); err == nil || err.Error() != cluster.ErrNotConnected.Error() {
		t.Fatalf("Navigation err = %v, want %v", err, cluster.ErrNotConnected)
	}
	if _, err := s.RefreshNavigation(id); err == nil {
		t.Fatal("RefreshNavigation succeeded while disconnected")
	}
}

func TestSavedNamespaces(t *testing.T) {
	s := newTestResourceService(t)
	if got := s.SavedNamespaces("c1"); got == nil || len(got) != 0 {
		t.Fatalf("SavedNamespaces before saving = %#v, want empty", got)
	}
	if err := s.SaveNamespaces("c1", []string{"web", "api"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveNamespaces("c2", []string{"kube-system"}); err != nil {
		t.Fatal(err)
	}
	if got := s.SavedNamespaces("c1"); !slices.Equal(got, []string{"web", "api"}) {
		t.Errorf("c1 = %v", got)
	}
	if err := s.SaveNamespaces("c1", nil); err != nil {
		t.Fatal(err)
	}
	if got := s.SavedNamespaces("c1"); len(got) != 0 {
		t.Errorf("c1 after saving all namespaces = %v, want empty", got)
	}
	if _, ok := s.clusters.settings.Get().Namespaces["c1"]; ok {
		t.Error("an all-namespaces choice is stored; want the entry removed")
	}
	if got := s.SavedNamespaces("c2"); !slices.Equal(got, []string{"kube-system"}) {
		t.Errorf("c2 = %v", got)
	}
}
