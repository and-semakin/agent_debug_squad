package modelprobe

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// CommandFailure carries only a classification, never backend diagnostics.
type CommandFailure struct{ Status string }

func (e *CommandFailure) Error() string { return "model list command failed" }

type privateDiagnostics struct {
	mu sync.Mutex
	b  []byte
}

func (d *privateDiagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(p)
	room := (64 << 10) - len(d.b)
	if len(p) > room {
		p = p[:room]
	}
	d.b = append(d.b, p...)
	return n, nil
}
func (d *privateDiagnostics) status() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := strings.ToLower(string(d.b))
	for _, marker := range []string{"not authenticated", "authentication required", "unauthorized", "please log in", "please login"} {
		if strings.Contains(s, marker) {
			return "auth_required"
		}
	}
	for _, marker := range []string{"unknown command", "unknown option", "unrecognized option", "unrecognized command"} {
		if strings.Contains(s, marker) {
			return "unsupported"
		}
	}
	return "error"
}
func CommandStatus(ctx context.Context, e error) string {
	status := "error"
	var f *CommandFailure
	if errors.As(e, &f) {
		status = f.Status
	}
	return ContextStatus(ctx, status)
}
