package app

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/browser"
	"kuberoot.in/kuberoot/internal/resources/cache"
	"kuberoot.in/kuberoot/internal/resources/view"
)

// Events emitted to the UI for resource views.
const (
	EventResourceRows   = "resources:rows"
	EventResourceStatus = "resources:viewstatus"
)

func init() {
	application.RegisterEvent[resources.RowBatch](EventResourceRows)
	application.RegisterEvent[resources.ViewStatus](EventResourceStatus)
}

// ResourceService exposes live resource tables to the UI. Rows and status
// changes arrive as events for the view ID the UI chose.
type ResourceService struct {
	clusters *ClusterService
	emit     func(name string, data any)

	informers *cache.Informers
	browser   *browser.Browser
}

// NewResourceService returns a service browsing the clusters connected
// through clusters. Register it after clusters, which it needs at startup.
func NewResourceService(clusters *ClusterService) *ResourceService {
	return &ResourceService{clusters: clusters, emit: clusters.emit}
}

// ServiceStartup creates the informer cache and the browser.
func (s *ResourceService) ServiceStartup(context.Context, application.ServiceOptions) error {
	s.start()
	return nil
}

func (s *ResourceService) start() {
	s.informers = cache.New(cache.Options{})
	s.browser = browser.New(sessions{s.clusters}, s.informers, s.sink, view.Options{})
}

// ServiceShutdown closes every view and stops every informer.
func (s *ResourceService) ServiceShutdown() error {
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

func (s *ResourceService) sink(string) resources.Sink { return eventSink{s.emit} }

// eventSink forwards a view's output to the UI as events.
type eventSink struct{ emit func(name string, data any) }

func (e eventSink) Send(b resources.RowBatch)      { e.emit(EventResourceRows, b) }
func (e eventSink) Status(st resources.ViewStatus) { e.emit(EventResourceStatus, st) }

// sessions gives the browser the cluster sessions of a ClusterService. The
// manager is created at service startup, so it is looked up on each call.
type sessions struct{ c *ClusterService }

func (s sessions) Session(id string) (cluster.Session, error) {
	if s.c.mgr == nil {
		return cluster.Session{}, cluster.ErrNotConnected
	}
	return s.c.mgr.Session(id)
}
