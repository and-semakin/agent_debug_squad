package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

// TestNewWorkflowOnlyLeavesManualStateUntouched verifies the one-shot
// initialization: no manual agent initialization and no unrelated run-record
// rewrites, while config persistence and run-ID bookkeeping are retained.
func TestNewWorkflowOnlyLeavesManualStateUntouched(t *testing.T) {
	cfg := domain.SessionConfig{
		SessionName:  "oneshot",
		SessionID:    "session_oneshot",
		WorkspaceDir: t.TempDir(),
		StateDirName: ".agent-debug-squad",
		Host:         "127.0.0.1",
		Port:         0,
		Agents: []domain.AgentSpec{
			{Name: "alpha", Backend: "fake", StartupPrompt: "You are alpha."},
		},
	}
	st := store.New(cfg)

	// A prior process left an active manual run behind; normal
	// initialization reconciles it, workflow-only startup must not.
	active := domain.RunRecord{
		RunID:     "run_000001",
		Agent:     "alpha",
		Message:   "left running",
		Status:    domain.RunRunning,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.SaveRun(active); err != nil {
		t.Fatalf("save run: %v", err)
	}

	orch, err := NewWorkflowOnly(context.Background(), cfg, st)
	if err != nil {
		t.Fatalf("NewWorkflowOnly: %v", err)
	}

	if agents := orch.Agents(); len(agents) != 0 {
		t.Fatalf("workflow-only startup initialized %d manual agents", len(agents))
	}
	if _, err := st.LoadAgentState("alpha"); err == nil {
		t.Fatalf("manual agent state must not be created by workflow-only startup")
	} else if _, statErr := st.LoadAgentState("alpha"); statErr == nil {
		t.Fatalf("manual agent state file exists")
	}

	got, err := st.LoadRun("run_000001")
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	if got.Status != domain.RunRunning {
		t.Fatalf("unrelated run was rewritten to %s by workflow-only startup", got.Status)
	}
	if orch.nextRun != 2 {
		t.Fatalf("run-ID bookkeeping: nextRun = %d, want 2", orch.nextRun)
	}
}

// TestNewWorkflowOnlySavesConfig verifies config persistence is retained.
func TestNewWorkflowOnlySavesConfig(t *testing.T) {
	cfg := domain.SessionConfig{
		SessionName:  "oneshot-config",
		SessionID:    "session_oneshot_config",
		WorkspaceDir: t.TempDir(),
		StateDirName: ".agent-debug-squad",
		Host:         "127.0.0.1",
		Port:         0,
	}
	st := store.New(cfg)
	if _, err := NewWorkflowOnly(context.Background(), cfg, st); err != nil {
		t.Fatalf("NewWorkflowOnly: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(st.SessionDir(), "config.json"))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var saved domain.SessionConfig
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("parse saved config: %v", err)
	}
	if saved.SessionID != cfg.SessionID {
		t.Fatalf("saved session id = %q", saved.SessionID)
	}
}
