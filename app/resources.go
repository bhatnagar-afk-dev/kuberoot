package app

import (
	"context"
	"errors"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/browser"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/catalog"
	"kuberoot.in/kuberoot/internal/resources/nav/curated"
	"kuberoot.in/kuberoot/internal/resources/view"
	"kuberoot.in/kuberoot/internal/settings"
)

// Events emitted to the UI for resource views.
const (
	EventResourceRows   = "resources:rows"
	EventResourceStatus = "resources:viewstatus"
	EventNavStale       = "resources:navstale"
)

func init() {
	application.RegisterEvent[resources.RowBatch](EventResourceRows)
	application.RegisterEvent[resources.ViewStatus](EventResourceStatus)
	application.RegisterEvent[resources.NavStale](EventNavStale)
}

// ResourceService exposes the navigation and live resource tables of
// connected clusters to the UI. Rows and status changes arrive as events for
// the view ID the UI chose.
type ResourceService struct {
	clusters *ClusterService
	emit     func(name string, data any)

	informers *cache.Informers
	browser   *browser.Browser
	catalogs  *browser.Catalogs
}

// NewResourceService returns a service browsing the clusters connected
// through clusters. Register it after clusters, which it needs at startup.
func NewResourceService(clusters *ClusterService) *ResourceService {
	return &ResourceService{clusters: clusters, emit: clusters.emit}
}

// ServiceStartup creates the informer cache, the browser and the catalogs.
func (s *ResourceService) ServiceStartup(context.Context, application.ServiceOptions) error {
	s.start()
	return nil
}

func (s *ResourceService) start() {
	s.informers = cache.New(cache.Options{})
	s.browser = browser.New(sessions{s.clusters}, s.informers, s.sink, view.Options{})
	s.catalogs = browser.NewCatalogs(sessions{s.clusters}, newCatalog, curated.Navigator{}, func(id string) {
		s.emit(EventNavStale, resources.NavStale{ContextID: id})
	}, browser.CatalogOptions{})
}

func newCatalog(s cluster.Session) (resources.Catalog, error) { return catalog.New(s) }

// ServiceShutdown closes every view and stops every informer and check.
func (s *ResourceService) ServiceShutdown() error {
	if s.catalogs != nil {
		s.catalogs.Close()
	}
	if s.browser != nil {
		s.browser.CloseAll()
	}
	if s.informers != nil {
		s.informers.Close()
	}
	return nil
}

// Open starts a live view. req.ViewID is chosen by the UI and must not be in
// use; rows follow as resources:rows events.
func (s *ResourceService) Open(req resources.ViewRequest) error {
	return s.browser.Open(req)
}

// Close stops a view.
func (s *ResourceService) Close(viewID string) {
	s.browser.Close(viewID)
}

// Navigation returns the navigation tree of a connected cluster. If its
// resource types change later, resources:navstale is emitted.
func (s *ResourceService) Navigation(contextID string) (resources.Navigation, error) {
	return s.catalogs.Navigation(contextID)
}

// RefreshNavigation re-reads the cluster's resource types and returns the
// new tree.
func (s *ResourceService) RefreshNavigation(contextID string) (resources.Navigation, error) {
	return s.catalogs.RefreshNavigation(contextID)
}

// SavedNamespaces returns the namespaces last chosen for a context; empty
// means all namespaces.
func (s *ResourceService) SavedNamespaces(contextID string) []string {
	if s.clusters.settings == nil {
		return []string{}
	}
	ns := s.clusters.settings.Get().Namespaces[contextID]
	if ns == nil {
		return []string{}
	}
	return ns
}

// SaveNamespaces remembers the namespaces chosen for a context; empty means
// all namespaces.
func (s *ResourceService) SaveNamespaces(contextID string, namespaces []string) error {
	if s.clusters.settings == nil {
		return errors.New("settings are not available")
	}
	return s.clusters.settings.Update(func(c *settings.Settings) {
		if len(namespaces) == 0 {
			delete(c.Namespaces, contextID)
			return
		}
		if c.Namespaces == nil {
			c.Namespaces = map[string][]string{}
		}
		c.Namespaces[contextID] = slices.Clone(namespaces)
	})
}

// sink sends a view's output to the UI, and asks for a catalog check when
// the view's type is gone.
func (s *ResourceService) sink(req resources.ViewRequest) resources.Sink {
	return eventSink{emit: s.emit, notFound: func() { s.catalogs.Check(req.ContextID) }}
}

// eventSink forwards a view's output to the UI as events.
type eventSink struct {
	emit     func(name string, data any)
	notFound func() // called when the view's type is gone; may be nil
}

func (e eventSink) Send(b resources.RowBatch) { e.emit(EventResourceRows, b) }

func (e eventSink) Status(st resources.ViewStatus) {
	if st.Reason == resources.ReasonNotFound && e.notFound != nil {
		e.notFound()
	}
	e.emit(EventResourceStatus, st)
}

// sessions gives the browser the cluster sessions of a ClusterService. The
// manager is created at service startup, so it is looked up on each call.
type sessions struct{ c *ClusterService }

func (s sessions) Session(id string) (cluster.Session, error) {
	if s.c.mgr == nil {
		return cluster.Session{}, cluster.ErrNotConnected
	}
	return s.c.mgr.Session(id)
}
