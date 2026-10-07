//go:build !windows

package management

import (
	"os/exec"
	"syscall"
)

// configureCapacityProcess puts the reader in its own process group and kills
// the whole group on timeout, so children bun spawns die with it.
func configureCapacityProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
