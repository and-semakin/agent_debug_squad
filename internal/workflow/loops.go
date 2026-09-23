package workflow

import (
	"fmt"
	"sort"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// newLoopExecutions initializes one running loop execution per declared loop
// at iteration 1. Loopless definitions keep a nil map so snapshots marshal
// byte-identically to pre-loop executions. Nested definitions additionally
// stamp each execution with its complete root-to-owner iteration path so every
// later advance/attempt can carry an unambiguous context identity.
func newLoopExecutions(def domain.WorkflowDefinition) map[string]*domain.WorkflowLoopExecution {
	if len(def.Loops) == 0 {
		return nil
	}
	if def.HasNesting() {
		return initNestedLoopExecutions(def)
	}
	loops := make(map[string]*domain.WorkflowLoopExecution, len(def.Loops))
	for name := range def.Loops {
		loops[name] = &domain.WorkflowLoopExecution{Iteration: 1, State: domain.WorkflowLoopRunning}
	}
	return loops
}

func sortedLoopNames(loops map[string]domain.WorkflowLoopDefinition) []string {
	names := make([]string, 0, len(loops))
	for name := range loops {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// loopBodyTasks returns the sorted task IDs whose loop label names the loop.
func loopBodyTasks(def domain.WorkflowDefinition, loopName string) []string {
	var ids []string
	for taskID, task := range def.Tasks {
		if task.Loop == loopName {
			ids = append(ids, taskID)
		}
	}
	sort.Strings(ids)
	return ids
}

// currentIteration returns the 1-based iteration of the loop a task belongs
// to, or 0 for tasks outside every loop.
func currentIteration(snapshot *domain.WorkflowSnapshot, taskDef domain.WorkflowTaskDefinition) int {
	if taskDef.Loop == "" {
		return 0
	}
	if loop := snapshot.Loops[taskDef.Loop]; loop != nil {
		return loop.Iteration
	}
	return 0
}

// attemptBelongsToLoopIteration reports whether one attempt belongs to a loop's
// current invocation context. A nested execution identifies the context by the
// complete root-to-owner iteration path; a nonnested execution keeps matching
// the direct owner's local iteration counter so its selection is unchanged.
func attemptBelongsToLoopIteration(attempt *domain.WorkflowAttempt, loop *domain.WorkflowLoopExecution, nested bool) bool {
	if loop == nil || attempt == nil {
		return false
	}
	if nested {
		return domain.PathsEqual(attempt.IterationPath, loop.IterationPath)
	}
	return attempt.Iteration == loop.Iteration
}

// lastAttemptInIteration returns the newest attempt belonging to the given
// iteration regardless of state, or nil when the task has no attempt in that
// iteration.
func lastAttemptInIteration(task *domain.WorkflowTaskExecution, iteration int) *domain.WorkflowAttempt {
	for i := len(task.Attempts) - 1; i >= 0; i-- {
		if task.Attempts[i].Iteration == iteration {
			return &task.Attempts[i]
		}
	}
	return nil
}

// lastCommittedAttemptInIteration returns the newest committed (terminal,
// non-interrupted) attempt of the given local iteration. It is used only by the
// nonnested carry-over path, whose attempts are identified by iteration.
func lastCommittedAttemptInIteration(task *domain.WorkflowTaskExecution, iteration int) *domain.WorkflowAttempt {
	for i := len(task.Attempts) - 1; i >= 0; i-- {
		if task.Attempts[i].Iteration == iteration && task.Attempts[i].State.Committed() {
			return &task.Attempts[i]
		}
	}
	return nil
}

// lastAttemptInLoopContext returns the newest attempt of the task belonging to
// the loop's current invocation context, regardless of state.
func lastAttemptInLoopContext(task *domain.WorkflowTaskExecution, loop *domain.WorkflowLoopExecution, nested bool) *domain.WorkflowAttempt {
	if task == nil {
		return nil
	}
	for i := len(task.Attempts) - 1; i >= 0; i-- {
		if attemptBelongsToLoopIteration(&task.Attempts[i], loop, nested) {
			return &task.Attempts[i]
		}
	}
	return nil
}

// lastCommittedAttemptInLoopContext returns the newest committed (terminal,
// non-interrupted) attempt of the loop's current invocation context.
func lastCommittedAttemptInLoopContext(task *domain.WorkflowTaskExecution, loop *domain.WorkflowLoopExecution, nested bool) *domain.WorkflowAttempt {
	if task == nil {
		return nil
	}
	for i := len(task.Attempts) - 1; i >= 0; i-- {
		if attemptBelongsToLoopIteration(&task.Attempts[i], loop, nested) && task.Attempts[i].State.Committed() {
			return &task.Attempts[i]
		}
	}
	return nil
}

// conditionVerdict returns the verdict value recorded on the loop's condition
// task (until_task) latest committed attempt in the loop's current iteration.
// ok is false when the loop has no condition, the condition task has no
// committed current-iteration attempt, or that attempt carries no verdict.
func conditionVerdict(snapshot *domain.WorkflowSnapshot, loopName string) (string, bool) {
	loopDef := snapshot.Definition.Loops[loopName]
	if loopDef.UntilTask == "" {
		return "", false
	}
	loop := snapshot.Loops[loopName]
	if loop == nil {
		return "", false
	}
	task := snapshot.Tasks[loopDef.UntilTask]
	if task == nil {
		return "", false
	}
	attempt := lastCommittedAttemptInLoopContext(task, loop, snapshot.Definition.HasNesting())
	if attempt == nil || attempt.Verdict == nil {
		return "", false
	}
	return attempt.Verdict.Value, true
}

// loopReasonContext renders the durable identity segment of a loop reason. A
// nonnested execution keeps the legacy `name:iteration` spelling so its reason
// strings are byte-identical; a nested execution renders the loop's complete
// root-to-owner iteration path as a single canonical field.
func loopReasonContext(snapshot *domain.WorkflowSnapshot, loopName string) string {
	loop := snapshot.Loops[loopName]
	if snapshot.Definition.HasNesting() {
		var path []domain.IterationEntry
		if loop != nil {
			path = loop.IterationPath
		}
		return domain.RenderIterationPath(path)
	}
	iteration := 0
	if loop != nil {
		iteration = loop.Iteration
	}
	return fmt.Sprintf("%s:%d", loopName, iteration)
}

// loopHoldReasons derives the durable attention reasons of every unfinished
// loop from persisted state: failure/blocking holds first, then — for a
// conditioned loop whose current iteration has settled acceptably — the
// condition-action and exhaustion holds. It also updates each loop's observed
// state (running versus needs_attention). Because every reason is recomputed
// from the counter, cap, and settled verdicts, recovery re-derives holds with
// no extra persistence. Done loops never hold.
func loopHoldReasons(snapshot *domain.WorkflowSnapshot) []string {
	var reasons []string
	def := snapshot.Definition
	for _, loopName := range sortedLoopNames(def.Loops) {
		loop := snapshot.Loops[loopName]
		if loop == nil || loop.State == domain.WorkflowLoopDone {
			continue
		}
		ctx := loopReasonContext(snapshot, loopName)
		held := false
		for _, taskID := range loopBodyTasks(def, loopName) {
			task := snapshot.Tasks[taskID]
			if task == nil {
				continue
			}
			switch task.State {
			case domain.WorkflowTaskFailed:
				if !def.Tasks[taskID].AllowedToFail {
					reasons = append(reasons, fmt.Sprintf("loop_failure:%s:%s:retry_or_cancel", ctx, taskID))
					held = true
				}
			case domain.WorkflowTaskBlocked:
				reasons = append(reasons, fmt.Sprintf("loop_blocked:%s:%s:retry_or_cancel", ctx, taskID))
				held = true
			}
		}
		// Failure/blocking holds take precedence and prevent condition
		// evaluation: a half-settled or failed iteration never steers the loop.
		if !held && def.Loops[loopName].HasCondition() && iterationSettledAcceptably(snapshot, loopName) {
			if reason, hold := conditionHoldReason(snapshot, loopName); hold {
				reasons = append(reasons, reason)
				held = true
			}
		}
		if held {
			loop.State = domain.WorkflowLoopNeedsAttention
		} else {
			loop.State = domain.WorkflowLoopRunning
		}
	}
	return reasons
}

// conditionHoldReason decides whether a settled acceptably conditioned loop
// holds for intervention. A needs_attention action always holds; a continue
// action holds only at the effective cap under the default needs_attention
// exhaustion policy (succeed completes without holding). break and below-cap
// continue produce no hold so the advance pass can act on them.
func conditionHoldReason(snapshot *domain.WorkflowSnapshot, loopName string) (string, bool) {
	loop := snapshot.Loops[loopName]
	loopDef := snapshot.Definition.Loops[loopName]
	verdict, ok := conditionVerdict(snapshot, loopName)
	if !ok {
		return "", false
	}
	ctx := loopReasonContext(snapshot, loopName)
	switch loopDef.OnVerdict[verdict] {
	case domain.WorkflowLoopActionNeedsAttention:
		return fmt.Sprintf("loop_attention:%s:%s:%s:override_or_stop_or_cancel", ctx, loopDef.UntilTask, verdict), true
	case domain.WorkflowLoopActionContinue:
		if loop.Iteration >= loop.EffectiveCap(loopDef.MaxIterations) && loopDef.EffectiveOnExhaustion() != domain.WorkflowExhaustionSucceed {
			return fmt.Sprintf("loop_exhausted:%s:extend_or_stop_or_cancel", ctx), true
		}
	}
	return "", false
}

// isLoadBearingConditionVerdict reports whether the given settled attempt is
// the verdict currently holding a conditioned loop through a needs_attention
// action: taskID is that loop's until_task, the attempt belongs to the loop's
// current iteration, the iteration has settled acceptably, and the attempt's
// recorded verdict maps to needs_attention and is the value the loop is
// actually holding on. This is the narrow target the manual override may
// rewrite; prior-iteration and ordinary settled verdicts never qualify.
func isLoadBearingConditionVerdict(snapshot *domain.WorkflowSnapshot, taskID string, attempt *domain.WorkflowAttempt) bool {
	if attempt.State != domain.WorkflowAttemptSucceeded || attempt.Verdict == nil {
		return false
	}
	nested := snapshot.Definition.HasNesting()
	for _, loopName := range sortedLoopNames(snapshot.Definition.Loops) {
		loopDef := snapshot.Definition.Loops[loopName]
		if !loopDef.HasCondition() || loopDef.UntilTask != taskID {
			continue
		}
		if loopDef.OnVerdict[attempt.Verdict.Value] != domain.WorkflowLoopActionNeedsAttention {
			continue
		}
		loop := snapshot.Loops[loopName]
		if loop == nil || loop.State == domain.WorkflowLoopDone {
			continue
		}
		// History immutability: only the loop's current invocation is live.
		if !attemptBelongsToLoopIteration(attempt, loop, nested) {
			continue
		}
		if !iterationSettledAcceptably(snapshot, loopName) {
			continue
		}
		if current, ok := conditionVerdict(snapshot, loopName); ok && current == attempt.Verdict.Value {
			return true
		}
	}
	return false
}

// iterationSettledAcceptably reports whether the loop's current invocation has
// settled acceptably. Every directly owned body task must have its newest
// current-context attempt committed acceptably (succeeded, or a tolerated
// failure), and — in a nested execution — every immediate child loop invocation
// must already be done. In-flight, queued, judging, interrupted, and blocked
// work, and unfinished children, all keep the invocation unsettled.
func iterationSettledAcceptably(snapshot *domain.WorkflowSnapshot, loopName string) bool {
	def := snapshot.Definition
	loop := snapshot.Loops[loopName]
	if loop == nil {
		return false
	}
	nested := def.HasNesting()
	for _, taskID := range loopBodyTasks(def, loopName) {
		task := snapshot.Tasks[taskID]
		if task == nil {
			return false
		}
		last := lastAttemptInLoopContext(task, loop, nested)
		if last == nil || !last.State.Committed() {
			return false
		}
		if last.State == domain.WorkflowAttemptFailed && !def.Tasks[taskID].AllowedToFail {
			return false
		}
	}
	if nested {
		for _, child := range def.ChildLoops(loopName) {
			if childExec := snapshot.Loops[child]; childExec == nil || childExec.State != domain.WorkflowLoopDone {
				return false
			}
		}
	}
	return true
}

// resolveStopsLocked resolves pending manual-stop intents whose loop's current
// iteration has settled acceptably, marking those loops done at that iteration
// so their condition-action and exhaustion holds clear. It is a local control
// resolution: it never dispatches work, never advances another loop, and never
// waives unfinished classification, failure, or interruption — those keep the
// iteration unsettled so the stop stays pending. Callers run it before the
// execution-state refresh so a stop can release its own cause even while other
// loops remain held. Returns whether any loop state changed.
func resolveStopsLocked(snapshot *domain.WorkflowSnapshot) bool {
	changed := false
	def := snapshot.Definition
	// Child-first so a stopped descendant reaches done before an ancestor stop
	// is reconsidered; a parent only settles once its children are done.
	for _, loopName := range loopPostorder(def) {
		loop := snapshot.Loops[loopName]
		if loop == nil || loop.State == domain.WorkflowLoopDone || !loop.StopRequested {
			continue
		}
		if !iterationSettledAcceptably(snapshot, loopName) {
			continue
		}
		loop.State = domain.WorkflowLoopDone
		changed = true
	}
	return changed
}

// advanceLoopsLocked re-arms loops whose current iteration settled
// acceptably and completes loops at their final iteration. Callers run it
// only while the execution is attention-free and in running mode; each loop
// moves at most one iteration per pass. A static loop runs its effective
// budget (declared max_iterations plus any accepted extension) unchanged. A
// conditioned loop acts on until_task's settled verdict:
// break completes at the current iteration, continue re-arms below the
// effective cap, and a continue at the cap under the succeed exhaustion policy
// completes like a break. needs_attention actions and needs_attention
// exhaustion are derived as holds in loopHoldReasons, which keeps the gate
// closed, so they never reach this pass.
func (m *Manager) advanceLoopsLocked(snapshot *domain.WorkflowSnapshot) bool {
	advanced := false
	def := snapshot.Definition
	nested := def.HasNesting()
	// Postorder (children before parents) lets a static parent advance in the
	// same pass its final child completes, while a parent's own advance can
	// reinitialize the descendants it just reset.
	for _, loopName := range loopPostorder(def) {
		loop := snapshot.Loops[loopName]
		if loop == nil || loop.State == domain.WorkflowLoopDone {
			continue
		}
		if !iterationSettledAcceptably(snapshot, loopName) {
			continue
		}
		loopDef := def.Loops[loopName]
		if !loopDef.HasCondition() {
			// A static loop runs its declared budget plus any accepted manual
			// extension: the effective cap, not the original max_iterations,
			// gates the last iteration.
			if loop.Iteration < loop.EffectiveCap(loopDef.MaxIterations) {
				advanceLoopIterationLocked(snapshot, loop, loopName, nested)
			} else {
				loop.State = domain.WorkflowLoopDone
			}
			advanced = true
			continue
		}
		verdict, ok := conditionVerdict(snapshot, loopName)
		if !ok {
			continue
		}
		switch loopDef.OnVerdict[verdict] {
		case domain.WorkflowLoopActionBreak:
			loop.State = domain.WorkflowLoopDone
			advanced = true
		case domain.WorkflowLoopActionContinue:
			if loop.Iteration < loop.EffectiveCap(loopDef.MaxIterations) {
				advanceLoopIterationLocked(snapshot, loop, loopName, nested)
				advanced = true
			} else if loopDef.EffectiveOnExhaustion() == domain.WorkflowExhaustionSucceed {
				loop.State = domain.WorkflowLoopDone
				advanced = true
			}
		}
	}
	return advanced
}

// advanceLoopIterationLocked performs the Advance transition of one loop: it
// increments the local counter and, for a nested execution, updates the loop's
// current iteration path and reinitializes every descendant invocation fresh at
// local iteration 1 — clearing each descendant's invocation-local extension and
// stop intent while resetting its path. The advancing loop keeps its own
// invocation-local extension. All attempt and control history is preserved;
// reset descendants simply start new invocations under the advanced prefix. A
// nonnested advance is the original single-counter increment.
func advanceLoopIterationLocked(snapshot *domain.WorkflowSnapshot, loop *domain.WorkflowLoopExecution, loopName string, nested bool) {
	loop.Iteration++
	if !nested {
		return
	}
	if n := len(loop.IterationPath); n > 0 {
		loop.IterationPath[n-1].Iteration = loop.Iteration
	}
	def := snapshot.Definition
	for _, desc := range childLoopsUnderParentAdvance(def, loopName) {
		child := snapshot.Loops[desc]
		if child == nil {
			continue
		}
		child.Iteration = 1
		child.ExtendedIterations = 0
		child.StopRequested = false
		child.State = domain.WorkflowLoopRunning
		child.IterationPath = loopPathForIteration(snapshot.Loops, def, desc, 1)
	}
}

// dependencyAttempt resolves the attempt a consumer's dependency
// on depID references: same-loop body dependencies resolve to the loop's
// current iteration, everything else keeps single-attempt resolution.
func dependencyAttempt(snapshot *domain.WorkflowSnapshot, consumerTaskID, depID string) *domain.WorkflowAttempt {
	dep := snapshot.Tasks[depID]
	if dep == nil {
		return nil
	}
	def := snapshot.Definition
	if def.HasNesting() {
		return nestedDependencyAttempt(snapshot, consumerTaskID, depID)
	}
	consumerLoop := def.Tasks[consumerTaskID].Loop
	depLoop := def.Tasks[depID].Loop
	if depLoop != "" && depLoop == consumerLoop {
		if loop := snapshot.Loops[depLoop]; loop != nil {
			return lastCommittedAttemptInLoopContext(dep, loop, false)
		}
		return nil
	}
	return lastCommittedAttempt(dep)
}

// previousIterationInputs renders the carry-over section: for every body task
// of the consumer's loop, the latest committed attempt of the given prior
// iteration (including any successful retry), in sorted task ID order.
func previousIterationInputs(snapshot *domain.WorkflowSnapshot, loopName string, iteration int) []domain.WorkflowDependencyInput {
	var entries []domain.WorkflowDependencyInput
	for _, bodyID := range loopBodyTasks(snapshot.Definition, loopName) {
		body := snapshot.Tasks[bodyID]
		if body == nil {
			continue
		}
		attempt := lastCommittedAttemptInIteration(body, iteration)
		if attempt == nil {
			continue
		}
		entry := domain.WorkflowDependencyInput{
			TaskID:    bodyID,
			Agent:     body.Agent,
			Attempt:   attempt.Attempt,
			Iteration: attempt.Iteration,
			RunID:     attempt.RunID,
			Status:    string(attempt.State),
			Error:     attempt.Error,
			Verdict:   attempt.Verdict,
		}
		if attempt.State == domain.WorkflowAttemptSucceeded {
			entry.ResultPath = attempt.ResultPath
			entry.ResultSize = attempt.ResultSize
			entry.ResultSHA256 = attempt.ResultSHA256
		}
		entries = append(entries, entry)
	}
	return entries
}
