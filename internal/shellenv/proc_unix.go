//go:build !windows

package shellenv

import (
	"os/exec"
	"syscall"
)

// setProcessGroup detaches the probe shell from any controlling terminal so an
// interactive shell cannot stop on SIGTTIN/SIGTTOU, and so the whole group is
// killed on timeout.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
