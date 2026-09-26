package workflow

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// --- nested-loop runtime fixtures --------------------------------------------

// nestedStaticDefinition is the smallest two-level nesting: an inner loop
// (implement -> review) runs its full budget inside every iteration of an
// outer static loop whose exit task consumes the inner result. Both loops are
// static (no condition), so this exercises path-keyed scheduling, the
// descendant completion barrier, and the parent-advance subtree reset without
// involving the judge.
func nestedStaticDefinition(innerMax, outerMax int) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		Version: 1, Name: "nested-static", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer": {MaxIterations: outerMax},
			"inner": {MaxIterations: innerMax, Parent: "outer"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"implement": {Agent: "a1", Prompt: "Implement.", Loop: "inner"},
			"review":    {Agent: "a2", Prompt: "Review.", Loop: "inner", Needs: []string{"implement"}},
			"exit":      {Agent: "a3", Prompt: "Exit.", Loop: "outer", Needs: []string{"review"}},
		},
	}
}

// attemptPathTrace renders every recorded attempt's iteration path in dispatch
// order for quick structural assertions.
func attemptPathTrace(t *testing.T, fx *managerFixture, taskID string) []string {
	t.Helper()
	snapshot := loopSnapshot(t, fx)
	var trace []string
	for _, attempt := range snapshot.Tasks[taskID].Attempts {
		trace = append(trace, domain.RenderIterationPath(attempt.IterationPath))
	}
	return trace
}

// loopIterationPath renders a loop execution's current stored path.
func loopIterationPath(t *testing.T, fx *managerFixture, loopName string) []domain.IterationEntry {
	t.Helper()
	snapshot := loopSnapshot(t, fx)
	loop := snapshot.Loops[loopName]
	if loop == nil {
		t.Fatalf("loop %q missing", loopName)
	}
	return loop.IterationPath
}

// --- 3.1 / 3.2 / 6.2: cartesian execution, distinct descendant invocations ---

func TestNestedStaticLoopsRunCartesianProduct(t *testing.T) {
	fx := newManagerFixture(t, nestedStaticDefinition(2, 2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-nested"); err != nil {
		t.Fatalf("create: %v", err)
	}
	snapshot := loopSnapshot(t, fx)
	if snapshot.SchemaVersion != domain.WorkflowSnapshotNestedSchemaVersion {
		t.Fatalf("nested execution must persist schema 3, got %d", snapshot.SchemaVersion)
	}
	// Every loop-owned object carries a complete root-to-owner path even at a
	// single depth, so a repeated local counter never aliases another ancestor
	// context.
	if got := loopIterationPath(t, fx, "inner"); !reflect.DeepEqual(got, []domain.IterationEntry{{Loop: "outer", Iteration: 1}, {Loop: "inner", Iteration: 1}}) {
		t.Fatalf("initial inner path: %+v", got)
	}

	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}

	// Each of the two outer iterations re-runs the full two-iteration inner
	// budget, so the leaf work appears exactly once per distinct path.
	wantInner := []string{"outer=1/inner=1", "outer=1/inner=2", "outer=2/inner=1", "outer=2/inner=2"}
	if got := attemptPathTrace(t, fx, "implement"); !reflect.DeepEqual(got, wantInner) {
		t.Fatalf("implement paths: %v", got)
	}
	if got := attemptPathTrace(t, fx, "review"); !reflect.DeepEqual(got, wantInner) {
		t.Fatalf("review paths: %v", got)
	}
	// The outer exit task runs once per outer iteration, once its descendant
	// barrier (the whole inner loop) finishes for that outer invocation.
	if got := attemptPathTrace(t, fx, "exit"); !reflect.DeepEqual(got, []string{"outer=1", "outer=2"}) {
		t.Fatalf("exit paths: %v", got)
	}
	if got := len(fx.exec.dispatched()); got != 4+4+2 {
		t.Fatalf("dispatch bound violated: %d", got)
	}
	// Local counters still agree with the last path entry.
	impl := loopSnapshot(t, fx).Tasks["implement"].Attempts
	for i, want := range []int{1, 2, 1, 2} {
		if impl[i].Iteration != want {
			t.Fatalf("implement attempt %d local iteration %d, want %d", i, impl[i].Iteration, want)
		}
	}
}

// --- 3.3 / 4.1: exit consumer resolves the descendant's final outcome --------

func TestNestedExitConsumesFinalInnerOutcome(t *testing.T) {
	fx := newManagerFixture(t, nestedStaticDefinition(2, 1), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-nested-consume"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	manifest := readAttemptManifest(t, fx, "exit", 1)
	if len(manifest.Dependencies) != 1 || manifest.Dependencies[0].TaskID != "review" {
		t.Fatalf("exit dependencies: %+v", manifest.Dependencies)
	}
	// A descendant producer resolves to the final committed outcome under the
	// consumer's current path prefix: the second inner iteration's review.
	dep := manifest.Dependencies[0]
	if dep.Attempt != 2 || dep.Iteration != 2 {
		t.Fatalf("exit must consume the final inner review: %+v", dep)
	}
	if want := (domain.IterationEntry{Loop: "inner", Iteration: 2}); len(dep.IterationPath) == 0 || dep.IterationPath[len(dep.IterationPath)-1] != want {
		t.Fatalf("dependency must carry the producer's full path: %+v", dep.IterationPath)
	}
}

// --- 4.1 / 4.2: whole-subtree carry-over with complete prefixes --------------

func TestNestedCarryOverSummarizesWholeSubtree(t *testing.T) {
	// A third leaf in the outer loop has no direct needs, so on every outer
	// advance it carries the previous outer iteration's whole subtree
	// (implement/review of both inner iterations plus the prior exit) forward.
	def := nestedStaticDefinition(2, 2)
	def.Tasks["carry"] = domain.WorkflowTaskDefinition{Agent: "a4", Prompt: "Carry.", Loop: "outer", Needs: []string{"exit"}}
	fx := newManagerFixture(t, def, "a1", "a2", "a3", "a4")
	if _, _, err := fx.m.Create(context.Background(), "req-nested-carry"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	// The second exit invocation carries the first outer iteration's subtree.
	manifest := readAttemptManifest(t, fx, "exit", 2)
	if manifest.PreviousIterationPath == nil {
		t.Fatalf("nested owner carry-over must record its summarized path")
	}
	if want := (domain.IterationEntry{Loop: "outer", Iteration: 1}); len(manifest.PreviousIterationPath) == 0 || manifest.PreviousIterationPath[len(manifest.PreviousIterationPath)-1] != want {
		t.Fatalf("previous_iteration_path: %+v", manifest.PreviousIterationPath)
	}
	seen := map[string]string{}
	for _, entry := range manifest.PreviousIteration {
		seen[entry.TaskID] = entry.Status
	}
	for _, taskID := range []string{"implement", "review", "exit"} {
		if status, ok := seen[taskID]; !ok || status != string(domain.WorkflowAttemptSucceeded) {
			t.Fatalf("subtree carry-over must include %q succeeded: %+v", taskID, manifest.PreviousIteration)
		}
	}
}

// --- 7.1: reason strings carry complete paths --------------------------------

func TestNestedFailureHoldReasonUsesIterationPath(t *testing.T) {
	fx := newManagerFixture(t, nestedStaticDefinition(2, 2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-nested-fail"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "implement"), "boom")
	fx.pump()

	view := mustView2(t, fx)
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("nested failure must hold, got %v", view.State)
	}
	// The failure reason names the loop by its complete path, not the legacy
	// `name:iteration` spelling, while keeping the surrounding template.
	if !hasReason(view, "loop_failure:outer=1/inner=1:implement:retry_or_cancel") {
		t.Fatalf("reason must render the iteration path: %v", view.AttentionReasons)
	}

	// A retry targets the failed attempt at the current path and stays in it.
	if _, created, err := fx.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{RequestID: "r1", ExpectedAttempt: 1}); err != nil || !created {
		t.Fatalf("nested retry must be accepted: created=%v err=%v", created, err)
	}
	queued := loopSnapshot(t, fx).Tasks["implement"].Attempts[1]
	if queued.State != domain.WorkflowAttemptQueued {
		t.Fatalf("retry must reserve a queued attempt: %+v", queued)
	}
	if got := domain.RenderIterationPath(queued.IterationPath); got != "outer=1/inner=1" {
		t.Fatalf("retry must retain the current path, got %q", got)
	}
}

// --- 5.1: shared-enclosing-context retry guard -------------------------------

// nestedGuardSnapshot builds an in-memory nested snapshot with a chosen
// descendant reservation path on review, the transitive consumer of implement,
// so the guard's context-scoping can be asserted without driving the runtime.
func nestedGuardSnapshot(innerCurrent []domain.IterationEntry, reviewPath []domain.IterationEntry) *domain.WorkflowSnapshot {
	snapshot := &domain.WorkflowSnapshot{
		SchemaVersion: domain.WorkflowSnapshotNestedSchemaVersion,
		Definition:    nestedStaticDefinition(2, 2),
		Loops: map[string]*domain.WorkflowLoopExecution{
			"outer": {Iteration: 1, State: domain.WorkflowLoopRunning, IterationPath: []domain.IterationEntry{{Loop: "outer", Iteration: 1}}},
			"inner": {Iteration: int(innerCurrent[len(innerCurrent)-1].Iteration), State: domain.WorkflowLoopRunning, IterationPath: innerCurrent},
		},
		Tasks: map[string]*domain.WorkflowTaskExecution{
			"implement": {TaskID: "implement", State: domain.WorkflowTaskFailed, Attempts: []domain.WorkflowAttempt{
				{Attempt: 1, Iteration: 1, IterationPath: innerCurrent, State: domain.WorkflowAttemptFailed},
			}},
			"review": {TaskID: "review", Attempts: []domain.WorkflowAttempt{
				{Attempt: 1, Iteration: int(reviewPath[len(reviewPath)-1].Iteration), IterationPath: reviewPath, State: domain.WorkflowAttemptSucceeded},
			}},
			"exit": {TaskID: "exit"},
		},
	}
	return snapshot
}

func TestNestedRetryGuardScopesBySharedEnclosingIteration(t *testing.T) {
	outer := []domain.IterationEntry{{Loop: "outer", Iteration: 1}}
	inner := func(n int) []domain.IterationEntry {
		return append(append([]domain.IterationEntry{}, outer...), domain.IterationEntry{Loop: "inner", Iteration: n})
	}

	// A same-loop review reservation at the current inner path blocks.
	if err := assertNoDescendantAttemptsNested(nestedGuardSnapshot(inner(1), inner(1)), "implement"); err == nil {
		t.Fatal("same-invocation descendant reservation must block")
	}
	// A review reservation from a prior inner iteration is history and never
	// blocks a repair of the current invocation.
	if err := assertNoDescendantAttemptsNested(nestedGuardSnapshot(inner(2), inner(1)), "implement"); err != nil {
		t.Fatalf("prior inner reservation must not block: %v", err)
	}
	// The descendant's inner counter is irrelevant for the enclosing outer
	// context: a review under the same outer iteration but different inner
	// iteration does not block when the current inner differs, matching the
	// per-loop common-ancestor scoping.
	if err := assertNoDescendantAttemptsNested(nestedGuardSnapshot(inner(2), inner(2)), "implement"); err == nil {
		t.Fatal("current-inner reservation must block")
	}
}

// --- 6.1 / 9: three-level authoring fixture ----------------------------------

// threeLevelDefinition is design section 9's compact valid graph:
// implement -> review -> consolidate -> test -> accept, with delivery_loop the
// root conditioned on accept, test_loop its child conditioned on test, and
// review_loop the innermost conditioned on consolidate.
func threeLevelDefinition() domain.WorkflowDefinition {
	verdicts := map[string]string{"pass": "clean", "again": "iterate"}
	action := map[string]string{"pass": domain.WorkflowLoopActionBreak, "again": domain.WorkflowLoopActionContinue}
	return domain.WorkflowDefinition{
		Version: 1, Name: "nested-three", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"delivery_loop": {MaxIterations: 1, UntilTask: "accept", OnVerdict: action},
			"test_loop":     {MaxIterations: 2, UntilTask: "test", OnVerdict: action, Parent: "delivery_loop"},
			"review_loop":   {MaxIterations: 3, UntilTask: "consolidate", OnVerdict: action, Parent: "test_loop"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"implement":   {Agent: "a1", Prompt: "Implement.", Loop: "review_loop"},
			"review":      {Agent: "a2", Prompt: "Review.", Loop: "review_loop", Needs: []string{"implement"}},
			"consolidate": {Agent: "a3", Prompt: "Consolidate.", Loop: "review_loop", Needs: []string{"review"}, Verdicts: verdicts},
			"test":        {Agent: "a4", Prompt: "Test.", Loop: "test_loop", Needs: []string{"consolidate"}, Verdicts: verdicts},
			"accept":      {Agent: "a5", Prompt: "Accept.", Loop: "delivery_loop", Needs: []string{"test"}, Verdicts: verdicts},
		},
	}
}

func TestThreeLevelNestedInitializesCompletePaths(t *testing.T) {
	fx := newManagerFixture(t, threeLevelDefinition(), "a1", "a2", "a3", "a4", "a5")
	if _, _, err := fx.m.Create(context.Background(), "req-three"); err != nil {
		t.Fatalf("create must accept the three-level authoring model: %v", err)
	}
	snapshot := loopSnapshot(t, fx)
	if snapshot.SchemaVersion != domain.WorkflowSnapshotNestedSchemaVersion {
		t.Fatalf("schema: %d", snapshot.SchemaVersion)
	}
	cases := map[string][]domain.IterationEntry{
		"delivery_loop": {{Loop: "delivery_loop", Iteration: 1}},
		"test_loop":     {{Loop: "delivery_loop", Iteration: 1}, {Loop: "test_loop", Iteration: 1}},
		"review_loop":   {{Loop: "delivery_loop", Iteration: 1}, {Loop: "test_loop", Iteration: 1}, {Loop: "review_loop", Iteration: 1}},
	}
	for name, want := range cases {
		if got := snapshot.Loops[name].IterationPath; !reflect.DeepEqual(got, want) {
			t.Fatalf("loop %q path %+v, want %+v", name, got, want)
		}
	}
	// The loop views expose parentage and paths, so observers can reconstruct
	// the forest from a single snapshot.
	view := mustView2(t, fx)
	parents := map[string]string{}
	for _, loop := range view.Loops {
		parents[loop.Name] = loop.Parent
	}
	if parents["test_loop"] != "delivery_loop" || parents["review_loop"] != "test_loop" || parents["delivery_loop"] != "" {
		t.Fatalf("loop view parents: %+v", parents)
	}
}

// --- review P1 #1/#2: ancestor carry-over at inner=1 and prompt rendering ----

// readAttemptPrompt returns the exact prompt bytes persisted for one attempt.
func readAttemptPrompt(t *testing.T, fx *managerFixture, taskID string, attemptNumber int) string {
	t.Helper()
	snapshot := loopSnapshot(t, fx)
	attempt := findAttempt(&snapshot, taskID, attemptNumber)
	if attempt == nil || attempt.PromptPath == "" {
		t.Fatalf("task %s attempt %d has no prompt", taskID, attemptNumber)
	}
	data, err := fx.st.ReadWorkflowArtifact("wf_000001", attempt.PromptPath)
	if err != nil {
		t.Fatalf("prompt read: %v", err)
	}
	return string(data)
}

// TestNestedAncestorCarryOverAtFirstInnerIteration proves the enclosing loop's
// previous-iteration feedback reaches an inner task even at its own first
// iteration (outer=2/inner=1), while the owner's own previous-iteration section
// stays absent, and that the prompt surfaces those ancestor references.
func TestNestedAncestorCarryOverAtFirstInnerIteration(t *testing.T) {
	fx := newManagerFixture(t, nestedStaticDefinition(2, 2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-ancestor-carry"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	// implement attempt 3 dispatches at outer=2/inner=1.
	man := readAttemptManifest(t, fx, "implement", 3)
	if got := domain.RenderIterationPath(man.IterationPath); got != "outer=2/inner=1" {
		t.Fatalf("attempt 3 path: %s", got)
	}
	if man.PreviousIterationPath != nil || len(man.PreviousIteration) != 0 {
		t.Fatalf("inner=1 must have no own previous-iteration section: %+v / %+v", man.PreviousIterationPath, man.PreviousIteration)
	}
	if len(man.AncestorPreviousIterations) != 1 {
		t.Fatalf("expected exactly one enclosing-loop section, got %d", len(man.AncestorPreviousIterations))
	}
	section := man.AncestorPreviousIterations[0]
	if got := domain.RenderIterationPath(section.IterationPath); got != "outer=1" {
		t.Fatalf("ancestor section path: %s", got)
	}
	byTask := map[string]domain.WorkflowDependencyInput{}
	for _, e := range section.Outcomes {
		byTask[e.TaskID] = e
	}
	for _, id := range []string{"implement", "review", "exit"} {
		if e, ok := byTask[id]; !ok || e.Status != string(domain.WorkflowAttemptSucceeded) {
			t.Fatalf("ancestor section must include %s succeeded: %+v", id, section.Outcomes)
		}
	}
	// The whole-subtree summary reaches the final inner outcomes (inner=2).
	if byTask["implement"].Iteration != 2 || byTask["review"].Iteration != 2 {
		t.Fatalf("ancestor section must carry inner=2 finals: impl=%d rev=%d", byTask["implement"].Iteration, byTask["review"].Iteration)
	}
	// The prompt (this attempt has no direct dependency inputs) must still
	// surface the ancestor section with readable references and the manifest.
	prompt := readAttemptPrompt(t, fx, "implement", 3)
	for _, want := range []string{"Enclosing loop previous-iteration outcomes", "[enclosing context outer=1]", "Input manifest:"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "exit") || !strings.Contains(prompt, "context outer=1/inner=2") {
		t.Fatalf("prompt must reference ancestor outcomes with their contexts:\n%s", prompt)
	}
	// attempt 4 (outer=2/inner=2) carries BOTH its own inner section and the
	// ancestor outer section, proving the two are computed independently.
	man4 := readAttemptManifest(t, fx, "implement", 4)
	if domain.RenderIterationPath(man4.PreviousIterationPath) != "outer=2/inner=1" || len(man4.PreviousIteration) == 0 {
		t.Fatalf("inner=2 must carry its own previous-iteration section: %+v", man4.PreviousIterationPath)
	}
	if len(man4.AncestorPreviousIterations) != 1 || domain.RenderIterationPath(man4.AncestorPreviousIterations[0].IterationPath) != "outer=1" {
		t.Fatalf("inner=2 must also carry the ancestor outer section: %+v", man4.AncestorPreviousIterations)
	}
}

// --- review P1 #3: ancestor carry-over artifact damage holds dispatch --------

func TestNestedAncestorCarryOverArtifactDamageHolds(t *testing.T) {
	fx := newManagerFixture(t, nestedStaticDefinition(2, 2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-ancestor-damage"); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer finishLiveRuns(t, fx)
	// Drive until the outer exit task of iteration 1 is live, then release it
	// and commit its result WITHOUT reconciling, so the outer loop has not yet
	// advanced and the next inner dispatch has not been scheduled.
	exitRun := ""
	for i := 0; i < 40 && exitRun == ""; i++ {
		fx.pump()
		for _, task := range mustView2(t, fx).Tasks {
			if task.TaskID != "exit" {
				continue
			}
			for _, attempt := range task.Attempts {
				if attempt.RunID != "" && fx.exec.OwnedRunActive(attempt.RunID) {
					exitRun = attempt.RunID
				}
			}
		}
		if exitRun != "" {
			break
		}
		for _, r := range fx.exec.liveRunIDs() {
			fx.exec.releaseSuccess(r, "ok")
		}
	}
	if exitRun == "" {
		t.Fatal("exit never reached a live run")
	}
	fx.exec.releaseSuccess(exitRun, "exit v1")
	c := <-fx.m.completions
	fx.m.handleCompletion(c)
	// Tamper the exit@1 result that the next inner dispatch will summarize as an
	// ancestor carry-over reference.
	execDir, err := fx.st.WorkflowDir("wf_000001")
	if err != nil {
		t.Fatalf("exec dir: %v", err)
	}
	abs := filepath.Join(execDir, "tasks", "exit", "attempts", "1", "response.txt")
	if err := os.WriteFile(abs, []byte("tampered content"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	// Reconciling advances the outer loop and tries to dispatch implement at
	// outer=2/inner=1, whose ancestor section references the damaged exit@1.
	fx.m.reconcile()

	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowNeedsAttention {
		t.Fatalf("damaged ancestor artifact must hold execution, got %v", view.State)
	}
	if !hasReason(view, "artifact:exit:1") {
		t.Fatalf("hold must name the damaged ancestor artifact: %v", view.AttentionReasons)
	}
	if n := len(loopSnapshot(t, fx).Tasks["implement"].Attempts); n != 2 {
		t.Fatalf("consumer must not be dispatched on an unverifiable ancestor input, implement has %d attempts", n)
	}
}

// --- review P3 #6: deterministic loop-wait barrier by lexicographic dep ID ----

func TestLoopWaitBarrierIsLexicographicByDependencyID(t *testing.T) {
	build := func(needs []string) domain.WorkflowDefinition {
		return domain.WorkflowDefinition{
			Version: 1, Name: "barrier", MaxParallel: 1, TaskTimeoutSeconds: 60,
			Loops: map[string]domain.WorkflowLoopDefinition{
				"Lz": {MaxIterations: 2},
				"La": {MaxIterations: 2},
			},
			Tasks: map[string]domain.WorkflowTaskDefinition{
				"ztask":  {Agent: "a1", Prompt: "z", Loop: "Lz"},
				"atask":  {Agent: "a2", Prompt: "a", Loop: "La"},
				"report": {Agent: "a3", Prompt: "r", Needs: needs},
			},
		}
	}
	for _, needs := range [][]string{{"ztask", "atask"}, {"atask", "ztask"}} {
		fx := newManagerFixture(t, build(needs), "a1", "a2", "a3")
		if _, _, err := fx.m.Create(context.Background(), "req-barrier"); err != nil {
			t.Fatalf("create: %v", err)
		}
		fx.pump()
		reason := findTaskView(mustView2(t, fx), "report").BlockedReason
		// Both barriers are unfinished; the reason must name the barrier of the
		// lexicographically first dependency (atask -> La) regardless of the
		// declaration order, and never the other one.
		if reason != "waiting_loop:La" {
			t.Fatalf("needs=%v: want deterministic barrier La, got %q", needs, reason)
		}
		if strings.Contains(reason, "Lz") {
			t.Fatalf("needs=%v: reason must not name Lz: %q", needs, reason)
		}
		defer finishLiveRuns(t, fx)
	}
}

// --- review P1 #1 secondary: three-level A=2/B=2/C=1 ancestor sections -------

// nestedThreeStaticDefinition is a full three-level static nesting: innermost
// runs inside middle, which runs inside outer. Each level's exit task consumes
// the descendant below it.
func nestedThreeStaticDefinition() domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		Version: 1, Name: "nested-three-static", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer":  {MaxIterations: 2},
			"middle": {MaxIterations: 2, Parent: "outer"},
			"inner":  {MaxIterations: 2, Parent: "middle"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A.", Loop: "inner"},
			"x": {Agent: "a2", Prompt: "X.", Loop: "middle", Needs: []string{"a"}},
			"o": {Agent: "a3", Prompt: "O.", Loop: "outer", Needs: []string{"x"}},
		},
	}
}

// attemptNumberForPath returns the attempt number of taskID whose iteration
// path renders to want, failing if none matches.
func attemptNumberForPath(t *testing.T, fx *managerFixture, taskID, want string) int {
	t.Helper()
	snapshot := loopSnapshot(t, fx)
	for _, attempt := range snapshot.Tasks[taskID].Attempts {
		if domain.RenderIterationPath(attempt.IterationPath) == want {
			return attempt.Attempt
		}
	}
	t.Fatalf("no %s attempt at path %q", taskID, want)
	return 0
}

// TestNestedThreeLevelAncestorCarryOverAtFirstInnerIteration proves that at
// outer=2/middle=2/inner=1 the innermost task receives BOTH enclosing-loop
// sections (middle and outer), each tied to its own complete path, while its own
// previous-iteration section stays absent.
func TestNestedThreeLevelAncestorCarryOverAtFirstInnerIteration(t *testing.T) {
	fx := newManagerFixture(t, nestedThreeStaticDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-three-ancestor"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	n := attemptNumberForPath(t, fx, "a", "outer=2/middle=2/inner=1")
	man := readAttemptManifest(t, fx, "a", n)
	if man.PreviousIterationPath != nil || len(man.PreviousIteration) != 0 {
		t.Fatalf("inner=1 must have no own previous-iteration section: %+v / %+v", man.PreviousIterationPath, man.PreviousIteration)
	}
	if len(man.AncestorPreviousIterations) != 2 {
		t.Fatalf("expected middle and outer sections, got %d", len(man.AncestorPreviousIterations))
	}
	// Nearest ancestor first: the middle section points at the prior middle
	// invocation under the CURRENT outer (outer=2/middle=1), not an outer=1 one.
	near := man.AncestorPreviousIterations[0]
	if got := domain.RenderIterationPath(near.IterationPath); got != "outer=2/middle=1" {
		t.Fatalf("nearest ancestor section path: %s", got)
	}
	// The farthest section is the prior outer invocation (outer=1).
	far := man.AncestorPreviousIterations[1]
	if got := domain.RenderIterationPath(far.IterationPath); got != "outer=1" {
		t.Fatalf("farthest ancestor section path: %s", got)
	}
	// The middle section carries the prior middle subtree (inner a and x) at
	// outer=2, distinguishing repeated local counters from the outer=1 pass.
	midByTask := map[string]domain.WorkflowDependencyInput{}
	for _, e := range near.Outcomes {
		midByTask[e.TaskID] = e
	}
	if e, ok := midByTask["a"]; !ok || domain.RenderIterationPath(e.IterationPath) != "outer=2/middle=1/inner=2" {
		t.Fatalf("middle section must carry outer=2/middle=1 inner=2 outcome: %+v", near.Outcomes)
	}
	if e, ok := midByTask["x"]; !ok || e.Status != string(domain.WorkflowAttemptSucceeded) {
		t.Fatalf("middle section must carry the prior middle exit outcome: %+v", near.Outcomes)
	}
	// The outer section carries the whole prior outer=1 subtree, including o.
	outerByTask := map[string]domain.WorkflowDependencyInput{}
	for _, e := range far.Outcomes {
		outerByTask[e.TaskID] = e
	}
	if e, ok := outerByTask["o"]; !ok || domain.RenderIterationPath(e.IterationPath) != "outer=1" {
		t.Fatalf("outer section must carry the outer=1 exit outcome: %+v", far.Outcomes)
	}
	if e, ok := outerByTask["a"]; !ok || domain.RenderIterationPath(e.IterationPath) != "outer=1/middle=2/inner=2" {
		t.Fatalf("outer section must carry the final outer=1 inner outcome: %+v", far.Outcomes)
	}
}

// --- review 3 #1: a delivered view must not alias the live loop path ----------

// loopViewPath returns the IterationPath a view exposes for one loop.
func loopViewPath(view domain.WorkflowExecutionView, name string) []domain.IterationEntry {
	for _, loop := range view.Loops {
		if loop.Name == name {
			return loop.IterationPath
		}
	}
	return nil
}

// TestNestedViewLoopPathIsImmutableAcrossAdvance proves the API snapshot no
// longer shares the loop's mutable backing array: an advance rewrites the live
// path in place, but a view delivered earlier keeps the path it was given.
func TestNestedViewLoopPathIsImmutableAcrossAdvance(t *testing.T) {
	fx := newManagerFixture(t, nestedStaticDefinition(2, 2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-view-immutable"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	view := mustView2(t, fx)
	innerPath := loopViewPath(view, "inner")
	if before := domain.RenderIterationPath(innerPath); before != "outer=1/inner=1" {
		t.Fatalf("initial inner view path: %s", before)
	}
	defer finishLiveRuns(t, fx)

	// Settle inner iteration 1 so the loop advances and rewrites its own path.
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "implement"), "implement v1")
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "review"), "review v1")
	fx.pump()

	if got := domain.RenderIterationPath(loopIterationPath(t, fx, "inner")); got != "outer=1/inner=2" {
		t.Fatalf("live inner path should have advanced: %s", got)
	}
	if got := domain.RenderIterationPath(innerPath); got != "outer=1/inner=1" {
		t.Fatalf("delivered view loop path mutated in place (data race): now %q", got)
	}
}

// --- review 3 #3: the own previous-iteration section names full context ------

// TestNestedPromptDistinguishesOwnAndAncestorContexts checks that at
// outer=2/inner=2 the prompt labels the owner's own previous invocation by its
// full path, clearly distinct from the ancestor outer=1 section.
func TestNestedPromptDistinguishesOwnAndAncestorContexts(t *testing.T) {
	fx := newManagerFixture(t, nestedStaticDefinition(2, 2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-prompt-context"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	prompt := readAttemptPrompt(t, fx, "implement", 4)
	for _, want := range []string{
		"Loop iteration outer=2/inner=2",
		"previous invocation outer=2/inner=1",
		"context outer=2/inner=1",
		"[enclosing context outer=1]",
		"context outer=1/inner=2",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	// The old ambiguous bare "iteration N" spelling must be gone for nested.
	if strings.Contains(prompt, "iteration 1 as follows") || strings.Contains(prompt, ", iteration 1, run ") {
		t.Fatalf("nested prompt must not use nonnested iteration-number labels:\n%s", prompt)
	}
}

// TestNonnestedPromptKeepsIterationLabels locks the byte-identical flat format.
func TestNonnestedPromptKeepsIterationLabels(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(2), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-flat-prompt"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if final := driveToTerminal(t, fx); final.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v (%v)", final.State, final.AttentionReasons)
	}
	prompt := readAttemptPrompt(t, fx, "implement", 2)
	if !strings.Contains(prompt, "Loop iteration 2; the loop body settled in iteration 1 as follows.") {
		t.Fatalf("nonnested prompt lost its iteration header:\n%s", prompt)
	}
	if !strings.Contains(prompt, ", iteration 1, run ") {
		t.Fatalf("nonnested entries must keep the iteration-number label:\n%s", prompt)
	}
	if strings.Contains(prompt, "context ") || strings.Contains(prompt, "invocation ") || strings.Contains(prompt, "[enclosing") {
		t.Fatalf("nonnested prompt must not gain nested context markers:\n%s", prompt)
	}
}
