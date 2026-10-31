package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/bhatnagar-afk-dev/kuberoot/internal/cluster"
	"github.com/bhatnagar-afk-dev/kuberoot/internal/kubeconfig"
	"github.com/bhatnagar-afk-dev/kuberoot/internal/settings"
)

const oneContext = `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: "https://127.0.0.1:1"}
users:
- name: u
  user: {token: t}
contexts:
- name: %s
  context: {cluster: c, user: u}
`

func writeKubeconfig(t *testing.T, path, ctxName string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte(sprintf(oneContext, ctxName))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

type events struct {
	mu    sync.Mutex
	names []string
}

func (e *events) emit(name string, _ any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.names = append(e.names, name)
}

func newTestService(t *testing.T) (*ClusterService, string, *events) {
	t.Helper()
	t.Setenv("KUBECONFIG", "")
	home := t.TempDir()
	writeKubeconfig(t, filepath.Join(home, ".kube", "config"), "default-ctx")
	st, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	s := &ClusterService{settings: st, home: home, goos: "windows", emit: ev.emit}
	s.start(context.Background())
	t.Cleanup(func() { _ = s.ServiceShutdown() })
	return s, home, ev
}

func contextNames(snap Snapshot) []string {
	var out []string
	for _, c := range snap.Contexts {
		out = append(out, c.Name)
	}
	return out
}

func TestServiceDefaults(t *testing.T) {
	s, _, _ := newTestService(t)
	snap := s.Snapshot()
	if !slices.Equal(contextNames(snap), []string{"default-ctx"}) {
		t.Errorf("contexts = %v", contextNames(snap))
	}
	if snap.MaxConnections != cluster.MaxConnections {
		t.Errorf("maxConnections = %d", snap.MaxConnections)
	}
}

func TestServiceAddRemoveSource(t *testing.T) {
	s, _, ev := newTestService(t)
	extra := filepath.Join(t.TempDir(), "team.yaml")
	writeKubeconfig(t, extra, "team-ctx")

	src, err := s.AddSource(extra)
	if err != nil {
		t.Fatal(err)
	}
	if src.Contexts != 1 || src.Origin != kubeconfig.OriginUser {
		t.Errorf("source = %+v", src)
	}
	if !slices.Contains(contextNames(s.Snapshot()), "team-ctx") {
		t.Error("added source's context missing")
	}
	if !slices.Contains(ev.names, EventClustersChanged) {
		t.Error("no clusters:changed event")
	}
	if _, err := s.AddSource(extra); err == nil {
		t.Error("duplicate source accepted")
	}
	if _, err := s.AddSource(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing path accepted")
	}

	if err := s.RemoveSource(extra); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(contextNames(s.Snapshot()), "team-ctx") {
		t.Error("removed source's context still listed")
	}
	if len(s.settings.Get().Sources) != 0 {
		t.Error("settings still list removed source")
	}
}

func TestServiceHideAndShowDefault(t *testing.T) {
	s, home, _ := newTestService(t)
	cfg := filepath.Join(home, ".kube", "config")
	if err := s.RemoveSource(cfg); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.settings.Get().HiddenSources, cfg) {
		t.Error("default source not hidden")
	}
	// The ~/.kube folder scan must not resurface the hidden file.
	if n := len(s.Snapshot().Contexts); n != 0 {
		t.Errorf("hidden file still contributes %d contexts", n)
	}
	if err := s.ShowSource(cfg); err != nil {
		t.Fatal(err)
	}
	if len(s.settings.Get().HiddenSources) != 0 {
		t.Error("source still hidden")
	}
	if n := len(s.Snapshot().Contexts); n != 1 {
		t.Errorf("after show: %d contexts, want 1", n)
	}
}

func TestServiceSetActive(t *testing.T) {
	s, _, _ := newTestService(t)
	id := s.Snapshot().Contexts[0].ID
	if err := s.SetActive(id); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().ActiveContext != id {
		t.Error("active context not persisted")
	}
	if err := s.SetActive("bogus"); err == nil {
		t.Error("unknown context accepted")
	}
}

func TestServiceConnectReportsFailureInStatus(t *testing.T) {
	s, _, ev := newTestService(t)
	id := s.Snapshot().Contexts[0].ID
	st := s.Connect(id) // server is 127.0.0.1:1, nothing listens there
	if st.Phase != cluster.PhaseError {
		t.Fatalf("status = %+v", st)
	}
	if !slices.Contains(ev.names, EventClusterStatus) {
		t.Error("no clusters:status event")
	}
}

func TestPickSourceCancelled(t *testing.T) {
	s, _, _ := newTestService(t)
	s.pick = func(bool) (string, error) { return "", nil }
	if src, err := s.PickSource(false); src != nil || err != nil {
		t.Errorf("cancelled pick = %v, %v", src, err)
	}
}
