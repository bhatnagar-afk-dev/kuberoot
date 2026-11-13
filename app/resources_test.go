package app

import (
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
	sink := eventSink{func(name string, data any) { got = append(got, event{name, data}) }}

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
