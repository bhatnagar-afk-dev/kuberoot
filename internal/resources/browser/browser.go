// Package browser is the entry point the app uses to browse resources. It
// owns the open views and connects them to the cluster sessions, the
// informer cache and the UI.
package browser

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/redact"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/view"
	"kuberoot.in/kuberoot/internal/resources/workload"
)

// SinkFactory returns the sink that receives one view's output.
type SinkFactory func(req resources.ViewRequest) resources.Sink

// ErrClosed is returned by Open after Close.
var ErrClosed = errors.New("resource browser is closed")

// Browser manages the open views. It is safe for concurrent use.
type Browser struct {
	conns resources.Connections
	subs  view.Subscriber
	pods  workload.PodMatcher
	sinks SinkFactory
	opts  view.Options

	mu      sync.Mutex
	views   map[string]closer
	pending map[string]bool // views being opened; true if Close was called meanwhile
	closed  bool
}

// closer is an open view.
type closer interface{ Close() }

// New returns a Browser. pods finds the pods of workloads for OpenPods; opts
// tunes every view it opens.
func New(conns resources.Connections, subs view.Subscriber, pods workload.PodMatcher, sinks SinkFactory, opts view.Options) *Browser {
	return &Browser{conns: conns, subs: subs, pods: pods, sinks: sinks, opts: opts, views: map[string]closer{}, pending: map[string]bool{}}
}

// Open starts a view. Its rows and status go to the sink for req.ViewID.
func (b *Browser) Open(req resources.ViewRequest) error {
	return b.start(req, b.openView(req, view.Request{ViewID: req.ViewID, Type: req.Type, Namespaces: req.Namespaces}))
}

// OpenEvents starts a view of the events about one object, like the Events
// section of `kubectl describe`. Its rows go to the sink for viewID.
func (b *Browser) OpenEvents(viewID string, ref resources.ObjectRef) error {
	vr := EventsRequest(viewID, ref)
	req := resources.ViewRequest{ViewID: viewID, ContextID: ref.ContextID, Type: vr.Type, Namespaces: vr.Namespaces}
	return b.start(req, b.openView(req, vr))
}

// OpenPods starts a view of the pods of a workload (the pods it owns) or a
// Service (the pods its selector matches). Its rows go to the sink for
// viewID. An object that does not select pods is an error wrapping
// workload.ErrNoPods.
func (b *Browser) OpenPods(viewID string, ref resources.ObjectRef) error {
	req := resources.ViewRequest{ViewID: viewID, ContextID: ref.ContextID, Type: workload.PodsType, Namespaces: []string{ref.Namespace}}
	return b.start(req, func(s cluster.Session) (closer, error) {
		ctx, cancel := context.WithTimeout(s.Ctx, objectTimeout)
		defer cancel()
		m, err := b.pods.Match(ctx, s, ref)
		if err != nil {
			return nil, err
		}
		vr := view.Request{
			ViewID:        viewID,
			Type:          workload.PodsType,
			Namespaces:    req.Namespaces,
			LabelSelector: m.Selector().String(),
			Filter:        ownedBy{m},
		}
		v, err := b.openView(req, vr)(s)
		if err != nil {
			m.Close()
			return nil, err
		}
		return podsView{v, m}, nil
	})
}

// openView returns a function that opens vr; req describes it to the sink
// factory.
func (b *Browser) openView(req resources.ViewRequest, vr view.Request) func(cluster.Session) (closer, error) {
	return func(s cluster.Session) (closer, error) {
		return view.Open(s, b.subs, vr, b.sinks(req), b.opts)
	}
}

// ownedBy filters a pods view by a workload's Membership.
type ownedBy struct{ workload.Membership }

func (o ownedBy) Keep(pod metav1.Object) bool { return o.Owns(pod) }

// podsView is a pods view and the membership that filters it.
type podsView struct {
	closer
	m workload.Membership
}

func (p podsView) Close() {
	p.closer.Close()
	p.m.Close()
}

// start opens a view with openFn, once the ID is reserved; req names it.
func (b *Browser) start(req resources.ViewRequest, openFn func(cluster.Session) (closer, error)) error {
	if req.ViewID == "" {
		return errors.New("view ID is empty")
	}
	b.mu.Lock()
	_, open := b.views[req.ViewID]
	_, opening := b.pending[req.ViewID]
	switch {
	case b.closed:
		b.mu.Unlock()
		return ErrClosed
	case open || opening:
		b.mu.Unlock()
		return fmt.Errorf("view %q is already open", req.ViewID)
	}
	b.pending[req.ViewID] = false // reserve the ID without holding the lock while opening
	b.mu.Unlock()

	v, err := b.open(req, openFn)

	b.mu.Lock()
	closed := b.closed
	cancelled := b.pending[req.ViewID] || closed
	delete(b.pending, req.ViewID)
	if err == nil && !cancelled {
		b.views[req.ViewID] = v
	}
	b.mu.Unlock()

	if err == nil && cancelled {
		v.Close()
		if closed {
			return ErrClosed
		}
	}
	return err
}

func (b *Browser) open(req resources.ViewRequest, openFn func(cluster.Session) (closer, error)) (closer, error) {
	s, err := b.conns.Session(req.ContextID)
	if err != nil {
		return nil, errors.New(redact.String(err.Error()))
	}
	v, err := openFn(s)
	switch {
	case errors.Is(err, workload.ErrNoPods):
		return nil, err // says what it is; nothing to redact
	case err != nil:
		return nil, fmt.Errorf("open %s: %s", req.Type.Resource, redact.String(err.Error()))
	}
	return v, nil
}

// Close stops a view. A view still being opened is stopped once Open
// finishes. Unknown IDs are ignored.
func (b *Browser) Close(viewID string) {
	b.mu.Lock()
	v := b.views[viewID]
	delete(b.views, viewID)
	if _, opening := b.pending[viewID]; opening {
		b.pending[viewID] = true
	}
	b.mu.Unlock()
	if v != nil {
		v.Close()
	}
}

// CloseAll stops every view; later calls to Open fail.
func (b *Browser) CloseAll() {
	b.mu.Lock()
	b.closed = true
	open := slices.Collect(maps.Values(b.views))
	clear(b.views)
	b.mu.Unlock()
	for _, v := range open {
		v.Close()
	}
}
