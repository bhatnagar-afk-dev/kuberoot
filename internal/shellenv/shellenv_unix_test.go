//go:build !windows

package shellenv

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeShell writes a script that behaves like `sh -l -i -c <script>` but prints
// a noisy profile banner first.
func fakeShell(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakesh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolve(t *testing.T) {
	sh := fakeShell(t, `echo "profile banner"
# Last argument is the script passed via -c.
for last; do :; done
export KUBEROOT_TEST_VAR=from-profile
exec /bin/sh -c "$last"
`)
	env, err := Resolve(context.Background(), sh)
	if err != nil {
		t.Fatal(err)
	}
	if env["KUBEROOT_TEST_VAR"] != "from-profile" {
		t.Errorf("KUBEROOT_TEST_VAR = %q", env["KUBEROOT_TEST_VAR"])
	}
	if env["PATH"] == "" {
		t.Error("PATH missing")
	}
}

func TestResolveTimeout(t *testing.T) {
	sh := fakeShell(t, "sleep 10\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Resolve(ctx, sh); err == nil {
		t.Fatal("expected timeout error")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Resolve took %v; process group was not killed promptly", d)
	}
}

func TestResolveFailure(t *testing.T) {
	sh := fakeShell(t, "echo broken >&2; exit 3\n")
	if _, err := Resolve(context.Background(), sh); err == nil {
		t.Fatal("expected error")
	}
}

func TestApplySkipsInTerminal(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	res, err := Apply(context.Background())
	if err != nil || !res.Skipped {
		t.Fatalf("res=%+v err=%v; want skipped", res, err)
	}
}

func TestApplyFromGUI(t *testing.T) {
	t.Setenv("TERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KUBEROOT_APPLY_VAR", "")
	sh := fakeShell(t, `for last; do :; done
export KUBEROOT_APPLY_VAR=resolved
exec /bin/sh -c "$last"
`)
	t.Setenv("SHELL", sh)
	res, err := Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped || res.Applied == 0 {
		t.Fatalf("res=%+v; want applied", res)
	}
	if got := os.Getenv("KUBEROOT_APPLY_VAR"); got != "resolved" {
		t.Errorf("KUBEROOT_APPLY_VAR = %q", got)
	}
	if os.Getenv("TERM") != "" {
		t.Error("TERM must not be copied from the probe shell")
	}
}
