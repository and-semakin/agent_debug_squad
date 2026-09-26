package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

// startScopedStack wires ownership, a workflow-only orchestrator, and a
// manager without starting it, exactly like the one-shot run command does.
func startScopedStack(t *testing.T, cfg domain.SessionConfig) (*Manager, *store.Store, func()) {
	t.Helper()
	st := store.New(cfg)
	ownership, err := store.AcquireSessionOwnership(st.SessionDir())
	if err != nil {
		t.Fatalf("ownership: %v", err)
	}
	orch, err := orchestrator.NewWorkflowOnly(context.Background(), cfg, st)
	if err != nil {
		_ = ownership.Release()
		t.Fatalf("workflow-only orchestrator: %v", err)
	}
	m := NewManager(cfg, st, orch)
	cleanup := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.Stop(stopCtx)
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer waitCancel()
		_ = orch.WaitForWorkers(waitCtx)
		_ = ownership.Release()
	}
	return m, st, cleanup
}

// TestOneShotStartScopedRecoversOnlySelected covers scoped startup: the
// selected nonterminal execution is recovered, a foreign nonterminal
// execution refuses startup, and an empty selection requires no nonterminal
// work.
func TestOneShotStartScopedRecoversOnlySelected(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "scoped", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "Do it."},
		},
	}

	t.Run("recovers selected execution", func(t *testing.T) {
		cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "50"))
		m, _, cleanup := startStack(t, cfg)
		view, created, err := m.Create(context.Background(), "req-scoped")
		if err != nil || !created {
			t.Fatalf("create: created=%v err=%v", created, err)
		}
		executionID := view.ExecutionID
		// Stop with an active owned worker: the attempt stays uncommitted so
		// the next scoped start must recover it conservatively.
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := m.Stop(stopCtx); err != nil {
			t.Logf("stop with live worker: %v", err)
		}
		cleanup()

		m2, _, cleanup2 := startScopedStack(t, cfg)
		defer cleanup2()
		if err := m2.StartScoped(context.Background(), executionID); err != nil {
			t.Fatalf("StartScoped: %v", err)
		}
		got, err := m2.View(executionID)
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		if got.State != domain.WorkflowNeedsAttention && got.State != domain.WorkflowRunning {
			t.Fatalf("recovered state = %s, want needs_attention or running", got.State)
		}
	})

	t.Run("refuses foreign nonterminal execution", func(t *testing.T) {
		cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "50"))
		m, _, cleanup := startStack(t, cfg)
		view, _, err := m.Create(context.Background(), "req-foreign")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		foreign := view.ExecutionID
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := m.Stop(stopCtx); err != nil {
			t.Logf("stop: %v", err)
		}
		cleanup()

		m2, _, cleanup2 := startScopedStack(t, cfg)
		defer cleanup2()
		err = m2.StartScoped(context.Background(), "wf_000042")
		if err == nil {
			t.Fatalf("StartScoped with a foreign nonterminal execution must fail")
		}
		if _, err := m2.View(foreign); err != nil {
			t.Fatalf("foreign execution must stay loadable: %v", err)
		}
	})

	t.Run("empty selection starts with only terminal history", func(t *testing.T) {
		cfg := e2eConfig(t, def, fakeAgent("alpha"))
		m, _, cleanup := startStack(t, cfg)
		view, _, err := m.Create(context.Background(), "req-done")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		waitTerminal(t, m, view.ExecutionID, 15*time.Second)
		cleanup()

		m2, _, cleanup2 := startScopedStack(t, cfg)
		defer cleanup2()
		if err := m2.StartScoped(context.Background(), ""); err != nil {
			t.Fatalf("StartScoped with terminal history: %v", err)
		}
	})
}

// TestOneShotObserveTerminalIsTerminalOnly verifies the observation surface:
// intervention states keep it waiting, terminal states release it, and
// context cancellation returns the current view without an error.
func TestOneShotObserveTerminalIsTerminalOnly(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "observe", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "Do it."},
		},
	}

	t.Run("releases only on terminal state", func(t *testing.T) {
		cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "30"))
		m, _, cleanup := startStack(t, cfg)
		defer cleanup()
		view, _, err := m.Create(context.Background(), "req-observe")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		revisions := 0
		final, err := m.ObserveTerminal(context.Background(), view.ExecutionID, func(domain.WorkflowExecutionView) {
			revisions++
		})
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if final.State != domain.WorkflowSucceeded {
			t.Fatalf("state = %s, want succeeded", final.State)
		}
		if revisions == 0 {
			t.Fatalf("observer never reported updates")
		}
	})

	t.Run("context cancellation returns without error", func(t *testing.T) {
		cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "5000"))
		m, _, cleanup := startStack(t, cfg)
		defer cleanup()
		view, _, err := m.Create(context.Background(), "req-cancel")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		got, err := m.ObserveTerminal(ctx, view.ExecutionID, nil)
		if err != nil {
			t.Fatalf("observe must return nil error on context expiry, got %v", err)
		}
		if got.State == domain.WorkflowSucceeded {
			t.Fatalf("execution must still be running")
		}
		// Clean up the long-running worker through the normal controls.
		_, _ = m.Cancel(view.ExecutionID, CancelOptions{})
		_, _ = m.ObserveTerminal(context.Background(), view.ExecutionID, nil)
	})

	t.Run("ignores intervention but keeps waiting", func(t *testing.T) {
		cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "200"))
		m, _, cleanup := startStack(t, cfg)
		defer cleanup()
		view, _, err := m.Create(context.Background(), "req-pause")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := m.Pause(view.ExecutionID); err != nil {
			t.Fatalf("pause: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		got, err := m.ObserveTerminal(ctx, view.ExecutionID, nil)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if got.State != domain.WorkflowPaused {
			t.Fatalf("state = %s, want paused (intervention must not end observation)", got.State)
		}
	})
}

// TestOneShotTerminalFence verifies the serialized fence: after the selected
// execution commits a terminal state, a retry can no longer reopen it.
func TestOneShotTerminalFence(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "fence", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "Do it.", AllowedToFail: true},
		},
	}
	cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "200"))
	m, _, cleanup := startStack(t, cfg)
	defer cleanup()

	view, _, err := m.Create(context.Background(), "req-fence")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	executionID := view.ExecutionID
	// The one-shot flow installs the fence target inside the same serialized
	// step as creation (CreateSelected); do the equivalent here.
	m.SetOneShotTarget(executionID)

	// Before completion the ordinary rules reject a retry of an unfinished
	// task; the fence is not involved yet.
	if _, _, err := m.RetryTask(context.Background(), executionID, "a", RetryRequest{RequestID: "req-retry", ExpectedAttempt: 1}); err == nil || errors.Is(err, ErrExecutionSealed) {
		t.Fatalf("pre-terminal retry must fail under ordinary rules, got %v", err)
	}

	final := waitTerminal(t, m, executionID, 15*time.Second)
	if final.State != domain.WorkflowSucceeded {
		t.Fatalf("state = %s", final.State)
	}

	_, _, err = m.RetryTask(context.Background(), executionID, "a", RetryRequest{RequestID: "req-retry2", ExpectedAttempt: 1})
	if !errors.Is(err, ErrExecutionSealed) {
		t.Fatalf("retry after terminal commitment: err = %v, want ErrExecutionSealed", err)
	}
}

// TestOneShotOwnsRun verifies run ownership resolution by execution number,
// including completed runs after the active slot was cleared.
func TestOneShotOwnsRun(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "owns", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "Do it."},
		},
	}
	cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "80"))
	m, _, cleanup := startStack(t, cfg)
	defer cleanup()

	view, _, err := m.Create(context.Background(), "req-owns")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	executionID := view.ExecutionID
	m.SetOneShotTarget(executionID)
	prefix := "wrun_" + executionNumber(executionID) + "_"

	if !m.OwnsRun(prefix + "000003") {
		t.Fatalf("run of the selected execution must be owned")
	}
	if m.OwnsRun("wrun_999999_000001") {
		t.Fatalf("run of another execution must not be owned")
	}
	if m.OwnsRun("run_000001") {
		t.Fatalf("manual run IDs must not be owned")
	}
	waitTerminal(t, m, executionID, 15*time.Second)
	if !m.OwnsRun(prefix + "000001") {
		t.Fatalf("completed runs of the selected execution must stay owned")
	}
}
