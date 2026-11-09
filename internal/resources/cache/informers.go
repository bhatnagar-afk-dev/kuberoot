// Package cache keeps lazily started, shared informers of table rows, one per
// (context, resource type, namespace). Informers start on first subscribe,
// are shared by all subscribers, and stop after an idle period with none.
package cache

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	k8scache "k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/table"
)

// Defaults for Options.
const (
	DefaultIdleTimeout   = 60 * time.Second
	DefaultMaxPerCluster = 30

	syncPoll = 20 * time.Millisecond
)

// Key identifies one informer. Namespace "" means all namespaces (or a
// cluster-scoped type).
type Key struct {
	ContextID string
	GVR       schema.GroupVersionResource
	Namespace string
}

// Source lists and watches rows for one Key. *table.Source implements it.
type Source interface {
	k8scache.ListerWatcherWithContext
	Columns() []resources.Column
}

// SourceFactory creates the Source for a key.
type SourceFactory func(s cluster.Session, k Key) (Source, error)

// TableSources is the default SourceFactory.
func TableSources(s cluster.Session, k Key) (Source, error) {
	return table.NewSource(s.Config, k.GVR, k.Namespace)
}

// Handler receives a subscription's events. Upsert and Delete calls are
// sequential; Synced and Error may be called concurrently with them, so
// implementations must lock. Handlers must not call back into Informers or
// close a Subscription while holding a lock that their own methods take.
type Handler interface {
	Upsert(*table.RowObject)
	Delete(*table.RowObject)
	// Synced is called once, after the initial rows have been delivered.
	Synced(columns []resources.Column)
	// Error reports a failure to list or watch, or nil once listing works
	// again.
	Error(error)
}

// Subscription is an active subscription; Close ends it.
type Subscription interface {
	Close()
}

// Options tunes Informers. Zero values select defaults.
type Options struct {
	IdleTimeout   time.Duration
	MaxPerCluster int // a soft limit: idle informers are stopped first
	NewSource     SourceFactory
	Clock         clock.WithDelayedExecution
}

// Informers is the informer cache. It is safe for concurrent use.
type Informers struct {
	opts Options

	mu      sync.Mutex
	entries map[Key]*entry
}

// New returns an empty cache.
func New(opts Options) *Informers {
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.MaxPerCluster <= 0 {
		opts.MaxPerCluster = DefaultMaxPerCluster
	}
	if opts.NewSource == nil {
		opts.NewSource = TableSources
	}
	if opts.Clock == nil {
		opts.Clock = clock.RealClock{}
	}
	return &Informers{opts: opts, entries: map[Key]*entry{}}
}

type entry struct {
	key      Key
	source   Source
	informer k8scache.SharedIndexInformer
	ctx      context.Context
	cancel   context.CancelFunc

	// Guarded by Informers.mu.
	subs      int
	idleSince time.Time
	idleTimer clock.Timer

	errMu    sync.Mutex
	err      error
	handlers map[*subscription]struct{}
}

// Subscribe starts (or joins) the informer for k and streams its rows to h.
// The informer stops when s.Ctx ends.
func (c *Informers) Subscribe(s cluster.Session, k Key, h Handler) (Subscription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := c.entries[k]
	if e != nil && e.ctx.Err() != nil { // its connection ended
		c.removeLocked(e)
		e = nil
	}
	if e == nil {
		var err error
		if e, err = c.startLocked(s, k); err != nil {
			return nil, err
		}
	}
	e.subs++
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}

	sub := &subscription{cache: c, entry: e, handler: h}
	sub.ctx, sub.cancel = context.WithCancel(e.ctx)
	reg, err := e.informer.AddEventHandler(sub)
	if err != nil {
		sub.cancel()
		c.releaseLocked(e)
		return nil, err
	}
	sub.reg = reg
	e.addHandler(sub)
	go sub.waitSynced()
	return sub, nil
}

// Running reports whether an informer for k is running.
func (c *Informers) Running(k Key) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[k]
	return e != nil && e.ctx.Err() == nil
}

// Close stops every informer.
func (c *Informers) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		c.removeLocked(e)
	}
}

func (c *Informers) startLocked(s cluster.Session, k Key) (*entry, error) {
	c.evictLocked(k.ContextID)
	src, err := c.opts.NewSource(s, k)
	if err != nil {
		return nil, err
	}
	e := &entry{key: k, source: src, handlers: map[*subscription]struct{}{}}
	e.ctx, e.cancel = context.WithCancel(s.Ctx)
	e.informer = k8scache.NewSharedIndexInformerWithOptions(&tracked{src, e}, &table.RowObject{}, k8scache.SharedIndexInformerOptions{})
	_ = e.informer.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *k8scache.Reflector, err error) {
		if !routine(err) {
			e.setErr(err)
		}
	})
	c.entries[k] = e
	go e.informer.RunWithContext(e.ctx)
	go func() {
		<-e.ctx.Done()
		c.mu.Lock()
		c.removeLocked(e)
		c.mu.Unlock()
	}()
	return e, nil
}

// evictLocked stops the longest-idle informers of a cluster until there is
// room for one more. Informers in use are never stopped.
func (c *Informers) evictLocked(contextID string) {
	for {
		n := 0
		var oldest *entry
		for _, e := range c.entries {
			if e.key.ContextID != contextID {
				continue
			}
			n++
			if e.subs == 0 && (oldest == nil || e.idleSince.Before(oldest.idleSince)) {
				oldest = e
			}
		}
		if n < c.opts.MaxPerCluster || oldest == nil {
			return
		}
		c.removeLocked(oldest)
	}
}

func (c *Informers) removeLocked(e *entry) {
	if c.entries[e.key] == e {
		delete(c.entries, e.key)
	}
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	e.cancel()
}

// releaseLocked drops one subscriber and starts the idle timer at zero.
func (c *Informers) releaseLocked(e *entry) {
	e.subs--
	if e.subs > 0 || c.entries[e.key] != e {
		return
	}
	e.idleSince = c.opts.Clock.Now()
	// The callback runs on its own goroutine so it never takes c.mu while a
	// clock implementation holds its own lock (releaseLocked takes them in
	// the opposite order).
	e.idleTimer = c.opts.Clock.AfterFunc(c.opts.IdleTimeout, func() {
		go func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if e.subs == 0 && c.entries[e.key] == e {
				c.removeLocked(e)
			}
		}()
	})
}

// routine reports errors that are part of normal watch operation.
func routine(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
		apierrors.IsResourceExpired(err) || apierrors.IsGone(err)
}

func (e *entry) addHandler(s *subscription) {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	e.handlers[s] = struct{}{}
	if e.err != nil {
		s.handler.Error(e.err)
	}
}

func (e *entry) removeHandler(s *subscription) {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	delete(e.handlers, s)
}

// setErr records the informer's error state and tells subscribers when it
// changes.
func (e *entry) setErr(err error) {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	if (e.err == nil) == (err == nil) && (err == nil || err.Error() == e.err.Error()) {
		return
	}
	e.err = err
	for s := range e.handlers {
		s.handler.Error(err)
	}
}

// tracked wraps a Source to clear the error state once listing succeeds.
type tracked struct {
	Source
	e *entry
}

func (t *tracked) ListWithContext(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
	obj, err := t.Source.ListWithContext(ctx, opts)
	if err == nil {
		t.e.setErr(nil)
	}
	return obj, err
}

func (t *tracked) List(opts metav1.ListOptions) (runtime.Object, error) {
	return t.ListWithContext(context.Background(), opts)
}

func (t *tracked) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	return t.WatchWithContext(context.Background(), opts)
}

// IsWatchListSemanticsUnSupported makes the reflector use LIST + WATCH; see
// table.Source.
func (t *tracked) IsWatchListSemanticsUnSupported() bool { return true }

type subscription struct {
	cache   *Informers
	entry   *entry
	handler Handler
	reg     k8scache.ResourceEventHandlerRegistration
	ctx     context.Context
	cancel  context.CancelFunc
	once    sync.Once
}

func (s *subscription) closed() bool { return s.ctx.Err() != nil }

func (s *subscription) waitSynced() {
	err := wait.PollUntilContextCancel(s.ctx, syncPoll, true, func(context.Context) (bool, error) {
		return s.reg.HasSynced(), nil
	})
	if err == nil && !s.closed() {
		s.handler.Synced(s.entry.source.Columns())
	}
}

// Close implements Subscription.
func (s *subscription) Close() {
	s.once.Do(func() {
		s.cancel()
		s.entry.removeHandler(s)
		_ = s.entry.informer.RemoveEventHandler(s.reg)
		s.cache.mu.Lock()
		s.cache.releaseLocked(s.entry)
		s.cache.mu.Unlock()
	})
}

// OnAdd implements k8scache.ResourceEventHandler.
func (s *subscription) OnAdd(obj any, _ bool) {
	if r, ok := obj.(*table.RowObject); ok && !s.closed() {
		s.handler.Upsert(r)
	}
}

// OnUpdate implements k8scache.ResourceEventHandler.
func (s *subscription) OnUpdate(_, obj any) { s.OnAdd(obj, false) }

// OnDelete implements k8scache.ResourceEventHandler.
func (s *subscription) OnDelete(obj any) {
	if t, ok := obj.(k8scache.DeletedFinalStateUnknown); ok {
		obj = t.Obj
	}
	if r, ok := obj.(*table.RowObject); ok && !s.closed() {
		s.handler.Delete(r)
	}
}
