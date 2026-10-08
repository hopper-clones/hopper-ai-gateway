//go:build windows

package capacitycodex

import (
	"os/exec"
	"syscall"
)

// configureProcess gives the reader its own process group; Cancel
// kills the process, and WaitDelay reaps lingering pipes.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
