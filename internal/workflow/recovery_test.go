package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestRecoveryDispatchesConsumerWithoutRerunningPredecessors(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "recover-chain", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	first := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := first.m.Create("req-crash"); err != nil {
		t.Fatalf("create: %v", err)
	}
	first.pump()
	first.exec.releaseSuccess(mustFindRunForTask(t, first, "a"), "a result")
	// Predecessor success is committed; crash before reserving b.
	c := <-first.m.completions
	first.m.handleCompletion(c)

	second := first.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer finishLiveRuns(t, second)
	second.pump()

	view, err := second.m.View("wf_000001")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if findTaskView(view, "a").State != domain.WorkflowTaskSucceeded {
		t.Fatalf("committed predecessor must be preserved: %+v", findTaskView(view, "a"))
	}
	b := findTaskView(view, "b")
	if b.State != domain.WorkflowTaskRunning {
		t.Fatalf("recovery must dispatch the consumer, got %v", b.State)
	}
	if len(b.Attempts) != 1 || b.Attempts[0].RunID == "wrun_000001_000001" {
		t.Fatalf("consumer must get a fresh run identity: %+v", b.Attempts)
	}
}

func TestRecoveryMarksUncertainAttemptInterrupted(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "recover-uncertain", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	first := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := first.m.Create("req-uncertain"); err != nil {
		t.Fatalf("create: %v", err)
	}
	first.pump() // a reserved and dispatched; crash before any outcome

	second := first.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer finishLiveRuns(t, second)
	second.pump()

	view, err := second.m.View("wf_000001")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	a := findTaskView(view, "a")
	if a.State != domain.WorkflowTaskInterrupted {
		t.Fatalf("uncertain attempt must interrupt, got %v", a.State)
	}
	if len(a.Attempts) != 1 || a.Attempts[0].Reason != "recovery" {
		t.Fatalf("attempt: %+v", a.Attempts)
	}
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("execution must need attention, got %v", view.State)
	}
	if len(second.exec.dispatched()) != 0 {
		t.Fatalf("no work may be resent automatically: %v", second.exec.dispatched())
	}
	if b := findTaskView(view, "b"); b.State != domain.WorkflowTaskPending {
		t.Fatalf("descendant stays pending: %v", b.State)
	}

	// Retry with cleanup confirmation resolves the uncertainty.
	if _, _, err := second.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-after-crash", ExpectedAttempt: 1, ConfirmPreviousStopped: true}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	second.pump()
	a = findTaskView(mustView2(t, second), "a")
	if len(a.Attempts) != 2 || a.Attempts[1].State != domain.WorkflowAttemptRunning {
		t.Fatalf("retry must dispatch after confirmation: %+v", a.Attempts)
	}
}

// finishLiveRuns releases every live fake run and stops the manager with a
// bounded context so recovery tests never hang on shutdown.
func finishLiveRuns(t *testing.T, fx *managerFixture) {
	t.Helper()
	for _, runID := range fx.exec.liveRunIDs() {
		fx.exec.releaseSuccess(runID, "done")
	}
	fx.pump()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = fx.m.Stop(ctx)
}

func mustView2(t *testing.T, fx *managerFixture) domain.WorkflowExecutionView {
	t.Helper()
	view, err := fx.m.View("wf_000001")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	return view
}

func TestRecoveryKeepsPausedPausedAndCancellingCancelling(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "recover-modes", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-modes"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	fx.exec.releaseSuccess(runA, "a result")
	fx.pump()

	second := fx.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	second.pump()
	view := mustView2(t, second)
	if view.State != domain.WorkflowPaused {
		t.Fatalf("paused stays paused after restart, got %v", view.State)
	}
	if len(second.exec.dispatched()) != 0 {
		t.Fatalf("no dispatch while paused: %v", second.exec.dispatched())
	}
	second.m.Stop(context.Background())

	// Cancelling intent survives restart and blocks new work until resolved.
	third := fx.restart()
	if err := third.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer finishLiveRuns(t, third)
	third.pump()
	if _, err := third.m.Resume("wf_000001"); err != nil {
		t.Fatalf("resume paused: %v", err)
	}
	third.pump()
	if _, err := third.m.Cancel("wf_000001", CancelOptions{}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// Simulate the crash: no completion was processed.
	fourth := fx.restart()
	if err := fourth.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer finishLiveRuns(t, fourth)
	fourth.pump()

	view = mustView2(t, fourth)
	if view.State != domain.WorkflowCancelling {
		t.Fatalf("cancelling intent must survive restart, got %v", view.State)
	}
	if len(fourth.exec.dispatched()) != 0 {
		t.Fatalf("cancelling recovery must not start tasks: %v", fourth.exec.dispatched())
	}
	// The interrupted attempt keeps cancellation open until the caller
	// asserts external cleanup.
	if _, err := fourth.m.Cancel("wf_000001", CancelOptions{ConfirmPreviousStopped: true}); err != nil {
		t.Fatalf("cancel with assertion: %v", err)
	}
	fourth.pump()
	if view = mustView2(t, fourth); view.State != domain.WorkflowCancelled {
		t.Fatalf("cancel must finish after assertion, got %v", view.State)
	}
}

func TestRecoveryRejectsDamagedSnapshot(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	st := fx.st
	execDir, err := st.WorkflowDir("wf_000001")
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	// Write an unsupported-version snapshot directly.
	if err := writeSnapshotFile(execDir, `{"schema_version": 99, "execution_id": "wf_000001"}`); err != nil {
		t.Fatalf("write: %v", err)
	}

	second := fx.restart()
	if err := second.m.Start(context.Background()); err == nil {
		second.m.Stop(context.Background())
		t.Fatal("unknown schema version must fail closed")
	}
}

func TestRestartNeverTreatsStartupAsSubmission(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-restart"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	for i := 0; i < 3; i++ {
		live := fx.exec.liveRunIDs()
		if len(live) == 0 {
			break
		}
		fx.exec.releaseSuccess(live[0], "ok")
		fx.pump()
	}

	second := fx.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer finishLiveRuns(t, second)
	second.pump()

	list, err := second.m.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("restart must not create executions: %+v", list)
	}

	// The same request id still replays to the original execution.
	view, created, err := second.m.Create("req-restart")
	if err != nil || created {
		t.Fatalf("replay after restart: created=%v err=%v", created, err)
	}
	if view.ExecutionID != "wf_000001" {
		t.Fatalf("replay id: %s", view.ExecutionID)
	}
}

func TestStopPreservesUncertaintyForUnjoinedWorkers(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "shutdown", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fx.m.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, _, err := fx.m.Create("req-stop"); err != nil {
		t.Fatalf("create: %v", err)
	}
	waitForDispatch(t, fx)

	// Stop with a very short bound: the worker never reports back.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stopCancel()
	if err := fx.m.Stop(stopCtx); err == nil {
		t.Skip("worker joined within the bound; nothing to assert")
	}

	second := fx.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer finishLiveRuns(t, second)
	second.pump()
	view := mustView2(t, second)
	a := findTaskView(view, "a")
	if a.State != domain.WorkflowTaskInterrupted {
		t.Fatalf("unjoined worker must surface as interrupted, got %v", a.State)
	}
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("state: %v", view.State)
	}
	if len(second.exec.dispatched()) != 0 {
		t.Fatalf("uncertain work must not repeat: %v", second.exec.dispatched())
	}
}

func waitForDispatch(t *testing.T, fx *managerFixture) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(fx.exec.dispatched()) > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("nothing was dispatched")
}

func TestRecoveryRefusesTwoNonterminalExecutions(t *testing.T) {
	st := newRecordingStore(t)
	cfg := testWorkflowConfig(chainDefinition(), "a1", "a2", "a3")
	cfg.WorkspaceDir = t.TempDir()
	def := cfg.Workflow
	agents := map[string]domain.AgentSpec{}
	for _, spec := range cfg.Agents {
		agents[spec.Name] = spec
	}
	now := time.Now().UTC()
	for i, id := range []string{"wf_000001", "wf_000002"} {
		snapshot := &domain.WorkflowSnapshot{
			SchemaVersion:  domain.WorkflowSchemaVersion,
			ExecutionID:    id,
			Revision:       1,
			Definition:     *def,
			DefinitionHash: HashWorkflowDefinition(*def, agents),
			Agents:         agents,
			RequestID:      "req-" + id,
			State:          domain.WorkflowRunning,
			Mode:           domain.WorkflowModeRunning,
			Tasks: map[string]*domain.WorkflowTaskExecution{
				"a": {TaskID: "a", Agent: "a1", State: domain.WorkflowTaskPending},
			},
			CreatedAt: now,
			UpdatedAt: now,
		}
		if i == 1 {
			snapshot.RequestID = "req-other"
		}
		if err := st.SaveWorkflowSnapshot(snapshot); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}

	m := NewManager(cfg, st, newFakeExecutor())
	if err := m.Start(context.Background()); err == nil {
		m.Stop(context.Background())
		t.Fatal("two nonterminal executions must fail closed")
	}
}

func writeSnapshotFile(execDir, body string) error {
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(execDir, "workflow.json"), []byte(body), 0o644)
}
