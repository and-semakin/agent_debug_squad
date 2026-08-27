package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
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

func TestSaveConfigKeepsRecoveryValuesOwnerReadable(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
		Agents: []domain.AgentSpec{{
			Name:    "Reviewer",
			Backend: "opencode",
			Options: map[string]any{"password": "recovery-secret"},
		}},
	}
	s := New(cfg)
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	path := filepath.Join(s.SessionDir(), "config.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(config.json) error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config.json mode = %#o, want 0600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(config.json) error = %v", err)
	}
	var saved domain.SessionConfig
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Unmarshal(config.json) error = %v", err)
	}
	if got := saved.Agents[0].Options["password"]; got != "recovery-secret" {
		t.Fatalf("persisted password = %#v, want recovery value", got)
	}
}

func TestStoreAppendsRunEventsAndStderr(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
	}
	s := New(cfg)
	run := domain.RunRecord{RunID: "run_000001", Agent: "Reviewer"}

	eventsPath, err := s.AppendRunEvents(run, "line one")
	if err != nil {
		t.Fatalf("AppendRunEvents(first) error = %v", err)
	}
	if _, err := s.AppendRunEvents(run, "line two"); err != nil {
		t.Fatalf("AppendRunEvents(second) error = %v", err)
	}
	stderrPath, err := s.AppendRunStderr(run, "err one")
	if err != nil {
		t.Fatalf("AppendRunStderr(error) = %v", err)
	}

	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("ReadFile(eventsPath) error = %v", err)
	}
	if string(events) != "line one\nline two\n" {
		t.Fatalf("events = %q", string(events))
	}
	stderr, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("ReadFile(stderrPath) error = %v", err)
	}
	if string(stderr) != "err one\n" {
		t.Fatalf("stderr = %q", string(stderr))
	}
}

func TestStoreSerializesConcurrentTranscriptAppends(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
	s := New(cfg)

	const count = 200
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.AppendTranscript(domain.TranscriptEvent{
				Type:  "facilitator_message",
				RunID: fmt.Sprintf("run_%06d", i),
				Text:  strings.Repeat(fmt.Sprintf("message-%d-", i), 128),
				At:    time.Now().UTC(),
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AppendTranscript() error = %v", err)
		}
	}

	events, err := s.ReadTranscript()
	if err != nil {
		t.Fatalf("ReadTranscript() error = %v", err)
	}
	if len(events) != count {
		t.Fatalf("len(events) = %d, want %d", len(events), count)
	}
	seen := make(map[string]bool, count)
	for _, event := range events {
		seen[event.RunID] = true
	}
	for i := 0; i < count; i++ {
		runID := fmt.Sprintf("run_%06d", i)
		if !seen[runID] {
			t.Fatalf("transcript is missing %s", runID)
		}
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

func TestStoreRejectsUnsafeAgentName(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
	}
	s := New(cfg)

	err := s.SaveAgentState(domain.AgentState{
		Name:      "../escape",
		Backend:   "fake",
		Status:    domain.AgentIdle,
		CreatedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("SaveAgentState() error = nil, want unsafe path error")
	}

	if _, statErr := os.Stat(filepath.Join(s.SessionDir(), "escape", "state.json")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe agent state was written, stat error = %v", statErr)
	}
}

func TestStoreRejectsUnsafeRunID(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
	}
	s := New(cfg)

	err := s.SaveRun(domain.RunRecord{
		RunID:     "../escape",
		Agent:     "Reviewer",
		Status:    domain.RunQueued,
		CreatedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("SaveRun() error = nil, want unsafe path error")
	}

	if _, statErr := os.Stat(filepath.Join(s.SessionDir(), "escape", "run.json")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe run was written, stat error = %v", statErr)
	}
}
