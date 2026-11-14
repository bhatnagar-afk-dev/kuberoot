package view

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/table"
)

var pods = resources.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}

func row(ns, name string) *table.RowObject {
	r := &table.RowObject{Cells: []any{name}}
	r.Namespace, r.Name, r.UID = ns, name, types.UID(ns+"/"+name)
	return r
}

type fakeSubscriber struct {
	mu       sync.Mutex
	running  map[cache.Key]bool
	handlers map[cache.Key]cache.Handler
	closed   int
	failOn   string // namespace whose Subscribe fails
}

func newFakeSubscriber() *fakeSubscriber {
	return &fakeSubscriber{running: map[cache.Key]bool{}, handlers: map[cache.Key]cache.Handler{}}
}

func (f *fakeSubscriber) Subscribe(_ cluster.Session, k cache.Key, h cache.Handler) (cache.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn != "" && k.Namespace == f.failOn {
		return nil, errors.New("boom")
	}
	f.handlers[k] = h
	return closer{f}, nil
}

func (f *fakeSubscriber) Running(k cache.Key) bool { return f.running[k] }

func (f *fakeSubscriber) handler(t *testing.T, ns string) cache.Handler {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.handlers[cache.Key{ContextID: "c1", GVR: pods.GVR(), Namespace: ns}]
	if !ok {
		t.Fatalf("no subscription for namespace %q (have %v)", ns, f.handlers)
	}
	return h
}

func (f *fakeSubscriber) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.handlers {
		out = append(out, k.Namespace)
	}
	slices.Sort(out)
	return out
}

type closer struct{ f *fakeSubscriber }

func (c closer) Close() {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.closed++
}

type fakeSink struct {
	mu       sync.Mutex
	batches  []resources.RowBatch
	statuses []resources.ViewStatus
}

func (s *fakeSink) Send(b resources.RowBatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, b)
}

func (s *fakeSink) Status(st resources.ViewStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = append(s.statuses, st)
}

// take returns and clears what the sink has received.
func (s *fakeSink) take() ([]resources.RowBatch, []resources.ViewStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, st := s.batches, s.statuses
	s.batches, s.statuses = nil, nil
	return b, st
}

func names(rows []resources.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Namespace + "/" + r.Name
	}
	return out
}

// open returns a view whose loop never ticks on its own; tests call flush.
func open(t *testing.T, req Request, subs *fakeSubscriber, opts Options) (*View, *fakeSink, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if opts.Clock == nil {
		opts.Clock = clocktesting.NewFakeClock(time.Now())
	}
	sink := &fakeSink{}
	req.ViewID = "v1"
	v, err := Open(cluster.Session{ContextID: "c1", Ctx: ctx}, subs, req, sink, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	return v, sink, cancel
}

func TestResetAfterAllNamespacesSync(t *testing.T) {
	subs := newFakeSubscriber()
	v, sink, _ := open(t, Request{Type: pods, Namespaces: []string{"web", "api", "web"}}, subs, Options{})

	if got := subs.keys(); !slices.Equal(got, []string{"api", "web"}) {
		t.Fatalf("subscribed namespaces = %v, want one informer per (deduplicated) namespace", got)
	}
	web, api := subs.handler(t, "web"), subs.handler(t, "api")
	web.Upsert(row("web", "w1"))
	web.Synced([]resources.Column{{Name: "Name"}})

	v.flush()
	batches, statuses := sink.take()
	if len(batches) != 0 {
		t.Fatalf("sent %d batches while a namespace is still loading", len(batches))
	}
	if len(statuses) != 1 || statuses[0].Phase != resources.ViewLoading {
		t.Fatalf("statuses = %+v, want loading", statuses)
	}

	api.Upsert(row("api", "a1"))
	api.Synced([]resources.Column{{Name: "Name"}})
	v.flush()
	batches, statuses = sink.take()
	if len(statuses) != 1 || statuses[0].Phase != resources.ViewReady {
		t.Fatalf("statuses = %+v, want ready", statuses)
	}
	if len(batches) != 1 || !batches[0].Reset || len(batches[0].Columns) != 1 {
		t.Fatalf("batches = %+v, want one reset with columns", batches)
	}
	if got := names(batches[0].Upserts); !slices.Equal(got, []string{"api/a1", "web/w1"}) {
		t.Errorf("reset rows = %v", got)
	}

	// Nothing changed: nothing sent.
	v.flush()
	if b, st := sink.take(); len(b) != 0 || len(st) != 0 {
		t.Errorf("idle flush sent %v %v", b, st)
	}
}

func TestIncrementalBatches(t *testing.T) {
	subs := newFakeSubscriber()
	v, sink, _ := open(t, Request{Type: pods}, subs, Options{})
	h := subs.handler(t, "")
	h.Upsert(row("a", "1"))
	h.Upsert(row("a", "2"))
	h.Synced(nil)
	v.flush()
	sink.take()

	h.Upsert(row("a", "3"))
	h.Upsert(row("a", "1")) // modified
	h.Delete(row("a", "2"))
	h.Upsert(row("a", "4"))
	h.Delete(row("a", "4")) // added and removed within one window: not sent
	h.Delete(row("a", "nope"))
	v.flush()
	batches, _ := sink.take()
	if len(batches) != 1 || batches[0].Reset {
		t.Fatalf("batches = %+v, want one incremental batch", batches)
	}
	if got := names(batches[0].Upserts); !slices.Equal(got, []string{"a/1", "a/3"}) {
		t.Errorf("upserts = %v", got)
	}
	if got := batches[0].Deletes; !slices.Equal(got, []string{"a/2"}) {
		t.Errorf("deletes = %v", got)
	}
}

func TestResetIsChunked(t *testing.T) {
	subs := newFakeSubscriber()
	v, sink, _ := open(t, Request{Type: pods}, subs, Options{ChunkSize: 2})
	h := subs.handler(t, "")
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		h.Upsert(row("ns", n))
	}
	h.Synced(nil)
	v.flush()
	batches, _ := sink.take()
	if len(batches) != 3 {
		t.Fatalf("got %d batches, want 3", len(batches))
	}
	if !batches[0].Reset || batches[1].Reset || batches[2].Reset {
		t.Error("only the first chunk should be a reset")
	}
	total := 0
	for _, b := range batches {
		total += len(b.Upserts)
	}
	if total != 5 {
		t.Errorf("rows sent = %d, want 5", total)
	}
}

func TestEmptyResetHasNoNilSlices(t *testing.T) {
	subs := newFakeSubscriber()
	v, sink, _ := open(t, Request{Type: pods}, subs, Options{})
	subs.handler(t, "").Synced(nil)
	v.flush()
	batches, _ := sink.take()
	if len(batches) != 1 || !batches[0].Reset || batches[0].Upserts == nil || batches[0].Deletes == nil {
		t.Errorf("batches = %+v, want a reset with empty (not null) row lists", batches)
	}
}

func TestReusesRunningClusterWideInformer(t *testing.T) {
	subs := newFakeSubscriber()
	subs.running[cache.Key{ContextID: "c1", GVR: pods.GVR()}] = true
	v, sink, _ := open(t, Request{Type: pods, Namespaces: []string{"web"}}, subs, Options{})
	if got := subs.keys(); !slices.Equal(got, []string{""}) {
		t.Fatalf("subscribed = %v, want the running cluster-wide informer", got)
	}
	h := subs.handler(t, "")
	h.Upsert(row("web", "w1"))
	h.Upsert(row("api", "a1")) // filtered out
	h.Synced(nil)
	v.flush()
	batches, _ := sink.take()
	if got := names(batches[0].Upserts); !slices.Equal(got, []string{"web/w1"}) {
		t.Errorf("rows = %v, want only namespace web", got)
	}
	h.Delete(row("api", "a1"))
	v.flush()
	if b, _ := sink.take(); len(b) != 0 {
		t.Errorf("delete outside the filter was sent: %+v", b)
	}
}

func TestFieldSelector(t *testing.T) {
	subs := newFakeSubscriber()
	// A running unfiltered informer can't serve a filtered view.
	subs.running[cache.Key{ContextID: "c1", GVR: pods.GVR()}] = true
	open(t, Request{Type: pods, Namespaces: []string{"web"}, FieldSelector: "spec.nodeName=n1"}, subs, Options{})
	want := cache.Key{ContextID: "c1", GVR: pods.GVR(), Namespace: "web", FieldSelector: "spec.nodeName=n1"}
	subs.mu.Lock()
	defer subs.mu.Unlock()
	if _, ok := subs.handlers[want]; !ok || len(subs.handlers) != 1 {
		t.Errorf("subscribed = %v, want only %v", subs.handlers, want)
	}
}

func TestClusterScopedIgnoresNamespaces(t *testing.T) {
	subs := newFakeSubscriber()
	nodes := resources.ResourceType{Version: "v1", Resource: "nodes", Kind: "Node"}
	open(t, Request{Type: nodes, Namespaces: []string{"web"}}, subs, Options{})
	if got := subs.keys(); !slices.Equal(got, []string{""}) {
		t.Errorf("subscribed = %v, want one cluster-wide informer", got)
	}
}

var forbidden = apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil)

func TestPartialForbidden(t *testing.T) {
	subs := newFakeSubscriber()
	v, sink, _ := open(t, Request{Type: pods, Namespaces: []string{"web", "kube-system"}}, subs, Options{})
	subs.handler(t, "kube-system").Error(forbidden)
	web := subs.handler(t, "web")
	web.Upsert(row("web", "w1"))
	web.Synced(nil)
	v.flush()
	batches, statuses := sink.take()
	st := statuses[0]
	if st.Phase != resources.ViewReady || !slices.Equal(st.Forbidden, []string{"kube-system"}) ||
		st.Message != `No permission to list pods in "kube-system"` {
		t.Errorf("status = %+v", st)
	}
	if len(batches) != 1 || len(batches[0].Upserts) != 1 {
		t.Errorf("batches = %+v, want the readable rows", batches)
	}

	// Permission granted later: the namespace syncs and the warning clears.
	ks := subs.handler(t, "kube-system")
	ks.Error(nil)
	ks.Upsert(row("kube-system", "k1"))
	ks.Synced(nil)
	v.flush()
	batches, statuses = sink.take()
	if len(statuses) != 1 || statuses[0].Phase != resources.ViewReady || len(statuses[0].Forbidden) != 0 || statuses[0].Message != "" {
		t.Errorf("statuses = %+v, want ready with no warning", statuses)
	}
	if len(batches) != 1 || batches[0].Reset || !slices.Equal(names(batches[0].Upserts), []string{"kube-system/k1"}) {
		t.Errorf("batches = %+v", batches)
	}
}

func TestAllForbidden(t *testing.T) {
	subs := newFakeSubscriber()
	v, sink, _ := open(t, Request{Type: pods}, subs, Options{})
	subs.handler(t, "").Error(forbidden)
	v.flush()
	batches, statuses := sink.take()
	if st := statuses[0]; st.Phase != resources.ViewError || st.Message != "No permission to list pods" {
		t.Errorf("status = %+v", st)
	}
	if len(batches) != 1 || !batches[0].Reset {
		t.Errorf("batches = %+v, want an empty reset so the UI leaves loading", batches)
	}
}

func TestOtherErrorsAreRedacted(t *testing.T) {
	subs := newFakeSubscriber()
	v, sink, _ := open(t, Request{Type: pods}, subs, Options{})
	subs.handler(t, "").Error(errors.New(`Get "https://user:secret@example.com/api": dial tcp: i/o timeout`))
	v.flush()
	_, statuses := sink.take()
	if msg := statuses[0].Message; msg == "" || strings.Contains(msg, "secret") {
		t.Errorf("message = %q, want a redacted error", msg)
	}
}

func TestNotFoundReason(t *testing.T) {
	subs := newFakeSubscriber()
	v, sink, _ := open(t, Request{Type: pods}, subs, Options{})
	subs.handler(t, "").Error(apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, ""))
	v.flush()
	_, statuses := sink.take()
	if st := statuses[0]; st.Phase != resources.ViewError || st.Reason != resources.ReasonNotFound {
		t.Errorf("status = %+v, want error with reason NotFound", st)
	}
}

func TestOpenFailureClosesSubscriptions(t *testing.T) {
	subs := newFakeSubscriber()
	subs.failOn = "web"
	_, err := Open(cluster.Session{ContextID: "c1", Ctx: context.Background()}, subs,
		Request{ViewID: "v1", Type: pods, Namespaces: []string{"api", "web"}}, &fakeSink{}, Options{})
	if err == nil {
		t.Fatal("want an error")
	}
	if subs.closed != 1 {
		t.Errorf("closed %d subscriptions, want 1 (api)", subs.closed)
	}
}

func TestTickerAndClose(t *testing.T) {
	subs := newFakeSubscriber()
	clk := clocktesting.NewFakeClock(time.Now())
	v, sink, _ := open(t, Request{Type: pods}, subs, Options{Clock: clk})
	subs.handler(t, "").Synced(nil)

	deadline := time.Now().Add(3 * time.Second)
	for {
		clk.Step(DefaultFlushInterval)
		sink.mu.Lock()
		n := len(sink.batches)
		sink.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ticker never flushed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	v.Close()
	if subs.closed != 1 {
		t.Errorf("closed %d subscriptions, want 1", subs.closed)
	}
	sink.take()
	subs.handler(t, "").Upsert(row("a", "late"))
	clk.Step(time.Second)
	time.Sleep(20 * time.Millisecond)
	if b, st := sink.take(); len(b) != 0 || len(st) != 0 {
		t.Errorf("sink called after Close: %v %v", b, st)
	}
}

func TestConnectionEndMarksStale(t *testing.T) {
	subs := newFakeSubscriber()
	_, sink, cancel := open(t, Request{Type: pods}, subs, Options{})
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		sink.mu.Lock()
		var last resources.ViewStatus
		if n := len(sink.statuses); n > 0 {
			last = sink.statuses[n-1]
		}
		sink.mu.Unlock()
		if last.Phase == resources.ViewStale {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("last status = %+v, want stale", last)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
