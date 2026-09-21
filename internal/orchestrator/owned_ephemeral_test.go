package orchestrator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// The dispatch and recovery paths revalidate saved agent specifications via
// NormalizeAgentOptions and agentSpecWithDefaults; the ephemeral flag must
// survive both.
func TestEphemeralSpecSurvivesRevalidationAndDefaults(t *testing.T) {
	spec := domain.AgentSpec{Name: "Reviewer", Backend: "fake", StartupPrompt: "One shot.", Ephemeral: true}
	normalized, err := config.NormalizeAgentOptions(spec)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !normalized.Ephemeral {
		t.Fatal("NormalizeAgentOptions must retain the ephemeral flag")
	}
	cfg := domain.SessionConfig{Defaults: domain.SessionDefaults{Yolo: true}}
	withDefaults := agentSpecWithDefaults(cfg, normalized)
	if !withDefaults.Ephemeral {
		t.Fatal("agentSpecWithDefaults must retain the ephemeral flag")
	}
}

// Submitting a saved ephemeral spec goes through the same owned-runtime path
// as any workflow attempt; the fresh-session-per-attempt property must hold.
func TestSubmitOwnedRunWithEphemeralSavedSpecKeepsFreshSessions(t *testing.T) {
	o, _ := newOwnedTestOrchestrator(t, "Reviewer")
	spec := domain.AgentSpec{Name: "Reviewer", Backend: "fake", StartupPrompt: "One shot.", Ephemeral: true}
	outcomes := make(chan domain.OwnedRunOutcome, 2)
	sessionIDs := map[string]bool{}
	for _, runID := range []string{"wrun_000001_000001", "wrun_000001_000002"} {
		statePath := filepath.Join(t.TempDir(), "state.json")
		if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
			RunID:     runID,
			Agent:     "Reviewer",
			Message:   "attempt",
			Spec:      &spec,
			OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
			StatePath: statePath,
		}); err != nil {
			t.Fatalf("submit %s: %v", runID, err)
		}
		select {
		case <-outcomes:
		case <-time.After(5 * time.Second):
			t.Fatal("owned run did not complete")
		}
		var state domain.AgentState
		readFileJSON(t, statePath, &state)
		if state.BackendSessionID == "" {
			t.Fatalf("owned state %s has no backend session id", statePath)
		}
		if sessionIDs[state.BackendSessionID] {
			t.Fatalf("ephemeral attempts shared backend session %q", state.BackendSessionID)
		}
		sessionIDs[state.BackendSessionID] = true
	}
}
