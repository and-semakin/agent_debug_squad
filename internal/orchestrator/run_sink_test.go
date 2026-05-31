package orchestrator

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/store"
)

func TestRunSinkWritesArtifactsAndLogs(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
	st := store.New(cfg)
	run := domain.RunRecord{RunID: "run_000001", Agent: "Reviewer"}

	var logs bytes.Buffer
	sink := newRunSink(st, run, log.New(&logs, "", 0))
	sink.StdoutLine(`{"type":"turn.started"}`)
	sink.StderrLine("warning")

	if err := sink.Err(); err != nil {
		t.Fatalf("sink.Err() = %v", err)
	}

	eventsPath := filepath.Join(root, ".agent-debug-squad", "sessions", "session_test", "runs", "run_000001", "Reviewer.events.jsonl")
	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("ReadFile(events) error = %v", err)
	}
	if string(events) != "{\"type\":\"turn.started\"}\n" {
		t.Fatalf("events = %q", string(events))
	}
	stderrPath := filepath.Join(root, ".agent-debug-squad", "sessions", "session_test", "runs", "run_000001", "Reviewer.stderr.log")
	stderr, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("ReadFile(stderr) error = %v", err)
	}
	if string(stderr) != "warning\n" {
		t.Fatalf("stderr = %q", string(stderr))
	}
	if !strings.Contains(logs.String(), `run=run_000001 agent=Reviewer stream=stdout {"type":"turn.started"}`) {
		t.Fatalf("logs = %q, want stdout line", logs.String())
	}
	if !strings.Contains(logs.String(), "run=run_000001 agent=Reviewer stream=stderr warning") {
		t.Fatalf("logs = %q, want stderr line", logs.String())
	}
}

func TestRunSinkCapturesFirstError(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
	st := store.New(cfg)
	run := domain.RunRecord{RunID: "../unsafe", Agent: "Reviewer"}

	var logs bytes.Buffer
	sink := newRunSink(st, run, log.New(&logs, "", 0))
	sink.StdoutLine("first")
	sink.StderrLine("second")

	err := sink.Err()
	if err == nil {
		t.Fatal("sink.Err() = nil, want captured error")
	}
	if !strings.Contains(err.Error(), "write stdout stream:") {
		t.Fatalf("sink.Err() = %v, want first stdout error", err)
	}
	if !strings.Contains(err.Error(), `unsafe run_id "../unsafe"`) {
		t.Fatalf("sink.Err() = %v, want unsafe run_id error", err)
	}
}
