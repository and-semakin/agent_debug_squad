//go:build darwin || linux

package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// TestRunCommandStreamingReapsDescriptorHoldingDescendants verifies owned
// cleanup: a background child that inherits stdout and outlives the direct
// command must not keep the pipes open after the leader exited.
func TestRunCommandStreamingReapsDescriptorHoldingDescendants(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "descendant.sh")
	script := `#!/bin/sh
sleep 30 &
echo started
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(context.Background(), scriptPath)
	stdout, stderr, err := runCommandStreaming(context.Background(), cmd, domain.DiscardRunSink())
	if err != nil {
		t.Fatalf("runCommandStreaming: %v (stderr: %s)", err, stderr)
	}
	if len(stdout) == 0 {
		t.Fatalf("stdout is empty")
	}
}
