//go:build darwin || linux

// Package procgroup manages the process group of an owned child command so
// cancellation reaches descriptor-holding descendants, not just the direct
// child. Adapters use it for subprocesses they own; it never addresses
// processes outside the group created for that command.
package procgroup

import (
	"os/exec"
	"syscall"
)

// Prepare puts the command into its own process group before Start so the
// whole group can later be signalled without matching unrelated processes.
func Prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Kill terminates the command's process group, including descendants that
// retained inherited descriptors after the leader exited.
func Kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
