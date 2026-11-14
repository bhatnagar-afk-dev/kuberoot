// Package view implements a live table view: the rows of one resource type
// across a set of namespaces, merged from shared informers and delivered to a
// Sink in batches.
package view

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/clock"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/redact"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/table"
)

// Defaults for Options.
const (
	DefaultFlushInterval = 100 * time.Millisecond // at most 10 batches/s
	DefaultChunkSize     = 1000
)

// Subscriber is the part of the informer cache a view uses.
// *cache.Informers implements it.
type Subscriber interface {
	Subscribe(s cluster.Session, k cache.Key, h cache.Handler) (cache.Subscription, error)
	Running(k cache.Key) bool
}

// Request describes what a view shows.
type Request struct {
	ViewID     string
	Type       resources.ResourceType
	Namespaces []string // empty means all namespaces; ignored for cluster-scoped types
	// FieldSelector, if set, limits the rows (e.g. the events of one object).
	FieldSelector string
}

// Options tunes a View. Zero values select defaults.
type Options struct {
	FlushInterval time.Duration
	ChunkSize     int // maximum rows per batch
	Clock         clock.WithTicker
}

// View is one open table. Close it when it is no longer shown.
type View struct {
	id   string
	typ  resources.ResourceType
	sink resources.Sink
	opts Options

	mu          sync.Mutex
	parts       map[string]*part // by namespace; "" for a cluster-wide informer
	rows        map[string]resources.Row
	dirty       map[string]struct{} // UIDs changed since the last flush
	added       map[string]struct{} // UIDs first seen since the last flush (the UI doesn't know them)
	deleted     map[string]struct{} // UIDs removed since the last flush
	columns     []resources.Column
	initialSent bool
	lastStatus  resources.ViewStatus

	subs      []cache.Subscription
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// part is the state of one informer subscription.
type part struct {
	synced bool
	err    error
}

// Open subscribes to the informers for req and starts delivering to sink.
func Open(s cluster.Session, subs Subscriber, req Request, sink resources.Sink, opts Options) (*View, error) {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = DefaultFlushInterval
	}
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = DefaultChunkSize
	}
	if opts.Clock == nil {
		opts.Clock = clock.RealClock{}
	}
	v := &View{
		id:      req.ViewID,
		typ:     req.Type,
		sink:    sink,
		opts:    opts,
		parts:   map[string]*part{},
		rows:    map[string]resources.Row{},
		dirty:   map[string]struct{}{},
		added:   map[string]struct{}{},
		deleted: map[string]struct{}{},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}

	keys, filter := plan(s.ContextID, req, subs)
	for _, k := range keys {
		v.parts[k.Namespace] = &part{}
	}
	for _, k := range keys {
		sub, err := subs.Subscribe(s, k, &handler{view: v, part: k.Namespace, filter: filter})
		if err != nil {
			v.closeSubs()
			return nil, err
		}
		v.subs = append(v.subs, sub)
	}
	go v.loop(s)
	return v, nil
}

// plan picks the informers for a request. It returns one cluster-wide key
// when all namespaces are requested, the type is cluster-scoped, or a
// cluster-wide informer for the type is already running (then filter limits
// it to the requested namespaces). Otherwise it returns one key per
// namespace.
func plan(contextID string, req Request, subs Subscriber) (keys []cache.Key, filter map[string]bool) {
	all := cache.Key{ContextID: contextID, GVR: req.Type.GVR(), FieldSelector: req.FieldSelector}
	namespaces := slices.Compact(slices.Sorted(slices.Values(req.Namespaces)))
	if !req.Type.Namespaced || len(namespaces) == 0 {
		return []cache.Key{all}, nil
	}
	if subs.Running(all) {
		filter = map[string]bool{}
		for _, ns := range namespaces {
			filter[ns] = true
		}
		return []cache.Key{all}, filter
	}
	for _, ns := range namespaces {
		k := all
		k.Namespace = ns
		keys = append(keys, k)
	}
	return keys, nil
}

// Close stops the view. No calls to the sink are made after it returns.
func (v *View) Close() {
	v.closeOnce.Do(func() {
		close(v.stop)
		<-v.done
		// Subscriptions are closed without holding v.mu: the cache may be
		// calling a handler, which takes v.mu, while it holds its own lock.
		v.closeSubs()
	})
}

func (v *View) closeSubs() {
	for _, s := range v.subs {
		s.Close()
	}
}

func (v *View) loop(s cluster.Session) {
	defer close(v.done)
	t := v.opts.Clock.NewTicker(v.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-v.stop:
			return
		case <-s.Ctx.Done():
			v.flush()
			v.sink.Status(resources.ViewStatus{ViewID: v.id, Phase: resources.ViewStale, Message: "Disconnected from the cluster"})
			return
		case <-t.C():
			v.flush()
		}
	}
}

// flush sends the current status (if changed) and pending row changes.
func (v *View) flush() {
	v.mu.Lock()
	status := v.statusLocked()
	sendStatus := !sameStatus(status, v.lastStatus)
	v.lastStatus = status

	var batches []resources.RowBatch
	switch {
	case !v.initialSent && status.Phase != resources.ViewLoading:
		batches = v.chunk(sortedRows(slices.Collect(maps.Values(v.rows))), nil)
		batches[0].Reset, batches[0].Columns = true, v.columns
		v.initialSent = true
	case v.initialSent && (len(v.dirty) > 0 || len(v.deleted) > 0):
		up := make([]resources.Row, 0, len(v.dirty))
		for uid := range v.dirty {
			up = append(up, v.rows[uid])
		}
		batches = v.chunk(sortedRows(up), slices.Sorted(maps.Keys(v.deleted)))
	}
	clear(v.dirty)
	clear(v.added)
	clear(v.deleted)
	v.mu.Unlock()

	// Status first, so the UI knows the phase before the rows arrive.
	if sendStatus {
		v.sink.Status(status)
	}
	for _, b := range batches {
		v.sink.Send(b)
	}
}

// chunk splits rows into batches of at most ChunkSize; deletes go in the
// first batch. It always returns at least one batch.
func (v *View) chunk(up []resources.Row, deletes []string) []resources.RowBatch {
	var out []resources.RowBatch
	for len(up) > 0 || len(out) == 0 {
		n := min(len(up), v.opts.ChunkSize)
		rows := up[:n:n]
		if rows == nil {
			rows = []resources.Row{} // the UI gets [], not null
		}
		out = append(out, resources.RowBatch{ViewID: v.id, Upserts: rows, Deletes: []string{}})
		up = up[n:]
	}
	if deletes != nil {
		out[0].Deletes = deletes
	}
	return out
}

func (v *View) statusLocked() resources.ViewStatus {
	st := resources.ViewStatus{ViewID: v.id, Phase: resources.ViewReady}
	failed, loading := 0, false
	var other []string
	for _, ns := range slices.Sorted(maps.Keys(v.parts)) {
		p := v.parts[ns]
		switch {
		case apierrors.IsForbidden(p.err):
			st.Forbidden = append(st.Forbidden, ns)
			failed++
		case p.err != nil:
			if apierrors.IsNotFound(p.err) {
				st.Reason = resources.ReasonNotFound
			}
			other = append(other, redact.String(p.err.Error()))
			failed++
		case !p.synced:
			loading = true
		}
	}
	switch {
	case failed == len(v.parts):
		st.Phase = resources.ViewError
	case loading:
		st.Phase = resources.ViewLoading
	}

	var msgs []string
	if len(st.Forbidden) > 0 {
		if st.Forbidden[0] == "" {
			msgs = append(msgs, fmt.Sprintf("No permission to list %s", v.typ.Resource))
		} else {
			msgs = append(msgs, fmt.Sprintf("No permission to list %s in %s", v.typ.Resource, quoteList(st.Forbidden)))
		}
	}
	if len(other) > 0 {
		msgs = append(msgs, fmt.Sprintf("Could not list %s: %s", v.typ.Resource, other[0]))
	}
	st.Message = strings.Join(msgs, ". ")
	return st
}

func sameStatus(a, b resources.ViewStatus) bool {
	return a.ViewID == b.ViewID && a.Phase == b.Phase && a.Reason == b.Reason && a.Message == b.Message && slices.Equal(a.Forbidden, b.Forbidden)
}

func quoteList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(q, ", ")
}

func sortedRows(rows []resources.Row) []resources.Row {
	slices.SortFunc(rows, func(a, b resources.Row) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name), cmp.Compare(a.UID, b.UID))
	})
	return rows
}

// handler feeds one subscription into the view.
type handler struct {
	view   *View
	part   string
	filter map[string]bool // nil means keep all rows
}

func (h *handler) keep(o *table.RowObject) bool { return h.filter == nil || h.filter[o.Namespace] }

func (h *handler) Upsert(o *table.RowObject) {
	if !h.keep(o) {
		return
	}
	v := h.view
	r := o.Row()
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, known := v.rows[r.UID]; !known {
		v.added[r.UID] = struct{}{}
	}
	v.rows[r.UID] = r
	v.dirty[r.UID] = struct{}{}
	delete(v.deleted, r.UID)
}

func (h *handler) Delete(o *table.RowObject) {
	if !h.keep(o) {
		return
	}
	v := h.view
	uid := string(o.UID)
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.rows[uid]; !ok {
		return
	}
	delete(v.rows, uid)
	delete(v.dirty, uid)
	if _, unsent := v.added[uid]; unsent {
		delete(v.added, uid) // added and removed between flushes: the UI never saw it
		return
	}
	if v.initialSent {
		v.deleted[uid] = struct{}{}
	}
}

func (h *handler) Synced(columns []resources.Column) {
	v := h.view
	v.mu.Lock()
	defer v.mu.Unlock()
	v.parts[h.part].synced = true
	if v.columns == nil && len(columns) > 0 {
		v.columns = columns
	}
}

func (h *handler) Error(err error) {
	v := h.view
	v.mu.Lock()
	defer v.mu.Unlock()
	v.parts[h.part].err = err
}
