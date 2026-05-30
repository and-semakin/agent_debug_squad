package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/store"
)

func TestSubmitRunCompletesAndWritesOutput(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "please review", map[string]string{"ticket": "123"})
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	if run.RunID != "run_000001" {
		t.Fatalf("RunID = %q, want run_000001", run.RunID)
	}
	if run.Status != domain.RunQueued {
		t.Fatalf("initial Status = %q, want %q", run.Status, domain.RunQueued)
	}

	completed, err := o.Wait(ctx, run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("completed Status = %q, want %q; error = %v", completed.Status, domain.RunCompleted, completed.Error)
	}
	if completed.OutputPath == nil || *completed.OutputPath == "" {
		t.Fatalf("OutputPath = %v, want non-empty", completed.OutputPath)
	}

	loaded, err := o.Run(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if loaded.Status != domain.RunCompleted {
		t.Fatalf("loaded Status = %q, want %q", loaded.Status, domain.RunCompleted)
	}

	events, err := o.Transcript(ctx)
	if err != nil {
		t.Fatalf("Transcript() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Type != "facilitator_message" {
		t.Fatalf("events[0].Type = %q, want facilitator_message", events[0].Type)
	}
	if events[1].Type != "agent_result" {
		t.Fatalf("events[1].Type = %q, want agent_result", events[1].Type)
	}
}

func TestSubmitRunRejectsConcurrentRunForSameAgent(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := o.SubmitRun(ctx, "Reviewer", "first", nil)
	if err != nil {
		t.Fatalf("SubmitRun(first) error = %v", err)
	}

	_, err = o.SubmitRun(ctx, "Reviewer", "second", nil)
	if !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("SubmitRun(second) error = %v, want %v", err, ErrAgentBusy)
	}

	if _, err := o.Wait(ctx, first.RunID, time.Second); err != nil {
		t.Fatalf("Wait(first) error = %v", err)
	}
}

func TestSubmitRunContinuesAfterCallerContextCancelled(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	submitCtx, cancel := context.WithCancel(ctx)
	run, err := o.SubmitRun(submitCtx, "Reviewer", "continue after submit", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	cancel()

	completed, err := o.Wait(ctx, run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("Status = %q, want %q; error = %v", completed.Status, domain.RunCompleted, completed.Error)
	}
}

func TestWaitReturnsImmediatelyForCompletedRun(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "finish first", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	completed, err := o.Wait(ctx, run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait(first) error = %v", err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("first Status = %q, want %q", completed.Status, domain.RunCompleted)
	}

	o.mu.Lock()
	o.waiters[run.RunID] = make(chan struct{})
	o.mu.Unlock()

	withTinyTimeout, err := o.Wait(ctx, run.RunID, time.Nanosecond)
	if err != nil {
		t.Fatalf("Wait(tiny timeout) error = %v", err)
	}
	if withTinyTimeout.Status != domain.RunCompleted {
		t.Fatalf("tiny timeout Status = %q, want %q", withTinyTimeout.Status, domain.RunCompleted)
	}

	zeroCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	withZeroTimeout, err := o.Wait(zeroCtx, run.RunID, 0)
	if err != nil {
		t.Fatalf("Wait(zero timeout) error = %v", err)
	}
	if withZeroTimeout.Status != domain.RunCompleted {
		t.Fatalf("zero timeout Status = %q, want %q", withZeroTimeout.Status, domain.RunCompleted)
	}
}

func TestSubmitRunAllowsDifferentAgentsConcurrently(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer", "Implementer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := o.SubmitRun(ctx, "Reviewer", "first", nil)
	if err != nil {
		t.Fatalf("SubmitRun(Reviewer) error = %v", err)
	}
	second, err := o.SubmitRun(ctx, "Implementer", "second", nil)
	if err != nil {
		t.Fatalf("SubmitRun(Implementer) error = %v", err)
	}

	firstDone, err := o.Wait(ctx, first.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait(first) error = %v", err)
	}
	secondDone, err := o.Wait(ctx, second.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait(second) error = %v", err)
	}
	if firstDone.Status != domain.RunCompleted || secondDone.Status != domain.RunCompleted {
		t.Fatalf("statuses = %q/%q, want both completed", firstDone.Status, secondDone.Status)
	}
}

func TestNewMarksExistingRunningRunInterrupted(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	s := store.New(cfg)
	existing := domain.RunRecord{
		RunID:     "run_000001",
		Agent:     "Reviewer",
		Status:    domain.RunRunning,
		Message:   "in flight",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.SaveRun(existing); err != nil {
		t.Fatalf("SaveRun() error = %v", err)
	}

	if _, err := New(ctx, cfg, s); err != nil {
		t.Fatalf("New() error = %v", err)
	}

	loaded, err := s.LoadRun(existing.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if loaded.Status != domain.RunInterrupted {
		t.Fatalf("Status = %q, want %q", loaded.Status, domain.RunInterrupted)
	}
	if loaded.Error == nil || *loaded.Error == "" {
		t.Fatalf("Error = %v, want startup interruption message", loaded.Error)
	}
}

func TestRunFailsWhenFinalAgentStatePersistenceFails(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	s := store.New(cfg)
	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	agentDir := filepath.Join(s.SessionDir(), "agents", "Reviewer")
	if err := os.Chmod(agentDir, 0o500); err != nil {
		t.Fatalf("Chmod(agentDir) error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(agentDir, 0o700); err != nil {
			t.Errorf("cleanup Chmod(agentDir) error = %v", err)
		}
	})

	run, err := o.SubmitRun(ctx, "Reviewer", "please review", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	completed, err := o.Wait(ctx, run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if completed.Status != domain.RunFailed {
		t.Fatalf("Status = %q, want %q", completed.Status, domain.RunFailed)
	}
	if completed.Error == nil || !strings.Contains(*completed.Error, "save agent state") {
		t.Fatalf("Error = %v, want save agent state error", completed.Error)
	}
}

func testConfig(t *testing.T, agentNames ...string) domain.SessionConfig {
	t.Helper()

	agents := make([]domain.AgentSpec, 0, len(agentNames))
	for _, name := range agentNames {
		agents = append(agents, domain.AgentSpec{
			Name:          name,
			Backend:       "fake",
			StartupPrompt: "You are " + name,
		})
	}

	return domain.SessionConfig{
		SessionName:  "test",
		SessionID:    "session_test",
		WorkspaceDir: t.TempDir(),
		StateDirName: ".agent-debug-squad",
		Host:         "127.0.0.1",
		Port:         8080,
		Agents:       agents,
	}
}
