package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func newFixtureFromDef(t *testing.T, def domain.WorkflowDefinition, agents ...string) *managerFixture {
	t.Helper()
	if def.TaskTimeoutSeconds == 0 {
		def.TaskTimeoutSeconds = 300
	}
	return newManagerFixture(t, def, agents...)
}

func TestOptionalReviewerFailureStillRunsConsumer(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "review-quorum", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"review_a": {Agent: "a1", Prompt: "Review A.", AllowedToFail: true},
			"review_b": {Agent: "a2", Prompt: "Review B.", AllowedToFail: true},
			"verify":   {Agent: "a3", Prompt: "Verify.", Needs: []string{"review_a", "review_b"}, MinSuccessfulDependencies: 1},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-review"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "review_a"), "reviewer a crashed")
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "review_b"), "review b findings")
	fx.pump()

	verifyRun := mustFindRunForTask(t, fx, "verify")
	opts, _ := fx.exec.options(verifyRun)
	if !containsSubstrings(opts.Message, "review_a: failed", "reviewer a crashed", "review_b: succeeded") {
		t.Fatalf("consumer must receive success plus tolerated failure:\n%s", opts.Message)
	}
	if strings.Contains(opts.Message, "Full response") == false {
		t.Fatalf("successful reference must expose result file:\n%s", opts.Message)
	}

	fx.exec.releaseSuccess(verifyRun, "verified report")
	fx.pump()
	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowCompletedWithError {
		t.Fatalf("tolerated failure must yield completed_with_errors, got %v", view.State)
	}
	if view.TaskCounts.Failed != 1 || view.TaskCounts.Succeeded != 2 {
		t.Fatalf("counts: %+v", view.TaskCounts)
	}
}

func TestAllReviewersFailBlocksConsumerWithThresholdReason(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "review-quorum", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"review_a": {Agent: "a1", Prompt: "A.", AllowedToFail: true},
			"review_b": {Agent: "a2", Prompt: "B.", AllowedToFail: true},
			"verify":   {Agent: "a3", Prompt: "Verify.", Needs: []string{"review_a", "review_b"}, MinSuccessfulDependencies: 1},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-allfail"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "review_a"), "a failed")
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "review_b"), "b failed")
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	verify := findTaskView(view, "verify")
	if verify.State != domain.WorkflowTaskBlocked {
		t.Fatalf("verify must be blocked, got %v (%s)", verify.State, verify.BlockedReason)
	}
	if !strings.Contains(verify.BlockedReason, "insufficient_successful_dependencies") {
		t.Fatalf("blocked reason: %s", verify.BlockedReason)
	}
	if len(fx.exec.dispatched()) != 2 {
		t.Fatalf("verify must not be dispatched: %v", fx.exec.dispatched())
	}
	if view.State != domain.WorkflowFailed {
		t.Fatalf("blocked task must fail the execution, got %v", view.State)
	}
}

func TestDefaultZeroThresholdRunsAfterToleratedFailures(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "tolerated", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"risky": {Agent: "a1", Prompt: "Risky.", AllowedToFail: true},
			"after": {Agent: "a2", Prompt: "After.", Needs: []string{"risky"}},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2")
	if _, _, err := fx.m.Create(context.Background(), "req-zero"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "risky"), "tolerated error")
	fx.pump()

	run := mustFindRunForTask(t, fx, "after")
	opts, _ := fx.exec.options(run)
	if !containsSubstrings(opts.Message, "risky: failed", "tolerated error") {
		t.Fatalf("consumer must receive failure information:\n%s", opts.Message)
	}
	fx.exec.releaseSuccess(run, "after result")
	fx.pump()
	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowCompletedWithError {
		t.Fatalf("state: %v", view.State)
	}
}

func TestMandatoryFailureBlocksDespiteEnoughSuccesses(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "mandatory", MaxParallel: 3, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"opt1": {Agent: "a1", Prompt: "1.", AllowedToFail: true},
			"mand": {Agent: "a2", Prompt: "2."},
			"agg":  {Agent: "a3", Prompt: "3.", Needs: []string{"opt1", "mand"}, MinSuccessfulDependencies: 1},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-mandatory"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "opt1"), "ok1")
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "mand"), "mandatory broke")
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	agg := findTaskView(view, "agg")
	if agg.State != domain.WorkflowTaskBlocked {
		t.Fatalf("agg must stay blocked by mandatory failure, got %v (%s)", agg.State, agg.BlockedReason)
	}
	if !strings.Contains(agg.BlockedReason, "dependency_failed:mand") {
		t.Fatalf("blocked reason: %s", agg.BlockedReason)
	}
	if len(fx.exec.dispatched()) != 2 {
		t.Fatalf("agg must not be dispatched: %v", fx.exec.dispatched())
	}
}

func TestThresholdDoesNotTriggerEarlyAggregation(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "wait-all", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"r1": {Agent: "a1", Prompt: "1."},
			"r2": {Agent: "a2", Prompt: "2."},
			"ag": {Agent: "a3", Prompt: "3.", Needs: []string{"r1", "r2"}, MinSuccessfulDependencies: 1},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-early"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "r1"), "one")
	fx.pump()

	if got := len(fx.exec.liveRunIDs()); got != 1 {
		t.Fatalf("aggregator must wait for the second reviewer, live=%v", fx.exec.liveRunIDs())
	}
	view, _ := fx.m.View("wf_000001")
	if findTaskView(view, "ag").State != domain.WorkflowTaskPending {
		t.Fatalf("ag must be pending while r2 runs")
	}

	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "r2"), "two")
	fx.pump()
	if findTask, _ := fx.m.View("wf_000001"); findTask.TaskCounts.Active != 1 {
		t.Fatalf("ag must dispatch after both settle: %+v", findTask.TaskCounts)
	}
}

func TestBlockingPropagatesButIndependentBranchContinues(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "branches", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"bad":      {Agent: "a1", Prompt: "Bad."},
			"child":    {Agent: "a2", Prompt: "Child.", Needs: []string{"bad"}},
			"grandson": {Agent: "a3", Prompt: "Grand.", Needs: []string{"child"}},
			"free":     {Agent: "a4", Prompt: "Free."},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2", "a3", "a4")
	if _, _, err := fx.m.Create(context.Background(), "req-branches"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "bad"), "root failure")
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	if findTaskView(view, "child").State != domain.WorkflowTaskBlocked {
		t.Fatalf("child must be blocked")
	}
	if findTaskView(view, "grandson").State != domain.WorkflowTaskBlocked {
		t.Fatalf("blocking must propagate to grandson")
	}
	if findTaskView(view, "free").State != domain.WorkflowTaskRunning {
		t.Fatalf("independent branch must continue, got %v", findTaskView(view, "free").State)
	}

	freeRun := mustFindRunForTask(t, fx, "free")
	fx.exec.releaseSuccess(freeRun, "free done")
	fx.pump()
	view, _ = fx.m.View("wf_000001")
	if view.State != domain.WorkflowFailed {
		t.Fatalf("blocked branch fails execution, got %v", view.State)
	}
}

func findTaskView(view domain.WorkflowExecutionView, taskID string) domain.WorkflowTaskView {
	for _, task := range view.Tasks {
		if task.TaskID == taskID {
			return task
		}
	}
	return domain.WorkflowTaskView{}
}

func TestEmptyResponseFailsWithMissingOutput(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "empty", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"silent": {Agent: "a1", Prompt: "Say nothing."},
			"after":  {Agent: "a2", Prompt: "After.", Needs: []string{"silent"}},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2")
	if _, _, err := fx.m.Create(context.Background(), "req-empty"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	run := mustFindRunForTask(t, fx, "silent")
	fx.exec.release(run, domain.OwnedRunOutcome{RunID: run, Status: domain.RunCompleted, FinalMessage: "   "})
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	silent := findTaskView(view, "silent")
	if silent.State != domain.WorkflowTaskFailed {
		t.Fatalf("empty response must fail the task, got %v", silent.State)
	}
	if len(silent.Attempts) != 1 || silent.Attempts[0].Reason != "missing_output" {
		t.Fatalf("attempt: %+v", silent.Attempts)
	}
	if silent.Result != nil {
		t.Fatal("empty response must not produce a successful result reference")
	}
	after := findTaskView(view, "after")
	if after.State != domain.WorkflowTaskBlocked {
		t.Fatalf("mandatory missing output must block consumer, got %v", after.State)
	}
}

func TestTimeoutCancelsAndFailsAfterConfirmedCleanup(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "slow", MaxParallel: 1, TaskTimeoutSeconds: 1,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"slow": {Agent: "a1", Prompt: "Slow.", AllowedToFail: true},
		},
	}
	fx := newFixtureFromDef(t, def, "a1")
	if _, _, err := fx.m.Create(context.Background(), "req-timeout"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	run := mustFindRunForTask(t, fx, "slow")

	// Simulate the deadline passing without a completion.
	fx.m.mu.Lock()
	for _, live := range fx.m.activeLiveLocked() {
		live.dispatchedAt = live.dispatchedAt.Add(-2 * time.Second)
	}
	fx.m.mu.Unlock()
	fx.m.reconcile()
	if !fx.exec.wasCancelled(run) {
		t.Fatal("timeout must cancel the owned run")
	}

	// The worker stops: cleanup confirmed -> failed with timeout reason.
	fx.exec.release(run, domain.OwnedRunOutcome{RunID: run, Status: domain.RunFailed, Error: "context canceled"})
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	slow := findTaskView(view, "slow")
	if slow.State != domain.WorkflowTaskFailed {
		t.Fatalf("timed-out task must fail, got %v", slow.State)
	}
	if slow.Attempts[0].Reason != "timeout" {
		t.Fatalf("failure reason: %+v", slow.Attempts[0])
	}
	if view.State != domain.WorkflowCompletedWithError {
		t.Fatalf("allowed_to_fail timeout must be waived, got %v", view.State)
	}
}

func TestUnconfirmedTimeoutCleanupInterruptsAndStopsScheduling(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "hang", MaxParallel: 1, TaskTimeoutSeconds: 1,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"hang":  {Agent: "a1", Prompt: "Hang."},
			"other": {Agent: "a2", Prompt: "Other."},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2")
	if _, _, err := fx.m.Create(context.Background(), "req-hang"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	hangRun := mustFindRunForTask(t, fx, "hang")

	fx.m.mu.Lock()
	if live := fx.m.activeLiveLocked()[hangRun]; live != nil {
		live.dispatchedAt = live.dispatchedAt.Add(-2 * time.Second)
	}
	fx.m.mu.Unlock()
	fx.m.reconcile() // cancel requested
	// The worker never reports back; exceed the cancel grace period.
	time.Sleep(60 * time.Millisecond)
	fx.m.reconcile()

	view, _ := fx.m.View("wf_000001")
	hang := findTaskView(view, "hang")
	if hang.State != domain.WorkflowTaskInterrupted {
		t.Fatalf("unconfirmed cleanup must interrupt, got %v", hang.State)
	}
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("execution must be held for attention, got %v", view.State)
	}
	if len(view.AttentionReasons) == 0 {
		t.Fatalf("attention reasons: %v", view.AttentionReasons)
	}
	if findTaskView(view, "other").State != domain.WorkflowTaskReady {
		t.Fatalf("the second task stays ready but unstarted, got %v", findTaskView(view, "other").State)
	}
	if !fx.exec.OwnedRunActive(hangRun) {
		t.Fatal("the hung worker stays live from the executor's point of view")
	}
}

func TestMissingCommittedResultHoldsAttentionBeforeDispatch(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "artifact", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"first":  {Agent: "a1", Prompt: "First."},
			"second": {Agent: "a2", Prompt: "Second.", Needs: []string{"first"}},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2")
	if _, _, err := fx.m.Create(context.Background(), "req-artifact"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "first"), "first result")
	// Consume the completion (durably saved) but do not reconcile yet.
	c := <-fx.m.completions
	fx.m.handleCompletion(c)

	// Corrupt the committed result before the consumer dispatch decision.
	execDir, err := fx.st.WorkflowDir("wf_000001")
	if err != nil {
		t.Fatalf("exec dir: %v", err)
	}
	abs := filepath.Join(execDir, "tasks", "first", "attempts", "1", "response.txt")
	if err := os.WriteFile(abs, []byte("tampered content"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	fx.m.reconcile()

	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("tampered dependency artifact must hold execution, got %v", view.State)
	}
	if findTaskView(view, "second").State == domain.WorkflowTaskRunning {
		t.Fatal("consumer must not be dispatched with an unverifiable input")
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("only the first task may be dispatched: %v", fx.exec.dispatched())
	}
}

func TestRestoredArtifactAllowsResume(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "restore", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"first":  {Agent: "a1", Prompt: "First."},
			"second": {Agent: "a2", Prompt: "Second.", Needs: []string{"first"}},
		},
	}
	fx := newFixtureFromDef(t, def, "a1", "a2")
	if _, _, err := fx.m.Create(context.Background(), "req-restore"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	firstRun := mustFindRunForTask(t, fx, "first")
	fx.exec.releaseSuccess(firstRun, "first result")
	fx.pump()

	// Pause, then simulate a missing result discovered by resume.
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("pause: %v", err)
	}

	// Simulate a missing result discovered by resume revalidation.
	fx.st.mu.Lock()
	fx.st.verifyErr = errArtifactMissing
	fx.st.mu.Unlock()
	if _, err := fx.m.Resume(context.Background(), "wf_000001"); err == nil {
		t.Fatal("resume must refuse while a committed artifact is unverifiable")
	}

	// Restore byte-for-byte and retry resume.
	fx.st.mu.Lock()
	fx.st.verifyErr = nil
	fx.st.mu.Unlock()

	if _, err := fx.m.Resume(context.Background(), "wf_000001"); err != nil {
		t.Fatalf("resume after restore: %v", err)
	}
	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowRunning {
		t.Fatalf("state after resume: %v", view.State)
	}
}

var errArtifactMissing = &testError{"committed result missing"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
