//go:build unix

package preflight

import "syscall"

// xOK is the POSIX X_OK access mode, constant across Unix targets; the
// standard library syscall package does not re-export it everywhere.
const xOK = 0x1

func syscallAccessOS(path string) error {
	return syscall.Access(path, xOK)
}
