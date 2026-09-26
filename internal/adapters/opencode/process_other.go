//go:build !darwin && !linux

package opencode

import (
	"os"
	"os/exec"
)

func prepareProcess(cmd *exec.Cmd) {}
func signalProcess(cmd *exec.Cmd, force bool) {
	if force {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}
}
