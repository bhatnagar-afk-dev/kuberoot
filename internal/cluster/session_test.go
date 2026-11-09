package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"kuberoot.in/kuberoot/internal/kubeconfig"
)

func TestSession(t *testing.T) {
	srv := newAPIServer(t, "good")
	st, path := fixture(t, ctxSpec{name: "dev", server: srv.URL, ca: srv.caPEM(), user: tokenUser("good")})
	m := NewManager(st, fastOptions(newRecorder()))
	defer m.Close()
	id := kubeconfig.ContextID(path, "dev")

	if _, err := m.Session(id); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Session before connect: err = %v, want ErrNotConnected", err)
	}
	if _, err := m.Connect(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	s, err := m.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if s.ContextID != id || s.Dynamic == nil || s.Discovery == nil || s.Mapper == nil || s.Config == nil {
		t.Fatalf("incomplete session: %+v", s)
	}
	if s.Config.Host != srv.URL {
		t.Errorf("Config.Host = %q, want %q", s.Config.Host, srv.URL)
	}

	// Clients are shared, but each caller gets its own config copy.
	s2, _ := m.Session(id)
	if s2.Discovery != s.Discovery {
		t.Error("discovery client was rebuilt; want it shared per connection")
	}
	if s2.Config == s.Config {
		t.Error("Config is shared; want a copy per call")
	}

	// The session context ends with the connection.
	m.Disconnect(id)
	select {
	case <-s.Ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("session context not cancelled on Disconnect")
	}
	if _, err := m.Session(id); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Session after disconnect: err = %v, want ErrNotConnected", err)
	}
}
