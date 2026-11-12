package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	clocktesting "k8s.io/utils/clock/testing"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/table"
)

const timeout = 3 * time.Second

var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

func row(ns, name, rv string) *table.RowObject {
	r := &table.RowObject{Cells: []any{name}}
	r.Namespace, r.Name, r.ResourceVersion = ns, name, rv
	r.UID = types.UID(ns + "/" + name)
	return r
}

// fakeSource serves a fixed list and hands each watch to the test.
type fakeSource struct {
	items   []table.RowObject
	lists   atomic.Int32
	forbid  atomic.Bool
	watches chan *watch.RaceFreeFakeWatcher
}

func newFakeSource(items ...*table.RowObject) *fakeSource {
	f := &fakeSource{watches: make(chan *watch.RaceFreeFakeWatcher, 10)}
	for _, it := range items {
		f.items = append(f.items, *it)
	}
	return f
}

func (f *fakeSource) ListWithContext(context.Context, metav1.ListOptions) (runtime.Object, error) {
	f.lists.Add(1)
	if f.forbid.Load() {
		return nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil)
	}
	l := &table.RowList{Items: f.items}
	l.ResourceVersion = "1"
	return l, nil
}

func (f *fakeSource) WatchWithContext(context.Context, metav1.ListOptions) (watch.Interface, error) {
	w := watch.NewRaceFreeFake()
	f.watches <- w
	return w, nil
}

func (f *fakeSource) Columns() []resources.Column { return []resources.Column{{Name: "Name"}} }

// recorder is a Handler that records everything it receives.
type recorder struct {
	mu      sync.Mutex
	rows    map[string]string // name -> resourceVersion
	deleted []string
	synced  chan []resources.Column
	errs    chan error
}

func newRecorder() *recorder {
	return &recorder{rows: map[string]string{}, synced: make(chan []resources.Column, 1), errs: make(chan error, 10)}
}

func (r *recorder) Upsert(o *table.RowObject) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[o.Name] = o.ResourceVersion
}

func (r *recorder) Delete(o *table.RowObject) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rows, o.Name)
	r.deleted = append(r.deleted, o.Name)
}

func (r *recorder) Synced(c []resources.Column) { r.synced <- c }
func (r *recorder) Error(err error)             { r.errs <- err }

func (r *recorder) waitSynced(t *testing.T) []resources.Column {
	t.Helper()
	select {
	case c := <-r.synced:
		return c
	case <-time.After(timeout):
		t.Fatal("not synced")
		return nil
	}
}

func (r *recorder) waitRow(t *testing.T, name, rv string) {
	t.Helper()
	eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.rows[name] == rv
	}, "row %s@%s", name, rv)
}

func (r *recorder) waitErr(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.errs:
		return err
	case <-time.After(timeout):
		t.Fatal("no error reported")
		return nil
	}
}

func eventually(t *testing.T, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for "+format, args...)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func session(t *testing.T) (cluster.Session, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return cluster.Session{ContextID: "c1", Ctx: ctx}, cancel
}

// sources returns a factory serving one fakeSource per key.
func sources(m map[Key]*fakeSource) SourceFactory {
	var mu sync.Mutex
	return func(_ cluster.Session, k Key) (Source, error) {
		mu.Lock()
		defer mu.Unlock()
		if m[k] == nil {
			m[k] = newFakeSource()
		}
		return m[k], nil
	}
}

func TestSubscribersShareOneInformer(t *testing.T) {
	s, _ := session(t)
	key := Key{ContextID: "c1", GVR: podsGVR, Namespace: "default"}
	src := newFakeSource(row("default", "a", "1"))
	c := New(Options{NewSource: sources(map[Key]*fakeSource{key: src})})
	defer c.Close()

	r1, r2 := newRecorder(), newRecorder()
	sub1, err := c.Subscribe(s, key, r1)
	if err != nil {
		t.Fatal(err)
	}
	defer sub1.Close()
	if cols := r1.waitSynced(t); len(cols) != 1 || cols[0].Name != "Name" {
		t.Errorf("columns = %+v", cols)
	}
	r1.waitRow(t, "a", "1")

	sub2, err := c.Subscribe(s, key, r2)
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	r2.waitSynced(t)
	r2.waitRow(t, "a", "1") // replayed to the late subscriber

	if n := src.lists.Load(); n != 1 {
		t.Errorf("LIST calls = %d, want 1 (shared informer)", n)
	}

	w := <-src.watches
	w.Add(row("default", "b", "2"))
	w.Modify(row("default", "a", "3"))
	w.Delete(row("default", "b", "4"))
	for _, r := range []*recorder{r1, r2} {
		r.waitRow(t, "a", "3")
		eventually(t, func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return len(r.deleted) == 1 && r.deleted[0] == "b"
		}, "delete of b")
	}
}

func TestIdleStop(t *testing.T) {
	s, _ := session(t)
	clk := clocktesting.NewFakeClock(time.Now())
	key := Key{ContextID: "c1", GVR: podsGVR}
	c := New(Options{NewSource: sources(map[Key]*fakeSource{}), Clock: clk, IdleTimeout: time.Minute})
	defer c.Close()

	sub, _ := c.Subscribe(s, key, newRecorder())
	sub.Close()
	sub.Close() // idempotent
	if !c.Running(key) {
		t.Fatal("stopped immediately; want it kept for the idle timeout")
	}

	// Re-subscribing within the timeout keeps it alive.
	clk.Step(30 * time.Second)
	sub, _ = c.Subscribe(s, key, newRecorder())
	clk.Step(time.Minute)
	time.Sleep(20 * time.Millisecond)
	if !c.Running(key) {
		t.Fatal("stopped while subscribed")
	}

	sub.Close()
	clk.Step(59 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if !c.Running(key) {
		t.Fatal("stopped before the idle timeout")
	}
	clk.Step(time.Second)
	eventually(t, func() bool { return !c.Running(key) }, "idle stop")
}

func TestEvictsLongestIdle(t *testing.T) {
	s, _ := session(t)
	clk := clocktesting.NewFakeClock(time.Now())
	c := New(Options{NewSource: sources(map[Key]*fakeSource{}), Clock: clk, MaxPerCluster: 2})
	defer c.Close()
	k := func(ns string) Key { return Key{ContextID: "c1", GVR: podsGVR, Namespace: ns} }

	a, _ := c.Subscribe(s, k("a"), newRecorder())
	b, _ := c.Subscribe(s, k("b"), newRecorder())
	a.Close()
	clk.Step(time.Second)
	b.Close()

	if _, err := c.Subscribe(s, k("c"), newRecorder()); err != nil {
		t.Fatal(err)
	}
	if c.Running(k("a")) || !c.Running(k("b")) || !c.Running(k("c")) {
		t.Errorf("running a=%v b=%v c=%v; want the longest-idle (a) evicted",
			c.Running(k("a")), c.Running(k("b")), c.Running(k("c")))
	}

	// With nothing idle, the limit is exceeded rather than breaking a view.
	if _, err := c.Subscribe(s, k("b"), newRecorder()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Subscribe(s, k("d"), newRecorder()); err != nil {
		t.Fatal(err)
	}
	if !c.Running(k("b")) || !c.Running(k("c")) || !c.Running(k("d")) {
		t.Error("an informer in use was evicted")
	}
}

func TestErrorAndRecovery(t *testing.T) {
	s, _ := session(t)
	key := Key{ContextID: "c1", GVR: podsGVR, Namespace: "kube-system"}
	src := newFakeSource(row("kube-system", "k", "1"))
	src.forbid.Store(true)
	c := New(Options{NewSource: sources(map[Key]*fakeSource{key: src})})
	defer c.Close()

	r1 := newRecorder()
	sub, _ := c.Subscribe(s, key, r1)
	defer sub.Close()
	if err := r1.waitErr(t); !apierrors.IsForbidden(err) {
		t.Fatalf("err = %v, want Forbidden", err)
	}

	// A later subscriber learns about the current error at once.
	r2 := newRecorder()
	sub2, _ := c.Subscribe(s, key, r2)
	defer sub2.Close()
	if err := r2.waitErr(t); !apierrors.IsForbidden(err) {
		t.Fatalf("late subscriber err = %v, want Forbidden", err)
	}

	src.forbid.Store(false) // e.g. RBAC granted; the reflector retries
	if err := r1.waitErr(t); err != nil {
		t.Fatalf("err = %v, want nil after recovery", err)
	}
	r1.waitSynced(t)
	r1.waitRow(t, "k", "1")
}

func TestConnectionEndStopsInformers(t *testing.T) {
	s, cancel := session(t)
	key := Key{ContextID: "c1", GVR: podsGVR}
	srcs := map[Key]*fakeSource{}
	c := New(Options{NewSource: sources(srcs)})
	defer c.Close()

	sub, _ := c.Subscribe(s, key, newRecorder())
	defer sub.Close()
	cancel()
	eventually(t, func() bool { return !c.Running(key) }, "stop on connection end")

	// After a reconnect, a new session starts a new informer.
	s2, _ := session(t)
	r := newRecorder()
	sub2, err := c.Subscribe(s2, key, r)
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	r.waitSynced(t)
	if !c.Running(key) {
		t.Error("not running after resubscribe")
	}
	if n := srcs[key].lists.Load(); n != 2 {
		t.Errorf("LIST calls = %d, want 2", n)
	}
}
