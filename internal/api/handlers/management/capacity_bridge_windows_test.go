//go:build windows

package management

import (
	"os/exec"
	"testing"
)

func assertCapacityProcessGroup(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.SysProcAttr == nil {
		t.Fatal("the reader must run in its own process group")
	}
}
