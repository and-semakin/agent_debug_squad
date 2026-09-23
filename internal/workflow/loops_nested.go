package workflow

import (
	"fmt"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// This file holds the nested-loop identity layer: schema selection, iteration
// path construction, and the recursive initialization of loop executions with
// complete root-to-owner paths. Every function here is a pure derivation over
// the definition and current loop state, so nested behavior stays isolated from
// the flat scheduling core. Definitions without nesting (HasNesting false) keep
// their existing schema-2 representation and never carry iteration paths.

// schemaVersionForDefinition selects the persisted snapshot layout for a new
// execution: schema 3 only when the definition declares a loop parent, schema 2
// for every nonnested execution (including recovered schema-1 state) so their
// wire format is unchanged.
func schemaVersionForDefinition(def domain.WorkflowDefinition) int {
	if def.HasNesting() {
		return domain.WorkflowSnapshotNestedSchemaVersion
	}
	return domain.WorkflowSnapshotNonNestedSchemaVersion
}

// loopPathForIteration renders the complete root-to-owner path of one loop
// whose ancestors already carry their current paths in executions. The parent
// prefix is read from each ancestor loop execution; the loop's own local
// iteration is appended. A root loop yields a one-entry path.
func loopPathForIteration(executions map[string]*domain.WorkflowLoopExecution, def domain.WorkflowDefinition, loopName string, iteration int) []domain.IterationEntry {
	var prefix []domain.IterationEntry
	if parent := def.Loops[loopName].Parent; parent != "" {
		if pe := executions[parent]; pe != nil {
			prefix = append(prefix, pe.IterationPath...)
		}
	}
	prefix = append(prefix, domain.IterationEntry{Loop: loopName, Iteration: iteration})
	return prefix
}

// initNestedLoopExecutions builds one running execution record per declared loop
// for a nested definition, each stamped with its complete root-to-owner path at
// iteration 1. Roots are initialized first so children can read their parent
// prefix; the ordering is by loop ancestry depth via repeated passes over sorted
// names, which is cycle-free because validation guarantees a forest.
func initNestedLoopExecutions(def domain.WorkflowDefinition) map[string]*domain.WorkflowLoopExecution {
	loops := make(map[string]*domain.WorkflowLoopExecution, len(def.Loops))
	for _, name := range sortedLoopNames(def.Loops) {
		loops[name] = &domain.WorkflowLoopExecution{Iteration: 1, State: domain.WorkflowLoopRunning}
	}
	// Assign paths in dependency order: a loop's path needs its parent's path,
	// so iterate until every loop is resolved (at most depth+1 passes).
	resolved := map[string]bool{}
	for progress := true; progress; {
		progress = false
		for _, name := range sortedLoopNames(def.Loops) {
			if resolved[name] {
				continue
			}
			parent := def.Loops[name].Parent
			if parent != "" && !resolved[parent] {
				continue
			}
			loops[name].IterationPath = loopPathForIteration(loops, def, name, loops[name].Iteration)
			resolved[name] = true
			progress = true
		}
	}
	return loops
}

// childLoopsUnderParentAdvance returns the descendant loop names of loopName
// (excluding loopName) in deterministic sorted order, used when an advance must
// recursively reinitialize fresh child invocations.
func childLoopsUnderParentAdvance(def domain.WorkflowDefinition, loopName string) []string {
	var out []string
	for _, child := range def.ChildLoops(loopName) {
		out = append(out, child)
		out = append(out, childLoopsUnderParentAdvance(def, child)...)
	}
	return out
}

// loopPostorder returns every declared loop with children ordered before their
// parent and siblings sorted by name, so a reconciliation pass settles a
// descendant invocation before reconsidering an ancestor. For a nonnested
// definition this is simply the sorted loop names.
func loopPostorder(def domain.WorkflowDefinition) []string {
	var order []string
	var visit func(name string)
	visited := map[string]bool{}
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		for _, child := range def.ChildLoops(name) {
			visit(child)
		}
		order = append(order, name)
	}
	for _, root := range def.RootLoops() {
		visit(root)
	}
	// Defensive: a malformed graph should never reach here, but include any
	// loop not already visited so no loop state is silently skipped.
	for _, name := range sortedLoopNames(def.Loops) {
		if !visited[name] {
			order = append(order, name)
		}
	}
	return order
}

// lastCommittedAttemptUnderPrefix returns the newest committed attempt whose
// iteration path extends prefix, or nil. It selects the final outcome of a
// task's whole sub-invocation tree beneath a given ancestor context.
func lastCommittedAttemptUnderPrefix(task *domain.WorkflowTaskExecution, prefix []domain.IterationEntry) *domain.WorkflowAttempt {
	if task == nil {
		return nil
	}
	for i := len(task.Attempts) - 1; i >= 0; i-- {
		attempt := &task.Attempts[i]
		if attempt.State.Committed() && domain.IsPathPrefix(prefix, attempt.IterationPath) {
			return attempt
		}
	}
	return nil
}

// loopCurrentPath returns a copy of the current root-to-owner iteration path of
// a loop, or nil for an unknown or nonnested loop. The result is always a fresh
// slice so a stamped attempt, manifest, or audit record never aliases the live
// loop execution — whose path entries an advance mutates in place.
func loopCurrentPath(snapshot *domain.WorkflowSnapshot, loopName string) []domain.IterationEntry {
	loop := snapshot.Loops[loopName]
	if loop == nil || loop.IterationPath == nil {
		return nil
	}
	out := make([]domain.IterationEntry, len(loop.IterationPath))
	copy(out, loop.IterationPath)
	return out
}

// rootLoopOf returns the root ancestor of a loop (its own name when it is a
// root), or "" for an unknown loop.
func rootLoopOf(def domain.WorkflowDefinition, loopName string) string {
	ancestry := def.LoopAncestry(loopName)
	if len(ancestry) == 0 {
		return ""
	}
	return ancestry[0]
}

// nestedDependencyAttempt resolves the attempt a nested consumer references for
// dependency depID using the single context table: an outside-all-loops
// producer keeps single-attempt resolution; a same-owner or ancestor-owner
// producer resolves at the producer's current invocation path (which equals the
// consumer path truncated to that owner); a descendant producer resolves to the
// final committed outcome inside the consumer's current iteration; and a
// workflow-scope consumer of a loop producer resolves to the final outcome under
// the producer's root-ancestor invocation.
func nestedDependencyAttempt(snapshot *domain.WorkflowSnapshot, consumerTaskID, depID string) *domain.WorkflowAttempt {
	def := snapshot.Definition
	dep := snapshot.Tasks[depID]
	if dep == nil {
		return nil
	}
	consumerLoop := def.Tasks[consumerTaskID].Loop
	depLoop := def.Tasks[depID].Loop
	switch {
	case depLoop == "":
		return lastCommittedAttempt(dep)
	case consumerLoop != "" && depLoop == consumerLoop:
		return lastCommittedAttemptInLoopContext(dep, snapshot.Loops[depLoop], true)
	case consumerLoop != "" && def.LoopContains(consumerLoop, depLoop):
		// depLoop is a strict descendant of the consumer's owner: take the
		// final outcome inside the consumer's current iteration.
		return lastCommittedAttemptUnderPrefix(dep, loopCurrentPath(snapshot, consumerLoop))
	case consumerLoop != "" && def.LoopContains(depLoop, consumerLoop):
		// depLoop is a strict ancestor: its current invocation is the consumer
		// path truncated to that owner, so the producer's current-context
		// committed attempt is the stable ancestor input.
		return lastCommittedAttemptInLoopContext(dep, snapshot.Loops[depLoop], true)
	case consumerLoop == "":
		// Workflow-scope consumer of a loop producer: the barrier is the
		// producer's root ancestor; resolve its final outcome once done.
		root := rootLoopOf(def, depLoop)
		if root == "" {
			return lastCommittedAttempt(dep)
		}
		return lastCommittedAttemptUnderPrefix(dep, loopCurrentPath(snapshot, root))
	default:
		// Unrelated branches cannot share a direct dependency edge (validation
		// rejects it), so fall back to the producer's latest committed outcome.
		return lastCommittedAttempt(dep)
	}
}

// nestedPreviousIterationOutcomes summarizes one loop invocation's whole-subtree
// final committed outcomes, given the complete path of the summarized context
// (the owner path or a truncated ancestor path with its decremented counter).
func nestedPreviousIterationOutcomes(snapshot *domain.WorkflowSnapshot, loopName string, summarizedPath []domain.IterationEntry) []domain.WorkflowDependencyInput {
	var entries []domain.WorkflowDependencyInput
	for _, taskID := range snapshot.Definition.LoopSubtreeTasks(loopName) {
		task := snapshot.Tasks[taskID]
		if task == nil {
			continue
		}
		attempt := lastCommittedAttemptUnderPrefix(task, summarizedPath)
		if attempt == nil {
			continue
		}
		entries = append(entries, dependencyInputFor(task, attempt))
	}
	return entries
}

// dependencyInputFor renders one carry-over/dependency outcome entry, including
// the nested iteration path and a verified result reference only for a
// succeeded attempt.
func dependencyInputFor(task *domain.WorkflowTaskExecution, attempt *domain.WorkflowAttempt) domain.WorkflowDependencyInput {
	entry := domain.WorkflowDependencyInput{
		TaskID:        task.TaskID,
		Agent:         task.Agent,
		Attempt:       attempt.Attempt,
		Iteration:     attempt.Iteration,
		IterationPath: attempt.IterationPath,
		RunID:         attempt.RunID,
		Status:        string(attempt.State),
		Error:         attempt.Error,
		Verdict:       attempt.Verdict,
	}
	if attempt.State == domain.WorkflowAttemptSucceeded {
		entry.ResultPath = attempt.ResultPath
		entry.ResultSize = attempt.ResultSize
		entry.ResultSHA256 = attempt.ResultSHA256
	}
	return entry
}

// decrementPath returns a copy of path with its final entry's counter reduced by
// one. The caller must only call it when that counter exceeds one.
func decrementPath(path []domain.IterationEntry) []domain.IterationEntry {
	out := make([]domain.IterationEntry, len(path))
	copy(out, path)
	out[len(out)-1] = domain.IterationEntry{Loop: out[len(out)-1].Loop, Iteration: out[len(out)-1].Iteration - 1}
	return out
}

// barrierLoopFor returns the loop whose completion gates a nested consumer that
// depends on a producer in another loop: the immediate child of the consumer's
// scope on the path to the producer when the producer sits in a descendant, or
// the producer's root ancestor when the consumer is workflow-scope. It returns
// "" when no cross-scope barrier applies.
func barrierLoopFor(def domain.WorkflowDefinition, consumerLoop, depLoop string) string {
	if depLoop == "" {
		return ""
	}
	if consumerLoop == "" {
		return rootLoopOf(def, depLoop)
	}
	if def.LoopContains(consumerLoop, depLoop) {
		// depLoop is a strict descendant: the barrier is the immediate child of
		// consumerLoop on the path to depLoop.
		ancestry := def.LoopAncestry(depLoop)
		for i, name := range ancestry {
			if name == consumerLoop && i+1 < len(ancestry) {
				return ancestry[i+1]
			}
		}
	}
	return ""
}

// waitLoopReason renders the loop-wait attention reason. A nonnested execution
// keeps the legacy `waiting_loop:<name>` spelling; a nested execution names the
// actual completion barrier by its current iteration path.
func waitLoopReason(snapshot *domain.WorkflowSnapshot, barrierLoop string) string {
	if snapshot.Definition.HasNesting() {
		return "waiting_loop:" + domain.RenderIterationPath(loopCurrentPath(snapshot, barrierLoop))
	}
	return "waiting_loop:" + barrierLoop
}

// nestedAncestorPreviousIterations walks the consumer's enclosing loops from
// nearest to farthest and summarizes each ancestor whose current local counter
// exceeds one: the previous iteration's whole-subtree final outcomes under the
// ancestor prefix with only that level's counter decremented.
func nestedAncestorPreviousIterations(snapshot *domain.WorkflowSnapshot, ownerLoop string) []domain.WorkflowAncestorIteration {
	def := snapshot.Definition
	ancestry := def.LoopAncestry(ownerLoop)
	var sections []domain.WorkflowAncestorIteration
	for i := len(ancestry) - 2; i >= 0; i-- { // skip the owner at the last index
		ancestor := ancestry[i]
		loop := snapshot.Loops[ancestor]
		if loop == nil || loop.Iteration <= 1 || len(loop.IterationPath) != i+1 {
			continue
		}
		summarized := decrementPath(loop.IterationPath)
		outcomes := nestedPreviousIterationOutcomes(snapshot, ancestor, summarized)
		if len(outcomes) == 0 {
			continue
		}
		sections = append(sections, domain.WorkflowAncestorIteration{IterationPath: summarized, Outcomes: outcomes})
	}
	return sections
}

// assertNoDescendantAttemptsNested scopes a retry's transitive-descendant
// reservation guard by the shared enclosing iteration (design section 5). For
// each dependency descendant that already has any attempt (every recorded
// attempt counts as a reservation regardless of state), find the deepest loop
// common to the producer's and the descendant's owner chains. A reservation
// blocks the retry when its path prefix through that common loop equals the
// producer's current prefix through the same loop; reservations under a
// different shared enclosing iteration are history and never block. When the
// producer or descendant sits outside all loops, or their chains share no loop,
// the guard falls back to blocking on any reservation, matching the workflow-
// scope whole-history rule.
func assertNoDescendantAttemptsNested(snapshot *domain.WorkflowSnapshot, taskID string) error {
	def := snapshot.Definition
	producerLoop := def.Tasks[taskID].Loop
	producerPath := loopCurrentPath(snapshot, producerLoop)
	descendants := map[string]bool{}
	var mark func(string)
	mark = func(current string) {
		for otherID, other := range def.Tasks {
			if descendants[otherID] {
				continue
			}
			for _, dep := range other.Needs {
				if dep == current || descendants[dep] {
					descendants[otherID] = true
					mark(otherID)
					break
				}
			}
		}
	}
	mark(taskID)
	for _, descendant := range sortedTaskIDs(def.Tasks) {
		if !descendants[descendant] {
			continue
		}
		task := snapshot.Tasks[descendant]
		if task == nil || len(task.Attempts) == 0 {
			continue
		}
		descLoop := def.Tasks[descendant].Loop
		common := def.CommonAncestorLoop(producerLoop, descLoop)
		if common == "" {
			return fmt.Errorf("descendant %s already has attempts", descendant)
		}
		depth := len(def.LoopAncestry(common))
		if depth > len(producerPath) {
			// The producer's current path cannot be shorter than a common
			// ancestor; treat the inconsistency as a conservative block.
			return fmt.Errorf("descendant %s already has attempts", descendant)
		}
		producerPrefix := producerPath[:depth]
		for i := range task.Attempts {
			path := task.Attempts[i].IterationPath
			if len(path) < depth {
				continue
			}
			if domain.PathsEqual(path[:depth], producerPrefix) {
				return fmt.Errorf("descendant %s already has an attempt in the enclosing iteration of loop %s", descendant, common)
			}
		}
	}
	return nil
}
