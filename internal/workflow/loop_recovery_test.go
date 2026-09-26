package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/judge"
)

// pumpReason drives deterministic rounds until an attention reason appears.
func pumpReason(t *testing.T, fx *managerFixture, substr string) domain.WorkflowExecutionView {
	t.Helper()
	if !fx.pumpUntil(2*time.Second, func() bool {
		return hasReason(mustView2(t, fx), substr)
	}) {
		t.Fatalf("reason %q never appeared: %+v", substr, mustView2(t, fx).AttentionReasons)
	}
	return mustView2(t, fx)
}

// --- 5.1: crash mid-iteration keeps the iteration; confirmed retry repairs --

func TestRecoveryMidIterationInterruptedRetriesCurrentIteration(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(3), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-crash-loop"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Iteration 1 completes fully; the crash lands while iteration 2's
	// implement attempt is live.
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "implement"), "implement v1")
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "review"), "review v1")
	fx.pump()
	view := mustView2(t, fx)
	if view.Loops[0].Iteration != 2 {
		t.Fatalf("crash must happen inside iteration 2: %+v", view.Loops[0])
	}

	second := fx.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer finishLiveRuns(t, second)
	second.pump()

	view = mustView2(t, second)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("interrupted body attempt must hold: %v", view.State)
	}
	if !hasReason(view, "interrupted_attempt:implement:2") {
		t.Fatalf("interruption must name the attempt: %v", view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 2 || view.Loops[0].State != domain.WorkflowLoopRunning {
		t.Fatalf("iteration counter must survive restart: %+v", view.Loops[0])
	}

	// The confirmed retry is accepted despite review's iteration-1 attempt:
	// same-loop descendants only block within the retried iteration.
	if _, _, err := second.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{
		RequestID: "r-recover", ExpectedAttempt: 2, ConfirmPreviousStopped: true,
	}); err != nil {
		t.Fatalf("confirmed retry after crash: %v", err)
	}
	queued := loopSnapshot(t, second).Tasks["implement"].Attempts[2]
	if queued.Iteration != 2 || queued.State != domain.WorkflowAttemptQueued {
		t.Fatalf("retry must re-enter iteration 2: %+v", queued)
	}

	if final := driveToTerminal(t, second); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, second, "implement"); len(got) != 4 ||
		got[0] != 1 || got[1] != 2 || got[2] != 2 || got[3] != 3 {
		t.Fatalf("iteration 1 preserved, retry stays in 2, iteration 3 follows: %v", got)
	}
	// Iteration 2's review consumes the retried result, not the interrupted one.
	manifest := readAttemptManifest(t, second, "review", 2)
	if dep := manifest.Dependencies[0]; dep.TaskID != "implement" || dep.Attempt != 3 || dep.Iteration != 2 {
		t.Fatalf("iteration-2 consumer must reference the retried attempt: %+v", dep)
	}

	// Repeating the accepted retry request after the advance replays it
	// without new work or counter changes.
	replayed, created, err := second.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{
		RequestID: "r-recover", ExpectedAttempt: 2, ConfirmPreviousStopped: true,
	})
	if err != nil || created {
		t.Fatalf("retry replay after advance: created=%v err=%v", created, err)
	}
	if got := iterationNumbersForTask(t, second, "implement"); len(got) != 4 {
		t.Fatalf("replay must not add attempts: %v", got)
	}
	if replayed.Loops[0].Iteration != 3 || replayed.Loops[0].State != domain.WorkflowLoopDone {
		t.Fatalf("replay must not change counters: %+v", replayed.Loops[0])
	}
}

func TestLoopFailureHoldSurvivesRestart(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-durable-hold"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "implement"), "boom")
	fx.pump()

	second := fx.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer finishLiveRuns(t, second)
	second.pump()

	view := mustView2(t, second)
	if view.State != domain.WorkflowNeedsAttention ||
		!hasReason(view, "loop_failure:refine:1:implement:retry_or_cancel") {
		t.Fatalf("failure hold must survive restart: %v (%v)", view.State, view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 1 {
		t.Fatalf("hold restores the same iteration: %+v", view.Loops[0])
	}
	if len(second.exec.dispatched()) != 0 {
		t.Fatalf("recovery must not resend failed work: %v", second.exec.dispatched())
	}
	held, err := second.m.Wait(context.Background(), "wf_000001", 2*time.Second)
	if err != nil || held.State != domain.WorkflowNeedsAttention ||
		!hasReason(held, "loop_failure:refine:1:implement") {
		t.Fatalf("wait must surface the durable hold: %+v err=%v", held.AttentionReasons, err)
	}

	if _, _, err := second.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{RequestID: "r1", ExpectedAttempt: 1}); err != nil {
		t.Fatalf("retry after restart: %v", err)
	}
	if got := iterationNumbersForTask(t, second, "implement"); len(got) != 2 || got[1] != 1 {
		t.Fatalf("post-restart retry stays in iteration 1: %v", got)
	}
}

// --- 5.2: iteration-scoped descendant and history guards ----------------------

func TestSameIterationConsumptionBlocksRetry(t *testing.T) {
	def := loopReviewDefinition(2)
	implement := def.Tasks["implement"]
	implement.AllowedToFail = true
	def.Tasks["implement"] = implement
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-consumed"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "implement"), "tolerated")
	fx.pump()
	// The tolerated failure lets review reserve its iteration-1 attempt.
	if review := findTaskView(mustView2(t, fx), "review"); len(review.Attempts) != 1 {
		t.Fatalf("review must have dispatched in iteration 1: %+v", review)
	}

	_, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{RequestID: "r-blocked", ExpectedAttempt: 1})
	if !errors.Is(err, ErrRetryConflict) ||
		!strings.Contains(err.Error(), "descendant review already has an attempt in loop refine iteration 1") {
		t.Fatalf("same-iteration consumption must block the retry, got %v", err)
	}
	snapshot := loopSnapshot(t, fx)
	if len(snapshot.Tasks["implement"].Attempts) != 1 || snapshot.Loops["refine"].Iteration != 1 {
		t.Fatalf("rejection must reserve nothing and keep counters: %+v", snapshot.Tasks["implement"].Attempts)
	}
}

func TestOutsideConsumptionBlocksRetry(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "outside-consumed", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"once": {MaxIterations: 1}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"solo":   {Agent: "a1", Prompt: "Solo.", Loop: "once", AllowedToFail: true},
			"report": {Agent: "a2", Prompt: "Report.", Needs: []string{"solo"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create(context.Background(), "req-outside"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "solo"), "tolerated")
	fx.pump()
	// The loop settles and completes; the outside consumer reserves on top
	// of the tolerated failure.
	if report := findTaskView(mustView2(t, fx), "report"); len(report.Attempts) != 1 {
		t.Fatalf("outside consumer must have dispatched: %+v", report)
	}

	_, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "solo", RetryRequest{RequestID: "r-outside", ExpectedAttempt: 1})
	if !errors.Is(err, ErrRetryConflict) ||
		!strings.Contains(err.Error(), "descendant report already has attempts") {
		t.Fatalf("outside consumption must block the retry, got %v", err)
	}

	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "report"), "consumed the failure")
	fx.pump()
	view := mustView2(t, fx)
	if view.State != domain.WorkflowCompletedWithError {
		t.Fatalf("tolerated history derives the final state: %v (%v)", view.State, view.AttentionReasons)
	}

	// Even after the outcome is terminal the consumed inputs stay frozen.
	if _, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "solo", RetryRequest{RequestID: "r-late", ExpectedAttempt: 1}); !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("consumed result must stay un-retryable: %v", err)
	}
	manifest := readAttemptManifest(t, fx, "report", 1)
	if dep := manifest.Dependencies[0]; dep.Status != string(domain.WorkflowAttemptFailed) || dep.Attempt != 1 {
		t.Fatalf("the outside attempt's saved inputs must be unchanged: %+v", dep)
	}
}

func TestHistoricalFailureCannotBeRetriedAfterAdvance(t *testing.T) {
	def := loopReviewDefinition(2)
	implement := def.Tasks["implement"]
	implement.AllowedToFail = true
	def.Tasks["implement"] = implement
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-history"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Iteration 1: implement fails tolerably, review succeeds, the loop
	// advances; iteration 2's implement is live.
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "implement"), "tolerated")
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "review"), "review v1")
	fx.pump()
	if view := mustView2(t, fx); view.Loops[0].Iteration != 2 {
		t.Fatalf("iteration 2 must be underway: %+v", view.Loops)
	}

	// Iteration 1's failure is immutable history: neither the failed task
	// (now re-running) nor its downstream (re-armed, no iteration-2 attempt)
	// may be retried against the past.
	if _, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{RequestID: "r-old", ExpectedAttempt: 1}); !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("stale expected_attempt must conflict, got %v", err)
	}
	if _, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "review", RetryRequest{RequestID: "r-old-review", ExpectedAttempt: 1}); !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("retry targeting iteration-1 history must conflict, got %v", err)
	}
	snapshot := loopSnapshot(t, fx)
	if got := iterationNumbersForTask(t, fx, "review"); len(got) != 1 || got[0] != 1 {
		t.Fatalf("rejections must not reserve anything: %v", got)
	}
	if snapshot.Loops["refine"].Iteration != 2 {
		t.Fatalf("rejections must not move the counter: %+v", snapshot.Loops["refine"])
	}
}

func TestFinalIterationRetryReopensDoneLoop(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "reopen", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"once": {MaxIterations: 1}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"solo":   {Agent: "a1", Prompt: "Solo.", Loop: "once", AllowedToFail: true},
			"anchor": {Agent: "a2", Prompt: "Anchor."},
			"report": {Agent: "a3", Prompt: "Report.", Needs: []string{"solo", "anchor"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-reopen"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "anchor"), "anchor down")
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "solo"), "tolerated")
	fx.pump()

	view := mustView2(t, fx)
	if view.Loops[0].State != domain.WorkflowLoopDone {
		t.Fatalf("tolerated final iteration completes the loop: %+v", view.Loops[0])
	}
	if report := findTaskView(view, "report"); len(report.Attempts) != 0 {
		t.Fatalf("blocked outside consumer must have no reservations: %+v", report)
	}

	// Both repairs queue before any further dispatch: the retry reopens the
	// done loop at the same iteration without creating iteration 2.
	if _, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "solo", RetryRequest{RequestID: "r-solo", ExpectedAttempt: 1}); err != nil {
		t.Fatalf("eligible final-iteration retry: %v", err)
	}
	reopened := mustView2(t, fx)
	if reopened.Loops[0].State != domain.WorkflowLoopRunning || reopened.Loops[0].Iteration != 1 {
		t.Fatalf("done loop must reopen at the same iteration: %+v", reopened.Loops[0])
	}
	if _, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "anchor", RetryRequest{RequestID: "r-anchor", ExpectedAttempt: 1}); err != nil {
		t.Fatalf("outside predecessor repair: %v", err)
	}

	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, fx, "solo"); len(got) != 2 || got[0] != 1 || got[1] != 1 {
		t.Fatalf("reopen must not create iteration 2: %v", got)
	}
	if final := mustView2(t, fx); final.Loops[0].State != domain.WorkflowLoopDone || final.Loops[0].Iteration != 1 {
		t.Fatalf("loop settles again at iteration 1: %+v", final.Loops[0])
	}
	manifest := readAttemptManifest(t, fx, "report", 1)
	for _, dep := range manifest.Dependencies {
		if dep.TaskID == "solo" && (dep.Attempt != 2 || dep.Status != string(domain.WorkflowAttemptSucceeded)) {
			t.Fatalf("outside consumer must wait for and consume the reopened settlement: %+v", dep)
		}
	}
}

// --- 5.3: paused repair --------------------------------------------------------

func TestPausedLoopRepairStaysPausedUntilResume(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "paused-repair", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 2}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"solo": {Agent: "a1", Prompt: "Refine.", Loop: "refine"},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create(context.Background(), "req-paused-repair"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "solo"), "boom")
	fx.pump()
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("pause under a loop hold: %v", err)
	}

	view, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "solo", RetryRequest{RequestID: "r-paused", ExpectedAttempt: 1})
	if err != nil {
		t.Fatalf("retry under pause: %v", err)
	}
	if view.State != domain.WorkflowPaused {
		t.Fatalf("paused execution must remain paused after retry, got %v", view.State)
	}
	if got := iterationNumbersForTask(t, fx, "solo"); len(got) != 2 || got[1] != 1 {
		t.Fatalf("paused repair keeps iteration 1 queued: %v", got)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("no dispatch while paused: %v", fx.exec.dispatched())
	}

	resumed, err := fx.m.Resume(context.Background(), "wf_000001")
	if err != nil {
		t.Fatalf("resume after repair: %v", err)
	}
	if resumed.State != domain.WorkflowRunning {
		t.Fatalf("resume must continue the repaired iteration: %v (%v)", resumed.State, resumed.AttentionReasons)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, fx, "solo"); len(got) != 3 ||
		got[0] != 1 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("repair stays in iteration 1, iteration 2 follows: %v", got)
	}
}

// --- 5.4: resume, artifact revalidation, and judge outages under holds --------

func TestRestoredArtifactRevalidatedDuringLoopFailure(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "artifact-under-hold", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 2}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"seed":      {Agent: "a1", Prompt: "Seed."},
			"implement": {Agent: "a2", Prompt: "Implement.", Loop: "refine", Needs: []string{"seed"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create(context.Background(), "req-artifact-hold"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "seed"), "seeded")
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "implement"), "boom")
	fx.pump()

	// A transient verification failure coexists with the loop failure hold.
	fx.st.verifyErr = errors.New("temporary read failure")
	resumed, err := fx.m.Resume(context.Background(), "wf_000001")
	if err != nil {
		t.Fatalf("resume must be accepted for loop executions: %v", err)
	}
	if resumed.State != domain.WorkflowNeedsAttention ||
		!hasReason(resumed, "artifact:seed:1") || !hasReason(resumed, "loop_failure:refine:1:implement") {
		t.Fatalf("resume must keep both unresolved causes visible: %v (%v)", resumed.State, resumed.AttentionReasons)
	}
	if _, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{RequestID: "r-blocked", ExpectedAttempt: 1}); !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("artifact uncertainty must precede loop retry: %v", err)
	}

	// Once the artifact verifies again, resume clears only the artifact
	// reason; the loop hold remains and now permits the eligible retry.
	fx.st.verifyErr = nil
	resumed, err = fx.m.Resume(context.Background(), "wf_000001")
	if err != nil {
		t.Fatalf("resume after restore: %v", err)
	}
	if hasReason(resumed, "artifact:") {
		t.Fatalf("repaired artifact reason must clear: %v", resumed.AttentionReasons)
	}
	if !hasReason(resumed, "loop_failure:refine:1:implement") {
		t.Fatalf("loop failure must survive artifact recovery: %v", resumed.AttentionReasons)
	}
	if _, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{RequestID: "r-repair", ExpectedAttempt: 1}); err != nil {
		t.Fatalf("retry permitted after artifact recovery: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, fx, "implement"); len(got) != 3 ||
		got[0] != 1 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("repair stays in iteration 1: %v", got)
	}
}

func TestLoopFailureAndJudgeOutageRepairedIndependently(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "judge-and-loop", MaxParallel: 3, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 2}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"implement": {Agent: "a1", Prompt: "Implement.", Loop: "refine"},
			"fix":       {Agent: "a2", Prompt: "Fix.", Loop: "refine"},
			"reviewer": {Agent: "a3", Prompt: "Review.", Loop: "refine", Needs: []string{"implement"},
				Verdicts: map[string]string{"passed": "work accepted", "failed": "work rejected"}},
			"finalize": {Agent: "a4", Prompt: "Finalize.", Needs: []string{"fix", "reviewer"}},
		},
	}
	jf := newJudgedFixture(t, def, "a1", "a2", "a3", "a4")
	if _, _, err := jf.m.Create(context.Background(), "req-judge-loop"); err != nil {
		t.Fatalf("create: %v", err)
	}
	jf.pump()
	jf.exec.releaseSuccess(mustFindRunForTask(t, jf.managerFixture, "implement"), "implemented")
	jf.pump()
	// The reviewer is live when the independent fix fails: running work may
	// finish while the new hold stops everything else.
	jf.exec.releaseFailure(mustFindRunForTask(t, jf.managerFixture, "fix"), "fix boom")
	jf.exec.releaseSuccess(mustFindRunForTask(t, jf.managerFixture, "reviewer"), "looks fine")

	// The judge is down: classification holds while the loop failure holds
	// independently.
	view := pumpReason(t, jf.managerFixture, "judge_unavailable:reviewer:1")
	if !hasReason(view, "loop_failure:refine:1:fix:retry_or_cancel") {
		t.Fatalf("both holds must be visible: %v", view.AttentionReasons)
	}
	if _, _, err := jf.m.RetryTask(context.Background(), "wf_000001", "fix", RetryRequest{RequestID: "r-blocked", ExpectedAttempt: 1}); !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("judge uncertainty must precede the loop retry: %v", err)
	}

	// Resume re-attempts the held classification without rerunning any
	// agent; repeated resumes deduplicate against the in-flight call.
	release := make(chan judge.Decision, 1)
	jf.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return <-release, nil
	})
	resumed, err := jf.m.Resume(context.Background(), "wf_000001")
	if err != nil {
		t.Fatalf("resume during combined holds must be accepted: %v", err)
	}
	if resumed.State != domain.WorkflowNeedsAttention ||
		!hasReason(resumed, "judge_unavailable:reviewer:1") ||
		!hasReason(resumed, "loop_failure:refine:1:fix") {
		t.Fatalf("resume must retain both unresolved holds: %v", resumed.AttentionReasons)
	}
	if !jf.pumpUntil(2*time.Second, func() bool { return jf.judge.requestCount() == 2 }) {
		t.Fatalf("resume must start exactly one classification")
	}
	if _, err := jf.m.Resume(context.Background(), "wf_000001"); err != nil {
		t.Fatalf("repeated resume must stay accepted: %v", err)
	}
	jf.pumpUntil(100*time.Millisecond, func() bool { return false })
	if got := jf.judge.requestCount(); got != 2 {
		t.Fatalf("concurrent duplicate judge calls forbidden, got %d", got)
	}
	if len(jf.exec.dispatched()) != 3 {
		t.Fatalf("classification recovery must not dispatch agent work: %v", jf.exec.dispatched())
	}

	// The provider recovers: settlement clears only the judge reason; the
	// loop failure keeps holding until the eligible retry repairs it.
	release <- passedDecision(0.95)
	if !jf.pumpUntil(2*time.Second, func() bool {
		snapshot := loopSnapshot(t, jf.managerFixture)
		attempt := findAttempt(&snapshot, "reviewer", 1)
		return attempt != nil && attempt.State == domain.WorkflowAttemptSucceeded
	}) {
		t.Fatal("reviewer classification must settle after the provider recovers")
	}
	view = mustView2(t, jf.managerFixture)
	if view.State != domain.WorkflowNeedsAttention || !hasReason(view, "loop_failure:refine:1:fix") {
		t.Fatalf("loop failure must still hold: %v (%v)", view.State, view.AttentionReasons)
	}
	if snapshot := loopSnapshot(t, jf.managerFixture); snapshot.Loops["refine"].Iteration != 1 {
		t.Fatalf("classification settlement must not advance the held loop: %+v", snapshot.Loops["refine"])
	}
	if _, _, err := jf.m.RetryTask(context.Background(), "wf_000001", "fix", RetryRequest{RequestID: "r-fix", ExpectedAttempt: 1}); err != nil {
		t.Fatalf("retry after judge recovery: %v", err)
	}

	jf.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return passedDecision(0.95), nil
	})
	if final := driveToTerminal(t, jf.managerFixture); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, jf.managerFixture, "fix"); len(got) != 3 ||
		got[0] != 1 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("fix repairs stay in iteration 1: %v", got)
	}
	if got := iterationNumbersForTask(t, jf.managerFixture, "reviewer"); len(got) != 2 ||
		got[0] != 1 || got[1] != 2 {
		t.Fatalf("reviewer runs one attempt per iteration: %v", got)
	}
	reviewer := loopSnapshot(t, jf.managerFixture).Tasks["reviewer"]
	if verdict := reviewer.Attempts[0].Verdict; verdict == nil ||
		verdict.Value != "passed" || verdict.Source != "judge" {
		t.Fatalf("held classification must commit its recovered verdict: %+v", reviewer.Attempts[0].Verdict)
	}
}

func TestCancelFromLoopFailureHold(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-cancel-hold"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "implement"), "boom")
	fx.pump()
	if view := mustView2(t, fx); view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("hold first: %v", view.State)
	}

	if _, err := fx.m.Cancel("wf_000001", CancelOptions{}); err != nil {
		t.Fatalf("cancel from a loop hold must be accepted: %v", err)
	}
	fx.pump()
	view := mustView2(t, fx)
	if view.State != domain.WorkflowCancelled {
		t.Fatalf("cancellation must outrank the failure hold: %v (%v)", view.State, view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 1 {
		t.Fatalf("cancel must not advance loops: %+v", view.Loops[0])
	}
	if got := iterationNumbersForTask(t, fx, "implement"); len(got) != 1 {
		t.Fatalf("cancel must not reserve more work: %v", got)
	}
}

// --- 5.5: advance-commit faults follow the last authoritative snapshot --------

func TestAdvanceCommitFaultAndBoundaryCrashes(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "advance-fault", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 2}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"solo": {Agent: "a1", Prompt: "Refine.", Loop: "refine"},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create(context.Background(), "req-advance-fault"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "solo"), "iteration one")
	c := <-fx.m.completions
	fx.m.handleCompletion(c)

	// The advance commits fail: next-iteration work must not dispatch from
	// unsaved state, and the durable snapshot stays at iteration 1.
	fx.st.saveErr = errors.New("advance write failed")
	fx.m.reconcile()
	fx.st.saveErr = nil

	view := mustView2(t, fx)
	if view.State != domain.WorkflowNeedsAttention || !hasReason(view, "storage_error") {
		t.Fatalf("failed advance must surface a storage error: %v (%v)", view.State, view.AttentionReasons)
	}
	onDisk := loopSnapshot(t, fx)
	if onDisk.Loops["refine"].Iteration != 1 {
		t.Fatalf("unsaved advance must not persist: %+v", onDisk.Loops["refine"])
	}
	if len(onDisk.Tasks["solo"].Attempts) != 1 {
		t.Fatalf("no iteration-2 reservation from unsaved state: %+v", onDisk.Tasks["solo"].Attempts)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("dispatch must stop on the advance fault: %v", fx.exec.dispatched())
	}

	// Recovery follows the last authoritative snapshot: exactly one advance.
	second := fx.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	second.pump()
	if got := iterationNumbersForTask(t, second, "solo"); len(got) != 2 || got[1] != 2 {
		t.Fatalf("recovery must advance once and start iteration 2: %v", got)
	}
	if onDisk := loopSnapshot(t, second); onDisk.Loops["refine"].Iteration != 2 {
		t.Fatalf("recovered counter must be durable: %+v", onDisk.Loops["refine"])
	}

	// A crash right after the iteration-2 reservation recovers as ordinary
	// interruption work: the counter stays at 2 and iteration 1 never reruns.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stopCancel()
	_ = second.m.Stop(stopCtx)
	third := second.restart()
	if err := third.m.Start(context.Background()); err != nil {
		t.Fatalf("restart after reservation: %v", err)
	}
	defer finishLiveRuns(t, third)
	third.pump()

	view = mustView2(t, third)
	if view.State != domain.WorkflowNeedsAttention || !hasReason(view, "interrupted_attempt:solo:2") {
		t.Fatalf("reserved iteration-2 work must interrupt: %v (%v)", view.State, view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 2 {
		t.Fatalf("post-advance crash keeps the counter: %+v", view.Loops[0])
	}
	if _, _, err := third.m.RetryTask(context.Background(), "wf_000001", "solo", RetryRequest{
		RequestID: "r-advance", ExpectedAttempt: 2, ConfirmPreviousStopped: true,
	}); err != nil {
		t.Fatalf("confirmed retry repairs iteration 2: %v", err)
	}
	if final := driveToTerminal(t, third); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, third, "solo"); len(got) != 3 ||
		got[0] != 1 || got[1] != 2 || got[2] != 2 {
		t.Fatalf("no repeated or skipped iterations: %v", got)
	}
}
