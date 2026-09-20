package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestCreateIsIdempotentPerRequestID(t *testing.T) {
	def := chainDefinition()
	fx := newManagerFixture(t, def, "a1", "a2", "a3")

	first, created, err := fx.m.Create("req-once")
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	second, createdAgain, err := fx.m.Create("req-once")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if createdAgain {
		t.Fatal("replay must not create a new execution")
	}
	if second.ExecutionID != first.ExecutionID || second.Revision != first.Revision {
		t.Fatalf("replay must return the original execution: %+v vs %+v", second, first)
	}
}

func TestCreateRejectsRequestIDReuseWithChangedDefinition(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Finish the first execution so the active check does not trigger.
	fx.pump()
	for i := 0; i < 3; i++ {
		live := fx.exec.liveRunIDs()
		if len(live) == 0 {
			break
		}
		fx.exec.releaseSuccess(live[0], "ok")
		fx.pump()
	}

	changed := chainDefinition()
	changed.Name = "chain-v2"
	fx.m.cfg.Workflow = &changed
	_, _, err := fx.m.Create("req-1")
	if !errors.Is(err, ErrDefinitionChanged) {
		t.Fatalf("changed definition with the same request id must conflict, got %v", err)
	}
	if _, _, err := fx.m.Create("req-2"); err != nil {
		t.Fatalf("a new request id with the new definition must work: %v", err)
	}
}

func TestCreateRejectsSecondNonterminalExecution(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-a"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := fx.m.Create("req-b"); !errors.Is(err, ErrExecutionActive) {
		t.Fatalf("competing submission must conflict, got %v", err)
	}
}

func TestConcurrentCreateAdmitsOneExecution(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	const workers = 8
	ids := make(chan string, workers)
	created := make(chan bool, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			view, wasCreated, err := fx.m.Create("shared-request")
			ids <- view.ExecutionID
			created <- wasCreated
			errs <- err
		}(i)
	}
	wg.Wait()
	close(ids)
	close(created)
	close(errs)

	createdCount := 0
	var firstID string
	for id := range ids {
		if firstID == "" {
			firstID = id
		}
		if id != firstID {
			t.Fatalf("concurrent submissions created multiple executions: %s vs %s", id, firstID)
		}
	}
	for wasCreated := range created {
		if wasCreated {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("exactly one creation expected, got %d", createdCount)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("replays must succeed: %v", err)
		}
	}
}

func TestPauseStopsReservationsAndPreservesFinishedWork(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "pause", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-pause"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")

	// Pause while a runs; its completion lands while paused.
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("repeated pause must be idempotent: %v", err)
	}
	fx.exec.releaseSuccess(runA, "a result")
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowPaused {
		t.Fatalf("state: %v", view.State)
	}
	if findTaskView(view, "a").State != domain.WorkflowTaskSucceeded {
		t.Fatalf("completed predecessor must be preserved: %+v", findTaskView(view, "a"))
	}
	if attempts := findTaskView(view, "b").Attempts; len(attempts) != 0 {
		t.Fatalf("no reservation while paused, got %+v", attempts)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("only a may be dispatched: %v", fx.exec.dispatched())
	}

	// Resume continues with b.
	if _, err := fx.m.Resume("wf_000001"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	fx.pump()
	if len(fx.exec.dispatched()) != 2 {
		t.Fatalf("b must dispatch after resume: %v", fx.exec.dispatched())
	}
}

func TestPauseRacesWithCompletion(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-race"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")

	// Completion arrives, then pause before the scheduler reconciles.
	fx.exec.releaseSuccess(runA, "a result")
	c := <-fx.m.completions
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	fx.m.handleCompletion(c)
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	if findTaskView(view, "a").State != domain.WorkflowTaskSucceeded {
		t.Fatalf("racing completion must be preserved: %+v", findTaskView(view, "a"))
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("no subsequent reservation until resume: %v", fx.exec.dispatched())
	}
}

func TestPauseRejectsTerminalAndCancelling(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-409"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "a"), "ok")

	if _, err := fx.m.Cancel("wf_000001", CancelOptions{}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	fx.pump()
	if _, err := fx.m.Pause("wf_000001"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("pause while cancelling must conflict, got %v", err)
	}
}

func TestCancelPreventsDownstreamAndCompletesWhenWorkersStop(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "cancel", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A.", AllowedToFail: true},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}, MinSuccessfulDependencies: 1},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-cancel"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")

	view, err := fx.m.Cancel("wf_000001", CancelOptions{})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if view.State != domain.WorkflowCancelling {
		t.Fatalf("cancelling state: %v", view.State)
	}
	if !fx.exec.wasCancelled(runA) {
		t.Fatal("active owned run must be cancelled")
	}

	// Repeated cancel is idempotent.
	if _, err := fx.m.Cancel("wf_000001", CancelOptions{}); err != nil {
		t.Fatalf("repeated cancel: %v", err)
	}

	// The worker stops with a cancellation error.
	fx.exec.release(runA, domain.OwnedRunOutcome{RunID: runA, Status: domain.RunFailed, Error: "context canceled"})
	fx.pump()

	view, _ = fx.m.View("wf_000001")
	if view.State != domain.WorkflowCancelled {
		t.Fatalf("final state: %v (%+v)", view.State, view.TaskCounts)
	}
	b := findTaskView(view, "b")
	if b.State != domain.WorkflowTaskCancelled {
		t.Fatalf("downstream must be cancelled, not released by allowed_to_fail: %v", b.State)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("b must never dispatch: %v", fx.exec.dispatched())
	}
}

func TestCancelWithCleanupAssertionClosesInterruptedWork(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "confirm", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-confirm"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")

	if _, err := fx.m.Cancel("wf_000001", CancelOptions{}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// The worker never stops in this process: interrupt it via recovery-style
	// state by leaving it live; cancel-with-confirm must refuse while active.
	if _, err := fx.m.Cancel("wf_000001", CancelOptions{ConfirmPreviousStopped: true}); err == nil {
		t.Fatal("cleanup assertion must not override a known active worker")
	}

	fx.exec.release(runA, domain.OwnedRunOutcome{RunID: runA, Status: domain.RunFailed, Error: "context canceled"})
	fx.pump()
	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowCancelled {
		t.Fatalf("after the worker stops the cancel must complete, got %v", view.State)
	}
}

func TestRetryFailedTaskReservesFreshAttempt(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "retry", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-retry"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "a"), "first attempt broke")
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowFailed {
		t.Fatalf("execution failed: %v", view.State)
	}

	// Idempotent retry: same request replays the same new attempt.
	first, created, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-1", ExpectedAttempt: 1})
	if err != nil || !created {
		t.Fatalf("retry: created=%v err=%v", created, err)
	}
	replay, createdAgain, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-1", ExpectedAttempt: 1})
	if err != nil || createdAgain {
		t.Fatalf("replay: created=%v err=%v", createdAgain, err)
	}
	if replay.Revision != first.Revision {
		t.Fatalf("replay must return the original attempt revision")
	}

	// Stale expected attempt conflicts.
	if _, _, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-2", ExpectedAttempt: 1}); !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("stale expected_attempt must conflict, got %v", err)
	}

	fx.pump()
	aView := findTaskView(mustView(t, fx), "a")
	if len(aView.Attempts) != 2 {
		t.Fatalf("retry must create a second attempt: %+v", aView.Attempts)
	}
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "a"), "second attempt ok")
	fx.pump()

	// b was blocked by the first failure; it must be reevaluated and run.
	view = mustView(t, fx)
	if findTaskView(view, "b").State != domain.WorkflowTaskRunning {
		t.Fatalf("blocked descendant must be reevaluated after retry, got %v", findTaskView(view, "b").State)
	}
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "b"), "b ok")
	fx.pump()
	if view = mustView(t, fx); view.State != domain.WorkflowSucceeded {
		t.Fatalf("final state after retry: %v", view.State)
	}
}

func mustView(t *testing.T, fx *managerFixture) domain.WorkflowExecutionView {
	t.Helper()
	view, err := fx.m.View("wf_000001")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	return view
}

func TestRetryRejectedAfterDownstreamConsumption(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "consumed", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A.", AllowedToFail: true},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-consumed"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "a"), "tolerated failure")
	fx.pump() // b consumes a's failure in its manifest
	runB := mustFindRunForTask(t, fx, "b")
	fx.exec.releaseSuccess(runB, "b ok")
	fx.pump()

	if view := mustView(t, fx); view.State != domain.WorkflowCompletedWithError {
		t.Fatalf("state: %v", view.State)
	}
	_, _, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-late", ExpectedAttempt: 1})
	if !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("retry after consumption must conflict, got %v", err)
	}
}

func TestRetryInterruptedRequiresCleanupConfirmation(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "interrupted", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create("req-interrupted"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")
	fx.exec.release(runA, domain.OwnedRunOutcome{RunID: runA, Status: domain.RunInterrupted, Error: "uncertain"})
	fx.pump()

	view := mustView(t, fx)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("interruption must hold attention: %v", view.State)
	}
	if _, _, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-1", ExpectedAttempt: 1}); !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("unconfirmed retry must be rejected, got %v", err)
	}
	if _, _, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-1", ExpectedAttempt: 1, ConfirmPreviousStopped: true}); err != nil {
		t.Fatalf("confirmed retry must be accepted: %v", err)
	}
	fx.pump()
	aView := findTaskView(mustView(t, fx), "a")
	if len(aView.Attempts) != 2 || aView.Attempts[1].State != domain.WorkflowAttemptRunning {
		t.Fatalf("retry must dispatch a fresh attempt: %+v", aView.Attempts)
	}
}

func TestRetryKeepsPausedExecutionPaused(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "paused-retry", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create("req-paused-retry"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")

	// Pause while the first attempt still runs, then let it fail.
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	fx.exec.releaseFailure(runA, "boom")
	fx.pump()

	if _, _, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-1", ExpectedAttempt: 1}); err != nil {
		t.Fatalf("retry while paused: %v", err)
	}
	fx.pump()
	view := mustView(t, fx)
	if view.State != domain.WorkflowPaused {
		t.Fatalf("paused execution stays paused after retry: %v", view.State)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("paused retry must not dispatch: %v", fx.exec.dispatched())
	}
	if _, err := fx.m.Resume("wf_000001"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	fx.pump()
	if len(fx.exec.dispatched()) != 2 {
		t.Fatalf("resume must dispatch the retried attempt: %v", fx.exec.dispatched())
	}
}

func TestConcurrentRetriesCreateOneAttempt(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "race-retry", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create("req-race-retry"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "a"), "fail")
	fx.pump()

	const workers = 6
	var wg sync.WaitGroup
	createdCount := 0
	var mu sync.Mutex
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, created, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-shared", ExpectedAttempt: 1})
			if err != nil {
				t.Errorf("retry: %v", err)
				return
			}
			if created {
				mu.Lock()
				createdCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if createdCount != 1 {
		t.Fatalf("exactly one retry attempt expected, got %d", createdCount)
	}
	fx.pump()
	aView := findTaskView(mustView(t, fx), "a")
	if len(aView.Attempts) != 2 {
		t.Fatalf("one new attempt expected: %+v", aView.Attempts)
	}
}

func TestStorageFailureStopsDispatchAndSurfacesError(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "storage", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-storage"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")

	fx.st.mu.Lock()
	fx.st.writeResponseErr = errors.New("disk full")
	fx.st.mu.Unlock()
	fx.exec.releaseSuccess(runA, "a result")
	fx.pump()

	view := mustView(t, fx)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("storage failure must hold attention, got %v", view.State)
	}
	if view.LastError == nil || !strings.Contains(*view.LastError, "disk full") {
		t.Fatalf("storage error must be observable: %+v", view.LastError)
	}
	if findTaskView(view, "a").State != domain.WorkflowTaskFailed {
		t.Fatalf("unsaved success must not be reported: %v", findTaskView(view, "a").State)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("no downstream dispatch on unsaved state: %v", fx.exec.dispatched())
	}
}

func TestDispatchReservationFailureNeverCallsExecutor(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-reserve"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.st.mu.Lock()
	fx.st.saveErr = errors.New("snapshot write failed")
	fx.st.mu.Unlock()
	fx.m.Notify()
	fx.pump()

	if got := len(fx.exec.dispatched()); got != 0 {
		t.Fatalf("failed reservation must not dispatch, dispatched=%v", fx.exec.dispatched())
	}
	view := mustView(t, fx)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("state: %v", view.State)
	}
}

func TestWaitReturnsOnTerminalAndExpiryKeepsRunning(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-wait"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	// Expiry returns the current state without cancelling anything.
	view, err := fx.m.Wait(context.Background(), "wf_000001", 30*time.Millisecond)
	if err != nil {
		t.Fatalf("wait expiry: %v", err)
	}
	if view.State != domain.WorkflowRunning {
		t.Fatalf("state after expiry: %v", view.State)
	}
	if len(fx.exec.liveRunIDs()) != 1 {
		t.Fatalf("expiry must not cancel work: %v", fx.exec.liveRunIDs())
	}

	// Terminal outcome returns promptly.
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			live := fx.exec.liveRunIDs()
			if len(live) > 0 {
				fx.exec.releaseSuccess(live[0], "ok")
				fx.pump()
			} else if fx.m.active == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	done := make(chan domain.WorkflowExecutionView, 1)
	go func() {
		v, _ := fx.m.Wait(context.Background(), "wf_000001", 5*time.Second)
		done <- v
	}()
	select {
	case v := <-done:
		if !v.State.Terminal() {
			t.Fatalf("wait must return on terminal state, got %v", v.State)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("wait did not observe the terminal state")
	}
}

func TestWaitReturnsOnPendingPermission(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "perm", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create("req-perm"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")

	// Surface a pending permission through the run observer.
	fx.exec.mu.Lock()
	fx.exec.runs[runA] = domain.RunRecord{
		RunID: runA, Agent: "a1", Status: domain.RunRunning,
		Progress: &domain.RunProgress{
			Phase:              domain.RunPhaseWaitingForPermission,
			PendingPermissions: []domain.PermissionRequest{{ID: "perm-1"}},
		},
	}
	fx.exec.mu.Unlock()

	view := mustView(t, fx)
	if len(view.PendingPermissions) != 1 || view.PendingPermissions[0].Request.ID != "perm-1" {
		t.Fatalf("pending permission must be observable: %+v", view.PendingPermissions)
	}

	got, err := fx.m.Wait(context.Background(), "wf_000001", 2*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if len(got.PendingPermissions) == 0 {
		t.Fatal("wait must wake on intervention")
	}
}

func TestUnknownExecutionAndTaskReturnNotFound(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, err := fx.m.View("wf_999999"); !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("unknown view: %v", err)
	}
	if _, err := fx.m.Pause("wf_999999"); !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("unknown pause: %v", err)
	}
	if _, _, err := fx.m.RetryTask("wf_999999", "a", RetryRequest{RequestID: "r", ExpectedAttempt: 1}); !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("unknown retry: %v", err)
	}
	if _, _, err := fx.m.Create("req-x"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := fx.m.RetryTask("wf_000001", "ghost", RetryRequest{RequestID: "r", ExpectedAttempt: 1}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("unknown task: %v", err)
	}
}

func TestCreateWithoutConfiguredDefinitionFails(t *testing.T) {
	cfg := testWorkflowConfig(chainDefinition(), "a1", "a2", "a3")
	cfg.Workflow = nil
	st := newRecordingStore(t)
	cfg.WorkspaceDir = t.TempDir()
	m := NewManager(cfg, st, newFakeExecutor())
	if _, _, err := m.Create("req-none"); !errors.Is(err, ErrNoDefinition) {
		t.Fatalf("expected ErrNoDefinition, got %v", err)
	}
}

// TestRetryParallelInterruptionsQueueWithoutDeadlock reproduces the mutual
// block: after a crash leaves two parallel attempts interrupted, confirming a
// retry for one must not be rejected because of the other. Queued retries
// hold their dispatch until every uncertainty is resolved.
func TestRetryParallelInterruptionsQueueWithoutDeadlock(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "dual-interrupt", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B."},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-dual"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")
	runB := mustFindRunForTask(t, fx, "b")
	fx.exec.release(runA, domain.OwnedRunOutcome{RunID: runA, Status: domain.RunInterrupted, Error: "uncertain a"})
	fx.exec.release(runB, domain.OwnedRunOutcome{RunID: runB, Status: domain.RunInterrupted, Error: "uncertain b"})
	fx.pump()
	if view := mustView(t, fx); view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("two interruptions must hold attention: %v", view.State)
	}

	// The first confirmed retry is accepted even though task b is still
	// uncertain, and it queues without dispatching.
	if _, created, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-a", ExpectedAttempt: 1, ConfirmPreviousStopped: true}); err != nil || !created {
		t.Fatalf("retry of a with sibling uncertainty pending: created=%v err=%v", created, err)
	}
	fx.pump()
	if got := len(fx.exec.dispatched()); got != 2 {
		t.Fatalf("no dispatch may start while b is unresolved: %v", fx.exec.dispatched())
	}
	if view := mustView(t, fx); view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("attention must remain while b is unresolved: %v", view.State)
	}

	// Confirming the second retry retires every uncertainty; both queued
	// attempts then dispatch.
	if _, created, err := fx.m.RetryTask("wf_000001", "b", RetryRequest{RequestID: "retry-b", ExpectedAttempt: 1, ConfirmPreviousStopped: true}); err != nil || !created {
		t.Fatalf("retry of b: created=%v err=%v", created, err)
	}
	fx.pump()
	if view := mustView(t, fx); view.State != domain.WorkflowRunning {
		t.Fatalf("resolved interruptions must release scheduling: %v", view.State)
	}
	if got := len(fx.exec.dispatched()); got != 4 {
		t.Fatalf("both retried attempts must dispatch, got %v", fx.exec.dispatched())
	}
	for _, taskID := range []string{"a", "b"} {
		task := findTaskView(mustView(t, fx), taskID)
		if len(task.Attempts) != 2 || task.Attempts[1].State != domain.WorkflowAttemptRunning {
			t.Fatalf("retried %s attempts: %+v", taskID, task.Attempts)
		}
	}
}

// TestResumeRevalidatesAndClearsTransientStorageError verifies that a storage
// failure whose cause is gone does not wedge resume until process restart.
func TestResumeRevalidatesAndClearsTransientStorageError(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "transient", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create("req-transient"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")

	// The committed result momentarily fails verification while b is being
	// dispatched: scheduling stops with a recorded storage error.
	fx.st.mu.Lock()
	fx.st.verifyErr = errors.New("temporary read failure")
	fx.st.mu.Unlock()
	fx.exec.releaseSuccess(runA, "a result")
	fx.pump()
	view := mustView(t, fx)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("verification failure must hold attention: %v", view.State)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("no dispatch past the failed verification: %v", fx.exec.dispatched())
	}

	// The underlying file was never damaged: once reads succeed again,
	// resume must revalidate, clear the resolvable error, and continue.
	fx.st.mu.Lock()
	fx.st.verifyErr = nil
	fx.st.mu.Unlock()
	resumed, err := fx.m.Resume("wf_000001")
	if err != nil {
		t.Fatalf("resume after the transient failure cleared: %v", err)
	}
	if resumed.State != domain.WorkflowRunning {
		t.Fatalf("resumed state: %v", resumed.State)
	}
	fx.pump()
	if len(fx.exec.dispatched()) != 2 {
		t.Fatalf("b must dispatch after the resumed execution: %v", fx.exec.dispatched())
	}
}

// TestDispatchUsesSavedAgentSpecInsteadOfCurrentConfig verifies a dispatch
// carries the execution's immutable saved agent configuration, so a YAML edit
// between submission and (re)dispatch cannot change the role that runs.
func TestDispatchUsesSavedAgentSpecInsteadOfCurrentConfig(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-saved-spec"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Simulate the config edit that precedes a recovery dispatch.
	for i := range fx.m.cfg.Agents {
		fx.m.cfg.Agents[i].StartupPrompt = "changed after submission"
		fx.m.cfg.Agents[i].Backend = "changed"
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")
	opts, ok := fx.exec.options(runA)
	if !ok {
		t.Fatalf("no recorded dispatch for %s", runA)
	}
	if opts.Spec == nil {
		t.Fatal("dispatch must carry the saved agent spec")
	}
	if opts.Spec.StartupPrompt != "You are a1" || opts.Spec.Backend != "fake" {
		t.Fatalf("dispatch must use the saved configuration, got %+v", opts.Spec)
	}
}

// TestSavedAgentSpecPinsGlobalYoloDefault verifies the effective Yolo flag is
// captured at creation: an agent without its own override must keep the
// session default that was active when the workflow was submitted, so
// flipping defaults.yolo and restarting cannot silently escalate a recovered
// execution to bypassing approvals.
func TestSavedAgentSpecPinsGlobalYoloDefault(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	fx.m.cfg.Defaults.Yolo = false
	if _, _, err := fx.m.Create("req-yolo-pin"); err != nil {
		t.Fatalf("create: %v", err)
	}
	snapshot, err := fx.st.LoadWorkflowSnapshot("wf_000001")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	saved := snapshot.Agents["a1"]
	if saved.Yolo == nil || *saved.Yolo {
		t.Fatalf("saved spec must pin the effective yolo default: %+v", saved.Yolo)
	}
	// A restart under a flipped global default must still dispatch with the
	// pinned value.
	fx.m.cfg.Defaults.Yolo = true
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")
	opts, ok := fx.exec.options(runA)
	if !ok {
		t.Fatalf("no recorded dispatch for %s", runA)
	}
	if opts.Spec == nil || opts.Spec.Yolo == nil || *opts.Spec.Yolo {
		t.Fatalf("dispatch must carry the pinned yolo=false, got %+v", opts.Spec)
	}
}

// TestResumeAcceptsConfirmedHistoricalInterruption replays the sequence
// interruption → confirmed retry → successful repeat → pause/resume: an
// interrupted attempt whose cleanup was asserted must not block resume again.
func TestResumeAcceptsConfirmedHistoricalInterruption(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-confirmed"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := mustFindRunForTask(t, fx, "a")
	fx.exec.release(runA, domain.OwnedRunOutcome{RunID: runA, Status: domain.RunInterrupted, Error: "crash"})
	fx.pump()
	if view := mustView(t, fx); view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("interruption must hold attention: %v", view.State)
	}
	if _, created, err := fx.m.RetryTask("wf_000001", "a", RetryRequest{RequestID: "retry-a", ExpectedAttempt: 1, ConfirmPreviousStopped: true}); err != nil || !created {
		t.Fatalf("confirmed retry: created=%v err=%v", created, err)
	}
	fx.pump()
	// The confirmed retry runs; the historical interrupted attempt remains on
	// record with its cleanup assertion.
	runA2 := mustFindRunForTask(t, fx, "a")
	fx.exec.releaseSuccess(runA2, "a result")
	fx.pump()
	if view := mustView(t, fx); view.State != domain.WorkflowRunning {
		t.Fatalf("state after the confirmed retry succeeded: %v", view.State)
	}
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	resumed, err := fx.m.Resume("wf_000001")
	if err != nil {
		t.Fatalf("resume must accept the already-confirmed interruption: %v", err)
	}
	if resumed.State != domain.WorkflowRunning {
		t.Fatalf("resumed state: %v", resumed.State)
	}
}
