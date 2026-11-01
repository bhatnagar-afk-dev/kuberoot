// Package shellenv makes GUI launches see the same environment as a terminal.
//
// Apps started from Finder, the Dock or a Linux desktop launcher do not inherit
// the user's shell PATH, KUBECONFIG or cloud CLI variables, so kubeconfig exec
// plugins (aws, gke-gcloud-auth-plugin, kubelogin, ...) cannot be found. Apply
// runs the user's login shell once and adopts its environment.
package shellenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	startMarker = "__KUBEROOT_ENV_START__"
	endMarker   = "__KUBEROOT_ENV_END__"

	// DefaultTimeout bounds how long a slow shell profile can delay startup.
	DefaultTimeout = 5 * time.Second
)

// skip lists session-specific variables that must not be copied from the
// probe shell into the app process.
var skip = map[string]bool{
	"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true,
	"TERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true, "TERM_SESSION_ID": true,
	"COLORTERM": true, "ITERM_SESSION_ID": true, "WINDOWID": true,
	"SHELL_SESSION_ID": true, "ZSH_EXECUTION_STRING": true,
}

// Result reports what Apply did.
type Result struct {
	Skipped bool   // true when no resolution was needed (Windows, or launched from a terminal)
	Reason  string // why it was skipped
	Shell   string // shell that was probed
	Applied int    // number of variables set
}

// Apply resolves the login-shell environment and sets it on the current
// process. It is a no-op on Windows and when the app was launched from a
// terminal. On failure the inherited environment is left untouched.
func Apply(ctx context.Context) (Result, error) {
	if runtime.GOOS == "windows" {
		return Result{Skipped: true, Reason: "windows"}, nil
	}
	if launchedFromTerminal() {
		return Result{Skipped: true, Reason: "launched from terminal"}, nil
	}
	shell := loginShell()
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	env, err := Resolve(ctx, shell)
	if err != nil {
		return Result{Shell: shell}, err
	}
	n := 0
	for k, v := range env {
		if skip[k] || os.Getenv(k) == v {
			continue
		}
		if err := os.Setenv(k, v); err == nil {
			n++
		}
	}
	return Result{Shell: shell, Applied: n}, nil
}

// Resolve runs shell as an interactive login shell and returns its
// environment. Output printed by shell profiles (banners, prompts) is ignored
// by bracketing the env dump with markers.
func Resolve(ctx context.Context, shell string) (map[string]string, error) {
	script := fmt.Sprintf("printf '%s'; command env -0; printf '%s'", startMarker, endMarker)
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", script)
	cmd.Stdin = nil // /dev/null: never let a profile wait for input
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	setProcessGroup(cmd)

	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("shellenv: %s timed out", shell)
		}
		// Some profiles exit non-zero after printing the env; trust markers if present.
		if !bytes.Contains(stdout.Bytes(), []byte(endMarker)) {
			return nil, fmt.Errorf("shellenv: %s: %w: %s", shell, err, strings.TrimSpace(stderr.String()))
		}
	}
	return parse(stdout.Bytes())
}

func parse(out []byte) (map[string]string, error) {
	start := bytes.Index(out, []byte(startMarker))
	end := bytes.LastIndex(out, []byte(endMarker))
	if start < 0 || end < 0 || end < start {
		return nil, errors.New("shellenv: environment markers not found in shell output")
	}
	body := out[start+len(startMarker) : end]
	env := make(map[string]string)
	for _, kv := range bytes.Split(body, []byte{0}) {
		k, v, ok := strings.Cut(string(kv), "=")
		if !ok || k == "" {
			continue
		}
		env[k] = v
	}
	if len(env) == 0 {
		return nil, errors.New("shellenv: shell returned an empty environment")
	}
	return env, nil
}

func launchedFromTerminal() bool {
	return os.Getenv("TERM_PROGRAM") != "" || os.Getenv("TERM") != ""
}

func loginShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}
