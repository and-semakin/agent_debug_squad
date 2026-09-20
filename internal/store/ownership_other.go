//go:build !darwin && !linux

package store

import (
	"fmt"
	"os"
	"runtime"
)

const ownershipUnsupported = true

// Exclusive session-directory ownership is implemented with flock semantics
// that this platform does not provide; fail closed instead of allowing two
// owners to mutate the same session state.
func lockFileExclusive(file *os.File) error {
	_ = file
	return fmt.Errorf("exclusive session ownership is not supported on %s", runtime.GOOS)
}

func unlockFile(file *os.File) error {
	return nil
}
