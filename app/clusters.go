package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"kuberoot.in/kuberoot/internal/cluster"
	"kuberoot.in/kuberoot/internal/kubeconfig"
	"kuberoot.in/kuberoot/internal/settings"
	"kuberoot.in/kuberoot/internal/version"
)

// Events emitted to the UI.
const (
	EventClustersChanged = "clusters:changed"
	EventClusterStatus   = "clusters:status"
)

func init() {
	application.RegisterEvent[Snapshot](EventClustersChanged)
	application.RegisterEvent[cluster.Status](EventClusterStatus)
}

// Snapshot is everything the UI needs to render the cluster sidebar.
type Snapshot struct {
	Sources        []kubeconfig.Source  `json:"sources"`
	Contexts       []kubeconfig.Context `json:"contexts"`
	Statuses       []cluster.Status     `json:"statuses"`
	ActiveContext  string               `json:"activeContext"`
	MaxConnections int                  `json:"maxConnections"`
}

// ClusterService exposes kubeconfig sources and cluster connections to the UI.
type ClusterService struct {
	settings *settings.Store
	home     string
	goos     string
	emit     func(name string, data any)
	pick     func(folder bool) (string, error)

	store *kubeconfig.Store
	mgr   *cluster.Manager

	mu     sync.Mutex // serializes source changes
	cancel context.CancelFunc
}

// NewClusterService returns a service persisting preferences in st.
func NewClusterService(st *settings.Store) *ClusterService {
	home, _ := os.UserHomeDir()
	return &ClusterService{
		settings: st,
		home:     home,
		goos:     runtime.GOOS,
		emit: func(name string, data any) {
			if a := application.Get(); a != nil {
				a.Event.Emit(name, data)
			}
		},
		pick: pickPath,
	}
}

// ServiceStartup loads kubeconfigs and starts watching them.
func (s *ClusterService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.start(ctx)
	return nil
}

func (s *ClusterService) start(ctx context.Context) {
	s.store = kubeconfig.NewStore("kuberoot/" + version.Version)
	s.mgr = cluster.NewManager(s.store, cluster.Options{
		OnStatus: func(st cluster.Status) { s.emit(EventClusterStatus, st) },
	})
	s.store.SetSources(s.specs())

	ctx, s.cancel = context.WithCancel(ctx)
	go func() {
		err := s.store.Watch(ctx, func() {
			s.mgr.SyncContexts(s.store.Contexts())
			s.emitSnapshot()
		})
		if err != nil {
			slog.Warn("kubeconfig watch stopped", "err", err)
		}
	}()
}

// ServiceShutdown stops watching and closes all connections.
func (s *ClusterService) ServiceShutdown() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.mgr != nil {
		s.mgr.Close()
	}
	return nil
}

// specs combines default sources with the user's settings.
func (s *ClusterService) specs() []kubeconfig.SourceSpec {
	cfg := s.settings.Get()
	specs := kubeconfig.DefaultSources(os.Getenv("KUBECONFIG"), s.home, s.goos)
	for i := range specs {
		specs[i].Hidden = slices.Contains(cfg.HiddenSources, specs[i].Path)
	}
	for _, p := range cfg.Sources {
		kind := kubeconfig.KindFile
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			kind = kubeconfig.KindFolder
		}
		specs = append(specs, kubeconfig.SourceSpec{Path: p, Kind: kind, Origin: kubeconfig.OriginUser})
	}
	return specs
}

func (s *ClusterService) reloadSources() {
	s.store.SetSources(s.specs())
	s.mgr.SyncContexts(s.store.Contexts())
	s.emitSnapshot()
}

func (s *ClusterService) emitSnapshot() { s.emit(EventClustersChanged, s.Snapshot()) }

// Snapshot returns the current sources, contexts and connection statuses.
func (s *ClusterService) Snapshot() Snapshot {
	return Snapshot{
		Sources:        s.store.Sources(),
		Contexts:       s.store.Contexts(),
		Statuses:       s.mgr.Statuses(),
		ActiveContext:  s.settings.Get().ActiveContext,
		MaxConnections: cluster.MaxConnections,
	}
}

// AddSource adds a kubeconfig file or folder.
func (s *ClusterService) AddSource(path string) (kubeconfig.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	abs, err := filepath.Abs(path)
	if err != nil {
		return kubeconfig.Source{}, fmt.Errorf("invalid path: %w", err)
	}
	abs = filepath.Clean(abs)
	if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		return kubeconfig.Source{}, fmt.Errorf("%s does not exist", abs)
	}
	for _, spec := range s.store.Specs() {
		if spec.Path == abs {
			if spec.Hidden {
				return s.showLocked(abs)
			}
			return kubeconfig.Source{}, fmt.Errorf("%s is already a source", abs)
		}
	}
	if err := s.settings.Update(func(c *settings.Settings) { c.Sources = append(c.Sources, abs) }); err != nil {
		return kubeconfig.Source{}, err
	}
	s.reloadSources()
	return s.source(abs), nil
}

// PickSource lets the user choose a file (or a folder) with a native dialog
// and adds it. It returns nil if the dialog was cancelled.
func (s *ClusterService) PickSource(folder bool) (*kubeconfig.Source, error) {
	path, err := s.pick(folder)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	src, err := s.AddSource(path)
	if err != nil {
		return nil, err
	}
	return &src, nil
}

// RemoveSource removes a user-added source, or hides a default one.
func (s *ClusterService) RemoveSource(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var origin kubeconfig.SourceOrigin
	for _, spec := range s.store.Specs() {
		if spec.Path == path {
			origin = spec.Origin
		}
	}
	var err error
	switch origin {
	case "":
		return fmt.Errorf("%s is not a source", path)
	case kubeconfig.OriginUser:
		err = s.settings.Update(func(c *settings.Settings) {
			c.Sources = slices.DeleteFunc(c.Sources, func(p string) bool { return p == path })
		})
	default:
		err = s.settings.Update(func(c *settings.Settings) {
			if !slices.Contains(c.HiddenSources, path) {
				c.HiddenSources = append(c.HiddenSources, path)
			}
		})
	}
	if err != nil {
		return err
	}
	s.reloadSources()
	return nil
}

// ShowSource un-hides a default source.
func (s *ClusterService) ShowSource(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.showLocked(path)
	return err
}

func (s *ClusterService) showLocked(path string) (kubeconfig.Source, error) {
	if err := s.settings.Update(func(c *settings.Settings) {
		c.HiddenSources = slices.DeleteFunc(c.HiddenSources, func(p string) bool { return p == path })
	}); err != nil {
		return kubeconfig.Source{}, err
	}
	s.reloadSources()
	return s.source(path), nil
}

func (s *ClusterService) source(path string) kubeconfig.Source {
	for _, src := range s.store.Sources() {
		if src.Path == path {
			return src
		}
	}
	return kubeconfig.Source{}
}

// Connect connects to a context. Failures are reported in the returned
// Status rather than as an error, so the UI handles one shape.
func (s *ClusterService) Connect(id string) cluster.Status {
	st, _ := s.mgr.Connect(context.Background(), id)
	return st
}

// Disconnect closes the connection to a context.
func (s *ClusterService) Disconnect(id string) {
	s.mgr.Disconnect(id)
}

// SetActive remembers the selected context across restarts.
func (s *ClusterService) SetActive(id string) error {
	if _, ok := s.store.Context(id); !ok && id != "" {
		return fmt.Errorf("unknown context %q", id)
	}
	return s.settings.Update(func(c *settings.Settings) { c.ActiveContext = id })
}

func pickPath(folder bool) (string, error) {
	a := application.Get()
	if a == nil {
		return "", errors.New("application not running")
	}
	d := a.Dialog.OpenFile().
		CanChooseFiles(!folder).
		CanChooseDirectories(folder).
		ShowHiddenFiles(true)
	if home, err := os.UserHomeDir(); err == nil {
		d.SetDirectory(filepath.Join(home, ".kube"))
	}
	if folder {
		d.SetTitle("Add kubeconfig folder")
	} else {
		d.SetTitle("Add kubeconfig file")
	}
	return d.PromptForSingleSelection()
}
