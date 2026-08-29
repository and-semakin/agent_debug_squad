package orchestrator

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

func TestRunSinkWritesArtifactsWithoutStreamLogsAtInfo(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
	st := store.New(cfg)
	run := domain.RunRecord{RunID: "run_000001", Agent: "Reviewer"}

	var logs bytes.Buffer
	sink := newRunSink(st, run, log.New(&logs, "", 0), domain.LogLevelInfo)
	sink.StdoutLine(`{"type":"turn.started"}`)
	sink.StderrLine("warning")
	sink.DiagnosticLine(`{"type":"adapter_invocation"}`)

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
	diagnosticsPath := filepath.Join(root, ".agent-debug-squad", "sessions", "session_test", "runs", "run_000001", "Reviewer.diagnostics.jsonl")
	diagnostics, err := os.ReadFile(diagnosticsPath)
	if err != nil {
		t.Fatalf("ReadFile(diagnostics) error = %v", err)
	}
	if string(diagnostics) != "{\"type\":\"adapter_invocation\"}\n" {
		t.Fatalf("diagnostics = %q", string(diagnostics))
	}
	if logs.Len() != 0 {
		t.Fatalf("logs = %q, want no stream lines at info", logs.String())
	}
}

func TestRunSinkLogLevels(t *testing.T) {
	tests := []struct {
		name       string
		level      domain.LogLevel
		wantStdout bool
		wantStderr bool
	}{
		{name: "quiet", level: domain.LogLevelQuiet},
		{name: "info", level: domain.LogLevelInfo},
		{name: "debug", level: domain.LogLevelDebug, wantStderr: true},
		{name: "trace", level: domain.LogLevelTrace, wantStdout: true, wantStderr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
			var logs bytes.Buffer
			sink := newRunSink(store.New(cfg), domain.RunRecord{RunID: "run_000001", Agent: "Reviewer"}, log.New(&logs, "", 0), tt.level)
			sink.StdoutLine("stdout event")
			sink.StderrLine("stderr event")

			if got := strings.Contains(logs.String(), "stdout event"); got != tt.wantStdout {
				t.Fatalf("stdout logged = %v, want %v; logs = %q", got, tt.wantStdout, logs.String())
			}
			if got := strings.Contains(logs.String(), "stderr event"); got != tt.wantStderr {
				t.Fatalf("stderr logged = %v, want %v; logs = %q", got, tt.wantStderr, logs.String())
			}
		})
	}
}

func TestRunSinkTouchesProgressForEveryStreamAndThrottlesPersistence(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
	st := store.New(cfg)
	started := time.Now().UTC().Add(-time.Minute)
	run := domain.RunRecord{
		RunID: "run_000001", Agent: "Reviewer", Status: domain.RunRunning,
		Progress: &domain.RunProgress{Phase: domain.RunPhaseRunning, LastActivityAt: started},
	}
	if err := st.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	sink := newRunSink(st, run, log.New(io.Discard, "", 0), domain.LogLevelInfo)

	sink.StdoutLine("stdout event")
	afterStdout, err := st.LoadRun(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterStdout.Progress.LastActivityAt.After(started) {
		t.Fatalf("last_activity_at = %v, want after %v", afterStdout.Progress.LastActivityAt, started)
	}

	firstActivity := afterStdout.Progress.LastActivityAt
	time.Sleep(time.Millisecond)
	sink.StderrLine("stderr event")
	afterStderr := sink.ProgressSnapshot()
	if !afterStderr.LastActivityAt.After(firstActivity) {
		t.Fatalf("last_activity_at = %v, want after %v", afterStderr.LastActivityAt, firstActivity)
	}
	persisted, err := st.LoadRun(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Progress.LastActivityAt.Equal(firstActivity) {
		t.Fatalf("persisted last_activity_at = %v, want throttled value %v", persisted.Progress.LastActivityAt, firstActivity)
	}
}

func TestRunSinkCapturesFirstError(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
	st := store.New(cfg)
	run := domain.RunRecord{RunID: "../unsafe", Agent: "Reviewer"}

	var logs bytes.Buffer
	sink := newRunSink(st, run, log.New(&logs, "", 0), domain.LogLevelTrace)
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
