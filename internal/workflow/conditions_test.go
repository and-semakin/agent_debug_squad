package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/judge"
)

// --- conditioned-loop fixtures -----------------------------------------------

// conditionedLoopDefinition is the canonical conditioned loop: body implement
// -> review for maxIterations iterations, review is the until_task sink whose
// verdict drives continuation, and the outside report consumes the final review.
// onExhaustion is "" (default needs_attention) or "succeed".
func conditionedLoopDefinition(maxIterations int, onExhaustion string) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		Version: 1, Name: "loop-conditions", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {
			MaxIterations: maxIterations,
			UntilTask:     "review",
			OnVerdict: map[string]string{
				"review_passed": domain.WorkflowLoopActionBreak,
				"issues_found":  domain.WorkflowLoopActionContinue,
				"needs_human":   domain.WorkflowLoopActionNeedsAttention,
			},
			OnExhaustion: onExhaustion,
		}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"implement": {Agent: "a1", Prompt: "Implement.", Loop: "refine"},
			"review": {Agent: "a2", Prompt: "Review.", Loop: "refine", Needs: []string{"implement"},
				Verdicts: map[string]string{
					"review_passed": "clean",
					"issues_found":  "issues remain",
					"needs_human":   "escalate",
				}},
			"report": {Agent: "a3", Prompt: "Report.", Needs: []string{"review"}},
		},
	}
}

func decisionFor(choice string, confidence float64) judge.Decision {
	return judge.Decision{
		Choice:        choice,
		Confidence:    confidence,
		Probabilities: map[string]float64{choice: confidence, "_other": 1 - confidence},
		Model:         "test-judge",
		Raw:           json.RawMessage(`{"choice":"` + choice + `"}`),
	}
}

// newConditionedFixture wires a queue-driven judge so each review classification
// yields the next verdict pushed onto the returned channel.
func newConditionedFixture(t *testing.T, def domain.WorkflowDefinition) (*judgedFixture, chan string) {
	t.Helper()
	fx := newJudgedFixture(t, def, "a1", "a2", "a3")
	queue := make(chan string, 16)
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return decisionFor(<-queue, 0.95), nil
	})
	return fx, queue
}

// runIteration drives one loop iteration: implement succeeds, then review is
// judged to the given verdict, leaving the iteration settled acceptably.
func runIteration(t *testing.T, fx *judgedFixture, queue chan<- string, verdict string) {
	t.Helper()
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "implement"), "implement")
	fx.pump()
	queue <- verdict
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "review"), "review")
	if !fx.pumpUntil(3*time.Second, func() bool {
		state, _ := attemptState(t, fx.managerFixture, "review")
		return state == domain.WorkflowAttemptSucceeded
	}) {
		t.Fatalf("review never settled with verdict %q", verdict)
	}
}

// --- 2.1: early break, repeat-until-clean, static parity ----------------------

func TestConditionedLoopBreaksEarlyOnVerdict(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(3, ""))
	if _, _, err := fx.m.Create("req-break"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "review_passed")
	fx.pump()

	view := mustView2(t, fx.managerFixture)
	if len(view.Loops) != 1 || view.Loops[0].State != domain.WorkflowLoopDone || view.Loops[0].Iteration != 1 {
		t.Fatalf("break must complete the loop at iteration 1: %+v", view.Loops)
	}
	// The outside consumer reads iteration-1 results only.
	if got := iterationNumbersForTask(t, fx.managerFixture, "implement"); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("break must skip iterations 2 and 3: %v", got)
	}
	if len(fx.exec.liveRunIDs()) != 1 {
		t.Fatalf("report must dispatch once the loop breaks: %v", fx.exec.liveRunIDs())
	}
	manifest := readAttemptManifest(t, fx.managerFixture, "report", 1)
	if dep := manifest.Dependencies[0]; dep.Iteration != 1 || dep.Attempt != 1 {
		t.Fatalf("outside consumer must get iteration-1 review: %+v", dep)
	}
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "report"), "report")
	if final := driveToTerminal(t, fx.managerFixture); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
}

func TestConditionedLoopRepeatsUntilClean(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(3, ""))
	if _, _, err := fx.m.Create("req-repeat"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "issues_found")
	if view := mustView2(t, fx.managerFixture); view.Loops[0].Iteration != 2 || view.Loops[0].State != domain.WorkflowLoopRunning {
		t.Fatalf("continue must re-arm iteration 2: %+v", view.Loops[0])
	}
	runIteration(t, fx, queue, "issues_found")
	if view := mustView2(t, fx.managerFixture); view.Loops[0].Iteration != 3 || view.Loops[0].State != domain.WorkflowLoopRunning {
		t.Fatalf("continue must re-arm iteration 3: %+v", view.Loops[0])
	}
	runIteration(t, fx, queue, "review_passed")
	fx.pump()
	if view := mustView2(t, fx.managerFixture); view.Loops[0].State != domain.WorkflowLoopDone || view.Loops[0].Iteration != 3 {
		t.Fatalf("clean review must break at iteration 3: %+v", view.Loops[0])
	}
	if got := iterationNumbersForTask(t, fx.managerFixture, "review"); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("one review per iteration until clean: %v", got)
	}
}

func TestStaticLoopViewCarriesNoConditionFields(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(1), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-static-parity"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v", final.State)
	}
	view := mustView2(t, fx)
	loop := view.Loops[0]
	if loop.UntilTask != "" || loop.LastConditionVerdict != "" || loop.EffectiveMaxIterations != 0 || loop.StopRequested {
		t.Fatalf("static-loop views must stay byte-identical: %+v", loop)
	}
}

// --- 2.1 / 3.1: needs_attention action holds, override redirects --------------

func TestConditionNeedsAttentionHoldsAndOverrideRedirectsToBreak(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(3, ""))
	if _, _, err := fx.m.Create("req-attention"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "needs_human")
	fx.pump()

	view := mustView2(t, fx.managerFixture)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("needs_attention action must hold: %v", view.State)
	}
	if !hasReason(view, "loop_attention:refine:1:review:needs_human") {
		t.Fatalf("hold reason must name loop/iteration/task/verdict: %v", view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 1 || view.Loops[0].State != domain.WorkflowLoopNeedsAttention {
		t.Fatalf("held loop stays at iteration 1: %+v", view.Loops[0])
	}
	if len(fx.exec.liveRunIDs()) != 0 {
		t.Fatalf("hold must stop new dispatch: %v", fx.exec.liveRunIDs())
	}

	// The settled needs_human verdict is load-bearing: overriding it to a break
	// re-derives the condition, clears the hold, and completes the loop.
	overridden, created, err := fx.m.OverrideVerdict("wf_000001", "review", 1, OverrideVerdictRequest{RequestID: "ovr-1", Verdict: "review_passed"})
	if err != nil || !created {
		t.Fatalf("override of a load-bearing verdict must be accepted: created=%v err=%v", created, err)
	}
	if overridden.State == domain.WorkflowNeedsAttention {
		t.Fatalf("override must clear the condition hold: %v", overridden.State)
	}
	if _, created, err := fx.m.OverrideVerdict("wf_000001", "review", 1, OverrideVerdictRequest{RequestID: "ovr-1", Verdict: "review_passed"}); err != nil || created {
		t.Fatalf("override replay must be idempotent: created=%v err=%v", created, err)
	}
	if _, _, err := fx.m.OverrideVerdict("wf_000001", "review", 1, OverrideVerdictRequest{RequestID: "ovr-2", Verdict: "issues_found"}); !errors.Is(err, ErrVerdictConflict) {
		t.Fatalf("settled (no longer load-bearing) verdict must reject further overrides: %v", err)
	}
	fx.pump()
	if view := mustView2(t, fx.managerFixture); view.Loops[0].State != domain.WorkflowLoopDone || view.Loops[0].Iteration != 1 {
		t.Fatalf("redirected break must complete at iteration 1: %+v", view.Loops[0])
	}
}

func TestOverrideRedirectsHeldConditionToContinueBelowCap(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(3, ""))
	if _, _, err := fx.m.Create("req-attention-continue"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "needs_human")
	fx.pump()
	if _, _, err := fx.m.OverrideVerdict("wf_000001", "review", 1, OverrideVerdictRequest{RequestID: "ovr-c", Verdict: "issues_found"}); err != nil {
		t.Fatalf("override to continue: %v", err)
	}
	fx.pump()
	if view := mustView2(t, fx.managerFixture); view.Loops[0].Iteration != 2 || view.Loops[0].State != domain.WorkflowLoopRunning {
		t.Fatalf("redirected continue must re-arm iteration 2 below cap: %+v", view.Loops[0])
	}
}

// --- 2.4: condition failure & uncertainty never steer the loop ----------------

// TestConditionFailureHoldsForRetryWithoutAdvancing covers the "Condition failure
// cannot advance without a verdict" scenario: when the until_task fails at the
// task level (timeout/backend failure/missing response) it produces no verdict,
// so the loop holds under the existing loop_failure policy — no condition action
// or exhaustion policy applies — and an eligible explicit retry repairs the same
// iteration.
func TestConditionFailureHoldsForRetryWithoutAdvancing(t *testing.T) {
	fx, _ := newConditionedFixture(t, conditionedLoopDefinition(3, ""))
	if _, _, err := fx.m.Create("req-cond-fail"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "implement"), "implement")
	fx.pump()
	// Fail the condition task before any classification: no verdict is produced.
	fx.exec.releaseFailure(mustFindRunForTask(t, fx.managerFixture, "review"), "backend boom")
	fx.pump()

	view := mustView2(t, fx.managerFixture)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("a failed condition task must hold for attention: %v", view.State)
	}
	if !hasReason(view, "loop_failure:refine:1:review:retry_or_cancel") {
		t.Fatalf("failure must hold via loop_failure, not a condition action: %v", view.AttentionReasons)
	}
	if hasReason(view, "loop_attention:refine") || hasReason(view, "loop_exhausted:refine") {
		t.Fatalf("no condition/exhaustion policy may apply to a failed iteration: %v", view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 1 || view.Loops[0].State != domain.WorkflowLoopNeedsAttention {
		t.Fatalf("a failed condition must not advance the loop: %+v", view.Loops[0])
	}
	// The failure is retry-repairable, so an explicit retry of the same iteration
	// is accepted.
	if _, created, err := fx.m.RetryTask("wf_000001", "review", RetryRequest{RequestID: "cfr-1", ExpectedAttempt: 1}); err != nil || !created {
		t.Fatalf("a failed condition task must be retryable: created=%v err=%v", created, err)
	}
}

// TestConditionUncertainAsErrorHoldsWithoutLookup covers the "Uncertainty as
// error holds the mandatory condition task" scenario: a below-threshold
// classification with on_uncertain: error fails the attempt with
// uncertain_verdict, the loop needs attention through the loop_failure policy,
// and the synthetic uncertain value is never looked up in on_verdict.
func TestConditionUncertainAsErrorHoldsWithoutLookup(t *testing.T) {
	def := conditionedLoopDefinition(3, "")
	def.OnUncertain = domain.WorkflowOnUncertainError
	fx := newJudgedFixture(t, def, "a1", "a2", "a3")
	// A declared choice, but below the 0.7 default threshold: uncertain.
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return decisionFor("review_passed", 0.5), nil
	})
	if _, _, err := fx.m.Create("req-cond-uncertain"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "implement"), "implement")
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "review"), "review")
	if !fx.pumpUntil(3*time.Second, func() bool {
		state, _ := attemptState(t, fx.managerFixture, "review")
		return state == domain.WorkflowAttemptFailed
	}) {
		t.Fatal("on_uncertain: error must fail the condition attempt")
	}

	_, attempt := attemptState(t, fx.managerFixture, "review")
	if attempt.Reason != "uncertain_verdict" {
		t.Fatalf("failure reason must be uncertain_verdict, got %q", attempt.Reason)
	}
	if attempt.Verdict == nil || attempt.Verdict.Value != domain.ReservedVerdictName {
		t.Fatalf("uncertain failure must carry the synthetic reserved verdict: %+v", attempt.Verdict)
	}
	view := mustView2(t, fx.managerFixture)
	if view.State != domain.WorkflowNeedsAttention || !hasReason(view, "loop_failure:refine:1:review") {
		t.Fatalf("a mandatory uncertain condition must hold via loop_failure: %v (%v)", view.State, view.AttentionReasons)
	}
	// The synthetic "uncertain" value must not be resolved against on_verdict as a
	// condition action, and no exhaustion policy may fire on the unsettled iteration.
	if hasReason(view, "loop_attention:refine") || hasReason(view, "loop_exhausted:refine") {
		t.Fatalf("the reserved uncertain verdict must not steer the loop: %v", view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 1 || view.Loops[0].State != domain.WorkflowLoopNeedsAttention {
		t.Fatalf("uncertainty must not advance the loop: %+v", view.Loops[0])
	}
}

// --- 2.2: exhaustion policies -------------------------------------------------

func TestExhaustionNeedsAttentionHoldsAtCapWithExtendGuidance(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(1, ""))
	if _, _, err := fx.m.Create("req-exhaust-attention"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "issues_found")
	fx.pump()

	view := mustView2(t, fx.managerFixture)
	if view.State != domain.WorkflowNeedsAttention || !hasReason(view, "loop_exhausted:refine:1:extend_or_stop_or_cancel") {
		t.Fatalf("default exhaustion must hold with extend/stop guidance: %v (%v)", view.State, view.AttentionReasons)
	}
	if view.Loops[0].State != domain.WorkflowLoopNeedsAttention {
		t.Fatalf("exhausted loop holds: %+v", view.Loops[0])
	}
}

func TestNeedsAttentionActionWinsOverExhaustionAtCap(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(1, domain.WorkflowExhaustionSucceed))
	if _, _, err := fx.m.Create("req-precedence"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// needs_attention at the cap under a succeed exhaustion policy still holds:
	// the action always beats the exhaustion choice.
	runIteration(t, fx, queue, "needs_human")
	fx.pump()
	view := mustView2(t, fx.managerFixture)
	if !hasReason(view, "loop_attention:refine:1:review:needs_human") || hasReason(view, "loop_exhausted:") {
		t.Fatalf("needs_attention action must take precedence over succeed exhaustion: %v", view.AttentionReasons)
	}
}

func TestExhaustionSucceedCompletesAtCapPreservingVerdict(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(1, domain.WorkflowExhaustionSucceed))
	if _, _, err := fx.m.Create("req-exhaust-succeed"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "issues_found")
	fx.pump()

	view := mustView2(t, fx.managerFixture)
	if view.Loops[0].State != domain.WorkflowLoopDone {
		t.Fatalf("succeed exhaustion must complete the loop: %+v", view.Loops[0])
	}
	if view.Loops[0].LastConditionVerdict != "issues_found" {
		t.Fatalf("succeed must preserve the condition verdict: %+v", view.Loops[0])
	}
	if len(fx.exec.liveRunIDs()) != 1 {
		t.Fatalf("report must run after succeed-exhaustion: %v", fx.exec.liveRunIDs())
	}
}

// --- 4.1: extend control ------------------------------------------------------

func TestExtendReleasesExhaustionHoldAndIsIdempotent(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(1, ""))
	if _, _, err := fx.m.Create("req-extend"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "issues_found")
	fx.pump()
	if view := mustView2(t, fx.managerFixture); !hasReason(view, "loop_exhausted:refine:1") {
		t.Fatalf("precondition: exhaustion hold expected: %v", view.AttentionReasons)
	}

	extended, created, err := fx.m.ExtendLoop("wf_000001", "refine", ExtendRequest{RequestID: "ext-1", AddIterations: 2})
	if err != nil || !created {
		t.Fatalf("extend must be accepted: created=%v err=%v", created, err)
	}
	if extended.Loops[0].EffectiveMaxIterations != 3 {
		t.Fatalf("effective cap must rise to declared+2: %+v", extended.Loops[0])
	}
	if hasReason(extended, "loop_exhausted:") {
		t.Fatalf("extend must release the exhaustion hold: %v", extended.AttentionReasons)
	}

	// Replaying the accepted request must not raise the cap again.
	replayed, createdAgain, err := fx.m.ExtendLoop("wf_000001", "refine", ExtendRequest{RequestID: "ext-1", AddIterations: 2})
	if err != nil || createdAgain {
		t.Fatalf("extend replay must be idempotent: created=%v err=%v", createdAgain, err)
	}
	if replayed.Loops[0].EffectiveMaxIterations != 3 {
		t.Fatalf("replay must not double the increase: %+v", replayed.Loops[0])
	}
	// Reusing the request id for a different amount conflicts.
	if _, _, err := fx.m.ExtendLoop("wf_000001", "refine", ExtendRequest{RequestID: "ext-1", AddIterations: 5}); !errors.Is(err, ErrLoopControlConflict) {
		t.Fatalf("conflicting extend amount: %v", err)
	}

	// Iteration 2 now runs under the raised cap.
	fx.pump()
	if view := mustView2(t, fx.managerFixture); view.Loops[0].Iteration != 2 {
		t.Fatalf("extend must let the loop continue: %+v", view.Loops[0])
	}
}

func TestExtendValidationAndLifecycleErrors(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(1, ""))
	if _, _, err := fx.m.Create("req-extend-errors"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := fx.m.ExtendLoop("wf_000001", "refine", ExtendRequest{RequestID: "", AddIterations: 1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing request id: %v", err)
	}
	if _, _, err := fx.m.ExtendLoop("wf_000001", "refine", ExtendRequest{RequestID: "e", AddIterations: 0}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-positive add_iterations: %v", err)
	}
	if _, _, err := fx.m.ExtendLoop("wf_000001", "ghost", ExtendRequest{RequestID: "e", AddIterations: 1}); !errors.Is(err, ErrLoopNotFound) {
		t.Fatalf("unknown loop: %v", err)
	}
	// Complete the loop by breaking, then reject a new extend on a done loop.
	runIteration(t, fx, queue, "review_passed")
	fx.pump()
	if view := mustView2(t, fx.managerFixture); view.Loops[0].State != domain.WorkflowLoopDone {
		t.Fatalf("precondition: loop should be done: %+v", view.Loops[0])
	}
	if _, _, err := fx.m.ExtendLoop("wf_000001", "refine", ExtendRequest{RequestID: "e2", AddIterations: 1}); !errors.Is(err, ErrLoopControlConflict) {
		t.Fatalf("extend on a done loop must conflict: %v", err)
	}
}

// --- regression: accepted extension governs the static scheduler --------------

// TestExtendRaisesStaticLoopCap covers the reported bug where a static loop
// ignored an accepted extension: the scheduler compared the iteration counter
// with the declared max_iterations instead of the effective cap, so a loop with
// max_iterations 1 finished after one iteration even after ExtendLoop raised
// effective_max_iterations to 2.
func TestExtendRaisesStaticLoopCap(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(1), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-extend-static"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	// Extend while iteration 1 is still running; the raised cap must govern the
	// completion check, not the original budget.
	if _, created, err := fx.m.ExtendLoop("wf_000001", "refine", ExtendRequest{RequestID: "ext-static", AddIterations: 1}); err != nil || !created {
		t.Fatalf("extend must be accepted: created=%v err=%v", created, err)
	}

	releaseNext := func(message string) {
		t.Helper()
		live := fx.exec.liveRunIDs()
		if len(live) != 1 {
			t.Fatalf("want exactly one live attempt, got %v", live)
		}
		fx.exec.releaseSuccess(live[0], message)
		fx.pump()
	}

	releaseNext("implement v1") // iteration 1 body continues with review
	releaseNext("review v1")    // iteration 1 settled -> must re-arm iteration 2, not complete

	view := mustView2(t, fx)
	if view.Loops[0].Iteration != 2 || view.Loops[0].State != domain.WorkflowLoopRunning {
		t.Fatalf("extension must re-arm iteration 2 of a static loop: %+v", view.Loops[0])
	}
	if got := iterationNumbersForTask(t, fx, "implement"); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("static loop must run the extended iteration: %v", got)
	}
	// Reaching the effective cap completes the loop.
	releaseNext("implement v2")
	releaseNext("review v2")
	if view := mustView2(t, fx); view.Loops[0].State != domain.WorkflowLoopDone || view.Loops[0].Iteration != 2 {
		t.Fatalf("static loop must complete at the extended cap: %+v", view.Loops[0])
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
}

// --- regression: a failed control save leaves no phantom effect ---------------

// TestExtendSaveFailureLeavesNoPhantomEffect covers the reported bug where a
// snapshot write failure during ExtendLoop left the live in-memory snapshot
// carrying both the cap increase and its idempotency record: a replayed request
// then returned success without persisting, and the confirmed extension vanished
// on restart. The effect and its replay record must commit only on a successful
// save.
func TestExtendSaveFailureLeavesNoPhantomEffect(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(1), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-extend-fail"); err != nil {
		t.Fatalf("create: %v", err)
	}
	req := ExtendRequest{RequestID: "ext-fail", AddIterations: 1}

	fx.st.mu.Lock()
	fx.st.saveErr = errors.New("snapshot write failed")
	fx.st.mu.Unlock()
	if _, created, err := fx.m.ExtendLoop("wf_000001", "refine", req); err == nil || created {
		t.Fatalf("extend must fail and report not-created: created=%v err=%v", created, err)
	}
	fx.st.mu.Lock()
	fx.st.saveErr = nil
	fx.st.mu.Unlock()

	// The failed attempt must not have raised the live cap or left a replay record.
	if view := mustView2(t, fx); view.Loops[0].EffectiveMaxIterations != 0 {
		t.Fatalf("a failed extend must leave no in-memory cap increase: %+v", view.Loops[0])
	}
	// Replaying the same request is now treated as new and succeeds durably.
	if _, created, err := fx.m.ExtendLoop("wf_000001", "refine", req); err != nil || !created {
		t.Fatalf("retry after a save failure must be accepted as new: created=%v err=%v", created, err)
	}
	// A further replay is idempotent and never doubles the increase.
	if view, created, err := fx.m.ExtendLoop("wf_000001", "refine", req); err != nil || created || view.Loops[0].EffectiveMaxIterations != 2 {
		t.Fatalf("replay must be idempotent at cap 2: created=%v cap=%d err=%v", created, view.Loops[0].EffectiveMaxIterations, err)
	}
	// The confirmed extension is persisted exactly once and survives a restart.
	if got := loopSnapshot(t, fx).Loops["refine"].ExtendedIterations; got != 1 {
		t.Fatalf("committed extension must persist exactly once: %d", got)
	}
}

// TestStopSaveFailureLeavesNoPhantomEffect is the StopLoop counterpart: a failed
// save must not leave the stop intent or its replay record on the live snapshot,
// so a replayed request re-applies and durably persists.
func TestStopSaveFailureLeavesNoPhantomEffect(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(3), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-stop-fail"); err != nil {
		t.Fatalf("create: %v", err)
	}
	req := StopRequest{RequestID: "stop-fail"}

	fx.st.mu.Lock()
	fx.st.saveErr = errors.New("snapshot write failed")
	fx.st.mu.Unlock()
	if _, created, err := fx.m.StopLoop("wf_000001", "refine", req); err == nil || created {
		t.Fatalf("stop must fail and report not-created: created=%v err=%v", created, err)
	}
	fx.st.mu.Lock()
	fx.st.saveErr = nil
	fx.st.mu.Unlock()

	// The failed attempt must not have set the live stop flag or left a replay record.
	if mustView2(t, fx).Loops[0].StopRequested {
		t.Fatalf("a failed stop must leave no in-memory intent: %+v", mustView2(t, fx).Loops[0])
	}
	// Replaying is now treated as new and succeeds; a further replay is idempotent.
	if _, created, err := fx.m.StopLoop("wf_000001", "refine", req); err != nil || !created {
		t.Fatalf("retry after a save failure must be accepted as new: created=%v err=%v", created, err)
	}
	if _, created, err := fx.m.StopLoop("wf_000001", "refine", req); err != nil || created {
		t.Fatalf("replay must be idempotent: created=%v err=%v", created, err)
	}
	if !mustView2(t, fx).Loops[0].StopRequested {
		t.Fatal("committed stop intent must be exposed")
	}
	if !loopSnapshot(t, fx).Loops["refine"].StopRequested {
		t.Fatal("stop intent must be durably recorded")
	}
}

// --- 4.2: stop control --------------------------------------------------------

func TestStopFinishesCurrentIterationWithoutAnother(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(3, ""))
	if _, _, err := fx.m.Create("req-stop"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	stopped, created, err := fx.m.StopLoop("wf_000001", "refine", StopRequest{RequestID: "stop-1"})
	if err != nil || !created {
		t.Fatalf("stop must be accepted: created=%v err=%v", created, err)
	}
	if !stopped.Loops[0].StopRequested {
		t.Fatalf("stop_requested must be exposed in the view: %+v", stopped.Loops[0])
	}

	// The current iteration finishes normally; the verdict would continue but
	// the stop retires the loop at iteration 1 instead of starting iteration 2.
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "implement"), "implement")
	fx.pump()
	queue <- "issues_found"
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "review"), "review")
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, _ := fx.m.View("wf_000001")
		return len(view.Loops) == 1 && view.Loops[0].State == domain.WorkflowLoopDone
	}) {
		t.Fatalf("stop must finish the settled iteration and complete the loop: %+v", mustView2(t, fx.managerFixture).Loops)
	}
	if got := iterationNumbersForTask(t, fx.managerFixture, "implement"); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("stop must never start a further iteration: %v", got)
	}
}

func TestStopResolvesOwnExhaustionHoldDespiteSiblingHold(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "sibling-holds", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"left": {MaxIterations: 1, UntilTask: "lreview", OnVerdict: map[string]string{
				"pass": domain.WorkflowLoopActionBreak, "again": domain.WorkflowLoopActionContinue}},
			"right": {MaxIterations: 1},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"limpl":   {Agent: "a1", Prompt: "p", Loop: "left"},
			"lreview": {Agent: "a2", Prompt: "review", Loop: "left", Needs: []string{"limpl"}, Verdicts: map[string]string{"pass": "", "again": ""}},
			"rfailer": {Agent: "a3", Prompt: "p", Loop: "right"},
		},
	}
	fx := newJudgedFixture(t, def, "a1", "a2", "a3")
	queue := make(chan string, 8)
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) { return decisionFor(<-queue, 0.95), nil })
	if _, _, err := fx.m.Create("req-sibling"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	// Settle the left loop to exhaustion first (rfailer keeps a slot busy and
	// stays live), then fail the right loop so both holds stand at once.
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "limpl"), "impl")
	fx.pump()
	queue <- "again"
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "lreview"), "review")
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, _ := fx.m.View("wf_000001")
		return hasReason(view, "loop_exhausted:left:1")
	}) {
		t.Fatalf("left loop must exhaust: %v", mustView2(t, fx.managerFixture).AttentionReasons)
	}
	fx.exec.releaseFailure(mustFindRunForTask(t, fx.managerFixture, "rfailer"), "boom")
	fx.pump()
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, _ := fx.m.View("wf_000001")
		return hasReason(view, "loop_exhausted:left:1") && hasReason(view, "loop_failure:right:1:rfailer")
	}) {
		t.Fatalf("both loops must hold simultaneously: %v", mustView2(t, fx.managerFixture).AttentionReasons)
	}

	// Stopping the exhausted left loop resolves its own cause even while the
	// right loop's failure hold still blocks all dispatch.
	if _, _, err := fx.m.StopLoop("wf_000001", "left", StopRequest{RequestID: "s-left"}); err != nil {
		t.Fatalf("stop left: %v", err)
	}
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, _ := fx.m.View("wf_000001")
		return view.Loops[loopIndex(view, "left")].State == domain.WorkflowLoopDone &&
			!hasReason(view, "loop_exhausted:left") && hasReason(view, "loop_failure:right")
	}) {
		view := mustView2(t, fx.managerFixture)
		t.Fatalf("stop must clear only its own cause: loops=%+v reasons=%v", view.Loops, view.AttentionReasons)
	}
	if len(fx.exec.liveRunIDs()) != 0 {
		t.Fatalf("the surviving sibling hold must still block dispatch: %v", fx.exec.liveRunIDs())
	}
}

func loopIndex(view domain.WorkflowExecutionView, name string) int {
	for i, loop := range view.Loops {
		if loop.Name == name {
			return i
		}
	}
	return -1
}

// --- 4.3: retry permitted under condition/exhaustion holds --------------------

func TestRetryPermittedUnderConditionHolds(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "retry-under-hold", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"broken": {MaxIterations: 2},
			"held": {MaxIterations: 1, UntilTask: "hreview", OnVerdict: map[string]string{
				"again": domain.WorkflowLoopActionContinue, "done": domain.WorkflowLoopActionBreak}},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"failer":  {Agent: "a1", Prompt: "p", Loop: "broken"},
			"himpl":   {Agent: "a2", Prompt: "p", Loop: "held"},
			"hreview": {Agent: "a3", Prompt: "review", Loop: "held", Needs: []string{"himpl"}, Verdicts: map[string]string{"again": "", "done": ""}},
		},
	}
	fx := newJudgedFixture(t, def, "a1", "a2", "a3")
	queue := make(chan string, 8)
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) { return decisionFor(<-queue, 0.95), nil })
	if _, _, err := fx.m.Create("req-retry-hold"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	// Settle the held loop to exhaustion first (failer keeps a slot busy and
	// stays live), then fail the broken loop so both holds stand at once.
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "himpl"), "impl")
	fx.pump()
	queue <- "again" // exhausted continue at cap -> loop_exhausted:held
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "hreview"), "review")
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, _ := fx.m.View("wf_000001")
		return hasReason(view, "loop_exhausted:held")
	}) {
		t.Fatalf("held loop must exhaust: %v", mustView2(t, fx.managerFixture).AttentionReasons)
	}
	fx.exec.releaseFailure(mustFindRunForTask(t, fx.managerFixture, "failer"), "boom")
	fx.pump()
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, _ := fx.m.View("wf_000001")
		return hasReason(view, "loop_failure:broken") && hasReason(view, "loop_exhausted:held")
	}) {
		t.Fatalf("both holds must stand: %v", mustView2(t, fx.managerFixture).AttentionReasons)
	}
	// Every remaining reason is retry-repairable, so retrying the failed body
	// task is accepted while the exhausted sibling holds.
	if _, created, err := fx.m.RetryTask("wf_000001", "failer", RetryRequest{RequestID: "r1", ExpectedAttempt: 1}); err != nil || !created {
		t.Fatalf("retry under condition/exhaustion holds must be accepted: created=%v err=%v", created, err)
	}
}

// --- 5.1: view exposure -------------------------------------------------------

func TestConditionedLoopViewsExposeConditionState(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(1, ""))
	if _, _, err := fx.m.Create("req-views"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "issues_found")
	fx.pump()

	view := mustView2(t, fx.managerFixture)
	loop := view.Loops[0]
	if loop.UntilTask != "review" || loop.EffectiveMaxIterations != 1 || loop.LastConditionVerdict != "issues_found" {
		t.Fatalf("held-on-exhaustion view: %+v", loop)
	}
	if _, _, err := fx.m.ExtendLoop("wf_000001", "refine", ExtendRequest{RequestID: "ext-v", AddIterations: 1}); err != nil {
		t.Fatalf("extend: %v", err)
	}
	after := mustView2(t, fx.managerFixture).Loops[0]
	if after.EffectiveMaxIterations != 2 {
		t.Fatalf("extension must raise the effective cap in views: %+v", after)
	}
}

// --- 2.1 / recovery: holds re-derive from persisted verdicts ------------------

func TestRecoveryReDerivesConditionHoldWithoutDispatch(t *testing.T) {
	fx, queue := newConditionedFixture(t, conditionedLoopDefinition(3, ""))
	if _, _, err := fx.m.Create("req-recover"); err != nil {
		t.Fatalf("create: %v", err)
	}
	runIteration(t, fx, queue, "needs_human")
	fx.pump()
	if view := mustView2(t, fx.managerFixture); view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("precondition: needs_attention hold expected: %v", view.State)
	}

	second := fx.managerFixture.restart()
	if err := second.m.Start(context.Background()); err != nil {
		t.Fatalf("start recovered manager: %v", err)
	}
	defer finishLiveRuns(t, second)
	deadline := time.Now().Add(3 * time.Second)
	var view domain.WorkflowExecutionView
	for time.Now().Before(deadline) {
		view, _ = second.m.View("wf_000001")
		if view.State == domain.WorkflowNeedsAttention && hasReason(view, "loop_attention:refine:1:review:needs_human") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if view.State != domain.WorkflowNeedsAttention || !hasReason(view, "loop_attention:refine:1:review:needs_human") {
		t.Fatalf("recovery must re-derive the condition hold: %v (%v)", view.State, view.AttentionReasons)
	}
	if got := len(second.exec.dispatched()); got != 0 {
		t.Fatalf("recovery must not dispatch under the hold: %v", got)
	}
}

// --- 1.4: saved "hold" policy rejected before scheduling ----------------------

func TestRecoveryRejectsSavedHoldPolicy(t *testing.T) {
	st := newRecordingStore(t)
	cfg := testWorkflowConfig(chainDefinition(), "a1", "a2", "a3")
	cfg.WorkspaceDir = t.TempDir()
	def := cfg.Workflow
	def.OnUncertain = "hold"
	snapshot := domain.WorkflowSnapshot{
		ExecutionID: "wf_000001", RequestID: "req-held", SchemaVersion: domain.WorkflowSnapshotNonNestedSchemaVersion,
		Definition: *def, State: domain.WorkflowRunning, Mode: domain.WorkflowModeRunning,
		Tasks: map[string]*domain.WorkflowTaskExecution{
			"a": {TaskID: "a", State: domain.WorkflowTaskPending},
		},
	}
	if err := st.SaveWorkflowSnapshot(&snapshot); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	before, _ := st.LoadWorkflowSnapshot("wf_000001")
	revBefore := before.Revision

	m := NewManager(cfg, st, newFakeExecutor())
	err := m.Start(context.Background())
	if !errors.Is(err, ErrUnsupportedPolicy) {
		t.Fatalf("saved hold policy must fail recovery: %v", err)
	}
	after, _ := st.LoadWorkflowSnapshot("wf_000001")
	if after.Revision != revBefore || after.Definition.OnUncertain != "hold" {
		t.Fatalf("rejection must not mutate or migrate the saved definition: rev %d->%d policy %q", revBefore, after.Revision, after.Definition.OnUncertain)
	}
}
