// Package browser is the entry point the app uses to browse resources. It
// owns the open views and connects them to the cluster sessions, the
// informer cache and the UI.
package browser

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"kuberoot.in/kuberoot/internal/redact"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/view"
)

// SinkFactory returns the sink that receives one view's output.
type SinkFactory func(req resources.ViewRequest) resources.Sink

// ErrClosed is returned by Open after Close.
var ErrClosed = errors.New("resource browser is closed")

// Browser manages the open views. It is safe for concurrent use.
type Browser struct {
	conns resources.Connections
	subs  view.Subscriber
	sinks SinkFactory
	opts  view.Options

	mu      sync.Mutex
	views   map[string]*view.View
	pending map[string]bool // views being opened; true if Close was called meanwhile
	closed  bool
}

// New returns a Browser. opts tunes every view it opens.
func New(conns resources.Connections, subs view.Subscriber, sinks SinkFactory, opts view.Options) *Browser {
	return &Browser{conns: conns, subs: subs, sinks: sinks, opts: opts, views: map[string]*view.View{}, pending: map[string]bool{}}
}

// Open starts a view. Its rows and status go to the sink for req.ViewID.
func (b *Browser) Open(req resources.ViewRequest) error {
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

	v, err := b.open(req)

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

func (b *Browser) open(req resources.ViewRequest) (*view.View, error) {
	s, err := b.conns.Session(req.ContextID)
	if err != nil {
		return nil, errors.New(redact.String(err.Error()))
	}
	v, err := view.Open(s, b.subs, view.Request{
		ViewID:     req.ViewID,
		Type:       req.Type,
		Namespaces: req.Namespaces,
	}, b.sinks(req), b.opts)
	if err != nil {
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
