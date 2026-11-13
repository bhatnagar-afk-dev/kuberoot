package browser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
)

// switchableConns returns sessions whose context can be replaced, like a
// reconnect.
type switchableConns struct {
	mu  sync.Mutex
	ctx context.Context
	err error
}

func (c *switchableConns) Session(id string) (cluster.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return cluster.Session{}, c.err
	}
	return cluster.Session{ContextID: id, Ctx: c.ctx}, nil
}

func (c *switchableConns) reconnect(t *testing.T) context.CancelFunc {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	c.ctx = ctx
	return cancel
}

type fakeCatalog struct {
	mu      sync.Mutex
	types   []resources.ResourceType
	err     error
	changed bool
	checks  int
}

func (f *fakeCatalog) ResourceTypes(context.Context) ([]resources.ResourceType, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.types, f.err
}

func (f *fakeCatalog) Changed(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.changed, nil
}

func (f *fakeCatalog) Refresh(context.Context) ([]resources.ResourceType, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changed = false
	return f.types, f.err
}

func (f *fakeCatalog) setChanged(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changed = v
}

func (f *fakeCatalog) checkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

// flatNav puts every type in one group.
type flatNav struct{}

func (flatNav) Navigation(types []resources.ResourceType) []resources.NavGroup {
	if len(types) == 0 {
		return nil
	}
	g := resources.NavGroup{Title: "All"}
	for _, t := range types {
		g.Items = append(g.Items, resources.NavItem{Title: t.Resource, Type: &t})
	}
	return []resources.NavGroup{g}
}

type catalogsFixture struct {
	c        *Catalogs
	conns    *switchableConns
	clock    *clocktesting.FakeClock
	catalogs []*fakeCatalog // one per connection, in creation order
	stale    chan string
	mu       sync.Mutex
}

func newCatalogsFixture(t *testing.T) *catalogsFixture {
	t.Helper()
	f := &catalogsFixture{
		conns: &switchableConns{ctx: t.Context()},
		clock: clocktesting.NewFakeClock(time.Now()),
		stale: make(chan string, 10),
	}
	factory := func(cluster.Session) (resources.Catalog, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		cat := &fakeCatalog{types: []resources.ResourceType{pods}}
		f.catalogs = append(f.catalogs, cat)
		return cat, nil
	}
	f.c = NewCatalogs(f.conns, factory, flatNav{}, func(id string) { f.stale <- id },
		CatalogOptions{CheckInterval: time.Minute, Clock: f.clock})
	t.Cleanup(f.c.Close)
	return f
}

func (f *catalogsFixture) catalog(i int) *fakeCatalog {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.catalogs[i]
}

func (f *catalogsFixture) created() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.catalogs)
}

// tick fires the periodic check once the ticker is waiting.
func (f *catalogsFixture) tick(t *testing.T) {
	t.Helper()
	waitFor(t, f.clock.HasWaiters)
	f.clock.Step(time.Minute)
}

func (f *catalogsFixture) expectStale(t *testing.T, want string) {
	t.Helper()
	select {
	case id := <-f.stale:
		if id != want {
			t.Errorf("stale context = %q, want %q", id, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("onStale was not called")
	}
}

func (f *catalogsFixture) expectNoStale(t *testing.T) {
	t.Helper()
	select {
	case id := <-f.stale:
		t.Fatalf("onStale(%q) called, want no call", id)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNavigationBuildsTree(t *testing.T) {
	f := newCatalogsFixture(t)
	nav, err := f.c.Navigation("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nav.Groups) != 1 || nav.Groups[0].Items[0].Title != "pods" || nav.Stale || len(nav.Failed) != 0 {
		t.Errorf("nav = %+v", nav)
	}
}

func TestNavigationPartialAndErrors(t *testing.T) {
	f := newCatalogsFixture(t)
	if _, err := f.c.Navigation("c1"); err != nil {
		t.Fatal(err)
	}
	cat := f.catalog(0)

	cat.err = &resources.PartialDiscoveryError{GroupVersions: []string{"metrics.k8s.io/v1beta1"}}
	nav, err := f.c.Navigation("c1")
	if err != nil {
		t.Fatalf("partial failure returned %v, want the working groups", err)
	}
	if len(nav.Groups) != 1 || len(nav.Failed) != 1 || nav.Failed[0] != "metrics.k8s.io/v1beta1" {
		t.Errorf("nav = %+v", nav)
	}

	cat.err = errors.New(`Get "https://u:secret@h/apis": timeout`)
	if _, err := f.c.Navigation("c1"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("err = %v, want a redacted error", err)
	}
}

func TestNavigationEmptyHasNoNilGroups(t *testing.T) {
	f := newCatalogsFixture(t)
	if _, err := f.c.Navigation("c1"); err != nil {
		t.Fatal(err)
	}
	f.catalog(0).types = nil
	nav, _ := f.c.Navigation("c1")
	if nav.Groups == nil {
		t.Error("Groups is nil, want empty")
	}
}

func TestNavigationNotConnected(t *testing.T) {
	f := newCatalogsFixture(t)
	f.conns.err = cluster.ErrNotConnected
	if _, err := f.c.Navigation("c1"); !strings.Contains(err.Error(), "not connected") {
		t.Errorf("err = %v", err)
	}
}

func TestCatalogPerConnection(t *testing.T) {
	f := newCatalogsFixture(t)
	for range 2 {
		if _, err := f.c.Navigation("c1"); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.created(); n != 1 {
		t.Fatalf("catalogs created = %d, want 1 for one connection", n)
	}
	// The second call (switching back to the cluster) checks for changes.
	waitFor(t, func() bool { return f.catalog(0).checkCount() == 1 })

	cancel := f.conns.reconnect(t)
	defer cancel()
	if _, err := f.c.Navigation("c1"); err != nil {
		t.Fatal(err)
	}
	if n := f.created(); n != 2 {
		t.Fatalf("catalogs created = %d, want a new one after reconnecting", n)
	}
	if f.catalog(1).checkCount() != 0 {
		t.Error("a new connection's first Navigation ran a check")
	}
}

func TestPeriodicCheckReportsOnceUntilRefresh(t *testing.T) {
	f := newCatalogsFixture(t)
	if _, err := f.c.Navigation("c1"); err != nil {
		t.Fatal(err)
	}
	cat := f.catalog(0)

	f.tick(t)
	waitFor(t, func() bool { return cat.checkCount() == 1 })
	f.expectNoStale(t)

	cat.setChanged(true)
	f.tick(t)
	f.expectStale(t, "c1")
	if nav, _ := f.c.Navigation("c1"); !nav.Stale {
		t.Error("Navigation after a change: Stale = false")
	}

	// Already reported: further ticks don't check or report again.
	checks := cat.checkCount()
	f.tick(t)
	f.expectNoStale(t)
	if cat.checkCount() != checks {
		t.Error("checked again after the change was reported")
	}

	nav, err := f.c.RefreshNavigation("c1")
	if err != nil || nav.Stale {
		t.Fatalf("RefreshNavigation = %+v, %v; want not stale", nav, err)
	}
	cat.setChanged(true)
	f.tick(t)
	f.expectStale(t, "c1")
}

func TestCheckOnDemand(t *testing.T) {
	f := newCatalogsFixture(t)
	f.c.Check("c1") // no tree yet
	if f.created() != 0 {
		t.Fatal("Check created a catalog")
	}
	if _, err := f.c.Navigation("c1"); err != nil {
		t.Fatal(err)
	}
	f.catalog(0).setChanged(true)
	f.c.Check("c1")
	f.expectStale(t, "c1")
}

func TestConnectionEndDropsCatalog(t *testing.T) {
	f := newCatalogsFixture(t)
	cancel := f.conns.reconnect(t)
	if _, err := f.c.Navigation("c1"); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitFor(t, func() bool {
		f.c.mu.Lock()
		defer f.c.mu.Unlock()
		return len(f.c.entries) == 0
	})
}

func TestCatalogsClose(t *testing.T) {
	f := newCatalogsFixture(t)
	if _, err := f.c.Navigation("c1"); err != nil {
		t.Fatal(err)
	}
	f.c.Close()
	f.c.Close()
	if _, err := f.c.Navigation("c1"); !errors.Is(err, ErrClosed) {
		t.Errorf("Navigation after Close = %v, want ErrClosed", err)
	}
}
