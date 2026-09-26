package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

func ephemeralHashDefinition() (domain.WorkflowDefinition, map[string]domain.AgentSpec) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "hash-fixture", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "Do the single step."},
		},
	}
	agents := map[string]domain.AgentSpec{
		"alpha": {Name: "alpha", Backend: "fake", StartupPrompt: "You are alpha."},
	}
	return def, agents
}

func TestHashWorkflowDefinitionStableWithoutEphemeral(t *testing.T) {
	def, agents := ephemeralHashDefinition()
	// Golden pin: with the flag unset and omitted from the JSON encoding, the
	// hash must match what binaries without the Ephemeral field computed, so
	// existing snapshots and replays stay stable.
	const golden = "3e484ddd3e24e42aa4bae9c16b4f4268ca00ddfdeed4b22e5f74715a2f5deaf2"
	if got := HashWorkflowDefinition(def, agents); got != golden {
		t.Fatalf("hash drift without the flag: got %s, want %s", got, golden)
	}

	ephemeral := agents["alpha"]
	ephemeral.Ephemeral = true
	agents["alpha"] = ephemeral
	if got := HashWorkflowDefinition(def, agents); got == golden {
		t.Fatal("setting ephemeral must change the definition hash")
	}
}

func TestEphemeralDeclarationPersistsWithExecution(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "ephemeral-persist", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "One shot."},
		},
	}
	alpha := fakeAgent("alpha")
	alpha.Ephemeral = true
	cfg := e2eConfig(t, def, alpha)
	m, _, cleanup := startStack(t, cfg)
	defer cleanup()

	view, created, err := m.Create(context.Background(), "req-ephemeral-persist")
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	if final := waitTerminal(t, m, view.ExecutionID, 15*time.Second); final.State != domain.WorkflowSucceeded {
		t.Fatalf("state: %v", final.State)
	}

	// The recovery path reloads and re-saves this same snapshot, so a store
	// round trip must retain the flag and stay hash-consistent.
	st := store.New(cfg)
	snapshot, err := st.LoadWorkflowSnapshot(view.ExecutionID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snapshot.Agents["alpha"].Ephemeral {
		t.Fatalf("saved agents must record the ephemeral flag: %+v", snapshot.Agents["alpha"])
	}
	if HashWorkflowDefinition(snapshot.Definition, snapshot.Agents) != snapshot.DefinitionHash {
		t.Fatal("persisted snapshot must keep the definition hash consistent with its saved agents")
	}
}

func TestEphemeralFlagParticipatesInReplayIdentity(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "ephemeral-replay", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "One shot."},
		},
	}
	cfg := e2eConfig(t, def, fakeAgent("alpha"))
	m, _, cleanup := startStack(t, cfg)
	defer cleanup()

	view, created, err := m.Create(context.Background(), "req-ephemeral-replay")
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	replay, createdAgain, err := m.Create(context.Background(), "req-ephemeral-replay")
	if err != nil || createdAgain || replay.ExecutionID != view.ExecutionID {
		t.Fatalf("unchanged flag must replay idempotently: created=%v err=%v", createdAgain, err)
	}

	cfg.Agents[0].Ephemeral = true
	m.cfg = cfg
	if _, _, err := m.Create(context.Background(), "req-ephemeral-replay"); !errors.Is(err, ErrDefinitionChanged) {
		t.Fatalf("flipped ephemeral must be a changed definition, got %v", err)
	}
}
