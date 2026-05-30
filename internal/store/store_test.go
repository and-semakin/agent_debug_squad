package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

func TestStoreWritesAgentRunOutputAndTranscript(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
	}
	s := New(cfg)

	if err := s.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	state := domain.AgentState{
		Name:             "Reviewer",
		Backend:          "fake",
		WorkspaceDir:     root,
		BackendSessionID: "backend_session_123",
		Status:           domain.AgentIdle,
		CreatedAt:        time.Now().UTC(),
	}
	if err := s.SaveAgentState(state); err != nil {
		t.Fatalf("SaveAgentState() error = %v", err)
	}

	started := time.Now().UTC()
	completed := started.Add(time.Second)
	run := domain.RunRecord{
		RunID:       "run_000001",
		Agent:       "Reviewer",
		Status:      domain.RunCompleted,
		Message:     "please review",
		CreatedAt:   started,
		StartedAt:   &started,
		CompletedAt: &completed,
	}
	if err := s.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error = %v", err)
	}

	outputPath, err := s.WriteAgentOutput(run, "final answer")
	if err != nil {
		t.Fatalf("WriteAgentOutput() error = %v", err)
	}

	if err := s.AppendTranscript(domain.TranscriptEvent{
		Type:       "agent_result",
		RunID:      run.RunID,
		Agent:      run.Agent,
		OutputPath: outputPath,
		Status:     domain.RunCompleted,
		At:         completed,
	}); err != nil {
		t.Fatalf("AppendTranscript() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(s.SessionDir(), "config.json")); err != nil {
		t.Fatalf("config.json missing: %v", err)
	}

	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile(outputPath) error = %v", err)
	}
	if !strings.Contains(string(output), "final answer") {
		t.Fatalf("output file does not contain final answer: %q", string(output))
	}

	loaded, err := s.LoadAgentState("Reviewer")
	if err != nil {
		t.Fatalf("LoadAgentState() error = %v", err)
	}
	if loaded.BackendSessionID != state.BackendSessionID {
		t.Fatalf("BackendSessionID = %q, want %q", loaded.BackendSessionID, state.BackendSessionID)
	}

	events, err := s.ReadTranscript()
	if err != nil {
		t.Fatalf("ReadTranscript() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Type != "agent_result" {
		t.Fatalf("events[0].Type = %q, want agent_result", events[0].Type)
	}
}

func TestMarkInterruptedUpdatesActiveRuns(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
	}
	s := New(cfg)

	if err := s.SaveRun(domain.RunRecord{
		RunID:     "run_1",
		Agent:     "A",
		Status:    domain.RunRunning,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveRun(run_1) error = %v", err)
	}
	if err := s.SaveRun(domain.RunRecord{
		RunID:     "run_2",
		Agent:     "B",
		Status:    domain.RunQueued,
		CreatedAt: time.Now().UTC().Add(time.Second),
	}); err != nil {
		t.Fatalf("SaveRun(run_2) error = %v", err)
	}

	if err := s.MarkActiveRunsInterrupted(); err != nil {
		t.Fatalf("MarkActiveRunsInterrupted() error = %v", err)
	}

	runs, err := s.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(runs))
	}
	for _, run := range runs {
		if run.Status != domain.RunInterrupted {
			t.Fatalf("run %s status = %q, want %q", run.RunID, run.Status, domain.RunInterrupted)
		}
	}
}
