package cluster

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// ErrNotConnected is returned when a context has no healthy connection.
var ErrNotConnected = errors.New("cluster is not connected")

// Session holds the clients for one connected cluster. Ctx is cancelled when
// the connection is dropped or replaced, so anything started with it (such as
// informers) stops with the connection.
type Session struct {
	ContextID string
	Ctx       context.Context
	Config    *rest.Config // a copy; callers may modify it
	Dynamic   dynamic.Interface
	Discovery discovery.CachedDiscoveryInterface
	Mapper    meta.ResettableRESTMapper
}

// Session returns the clients for a connected context. They are created on
// first use and shared until the connection ends.
func (m *Manager) Session(id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	if !ok || c.status.Phase != PhaseConnected {
		return Session{}, ErrNotConnected
	}
	if c.session == nil {
		s, err := newSession(c.ctx, id, c.cfg)
		if err != nil {
			return Session{}, err
		}
		c.session = &s
	}
	s := *c.session
	s.Config = rest.CopyConfig(s.Config)
	return s, nil
}

func newSession(ctx context.Context, id string, cfg *rest.Config) (Session, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return Session{}, fmt.Errorf("dynamic client: %w", err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return Session{}, fmt.Errorf("discovery client: %w", err)
	}
	cached := memory.NewMemCacheClient(disc)
	return Session{
		ContextID: id,
		Ctx:       ctx,
		Config:    rest.CopyConfig(cfg),
		Dynamic:   dyn,
		Discovery: cached,
		Mapper:    restmapper.NewDeferredDiscoveryRESTMapper(cached),
	}, nil
}
