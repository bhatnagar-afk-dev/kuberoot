package browser

import (
	"context"
	"errors"
	"sync"
	"time"

	"k8s.io/utils/clock"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/redact"
	"kuberoot.in/kuberoot/internal/resources"
)

// Defaults for CatalogOptions.
const (
	DefaultCheckInterval = 5 * time.Minute
	DefaultCheckTimeout  = 30 * time.Second
)

// CatalogFactory returns a catalog for one connected session.
type CatalogFactory func(cluster.Session) (resources.Catalog, error)

// CatalogOptions tunes Catalogs. Zero values select defaults.
type CatalogOptions struct {
	CheckInterval time.Duration // how often to look for changed resource types
	CheckTimeout  time.Duration // limit for one check or listing
	Clock         clock.WithTicker
}

// Catalogs builds the navigation of each connected cluster and notices when
// its resource types change. It keeps one catalog per connection: a new
// connection to a context starts with a new catalog. It is safe for
// concurrent use.
type Catalogs struct {
	conns      resources.Connections
	newCatalog CatalogFactory
	nav        resources.Navigator
	onStale    func(contextID string)
	opts       CatalogOptions

	mu      sync.Mutex
	entries map[string]*entry // by context ID
	stop    chan struct{}
	closed  bool
}

// entry is the catalog of one connection.
type entry struct {
	ctx     context.Context // the connection's; done when it ends
	catalog resources.Catalog

	mu       sync.Mutex
	stale    bool // onStale was called since the last refresh
	checking bool
	gen      int // incremented by each refresh; a check from an older one is ignored
}

// NewCatalogs returns Catalogs that call onStale (from any goroutine) once
// each time a cluster's resource types are found to have changed.
func NewCatalogs(conns resources.Connections, newCatalog CatalogFactory, nav resources.Navigator,
	onStale func(contextID string), opts CatalogOptions,
) *Catalogs {
	if opts.CheckInterval <= 0 {
		opts.CheckInterval = DefaultCheckInterval
	}
	if opts.CheckTimeout <= 0 {
		opts.CheckTimeout = DefaultCheckTimeout
	}
	if opts.Clock == nil {
		opts.Clock = clock.RealClock{}
	}
	return &Catalogs{
		conns: conns, newCatalog: newCatalog, nav: nav, onStale: onStale, opts: opts,
		entries: map[string]*entry{}, stop: make(chan struct{}),
	}
}

// Navigation returns the navigation tree of a connected cluster, from cached
// discovery when possible. Calling it for a cluster that already has a tree
// (e.g. when the user switches back to it) also checks for changed types in
// the background.
func (c *Catalogs) Navigation(contextID string) (resources.Navigation, error) {
	e, created, err := c.entry(contextID)
	if err != nil {
		return resources.Navigation{}, err
	}
	nav, err := c.build(e, e.catalog.ResourceTypes)
	if err == nil && !created {
		c.check(contextID, e)
	}
	return nav, err
}

// RefreshNavigation drops the cluster's cached resource types and builds
// the tree again.
func (c *Catalogs) RefreshNavigation(contextID string) (resources.Navigation, error) {
	e, _, err := c.entry(contextID)
	if err != nil {
		return resources.Navigation{}, err
	}
	nav, err := c.build(e, e.catalog.Refresh)
	if err == nil {
		e.mu.Lock()
		e.stale = false
		e.gen++
		e.mu.Unlock()
		nav.Stale = false
	}
	return nav, err
}

// Check looks for changed resource types in the background, e.g. after a
// view found its type gone. It does nothing for a cluster without a tree.
func (c *Catalogs) Check(contextID string) {
	c.mu.Lock()
	e := c.entries[contextID]
	c.mu.Unlock()
	if e != nil {
		c.check(contextID, e)
	}
}

// Close stops all background checks.
func (c *Catalogs) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.stop)
		clear(c.entries)
	}
}

// entry returns the catalog of the context's current connection, creating
// it (and its periodic check) on first use.
func (c *Catalogs) entry(contextID string) (*entry, bool, error) {
	s, err := c.conns.Session(contextID)
	if err != nil {
		return nil, false, errors.New(redact.String(err.Error()))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, false, ErrClosed
	}
	if e := c.entries[contextID]; e != nil && e.ctx == s.Ctx {
		return e, false, nil
	}
	cat, err := c.newCatalog(s)
	if err != nil {
		return nil, false, errors.New(redact.String(err.Error()))
	}
	e := &entry{ctx: s.Ctx, catalog: cat}
	c.entries[contextID] = e
	go c.watch(contextID, e)
	return e, true, nil
}

func (c *Catalogs) build(e *entry, list func(context.Context) ([]resources.ResourceType, error)) (resources.Navigation, error) {
	ctx, cancel := context.WithTimeout(e.ctx, c.opts.CheckTimeout)
	defer cancel()
	types, err := list(ctx)
	var nav resources.Navigation
	var partial *resources.PartialDiscoveryError
	switch {
	case errors.As(err, &partial):
		nav.Failed = partial.GroupVersions
	case err != nil:
		return resources.Navigation{}, errors.New(redact.String(err.Error()))
	}
	nav.Groups = c.nav.Navigation(types)
	if nav.Groups == nil {
		nav.Groups = []resources.NavGroup{}
	}
	e.mu.Lock()
	nav.Stale = e.stale
	e.mu.Unlock()
	return nav, nil
}

// watch checks for changed types periodically until the connection ends.
func (c *Catalogs) watch(contextID string, e *entry) {
	t := c.opts.Clock.NewTicker(c.opts.CheckInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C():
			c.check(contextID, e)
		case <-e.ctx.Done():
			c.mu.Lock()
			if c.entries[contextID] == e {
				delete(c.entries, contextID)
			}
			c.mu.Unlock()
			return
		case <-c.stop:
			return
		}
	}
}

// check runs one background check unless one is running or the change was
// already reported.
func (c *Catalogs) check(contextID string, e *entry) {
	e.mu.Lock()
	if e.checking || e.stale {
		e.mu.Unlock()
		return
	}
	e.checking = true
	gen := e.gen
	e.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, c.opts.CheckTimeout)
		defer cancel()
		changed, err := e.catalog.Changed(ctx)

		e.mu.Lock()
		e.checking = false
		report := err == nil && changed && !e.stale && e.gen == gen
		if report {
			e.stale = true
		}
		e.mu.Unlock()
		if report {
			c.onStale(contextID)
		}
	}()
}
