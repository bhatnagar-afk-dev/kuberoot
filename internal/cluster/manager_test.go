package cluster

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kuberoot.in/kuberoot/internal/kubeconfig"
)

var fakePlugin string // path to the compiled fake exec plugin

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kuberoot-fakeplugin")
	if err != nil {
		panic(err)
	}
	fakePlugin = filepath.Join(dir, "fakeplugin")
	if runtime.GOOS == "windows" {
		fakePlugin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", fakePlugin, "./testdata/fakeplugin").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build fake plugin: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// apiServer is a minimal fake Kubernetes API server.
type apiServer struct {
	*httptest.Server
	token string
	mode  atomic.Value // "ok", "unauthorized", "fail", "hang", "nossr"
}

func newAPIServer(t *testing.T, token string) *apiServer {
	t.Helper()
	s := &apiServer{token: token}
	s.mode.Store("ok")
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *apiServer) serve(w http.ResponseWriter, r *http.Request) {
	mode := s.mode.Load().(string)
	switch mode {
	case "hang":
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		return
	case "fail":
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	if mode == "unauthorized" || r.Header.Get("Authorization") != "Bearer "+s.token {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Unauthorized","code":401}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/version":
		fmt.Fprint(w, `{"major":"1","minor":"37","gitVersion":"v1.37.0"}`)
	case r.URL.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews" && r.Method == http.MethodPost:
		if mode == "nossr" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"kind":"SelfSubjectReview","apiVersion":"authentication.k8s.io/v1","status":{"userInfo":{"username":"alice"}}}`)
	default:
		http.NotFound(w, r)
	}
}

func (s *apiServer) caPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}))
}

// fixture writes a kubeconfig with the given contexts and returns a loaded store.
type ctxSpec struct {
	name, server, ca string
	user             string // YAML for the user block
}

func tokenUser(token string) string { return fmt.Sprintf("    token: %s\n", token) }

func execUser(command, mode, token string) string {
	return fmt.Sprintf(`    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: %q
      interactiveMode: Never
      env:
      - {name: FAKEPLUGIN_MODE, value: %s}
      - {name: FAKEPLUGIN_TOKEN, value: %s}
`, command, mode, token)
}

func fixture(t *testing.T, specs ...ctxSpec) (*kubeconfig.Store, string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: Config\nclusters:\n")
	for _, s := range specs {
		fmt.Fprintf(&b, "- name: %s\n  cluster:\n    server: %s\n", s.name, s.server)
		if s.ca != "" {
			fmt.Fprintf(&b, "    certificate-authority-data: %s\n", b64(s.ca))
		}
	}
	b.WriteString("users:\n")
	for _, s := range specs {
		fmt.Fprintf(&b, "- name: %s\n  user:\n%s", s.name, s.user)
	}
	b.WriteString("contexts:\n")
	for _, s := range specs {
		fmt.Fprintf(&b, "- name: %s\n  context: {cluster: %s, user: %s}\n", s.name, s.name, s.name)
	}
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	st := kubeconfig.NewStore("kuberoot-test")
	st.SetSources([]kubeconfig.SourceSpec{{Path: path, Kind: kubeconfig.KindFile, Origin: kubeconfig.OriginUser}})
	return st, path
}

// recorder collects status notifications.
type recorder struct {
	mu   sync.Mutex
	seen []Status
	ch   chan Status
}

func newRecorder() *recorder { return &recorder{ch: make(chan Status, 100)} }

func (r *recorder) record(s Status) {
	r.mu.Lock()
	r.seen = append(r.seen, s)
	r.mu.Unlock()
	r.ch <- s
}

// waitPhase waits for a notification for id with the given phase.
func (r *recorder) waitPhase(t *testing.T, id string, phase Phase) Status {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case s := <-r.ch:
			if s.ContextID == id && s.Phase == phase {
				return s
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s to become %s", id, phase)
		}
	}
}

func fastOptions(rec *recorder) Options {
	return Options{
		ConnectTimeout: 2 * time.Second,
		HealthInterval: 50 * time.Millisecond,
		RetryBackoff:   10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		OnStatus:       rec.record,
	}
}

func TestConnectHealthy(t *testing.T) {
	srv := newAPIServer(t, "good")
	st, path := fixture(t, ctxSpec{name: "dev", server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")})
	rec := newRecorder()
	m := NewManager(st, fastOptions(rec))
	defer m.Close()

	id := kubeconfig.ContextID(path, "dev")
	s, err := m.Connect(context.Background(), id)
	if err != nil {
		t.Fatalf("Connect: %v (%+v)", err, s)
	}
	if s.Phase != PhaseConnected || s.ServerVersion != "v1.37.0" || s.Identity != "alice" {
		t.Errorf("status = %+v", s)
	}
	if rec.seen[0].Phase != PhaseConnecting {
		t.Errorf("first notification = %+v, want connecting", rec.seen[0])
	}
	if c, _, ok := m.Client(id); !ok || c == nil {
		t.Error("Client not available after connect")
	}
	// Connecting again is a no-op.
	if again, err := m.Connect(context.Background(), id); err != nil || again.Phase != PhaseConnected {
		t.Errorf("second Connect = %+v, %v", again, err)
	}
}

func TestConnectErrors(t *testing.T) {
	srv := newAPIServer(t, "good")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closedAddr := "https://" + ln.Addr().String()
	ln.Close()

	tests := []struct {
		name       string
		spec       ctxSpec
		mode       string
		wantReason Reason
	}{
		{"bad token", ctxSpec{server: srv.URL, ca: srv.caPEM(), user: tokenUser("wrong")}, "ok", ReasonUnauthorized},
		{"unknown CA", ctxSpec{server: srv.URL, user: tokenUser("good")}, "ok", ReasonTLSError},
		{"unreachable", ctxSpec{server: closedAddr, user: tokenUser("good")}, "ok", ReasonUnreachable},
		{"timeout", ctxSpec{server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")}, "hang", ReasonTimeout},
		{"exec missing", ctxSpec{server: srv.URL, ca: srv.caPEM(), user: execUser("definitely-not-installed-plugin", "ok", "good")}, "ok", ReasonExecPluginNotFound},
		{"exec fails", ctxSpec{server: srv.URL, ca: srv.caPEM(), user: execUser(fakePlugin, "fail", "")}, "ok", ReasonExecPluginFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv.mode.Store(tt.mode)
			defer srv.mode.Store("ok")
			tt.spec.name = "c"
			st, path := fixture(t, tt.spec)
			rec := newRecorder()
			opts := fastOptions(rec)
			opts.ConnectTimeout = 500 * time.Millisecond
			m := NewManager(st, opts)
			defer m.Close()

			id := kubeconfig.ContextID(path, "c")
			s, err := m.Connect(context.Background(), id)
			if err == nil {
				t.Fatalf("Connect succeeded: %+v", s)
			}
			if s.Phase != PhaseError || s.Reason != tt.wantReason {
				t.Fatalf("status = %+v, want reason %s", s, tt.wantReason)
			}
			if s.Message == "" {
				t.Error("empty message")
			}
			if m.Status(id).Reason != tt.wantReason {
				t.Error("Status() does not report the failure")
			}
			// A failed connection doesn't hold a slot.
			if len(m.conns) != 0 {
				t.Error("failed connection still held")
			}
		})
	}
}

func TestExecPluginStderrIsCapturedAndRedacted(t *testing.T) {
	srv := newAPIServer(t, "good")
	st, path := fixture(t, ctxSpec{name: "c", server: srv.URL, ca: srv.caPEM(), user: execUser(fakePlugin, "fail", "")})
	m := NewManager(st, fastOptions(newRecorder()))
	defer m.Close()

	s, _ := m.Connect(context.Background(), kubeconfig.ContextID(path, "c"))
	if !strings.Contains(s.Detail, "SSO session expired") {
		t.Errorf("detail missing plugin stderr: %q", s.Detail)
	}
	if strings.Contains(s.Detail, "leaky-secret") {
		t.Errorf("detail leaked a secret: %q", s.Detail)
	}
}

func TestConnectExecPlugin(t *testing.T) {
	srv := newAPIServer(t, "from-plugin")
	st, path := fixture(t, ctxSpec{name: "c", server: srv.URL, ca: srv.caPEM(), user: execUser(fakePlugin, "ok", "from-plugin")})
	m := NewManager(st, fastOptions(newRecorder()))
	defer m.Close()
	if s, err := m.Connect(context.Background(), kubeconfig.ContextID(path, "c")); err != nil {
		t.Fatalf("Connect: %v (%+v)", err, s)
	}
}

func TestSelfSubjectReviewUnavailable(t *testing.T) {
	srv := newAPIServer(t, "good")
	srv.mode.Store("nossr")
	st, path := fixture(t, ctxSpec{name: "c", server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")})
	m := NewManager(st, fastOptions(newRecorder()))
	defer m.Close()
	s, err := m.Connect(context.Background(), kubeconfig.ContextID(path, "c"))
	if err != nil || s.Phase != PhaseConnected || s.Identity != "" {
		t.Fatalf("status = %+v, err = %v; want connected without identity", s, err)
	}
}

func TestConnectionLimit(t *testing.T) {
	srv := newAPIServer(t, "good")
	var specs []ctxSpec
	for i := range 3 {
		specs = append(specs, ctxSpec{name: fmt.Sprintf("c%d", i), server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")})
	}
	st, path := fixture(t, specs...)
	opts := fastOptions(newRecorder())
	opts.MaxConnections = 2
	m := NewManager(st, opts)
	defer m.Close()

	for i := range 2 {
		if _, err := m.Connect(context.Background(), kubeconfig.ContextID(path, fmt.Sprintf("c%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	third := kubeconfig.ContextID(path, "c2")
	s, err := m.Connect(context.Background(), third)
	if !errors.Is(err, ErrConnectionLimit) || s.Reason != ReasonConnectionLimit || !strings.Contains(s.Message, "2 clusters") {
		t.Fatalf("third connect = %+v, %v", s, err)
	}
	m.Disconnect(kubeconfig.ContextID(path, "c0"))
	if _, err := m.Connect(context.Background(), third); err != nil {
		t.Fatalf("connect after freeing a slot: %v", err)
	}
}

func TestDefaultLimitIsFive(t *testing.T) {
	if NewManager(nil, Options{}).opts.MaxConnections != 5 {
		t.Error("default connection limit is not 5")
	}
}

func TestHealthFailureAndRecovery(t *testing.T) {
	srv := newAPIServer(t, "good")
	st, path := fixture(t, ctxSpec{name: "c", server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")})
	rec := newRecorder()
	m := NewManager(st, fastOptions(rec))
	defer m.Close()
	id := kubeconfig.ContextID(path, "c")
	if _, err := m.Connect(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	srv.mode.Store("fail")
	s := rec.waitPhase(t, id, PhaseError)
	if s.Identity != "alice" {
		t.Errorf("identity lost on health failure: %+v", s)
	}
	if _, _, ok := m.Client(id); ok {
		t.Error("Client available while unhealthy")
	}

	srv.mode.Store("ok")
	rec.waitPhase(t, id, PhaseConnected)
}

func TestDisconnect(t *testing.T) {
	srv := newAPIServer(t, "good")
	st, path := fixture(t, ctxSpec{name: "c", server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")})
	rec := newRecorder()
	m := NewManager(st, fastOptions(rec))
	defer m.Close()
	id := kubeconfig.ContextID(path, "c")
	_, _ = m.Connect(context.Background(), id)
	m.Disconnect(id)
	if s := m.Status(id); s.Phase != PhaseDisconnected {
		t.Errorf("status = %+v", s)
	}
	rec.waitPhase(t, id, PhaseDisconnected)
}

func TestSyncContexts(t *testing.T) {
	srv := newAPIServer(t, "good")
	st, path := fixture(t,
		ctxSpec{name: "keep", server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")},
		ctxSpec{name: "change", server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")},
		ctxSpec{name: "remove", server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")},
	)
	rec := newRecorder()
	m := NewManager(st, fastOptions(rec))
	defer m.Close()
	keep, change, remove := kubeconfig.ContextID(path, "keep"), kubeconfig.ContextID(path, "change"), kubeconfig.ContextID(path, "remove")
	for _, id := range []string{keep, change, remove} {
		if _, err := m.Connect(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}

	var next []kubeconfig.Context
	for _, c := range st.Contexts() {
		switch c.ID {
		case remove:
			continue
		case change:
			c.Fingerprint = "rotated"
		}
		next = append(next, c)
	}
	for len(rec.ch) > 0 { // discard notifications from the initial connects
		<-rec.ch
	}
	m.SyncContexts(next)

	if s := m.Status(remove); s.Phase != PhaseDisconnected || s.Reason != ReasonInvalidConfig {
		t.Errorf("removed context status = %+v", s)
	}
	s := rec.waitPhase(t, change, PhaseConnecting)
	if s.Reason != ReasonConfigChanged {
		t.Errorf("reconnect reason = %s", s.Reason)
	}
	rec.waitPhase(t, change, PhaseConnected)
	if m.Status(keep).Phase != PhaseConnected {
		t.Error("unchanged context was disturbed")
	}
}
