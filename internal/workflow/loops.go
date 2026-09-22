package workflow

import (
	"fmt"
	"sort"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// newLoopExecutions initializes one running loop execution per declared loop
// at iteration 1. Loopless definitions keep a nil map so snapshots marshal
// byte-identically to pre-loop executions.
func newLoopExecutions(def domain.WorkflowDefinition) map[string]*domain.WorkflowLoopExecution {
	if len(def.Loops) == 0 {
		return nil
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
// non-interrupted) attempt of the given iteration.
func lastCommittedAttemptInIteration(task *domain.WorkflowTaskExecution, iteration int) *domain.WorkflowAttempt {
	for i := len(task.Attempts) - 1; i >= 0; i-- {
		if task.Attempts[i].Iteration == iteration && task.Attempts[i].State.Committed() {
			return &task.Attempts[i]
		}
	}
	return nil
}

// loopHoldReasons derives the durable failure/blocking attention reasons of
// every unfinished loop from the current-iteration task states and updates
// each loop's observable state (running versus needs_attention). Reasons name
// the loop, iteration, and offending task, and point at retry-or-cancel
// intervention. Done loops never hold.
func loopHoldReasons(snapshot *domain.WorkflowSnapshot) []string {
	var reasons []string
	def := snapshot.Definition
	for _, loopName := range sortedLoopNames(def.Loops) {
		loop := snapshot.Loops[loopName]
		if loop == nil || loop.State == domain.WorkflowLoopDone {
			continue
		}
		held := false
		for _, taskID := range loopBodyTasks(def, loopName) {
			task := snapshot.Tasks[taskID]
			if task == nil {
				continue
			}
			switch task.State {
			case domain.WorkflowTaskFailed:
				if !def.Tasks[taskID].AllowedToFail {
					reasons = append(reasons, fmt.Sprintf("loop_failure:%s:%d:%s:retry_or_cancel", loopName, loop.Iteration, taskID))
					held = true
				}
			case domain.WorkflowTaskBlocked:
				reasons = append(reasons, fmt.Sprintf("loop_blocked:%s:%d:%s:retry_or_cancel", loopName, loop.Iteration, taskID))
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

// iterationSettledAcceptably reports whether every body task of the loop has
// its newest current-iteration attempt committed acceptably (succeeded, or a
// tolerated failure). In-flight, queued, judging, interrupted, and blocked
// work all keep the iteration unsettled.
func iterationSettledAcceptably(snapshot *domain.WorkflowSnapshot, loopName string) bool {
	def := snapshot.Definition
	loop := snapshot.Loops[loopName]
	if loop == nil {
		return false
	}
	for _, taskID := range loopBodyTasks(def, loopName) {
		task := snapshot.Tasks[taskID]
		if task == nil {
			return false
		}
		last := lastAttemptInIteration(task, loop.Iteration)
		if last == nil || !last.State.Committed() {
			return false
		}
		if last.State == domain.WorkflowAttemptFailed && !def.Tasks[taskID].AllowedToFail {
			return false
		}
	}
	return true
}

// advanceLoopsLocked re-arms loops whose current iteration settled
// acceptably and completes loops at their final iteration. Callers run it
// only while the execution is attention-free and in running mode; each loop
// moves at most one iteration per pass.
func (m *Manager) advanceLoopsLocked(snapshot *domain.WorkflowSnapshot) bool {
	advanced := false
	def := snapshot.Definition
	for _, loopName := range sortedLoopNames(def.Loops) {
		loop := snapshot.Loops[loopName]
		if loop == nil || loop.State == domain.WorkflowLoopDone {
			continue
		}
		if !iterationSettledAcceptably(snapshot, loopName) {
			continue
		}
		if loop.Iteration < def.Loops[loopName].MaxIterations {
			loop.Iteration++
		} else {
			loop.State = domain.WorkflowLoopDone
		}
		advanced = true
	}
	return advanced
}

// dependencyAttempt resolves the attempt a consumer's dependency
// on depID references: same-loop body dependencies resolve to the loop's
// current iteration, everything else keeps single-attempt resolution.
func dependencyAttempt(snapshot *domain.WorkflowSnapshot, consumerTaskID, depID string) *domain.WorkflowAttempt {
	dep := snapshot.Tasks[depID]
	if dep == nil {
		return nil
	}
	consumerLoop := snapshot.Definition.Tasks[consumerTaskID].Loop
	depLoop := snapshot.Definition.Tasks[depID].Loop
	if depLoop != "" && depLoop == consumerLoop {
		if loop := snapshot.Loops[depLoop]; loop != nil {
			return lastCommittedAttemptInIteration(dep, loop.Iteration)
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
