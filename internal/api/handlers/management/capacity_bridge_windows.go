//go:build windows

package management

import (
	"os/exec"
	"syscall"
)

// configureCapacityProcess gives the reader its own process group; Cancel
// kills the process, and WaitDelay reaps lingering pipes.
func configureCapacityProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
