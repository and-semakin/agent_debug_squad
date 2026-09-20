package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// SessionOwnership is an OS-backed exclusive lock over one session directory.
// It must be acquired before any startup mutation of session state and held
// for the lifetime of the owning process.
type SessionOwnership struct {
	file *os.File
	path string
}

func AcquireSessionOwnership(sessionDir string) (*SessionOwnership, error) {
	if sessionDir == "" {
		return nil, fmt.Errorf("session directory is required for ownership")
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	path := filepath.Join(sessionDir, "ownership.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open ownership lock: %w", err)
	}
	if err := lockFileExclusive(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("session state at %s is already owned by another agent-debug-squad process: %w", sessionDir, err)
	}
	return &SessionOwnership{file: file, path: path}, nil
}

func (o *SessionOwnership) Release() error {
	if o == nil || o.file == nil {
		return nil
	}
	err := unlockFile(o.file)
	closeErr := o.file.Close()
	o.file = nil
	if err != nil {
		return fmt.Errorf("release ownership lock %s: %w", o.path, err)
	}
	return closeErr
}
