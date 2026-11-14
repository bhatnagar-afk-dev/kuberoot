package browser

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/view"
)

var pods = resources.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}

type fakeConns struct {
	ctx context.Context
	err error
}

func (f fakeConns) Session(id string) (cluster.Session, error) {
	if f.err != nil {
		return cluster.Session{}, f.err
	}
	return cluster.Session{ContextID: id, Ctx: f.ctx}, nil
}

type fakeSubscriber struct {
	mu     sync.Mutex
	open   int
	keys   []cache.Key
	gate   chan struct{} // if set, Subscribe waits for it
	failOn string
}

func (f *fakeSubscriber) Subscribe(_ cluster.Session, k cache.Key, _ cache.Handler) (cache.Subscription, error) {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if k.Namespace == f.failOn && f.failOn != "" {
		return nil, errors.New("boom token=abcdef123456")
	}
	f.open++
	f.keys = append(f.keys, k)
	return sub{f}, nil
}

func (f *fakeSubscriber) Running(cache.Key) bool { return false }

func (f *fakeSubscriber) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open
}

type sub struct{ f *fakeSubscriber }

func (s sub) Close() {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	s.f.open--
}

type nopSink struct{}

func (nopSink) Send(resources.RowBatch)     {}
func (nopSink) Status(resources.ViewStatus) {}

func newBrowser(t *testing.T, conns resources.Connections, subs *fakeSubscriber) *Browser {
	t.Helper()
	b := New(conns, subs, func(resources.ViewRequest) resources.Sink { return nopSink{} },
		view.Options{Clock: clocktesting.NewFakeClock(time.Now())})
	t.Cleanup(b.CloseAll)
	return b
}

func req(id string, namespaces ...string) resources.ViewRequest {
	return resources.ViewRequest{ViewID: id, ContextID: "c1", Type: pods, Namespaces: namespaces}
}

func TestOpenAndClose(t *testing.T) {
	subs := &fakeSubscriber{}
	b := newBrowser(t, fakeConns{ctx: t.Context()}, subs)

	if err := b.Open(req("v1", "a", "b")); err != nil {
		t.Fatal(err)
	}
	if got := subs.count(); got != 2 {
		t.Fatalf("subscriptions = %d, want 2", got)
	}
	b.Close("v1")
	b.Close("v1")      // idempotent
	b.Close("unknown") // ignored
	if got := subs.count(); got != 0 {
		t.Fatalf("subscriptions after close = %d, want 0", got)
	}
	// The ID is free again.
	if err := b.Open(req("v1")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenEvents(t *testing.T) {
	subs := &fakeSubscriber{}
	var sinkReq resources.ViewRequest
	b := New(fakeConns{ctx: t.Context()}, subs, func(r resources.ViewRequest) resources.Sink { sinkReq = r; return nopSink{} },
		view.Options{Clock: clocktesting.NewFakeClock(time.Now())})
	t.Cleanup(b.CloseAll)

	ref := resources.ObjectRef{ContextID: "c1", Type: pods, Namespace: "web", Name: "p1", UID: "u1"}
	if err := b.OpenEvents("e1", ref); err != nil {
		t.Fatal(err)
	}
	want := cache.Key{ContextID: "c1", GVR: EventsType.GVR(), Namespace: "web",
		FieldSelector: "involvedObject.name=p1,involvedObject.namespace=web,involvedObject.uid=u1"}
	if len(subs.keys) != 1 || subs.keys[0] != want {
		t.Errorf("subscribed = %+v, want %+v", subs.keys, want)
	}
	if sinkReq.ViewID != "e1" || sinkReq.ContextID != "c1" || sinkReq.Type != EventsType {
		t.Errorf("sink request = %+v", sinkReq)
	}
	if err := b.OpenEvents("e1", ref); err == nil {
		t.Error("duplicate events view ID accepted")
	}
	b.Close("e1")
	if subs.count() != 0 {
		t.Error("events view not closed")
	}
}

func TestEventsRequest(t *testing.T) {
	nodes := resources.ResourceType{Version: "v1", Resource: "nodes", Kind: "Node"}
	pvs := resources.ResourceType{Version: "v1", Resource: "persistentvolumes", Kind: "PersistentVolume"}
	tests := []struct {
		name       string
		ref        resources.ObjectRef
		namespaces []string
		selector   string
	}{
		{"namespaced", resources.ObjectRef{Type: pods, Namespace: "web", Name: "p1", UID: "u1"},
			[]string{"web"}, "involvedObject.name=p1,involvedObject.namespace=web,involvedObject.uid=u1"},
		{"no UID", resources.ObjectRef{Type: pods, Namespace: "web", Name: "p1"},
			[]string{"web"}, "involvedObject.name=p1,involvedObject.namespace=web"},
		{"cluster-scoped", resources.ObjectRef{Type: pvs, Name: "pv1", UID: "u2"},
			nil, "involvedObject.name=pv1,involvedObject.namespace=,involvedObject.uid=u2"},
		{"node by kind", resources.ObjectRef{Type: nodes, Name: "n1", UID: "u3"},
			nil, "involvedObject.kind=Node,involvedObject.name=n1,involvedObject.namespace="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := EventsRequest("v", tt.ref)
			if r.ViewID != "v" || r.Type != EventsType || !slices.Equal(r.Namespaces, tt.namespaces) || r.FieldSelector != tt.selector {
				t.Errorf("request = %+v, want namespaces %v and selector %q", r, tt.namespaces, tt.selector)
			}
		})
	}
}

func TestOpenRejectsBadIDs(t *testing.T) {
	b := newBrowser(t, fakeConns{ctx: t.Context()}, &fakeSubscriber{})
	if err := b.Open(req("")); err == nil {
		t.Fatal("empty ID accepted")
	}
	if err := b.Open(req("v1")); err != nil {
		t.Fatal(err)
	}
	if err := b.Open(req("v1")); err == nil || !strings.Contains(err.Error(), "already open") {
		t.Fatalf("duplicate ID: err = %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	b := newBrowser(t, fakeConns{err: cluster.ErrNotConnected}, &fakeSubscriber{})
	if err := b.Open(req("v1")); err == nil || err.Error() != cluster.ErrNotConnected.Error() {
		t.Fatalf("err = %v, want %v", err, cluster.ErrNotConnected)
	}

	subs := &fakeSubscriber{failOn: "b"}
	b = newBrowser(t, fakeConns{ctx: t.Context()}, subs)
	err := b.Open(req("v1", "a", "b"))
	if err == nil || strings.Contains(err.Error(), "abcdef123456") {
		t.Fatalf("err = %v, want a redacted error", err)
	}
	if got := subs.count(); got != 0 {
		t.Fatalf("subscriptions after failed open = %d, want 0", got)
	}
	// A failed Open frees the ID.
	subs.failOn = ""
	if err := b.Open(req("v1", "a", "b")); err != nil {
		t.Fatal(err)
	}
}

func TestCloseDuringOpen(t *testing.T) {
	subs := &fakeSubscriber{gate: make(chan struct{})}
	b := newBrowser(t, fakeConns{ctx: t.Context()}, subs)

	done := make(chan error)
	go func() { done <- b.Open(req("v1")) }()
	waitPending(t, b, "v1")
	b.Close("v1")
	close(subs.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := subs.count(); got != 0 {
		t.Fatalf("subscriptions = %d, want 0 (closed while opening)", got)
	}
}

func TestCloseAll(t *testing.T) {
	subs := &fakeSubscriber{}
	b := newBrowser(t, fakeConns{ctx: t.Context()}, subs)
	for _, id := range []string{"v1", "v2"} {
		if err := b.Open(req(id)); err != nil {
			t.Fatal(err)
		}
	}
	b.CloseAll()
	if got := subs.count(); got != 0 {
		t.Fatalf("subscriptions = %d, want 0", got)
	}
	if err := b.Open(req("v3")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Open after CloseAll: err = %v, want ErrClosed", err)
	}
}

func waitPending(t *testing.T, b *Browser, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		_, ok := b.pending[id]
		b.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("view %q never started opening", id)
}
