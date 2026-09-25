//go:build !darwin && !linux

package procgroup

import (
	"os/exec"
)

// Prepare is a no-op on platforms without POSIX process groups.
func Prepare(cmd *exec.Cmd) {}

// Kill terminates the direct child process.
func Kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
