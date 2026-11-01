package kubeconfig

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

// Client defaults: client-go's 5 QPS / 10 burst is too low for an interactive UI.
const (
	defaultQPS   = 50
	defaultBurst = 100
)

// ErrContextNotFound is returned for an unknown context ID.
var ErrContextNotFound = errors.New("kubeconfig: context not found")

// LegacyAuthProviderError is returned for contexts using an auth-provider that
// client-go removed in v1.26.
type LegacyAuthProviderError struct{ Provider string }

func (e *LegacyAuthProviderError) Error() string {
	hint := ""
	switch e.Provider {
	case "gcp":
		hint = "; install gke-gcloud-auth-plugin and re-run `gcloud container clusters get-credentials`"
	case "azure":
		hint = "; install kubelogin and run `kubelogin convert-kubeconfig`"
	}
	return fmt.Sprintf("the %q auth-provider was removed from Kubernetes clients in v1.26%s", e.Provider, hint)
}

// Store holds the configured sources and the contexts loaded from them.
// It is safe for concurrent use.
type Store struct {
	userAgent string
	resync    chan struct{} // signals Watch that the source list changed

	mu    sync.RWMutex
	specs []SourceSpec
	snap  snapshot
}

// NewStore returns an empty store. userAgent is sent on API requests.
func NewStore(userAgent string) *Store {
	return &Store{
		userAgent: userAgent,
		resync:    make(chan struct{}, 1),
		snap:      snapshot{entries: map[string]*entry{}},
	}
}

// SetSources replaces the configured sources and reloads. It reports whether
// the visible contexts changed.
func (s *Store) SetSources(specs []SourceSpec) bool {
	s.mu.Lock()
	s.specs = slices.Clone(specs)
	s.mu.Unlock()
	select {
	case s.resync <- struct{}{}:
	default:
	}
	return s.Reload()
}

// Reload re-reads every source. It reports whether contexts or sources changed.
func (s *Store) Reload() bool {
	s.mu.RLock()
	specs := slices.Clone(s.specs)
	s.mu.RUnlock()

	next := load(specs)

	s.mu.Lock()
	defer s.mu.Unlock()
	changed := !sameSnapshot(s.snap, next)
	s.snap = next
	return changed
}

func sameSnapshot(a, b snapshot) bool {
	if !slices.Equal(a.sources, b.sources) || len(a.contexts) != len(b.contexts) {
		return false
	}
	for i := range a.contexts {
		if a.contexts[i] != b.contexts[i] {
			return false
		}
	}
	return true
}

// Specs returns the configured sources.
func (s *Store) Specs() []SourceSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.specs)
}

// Sources returns the configured sources with load results.
func (s *Store) Sources() []Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.snap.sources)
}

// Contexts returns all contexts from visible sources.
func (s *Store) Contexts() []Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.snap.contexts)
}

// Context returns the context with the given ID.
func (s *Store) Context(id string) (Context, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.snap.contexts {
		if c.ID == id {
			return c, true
		}
	}
	return Context{}, false
}

// RESTConfig builds a client configuration for the context. Exec plugins are
// configured to never prompt, since there is no terminal.
func (s *Store) RESTConfig(id string) (*rest.Config, error) {
	s.mu.RLock()
	e, ok := s.snap.entries[id]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrContextNotFound
	}

	cfg := e.cfg.DeepCopy()
	kctx := cfg.Contexts[e.name]
	if _, ok := cfg.Clusters[kctx.Cluster]; !ok {
		return nil, fmt.Errorf("cluster %q referenced by context %q is not defined", kctx.Cluster, e.name)
	}
	if auth := cfg.AuthInfos[kctx.AuthInfo]; auth != nil {
		if ap := auth.AuthProvider; ap != nil && (ap.Name == "gcp" || ap.Name == "azure") {
			return nil, &LegacyAuthProviderError{Provider: ap.Name}
		}
		if auth.Exec != nil {
			auth.Exec.InteractiveMode = api.NeverExecInteractiveMode
			auth.Exec.StdinUnavailable = true
			auth.Exec.StdinUnavailableMessage = "KubeRoot cannot answer interactive prompts; run the command in a terminal first"
		}
	}

	rc, err := clientcmd.NewNonInteractiveClientConfig(*cfg, e.name, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, err
	}
	rc.QPS = defaultQPS
	rc.Burst = defaultBurst
	rc.UserAgent = s.userAgent
	return rc, nil
}
