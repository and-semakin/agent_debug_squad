package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// --- shared loop fixtures -----------------------------------------------------

// loopReviewDefinition is the canonical loop shape: the "refine" body runs
// implement -> review for maxIterations iterations, and the outside report
// consumes the final iteration's review result.
func loopReviewDefinition(maxIterations int) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		Version: 1, Name: "loop-review", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: maxIterations}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"implement": {Agent: "a1", Prompt: "Implement.", Loop: "refine"},
			"review":    {Agent: "a2", Prompt: "Review.", Loop: "refine", Needs: []string{"implement"}},
			"report":    {Agent: "a3", Prompt: "Report.", Needs: []string{"review"}},
		},
	}
}

func loopSnapshot(t *testing.T, fx *managerFixture) domain.WorkflowSnapshot {
	t.Helper()
	snapshot, err := fx.st.LoadWorkflowSnapshot("wf_000001")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snapshot
}

func iterationNumbersForTask(t *testing.T, fx *managerFixture, taskID string) []int {
	t.Helper()
	snapshot := loopSnapshot(t, fx)
	var iterations []int
	for _, attempt := range snapshot.Tasks[taskID].Attempts {
		iterations = append(iterations, attempt.Iteration)
	}
	return iterations
}

func readAttemptManifest(t *testing.T, fx *managerFixture, taskID string, attemptNumber int) domain.WorkflowInputManifest {
	t.Helper()
	snapshot := loopSnapshot(t, fx)
	attempt := findAttempt(&snapshot, taskID, attemptNumber)
	if attempt == nil || attempt.ManifestPath == "" {
		t.Fatalf("task %s attempt %d has no manifest", taskID, attemptNumber)
	}
	data, err := fx.st.ReadWorkflowArtifact("wf_000001", attempt.ManifestPath)
	if err != nil {
		t.Fatalf("manifest read: %v", err)
	}
	var manifest domain.WorkflowInputManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest decode: %v", err)
	}
	return manifest
}

func hasReason(view domain.WorkflowExecutionView, want string) bool {
	for _, reason := range view.AttentionReasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}

// driveToTerminal releases every live attempt successfully, pumping until the
// execution settles. It returns early (without failing) on attention holds so
// tests can assert the held state.
func driveToTerminal(t *testing.T, fx *managerFixture) domain.WorkflowExecutionView {
	t.Helper()
	for i := 0; i < 60; i++ {
		fx.pump()
		view := mustView2(t, fx)
		if view.State.Terminal() {
			return view
		}
		live := fx.exec.liveRunIDs()
		if len(live) == 0 {
			if view.State == domain.WorkflowNeedsAttention || view.State == domain.WorkflowPaused {
				return view
			}
			continue
		}
		for _, runID := range live {
			fx.exec.releaseSuccess(runID, "result of "+runID)
		}
	}
	t.Fatal("execution did not settle")
	return domain.WorkflowExecutionView{}
}

// --- 3.2 / 6.1: exact iterations, numbered attempts, loop views --------------

func TestLoopRunsExactlyMaxIterationsWithNumberedAttempts(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(3), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-loop"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	releaseNext := func(message string) {
		t.Helper()
		live := fx.exec.liveRunIDs()
		if len(live) != 1 {
			t.Fatalf("want exactly one live attempt, got %v", live)
		}
		fx.exec.releaseSuccess(live[0], message)
		fx.pump()
	}

	// Iteration 1.
	releaseNext("implement v1")
	releaseNext("review v1")

	// Mid-loop observation: the advance and the iteration-2 dispatch share
	// one pass, so the view now shows iteration 2 with a numbered attempt.
	view := mustView2(t, fx)
	if len(view.Loops) != 1 {
		t.Fatalf("loop views: %+v", view.Loops)
	}
	if view.Loops[0].Name != "refine" || view.Loops[0].Iteration != 2 ||
		view.Loops[0].MaxIterations != 3 || view.Loops[0].State != domain.WorkflowLoopRunning {
		t.Fatalf("mid-loop observation: %+v", view.Loops[0])
	}
	impl := findTaskView(view, "implement")
	if last := impl.Attempts[len(impl.Attempts)-1]; last.Attempt != 2 || last.Iteration != 2 {
		t.Fatalf("iteration-2 attempt must carry its number and iteration: %+v", last)
	}
	if report := findTaskView(view, "report"); report.State != domain.WorkflowTaskPending ||
		report.BlockedReason != "waiting_loop:refine" {
		t.Fatalf("outside consumer must wait with a loop reason: %+v", report)
	}

	releaseNext("implement v2")
	releaseNext("review v2")
	releaseNext("implement v3")
	releaseNext("review v3")

	view = mustView2(t, fx)
	if view.Loops[0].Iteration != 3 || view.Loops[0].State != domain.WorkflowLoopDone {
		t.Fatalf("final loop state: %+v", view.Loops[0])
	}
	if len(fx.exec.liveRunIDs()) != 1 {
		t.Fatalf("report must dispatch after the third iteration settles: %v", fx.exec.liveRunIDs())
	}
	releaseNext("report")

	view = mustView2(t, fx)
	if view.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", view.State, view.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, fx, "implement"); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("implement iterations: %v", got)
	}
	if got := iterationNumbersForTask(t, fx, "review"); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("review iterations: %v", got)
	}
	if got := iterationNumbersForTask(t, fx, "report"); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("outside attempts must carry iteration 0: %v", got)
	}

	// The outside consumer sees the final iteration's attempt, and all seven
	// dispatches (6 body + 1 report) happened exactly once each.
	manifest := readAttemptManifest(t, fx, "report", 1)
	dep := manifest.Dependencies[0]
	if dep.TaskID != "review" || dep.Attempt != 3 || dep.Iteration != 3 {
		t.Fatalf("report manifest must reference the final iteration: %+v", dep)
	}
	if got := len(fx.exec.dispatched()); got != 7 {
		t.Fatalf("dispatch bound 2*3+1 violated, dispatched=%d", got)
	}
}

// --- 3.4 / 5.4: durable failure hold, resume acceptance, retry repair --------

func TestLoopHardFailureHoldsAndRetryRepairsCurrentIteration(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(3), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-hold"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "implement"), "boom")
	fx.pump()

	view := mustView2(t, fx)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("hard failure must hold, got %v", view.State)
	}
	if !hasReason(view, "loop_failure:refine:1:implement:retry_or_cancel") {
		t.Fatalf("attention reasons must name loop/iteration/task: %v", view.AttentionReasons)
	}
	if view.Loops[0].State != domain.WorkflowLoopNeedsAttention || view.Loops[0].Iteration != 1 {
		t.Fatalf("loop must hold at iteration 1: %+v", view.Loops[0])
	}
	if report := findTaskView(view, "report"); report.State != domain.WorkflowTaskPending ||
		report.BlockedReason != "waiting_loop:refine" {
		t.Fatalf("outside consumer must stay pending: %+v", report)
	}
	if len(fx.exec.liveRunIDs()) != 0 || len(fx.exec.dispatched()) != 1 {
		t.Fatalf("hold must stop all new dispatch: live=%v dispatched=%v", fx.exec.liveRunIDs(), fx.exec.dispatched())
	}
	if held, err := fx.m.Wait(context.Background(), "wf_000001", 2*time.Second); err != nil || held.State != domain.WorkflowNeedsAttention {
		t.Fatalf("wait must wake on the hold: state=%v err=%v", held.State, err)
	}

	// Resume is accepted with 200/current-view semantics but never waives
	// the failure hold, and it does not dispatch anything.
	resumed, err := fx.m.Resume("wf_000001")
	if err != nil {
		t.Fatalf("resume during a loop hold must be accepted: %v", err)
	}
	if resumed.State != domain.WorkflowNeedsAttention || !hasReason(resumed, "loop_failure:refine:1:implement") {
		t.Fatalf("resume must preserve the unresolved hold: %+v", resumed.AttentionReasons)
	}
	fx.pump()
	if got := len(fx.exec.dispatched()); got != 1 {
		t.Fatalf("failure-only resume must not dispatch, got %d", got)
	}

	// An eligible retry repairs iteration 1 and retains the iteration.
	retryView, created, err := fx.m.RetryTask("wf_000001", "implement", RetryRequest{RequestID: "r1", ExpectedAttempt: 1})
	if err != nil || !created {
		t.Fatalf("retry under a loop hold must be accepted: created=%v err=%v", created, err)
	}
	if retryView.State != domain.WorkflowRunning {
		t.Fatalf("queued repair must release the hold, got %v", retryView.State)
	}
	snapshot := loopSnapshot(t, fx)
	if queued := snapshot.Tasks["implement"].Attempts[1]; queued.Iteration != 1 || queued.State != domain.WorkflowAttemptQueued {
		t.Fatalf("retry must stay in iteration 1: %+v", queued)
	}

	// Replaying the accepted request must not add work.
	if _, createdAgain, err := fx.m.RetryTask("wf_000001", "implement", RetryRequest{RequestID: "r1", ExpectedAttempt: 1}); err != nil || createdAgain {
		t.Fatalf("retry replay must be idempotent: created=%v err=%v", createdAgain, err)
	}

	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, fx, "implement"); !reflect.DeepEqual(got, []int{1, 1, 2, 3}) {
		t.Fatalf("retry stays in iteration 1, explicit attempts excluded from the bound: %v", got)
	}
	// The failed attempt remains inspectable history.
	if first := loopSnapshot(t, fx).Tasks["implement"].Attempts[0]; first.State != domain.WorkflowAttemptFailed || first.Error != "boom" {
		t.Fatalf("failed attempt must retain its state and reason: %+v", first)
	}
}

// --- 3.2 / 3.4: tolerated failures re-run, thresholds compose ----------------

func TestToleratedFailureReRunsNextIterationWithoutHold(t *testing.T) {
	def := loopReviewDefinition(2)
	review := def.Tasks["review"]
	review.AllowedToFail = true
	def.Tasks["review"] = review
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-tolerated"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "implement"), "implement v1")
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "review"), "review failed")
	fx.pump()

	view := mustView2(t, fx)
	if view.State == domain.WorkflowNeedsAttention {
		t.Fatalf("an acceptable tolerated failure must not hold: %v", view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 2 {
		t.Fatalf("tolerated failure settles the iteration acceptably: %+v", view.Loops[0])
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state derives from the last iteration: %v (%v)", final.State, final.AttentionReasons)
	}
	snapshot := loopSnapshot(t, fx)
	if len(snapshot.Tasks["review"].Attempts) != 2 {
		t.Fatalf("review attempts: %+v", snapshot.Tasks["review"].Attempts)
	}
	if snapshot.Tasks["review"].Attempts[0].State != domain.WorkflowAttemptFailed ||
		snapshot.Tasks["review"].Attempts[0].Iteration != 1 ||
		snapshot.Tasks["review"].Attempts[1].Iteration != 2 {
		t.Fatalf("iteration-1 failure must remain visible history: %+v", snapshot.Tasks["review"].Attempts)
	}
}

func TestBlockedBodyTaskHoldsLoopUntilOutsidePrerequisiteRepaired(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "loop-prereq", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 1}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"seed":      {Agent: "a1", Prompt: "Seed."},
			"implement": {Agent: "a2", Prompt: "Implement.", Loop: "refine", Needs: []string{"seed"}},
			"report":    {Agent: "a3", Prompt: "Report.", Needs: []string{"implement"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-prereq"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "seed"), "seed failed")
	fx.pump()

	view := mustView2(t, fx)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("blocked body task must hold, got %v", view.State)
	}
	if !hasReason(view, "loop_blocked:refine:1:implement:retry_or_cancel") {
		t.Fatalf("blocking hold must name loop/iteration/task: %v", view.AttentionReasons)
	}
	if impl := findTaskView(view, "implement"); impl.State != domain.WorkflowTaskBlocked ||
		!strings.Contains(impl.BlockedReason, "seed") {
		t.Fatalf("blocked task keeps its state and reason: %+v", impl)
	}

	if _, created, err := fx.m.RetryTask("wf_000001", "seed", RetryRequest{RequestID: "r1", ExpectedAttempt: 1}); err != nil || !created {
		t.Fatalf("retry of the outside prerequisite must be accepted: created=%v err=%v", created, err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
}

func TestHoldStopsIndependentWorkAndAnyLoopAdvance(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "hold-siblings", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"left":  {MaxIterations: 2},
			"right": {MaxIterations: 1},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"failer": {Agent: "a1", Prompt: "Looped.", Loop: "left"},
			"solo":   {Agent: "a2", Prompt: "Right loop.", Loop: "right"},
			"plain":  {Agent: "a3", Prompt: "Independent."},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-siblings"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	// failer and plain occupy the two slots; solo is ready but not dispatched.
	if got := len(fx.exec.dispatched()); got != 2 {
		t.Fatalf("two slots must dispatch failer and plain, got %v", fx.exec.dispatched())
	}
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "failer"), "left loop failure")
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "plain"), "plain done")
	fx.pump()

	view := mustView2(t, fx)
	if view.State != domain.WorkflowNeedsAttention || !hasReason(view, "loop_failure:left:1:failer") {
		t.Fatalf("left loop must hold: %v (%v)", view.State, view.AttentionReasons)
	}
	if len(fx.exec.dispatched()) != 2 {
		t.Fatalf("ready work must not dispatch under the hold: %v", fx.exec.dispatched())
	}
	for _, loop := range view.Loops {
		if loop.Name == "right" && loop.State != domain.WorkflowLoopRunning {
			t.Fatalf("no loop may advance while another holds: %+v", loop)
		}
	}

	// Repairing the hold releases everything: live finished work was kept,
	// ready work now flows, and both loops run to completion.
	if _, _, err := fx.m.RetryTask("wf_000001", "failer", RetryRequest{RequestID: "r1", ExpectedAttempt: 1}); err != nil {
		t.Fatalf("retry under loop hold: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	iterations := map[string][]int{}
	for _, name := range []string{"failer", "solo"} {
		iterations[name] = iterationNumbersForTask(t, fx, name)
	}
	if !reflect.DeepEqual(iterations["failer"], []int{1, 1, 2}) || !reflect.DeepEqual(iterations["solo"], []int{1}) {
		t.Fatalf("sibling loops run their own bounded iterations: %v", iterations)
	}
}

// --- 3.5: dispatch bound excludes explicit retries ----------------------------

func TestExplicitRetriesStayInSingleIterationAndNeverAdvance(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "single-iter", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"once": {MaxIterations: 1}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"solo": {Agent: "a1", Prompt: "Once."},
		},
	}
	def.Tasks["solo"] = domain.WorkflowTaskDefinition{Agent: "a1", Prompt: "Once.", Loop: "once"}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create("req-single"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "solo"), "first failure")
	fx.pump()
	// No automatic retry ever fires while the hold stands.
	fx.pump()
	if got := len(fx.exec.dispatched()); got != 1 {
		t.Fatalf("failures must not retry automatically, got %v", fx.exec.dispatched())
	}

	if _, _, err := fx.m.RetryTask("wf_000001", "solo", RetryRequest{RequestID: "r1", ExpectedAttempt: 1}); err != nil {
		t.Fatalf("first explicit retry: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "solo"), "second failure")
	fx.pump()
	if _, _, err := fx.m.RetryTask("wf_000001", "solo", RetryRequest{RequestID: "r2", ExpectedAttempt: 2}); err != nil {
		t.Fatalf("successive eligible retry: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "solo"), "finally")

	view := driveToTerminal(t, fx)
	if view.State != domain.WorkflowSucceeded {
		t.Fatalf("loop completes after the successful retry: %v (%v)", view.State, view.AttentionReasons)
	}
	if view.Loops[0].Iteration != 1 || view.Loops[0].State != domain.WorkflowLoopDone {
		t.Fatalf("retries must not create iteration 2: %+v", view.Loops[0])
	}
	if got := iterationNumbersForTask(t, fx, "solo"); !reflect.DeepEqual(got, []int{1, 1, 1}) {
		t.Fatalf("all retry attempts stay in iteration 1: %v", got)
	}
}

// --- 4.1 / 4.2: iteration-scoped handoff and carry-over -----------------------

func TestManifestsResolveSameIterationAndStableOutsideDependencies(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "loop-handoff", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 2}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"seed":      {Agent: "a1", Prompt: "Seed."},
			"implement": {Agent: "a2", Prompt: "Implement.", Loop: "refine", Needs: []string{"seed"}},
			"review":    {Agent: "a3", Prompt: "Review.", Loop: "refine", Needs: []string{"implement"}},
			"report":    {Agent: "a4", Prompt: "Report.", Needs: []string{"review"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2", "a3", "a4")
	if _, _, err := fx.m.Create("req-handoff"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}

	// Iteration-2 review resolves its same-loop dependency to iteration 2,
	// and iteration-2 implement resolves its outside dependency to the single
	// settled outside attempt, stable across iterations.
	manifest := readAttemptManifest(t, fx, "review", 2)
	if manifest.Iteration != 2 {
		t.Fatalf("manifest must carry the dispatch iteration: %+v", manifest)
	}
	if len(manifest.Dependencies) != 1 || manifest.Dependencies[0].TaskID != "implement" {
		t.Fatalf("review manifest dependencies: %+v", manifest.Dependencies)
	}
	if dep := manifest.Dependencies[0]; dep.Attempt != 2 || dep.Iteration != 2 {
		t.Fatalf("same-loop dependency must resolve to the current iteration: %+v", dep)
	}
	implementManifest := readAttemptManifest(t, fx, "implement", 2)
	if len(implementManifest.Dependencies) != 1 || implementManifest.Dependencies[0].TaskID != "seed" {
		t.Fatalf("implement manifest dependencies: %+v", implementManifest.Dependencies)
	}
	if seedEntry := implementManifest.Dependencies[0]; seedEntry.Attempt != 1 || seedEntry.Iteration != 0 {
		t.Fatalf("outside dependency must stay stable across iterations: %+v", seedEntry)
	}
	if iterationNumbersForTask(t, fx, "implement")[0] != 1 {
		t.Fatalf("iteration-1 manifest inputs must be untouched")
	}

	// The outside consumer sees the final iteration only.
	report := readAttemptManifest(t, fx, "report", 1)
	if report.Dependencies[0].Iteration != 2 || report.Dependencies[0].Attempt != 2 {
		t.Fatalf("outside consumer must see final-iteration results: %+v", report.Dependencies[0])
	}
}

func TestCarryOverReachesUpstreamBodyTask(t *testing.T) {
	def := loopReviewDefinition(2)
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-carryover"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "implement"), "implement v1")
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "review"), "review v1")
	fx.pump()

	// Iteration 2 has begun: the implementer (which has no needs) carries
	// the reviewers' iteration-1 outcomes into its fresh attempt.
	runID := mustFindRunForTask(t, fx, "implement")
	opts, ok := fx.exec.options(runID)
	if !ok {
		t.Fatalf("no options for %s", runID)
	}
	for _, want := range []string{
		"--- Previous iteration outcomes ---",
		"Loop iteration 2",
		"review: succeeded",
		"tasks/review/attempts/1/response.txt",
	} {
		if !strings.Contains(opts.Message, want) {
			t.Fatalf("carry-over prompt must contain %q:\n%s", want, opts.Message)
		}
	}
	if idx := strings.Index(opts.Message, "review: succeeded"); idx < strings.Index(opts.Message, "--- Previous iteration outcomes ---") {
		t.Fatalf("carry-over block must be delimited after the header:\n%s", opts.Message)
	}

	manifest := readAttemptManifest(t, fx, "implement", 2)
	if len(manifest.PreviousIteration) != 2 {
		t.Fatalf("carry-over covers every body task: %+v", manifest.PreviousIteration)
	}
	if manifest.PreviousIteration[0].TaskID != "implement" || manifest.PreviousIteration[1].TaskID != "review" {
		t.Fatalf("carry-over must be in sorted task order: %+v", manifest.PreviousIteration)
	}
	for _, entry := range manifest.PreviousIteration {
		if entry.Iteration != 1 || entry.Status != string(domain.WorkflowAttemptSucceeded) || entry.ResultPath == "" {
			t.Fatalf("carry-over entries reference prior-iteration results: %+v", entry)
		}
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v", final.State)
	}
}

func TestDamagedCarryOverArtifactHoldsNextIteration(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "carryover-verify", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"again": {MaxIterations: 2}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"solo": {Agent: "a1", Prompt: "Again.", Loop: "again"},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create("req-carryover-verify"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "solo"), "iteration one")

	// The advance commits, but the next-iteration dispatch cannot verify the
	// carried-over iteration-1 artifact: dispatch stops with an artifact hold.
	fx.st.verifyErr = errors.New("temporary read failure")
	fx.pump()
	fx.st.verifyErr = nil

	view := mustView2(t, fx)
	if view.State != domain.WorkflowNeedsAttention || !hasReason(view, "artifact:solo:1") {
		t.Fatalf("damaged carry-over must hold dispatch: %v (%v)", view.State, view.AttentionReasons)
	}
	if len(fx.exec.liveRunIDs()) != 0 {
		t.Fatalf("no attempt may dispatch with unverifiable inputs: %v", fx.exec.liveRunIDs())
	}

	// Resume revalidates the (now restored) artifact, the hold clears, and
	// iteration 2 runs exactly once.
	if resumed, err := fx.m.Resume("wf_000001"); err != nil || resumed.State != domain.WorkflowRunning {
		t.Fatalf("resume after artifact restoration: state=%v err=%v", resumed.State, err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	if got := iterationNumbersForTask(t, fx, "solo"); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("iterations must neither repeat nor skip: %v", got)
	}
}

// --- 3.3: pause and cancellation are iteration-neutral -------------------------

func TestPauseMidIterationDrainsAndResumeReArms(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "loop-pause", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{"again": {MaxIterations: 2}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"solo": {Agent: "a1", Prompt: "Again.", Loop: "again"},
		},
	}
	fx := newManagerFixture(t, def, "a1")
	if _, _, err := fx.m.Create("req-loop-pause"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	if _, err := fx.m.Pause("wf_000001"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "solo"), "iteration one")
	fx.pump()

	view := mustView2(t, fx)
	if view.State != domain.WorkflowPaused {
		t.Fatalf("pause must hold while the iteration drains: %v", view.State)
	}
	if view.Loops[0].Iteration != 1 {
		t.Fatalf("no advance while paused: %+v", view.Loops[0])
	}
	if got := len(fx.exec.dispatched()); got != 1 {
		t.Fatalf("no new dispatch while paused: %v", fx.exec.dispatched())
	}

	if _, err := fx.m.Resume("wf_000001"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v", final.State)
	}
	if got := iterationNumbersForTask(t, fx, "solo"); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("resume must re-arm normally: %v", got)
	}
}

func TestCancelMidIterationAbandonsExecution(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(3), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-loop-cancel"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runID := mustFindRunForTask(t, fx, "implement")

	if _, err := fx.m.Cancel("wf_000001", CancelOptions{}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !fx.exec.wasCancelled(runID) {
		t.Fatal("the live body run must be cancelled")
	}
	fx.exec.release(runID, domain.OwnedRunOutcome{RunID: runID, Status: domain.RunFailed, Error: "context canceled"})
	fx.pump()

	view := mustView2(t, fx)
	if view.State != domain.WorkflowCancelled {
		t.Fatalf("cancellation may end the loop early: %v (%v)", view.State, view.AttentionReasons)
	}
	if got := len(fx.exec.dispatched()); got != 1 {
		t.Fatalf("nothing further dispatches after cancel: %v", fx.exec.dispatched())
	}
}

// --- 6.1: loopless views stay unchanged ---------------------------------------

func TestLooplessViewsCarryNoLoopState(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create("req-loopless"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v", final.State)
	}
	if final := mustView2(t, fx); final.Loops != nil {
		t.Fatalf("loopless executions must expose no loops: %+v", final.Loops)
	}
	snapshot := loopSnapshot(t, fx)
	if snapshot.Loops != nil {
		t.Fatalf("loopless snapshots must keep a nil loop map: %+v", snapshot.Loops)
	}
	for _, task := range snapshot.Tasks {
		for _, attempt := range task.Attempts {
			if attempt.Iteration != 0 {
				t.Fatalf("loopless attempts must carry iteration 0: %+v", attempt)
			}
		}
	}
}

// --- 2.3: loop settings participate in replay identity -------------------------

func TestLoopSettingsParticipateInReplayIdentity(t *testing.T) {
	def, agents := ephemeralHashDefinition()
	const golden = "3e484ddd3e24e42aa4bae9c16b4f4268ca00ddfdeed4b22e5f74715a2f5deaf2"
	if got := HashWorkflowDefinition(def, agents); got != golden {
		t.Fatalf("loopless definitions must keep the pre-loops golden hash: got %s", got)
	}

	labeled := def
	labeled.Tasks = map[string]domain.WorkflowTaskDefinition{
		"a": {Agent: "alpha", Prompt: "Do the single step.", Loop: "refine"},
	}
	labelHash := HashWorkflowDefinition(labeled, agents)
	if labelHash == golden {
		t.Fatal("a task loop label must change the definition hash")
	}

	declared := labeled
	declared.Loops = map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 3}}
	declaredHash := HashWorkflowDefinition(declared, agents)
	if declaredHash == labelHash {
		t.Fatal("a loop declaration must change the definition hash")
	}

	raised := declared
	raised.Loops = map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 4}}
	if HashWorkflowDefinition(raised, agents) == declaredHash {
		t.Fatal("changing max_iterations must change the definition hash")
	}
}
