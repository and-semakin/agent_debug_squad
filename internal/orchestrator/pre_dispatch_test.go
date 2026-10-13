package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/adapters"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

type allocationAdapter struct {
	adapters.AgentAdapter
	allocated, sent, closed int
}

func (a *allocationAdapter) Init(_ context.Context, _ domain.AgentSpec, s domain.AgentState) (domain.AgentState, error) {
	a.allocated++
	s.BackendSessionID = "allocated-before-storage"
	return s, nil
}
func (a *allocationAdapter) Send(_ context.Context, s domain.AgentState, _ domain.RunRequest, _ domain.RunSink) (domain.RunResult, domain.AgentState, error) {
	a.sent++
	return domain.RunResult{FinalMessage: "must not dispatch"}, s, nil
}
func (a *allocationAdapter) ClosePreparation() error { a.closed++; return nil }

func TestSessionPersistenceFailureStopsDispatch(t *testing.T) {
	for _, owned := range []bool{false, true} {
		name := "manual"
		if owned {
			name = "owned"
		}
		t.Run(name, func(t *testing.T) {
			o, st := newOwnedTestOrchestrator(t, "Reviewer")
			rt := o.runtimes["Reviewer"]
			a := &allocationAdapter{AgentAdapter: rt.adapter}
			rt.adapter = a
			rt.initialized = false
			before := filepath.Join(st.SessionDir(), "agents", "Reviewer", "state.json")
			original, _ := os.ReadFile(before)
			blocker := filepath.Join(t.TempDir(), "file")
			os.WriteFile(blocker, []byte("keep"), 0600)
			rt.statePath = filepath.Join(blocker, "state.json")
			rt.owned = owned
			run := domain.RunRecord{RunID: "run_storage_failure", Agent: "Reviewer", Message: "never send", Status: domain.RunQueued}
			waiter := make(chan struct{})
			o.workerWG.Add(1)
			var outcome domain.OwnedRunOutcome
			o.runWorker(context.Background(), "Reviewer", run, waiter, func(v domain.OwnedRunOutcome) { outcome = v })
			if a.allocated != 1 || a.sent != 0 || a.closed != 1 {
				t.Fatalf("allocation=%d dispatch=%d cleanup=%d", a.allocated, a.sent, a.closed)
			}
			if outcome.Status != domain.RunFailed || !strings.Contains(outcome.Error, "before dispatch") {
				t.Fatal(outcome)
			}
			after, _ := os.ReadFile(before)
			if string(after) != string(original) {
				t.Fatal("previous durable state damaged")
			}
		})
	}
}
