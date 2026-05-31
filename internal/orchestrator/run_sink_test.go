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
	if !strings.Contains(logs.String(), `run=run_000001 agent=Reviewer stream=stdout {"type":"turn.started"}`) {
		t.Fatalf("logs = %q, want stdout line", logs.String())
	}
}
