//go:build integration

package cluster

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"kuberoot.in/kuberoot/internal/kubeconfig"
)

// TestIntegrationConnect connects to a real cluster using the same discovery
// path as the app. Run with:
//
//	KUBEROOT_IT_CONTEXT=kind-kuberoot go test -tags integration -run Integration ./internal/cluster/
func TestIntegrationConnect(t *testing.T) {
	name := os.Getenv("KUBEROOT_IT_CONTEXT")
	if name == "" {
		t.Skip("KUBEROOT_IT_CONTEXT not set")
	}
	home, _ := os.UserHomeDir()
	st := kubeconfig.NewStore("kuberoot-integration")
	st.SetSources(kubeconfig.DefaultSources(os.Getenv("KUBECONFIG"), home, runtime.GOOS))

	var id string
	for _, c := range st.Contexts() {
		if c.Name == name {
			id = c.ID
			break
		}
	}
	if id == "" {
		t.Fatalf("context %q not discovered; found %d contexts", name, len(st.Contexts()))
	}

	m := NewManager(st, Options{ConnectTimeout: 30 * time.Second})
	defer m.Close()
	s, err := m.Connect(context.Background(), id)
	if err != nil {
		t.Fatalf("Connect: %v (%+v)", err, s)
	}
	if s.Phase != PhaseConnected || s.ServerVersion == "" || s.Identity == "" {
		t.Fatalf("status = %+v", s)
	}
	t.Logf("connected to %s as %s", s.ServerVersion, s.Identity)

	client, _, ok := m.Client(id)
	if !ok {
		t.Fatal("no client")
	}
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), "kube-system", metav1GetOptions()); err != nil {
		t.Fatalf("get kube-system: %v", err)
	}
}
