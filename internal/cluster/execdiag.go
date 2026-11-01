package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	api "k8s.io/client-go/tools/clientcmd/api"

	"github.com/bhatnagar-afk-dev/kuberoot/internal/redact"
)

const (
	diagTimeout   = 15 * time.Second
	maxDiagStderr = 4 << 10
)

// diagnoseExec re-runs an exec credential plugin to capture its stderr.
// client-go streams plugin stderr to the process's stderr, so it is not
// available in the returned error. stdout (which carries the token) is
// discarded; stderr is truncated and redacted.
func diagnoseExec(ctx context.Context, ec *api.ExecConfig) string {
	if ec == nil || ec.Command == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, diagTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ec.Command, ec.Args...)
	cmd.Env = os.Environ()
	for _, e := range ec.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}
	info, _ := json.Marshal(map[string]any{
		"apiVersion": ec.APIVersion,
		"kind":       "ExecCredential",
		"spec":       map[string]any{"interactive": false},
	})
	cmd.Env = append(cmd.Env, "KUBERNETES_EXEC_INFO="+string(info))
	cmd.Stdin = nil
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: maxDiagStderr}
	_ = cmd.Run()
	return redact.String(strings.TrimSpace(stderr.String()))
}

type limitedWriter struct {
	w *bytes.Buffer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if room := l.n - l.w.Len(); room > 0 {
		if len(p) > room {
			l.w.Write(p[:room])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}
