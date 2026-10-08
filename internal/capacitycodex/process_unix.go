//go:build !windows

package capacitycodex

import (
	"os/exec"
	"syscall"
)

// configureProcess puts the reader in its own process group and kills
// the whole group on timeout, so children bun spawns die with it.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
