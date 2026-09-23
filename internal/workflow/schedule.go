package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// reconcile is the serialized heart of the scheduler: every decision happens
// under the manager lock, against the persisted snapshot.
func (m *Manager) reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconcileLocked()
}

// enforceTimeoutsLocked cancels attempts past their wall-clock budget. Cleanup
// confirmation arrives as a completion; if it never arrives within the cancel
// grace period, the attempt is marked interrupted and holds the workflow.
func (m *Manager) enforceTimeoutsLocked(snapshot *domain.WorkflowSnapshot) bool {
	now := m.now()
	changed := false
	for runID, live := range m.active.live {
		attempt := findAttempt(snapshot, live.taskID, live.attempt)
		if attempt == nil {
			continue
		}
		if live.cancelRequestedAt.IsZero() {
			if now.Sub(live.dispatchedAt) < live.timeout {
				continue
			}
			if m.exec.CancelOwnedRun(runID) {
				live.cancelRequestedAt = now
				attempt.State = domain.WorkflowAttemptCancelling
				attempt.Reason = "timeout"
				requested := now
				attempt.CancelRequestedAt = &requested
				changed = true
			}
			continue
		}
		if now.Sub(live.cancelRequestedAt) > m.cancelGrace {
			// Cleanup cannot be confirmed in this process: treat the attempt
			// as uncertain and stop scheduling.
			delete(m.active.live, runID)
			attempt.State = domain.WorkflowAttemptInterrupted
			attempt.Reason = "timeout_cleanup_unconfirmed"
			completed := now
			attempt.CompletedAt = &completed
			live.confirmedUncertain = true
			changed = true
		}
	}
	return changed
}

// applyCancellingLocked advances a cancelling execution: unstarted tasks
// become cancelled, and once no live workers remain the execution can finish.
func (m *Manager) applyCancellingLocked(snapshot *domain.WorkflowSnapshot) bool {
	if snapshot.Mode != domain.WorkflowModeCancelling {
		return false
	}
	changed := false
	now := m.now()
	for _, task := range snapshot.Tasks {
		switch task.State {
		case domain.WorkflowTaskPending, domain.WorkflowTaskReady, domain.WorkflowTaskBlocked, domain.WorkflowTaskDispatching, domain.WorkflowTaskRunning:
			if _, live := m.liveForTaskLocked(task.TaskID); !live {
				task.State = domain.WorkflowTaskCancelled
				task.BlockedReason = ""
				changed = true
			}
		}
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			switch attempt.State {
			case domain.WorkflowAttemptQueued, domain.WorkflowAttemptJudging:
				// Judging attempts have no live worker; a late
				// classification result is dropped by the state guard.
				attempt.State = domain.WorkflowAttemptCancelled
				attempt.Reason = "cancelled"
				completed := now
				attempt.CompletedAt = &completed
				changed = true
			}
		}
	}
	if len(m.active.live) == 0 && snapshot.State != domain.WorkflowCancelled {
		unconfirmed := false
		for _, task := range snapshot.Tasks {
			for i := range task.Attempts {
				attempt := &task.Attempts[i]
				if attempt.State == domain.WorkflowAttemptInterrupted && attempt.CleanupConfirmedAt == nil {
					unconfirmed = true
				}
			}
		}
		if !unconfirmed {
			// Interrupted attempts with a recorded cleanup assertion close as
			// cancelled; without one the execution stays cancelling and
			// observable.
			for _, task := range snapshot.Tasks {
				for i := range task.Attempts {
					attempt := &task.Attempts[i]
					if attempt.State == domain.WorkflowAttemptInterrupted && attempt.CleanupConfirmedAt != nil {
						attempt.State = domain.WorkflowAttemptCancelled
						attempt.Reason = "cancelled_cleanup_confirmed"
						changed = true
					}
				}
				if task.State == domain.WorkflowTaskInterrupted {
					task.State = domain.WorkflowTaskCancelled
					changed = true
				}
			}
			snapshot.State = domain.WorkflowCancelled
			changed = true
		}
	}
	return changed
}

func (m *Manager) liveForTaskLocked(taskID string) (*liveAttempt, bool) {
	for _, live := range m.active.live {
		if live.taskID == taskID {
			return live, true
		}
	}
	return nil, false
}

// dependencyAcceptable reports whether a settled dependency may hand off to
// its consumer. Cancellation and interruption are never waivable.
func dependencyAcceptable(dep *domain.WorkflowTaskExecution, def domain.WorkflowTaskDefinition) bool {
	switch dep.State {
	case domain.WorkflowTaskSucceeded:
		return true
	case domain.WorkflowTaskFailed:
		return def.AllowedToFail
	default:
		return false
	}
}

// recomputeTaskStatesLocked evaluates readiness and blocked propagation in
// topological order; the definition is already cycle-free.
func (m *Manager) recomputeTaskStatesLocked(snapshot *domain.WorkflowSnapshot) bool {
	changed := false
	def := snapshot.Definition
	nested := def.HasNesting()
	for _, taskID := range topologicalOrder(def) {
		task := snapshot.Tasks[taskID]
		if task == nil {
			continue
		}
		if task.State == domain.WorkflowTaskCancelled {
			// Cancellation is terminal for a task that never produced a
			// committed outcome; dependency reevaluation must not resurrect it.
			continue
		}
		if _, live := m.liveForTaskLocked(taskID); live {
			continue
		}
		// For a loop body task "the last attempt" means the last attempt of the
		// loop's current invocation context (a complete iteration path when
		// nested, the local iteration otherwise); with none yet, the task
		// re-evaluates from dependencies below. Loopless tasks keep whole-history
		// scope.
		taskDef := def.Tasks[taskID]
		var last *domain.WorkflowAttempt
		if taskDef.Loop != "" {
			last = lastAttemptInLoopContext(task, snapshot.Loops[taskDef.Loop], nested)
		} else {
			last = task.LastAttempt()
		}
		if last != nil {
			switch last.State {
			case domain.WorkflowAttemptRunning, domain.WorkflowAttemptDispatching, domain.WorkflowAttemptCancelling, domain.WorkflowAttemptJudging:
				task.State = map[domain.WorkflowAttemptState]domain.WorkflowTaskState{
					domain.WorkflowAttemptRunning:     domain.WorkflowTaskRunning,
					domain.WorkflowAttemptDispatching: domain.WorkflowTaskDispatching,
					domain.WorkflowAttemptCancelling:  domain.WorkflowTaskRunning,
					domain.WorkflowAttemptJudging:     domain.WorkflowTaskRunning,
				}[last.State]
				continue
			case domain.WorkflowAttemptSucceeded:
				if task.State != domain.WorkflowTaskSucceeded {
					task.State = domain.WorkflowTaskSucceeded
					task.BlockedReason = ""
					changed = true
				}
				continue
			case domain.WorkflowAttemptFailed:
				if task.State != domain.WorkflowTaskFailed {
					task.State = domain.WorkflowTaskFailed
					task.BlockedReason = ""
					changed = true
				}
				continue
			case domain.WorkflowAttemptInterrupted:
				if task.State != domain.WorkflowTaskInterrupted {
					task.State = domain.WorkflowTaskInterrupted
					task.BlockedReason = ""
					changed = true
				}
				continue
			case domain.WorkflowAttemptCancelled:
				if task.State != domain.WorkflowTaskCancelled {
					task.State = domain.WorkflowTaskCancelled
					changed = true
				}
				continue
			case domain.WorkflowAttemptQueued:
				// A retry is queued: fall through to dependency evaluation.
			}
		}

		// No live or committed attempt in scope: evaluate dependencies.
		depDef := taskDef
		allSettled := true
		acceptable := true
		blockedBy := ""
		waitingLoop := ""
		successes := 0
		for _, depID := range sortedDependencyIDs(depDef.Needs) {
			dep := snapshot.Tasks[depID]
			if dep == nil {
				continue
			}
			depTaskDef := def.Tasks[depID]
			if depTaskDef.Loop != "" && depTaskDef.Loop != taskDef.Loop {
				// A cross-scope dependency where the producer sits in a
				// descendant loop (or in any loop for a workflow-scope
				// consumer) waits for the actual completion barrier to finish;
				// an ancestor-loop producer is stable within the consumer's
				// current invocation and needs no barrier wait. For a
				// nonnested definition the barrier is the producer's own loop,
				// reproducing the legacy loop-wait gate exactly.
				barrier := depTaskDef.Loop
				if nested {
					barrier = barrierLoopFor(def, taskDef.Loop, depTaskDef.Loop)
				}
				if barrier != "" {
					if loop := snapshot.Loops[barrier]; loop != nil && loop.State != domain.WorkflowLoopDone {
						allSettled = false
						if waitingLoop == "" {
							waitingLoop = waitLoopReason(snapshot, barrier)
						}
						continue
					}
				}
			}
			if dep.State == domain.WorkflowTaskInterrupted {
				// Uncertain dependency: the consumer stays pending until
				// intervention resolves it; the execution is already held.
				allSettled = false
				continue
			}
			if !dep.State.Settled() {
				allSettled = false
				continue
			}
			if !dependencyAcceptable(dep, depTaskDef) {
				acceptable = false
				if blockedBy == "" {
					blockedBy = fmt.Sprintf("dependency_%s:%s", dep.State, depID)
				}
			}
			if dep.State == domain.WorkflowTaskSucceeded {
				successes++
			}
		}
		next := domain.WorkflowTaskPending
		reason := ""
		switch {
		case !allSettled:
			next = domain.WorkflowTaskPending
			reason = waitingLoop
		case !acceptable:
			next = domain.WorkflowTaskBlocked
			reason = blockedBy
		case successes < depDef.MinSuccessfulDependencies:
			next = domain.WorkflowTaskBlocked
			reason = fmt.Sprintf("insufficient_successful_dependencies:%d_of_%d", successes, depDef.MinSuccessfulDependencies)
		default:
			next = domain.WorkflowTaskReady
		}
		if task.State != next || task.BlockedReason != reason {
			task.State = next
			task.BlockedReason = reason
			changed = true
		}
	}
	return changed
}

// dispatchReadyTasksLocked reserves attempts and sends them to the executor,
// in lexicographic task order, while parallel slots remain.
func (m *Manager) dispatchReadyTasksLocked(snapshot *domain.WorkflowSnapshot) bool {
	changed := false
	for {
		if len(m.active.live) >= snapshot.Definition.MaxParallel {
			break
		}
		target := ""
		for _, taskID := range sortedTaskIDs(snapshot.Definition.Tasks) {
			task := snapshot.Tasks[taskID]
			if task == nil || task.State != domain.WorkflowTaskReady {
				continue
			}
			target = taskID
			break
		}
		if target == "" {
			break
		}
		if !m.dispatchTaskLocked(snapshot, target) {
			break
		}
		changed = true
	}
	return changed
}

// savedAgentSpec resolves the agent configuration an execution persisted at
// submission time. A snapshot without saved agents (for example one written
// before agent retention existed) falls back to the live configuration.
func (m *Manager) savedAgentSpec(snapshot *domain.WorkflowSnapshot, agentName string) *domain.AgentSpec {
	if spec, ok := snapshot.Agents[agentName]; ok {
		return &spec
	}
	for _, candidate := range m.cfg.Agents {
		if candidate.Name == agentName {
			spec := candidate
			return &spec
		}
	}
	return nil
}

func (m *Manager) dispatchTaskLocked(snapshot *domain.WorkflowSnapshot, taskID string) bool {
	task := snapshot.Tasks[taskID]
	def := snapshot.Definition.Tasks[taskID]

	// Verify every committed dependency and carry-over result before
	// dispatch. The manifest is the authoritative input list, so artifact
	// verification covers exactly what the attempt will read, including every
	// enclosing loop's ancestor carry-over section.
	manifest := buildManifest(snapshot, taskID)
	inputs := append(append([]domain.WorkflowDependencyInput{}, manifest.Dependencies...), manifest.PreviousIteration...)
	for _, section := range manifest.AncestorPreviousIterations {
		inputs = append(inputs, section.Outcomes...)
	}
	for _, entry := range inputs {
		if entry.Status != string(domain.WorkflowAttemptSucceeded) || entry.ResultPath == "" {
			continue
		}
		if err := m.store.VerifyWorkflowArtifact(snapshot.ExecutionID, entry.ResultPath, entry.ResultSize, entry.ResultSHA256); err != nil {
			snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, fmt.Sprintf("artifact:%s:%d", entry.TaskID, entry.Attempt))
			m.setStorageErrorLocked(fmt.Errorf("verify dependency %s: %w", entry.TaskID, err))
			return false
		}
	}

	// A retry reserves its attempt record as queued before dispatch; a fresh
	// dispatch appends a new one, stamped with the loop's current iteration.
	var attemptSlot *domain.WorkflowAttempt
	if last := task.LastAttempt(); last != nil && last.State == domain.WorkflowAttemptQueued {
		attemptSlot = last
	} else {
		newAttempt := domain.WorkflowAttempt{
			Attempt:   len(task.Attempts) + 1,
			Iteration: currentIteration(snapshot, def),
		}
		if snapshot.Definition.HasNesting() && def.Loop != "" {
			newAttempt.IterationPath = loopCurrentPath(snapshot, def.Loop)
		}
		task.Attempts = append(task.Attempts, newAttempt)
		attemptSlot = &task.Attempts[len(task.Attempts)-1]
	}
	attemptNumber := attemptSlot.Attempt
	manifest.Attempt = attemptNumber
	runID := fmt.Sprintf("wrun_%s_%06d", executionNumber(snapshot.ExecutionID), snapshot.NextRunSeq+1)
	prompt := m.buildPromptLocked(snapshot, taskID, manifest)

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		m.setStorageErrorLocked(err)
		return false
	}
	promptPath, manifestPath, err := m.store.WriteWorkflowAttemptInput(snapshot.ExecutionID, taskID, attemptNumber, []byte(prompt), manifestJSON)
	if err != nil {
		m.setStorageErrorLocked(fmt.Errorf("persist attempt input: %w", err))
		return false
	}

	statePath, err := m.store.WorkflowAttemptStatePath(snapshot.ExecutionID, taskID, attemptNumber)
	if err != nil {
		m.setStorageErrorLocked(err)
		return false
	}

	now := m.now()
	attemptSlot.RunID = runID
	attemptSlot.State = domain.WorkflowAttemptDispatching
	attemptSlot.Reason = ""
	attemptSlot.PromptPath = promptPath
	attemptSlot.ManifestPath = manifestPath
	if attemptSlot.ReservedAt == nil {
		attemptSlot.ReservedAt = &now
	}
	task.State = domain.WorkflowTaskDispatching
	task.BlockedReason = ""
	snapshot.NextRunSeq++
	if err := m.persistLocked(snapshot); err != nil {
		m.setStorageErrorLocked(err)
		return false
	}

	err = m.exec.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     runID,
		Agent:     def.Agent,
		Message:   prompt,
		StatePath: statePath,
		// The snapshot's saved agent configuration is authoritative: a
		// recovery after a config edit must run the original role, model,
		// backend, and permissions, never the server's current YAML.
		Spec: m.savedAgentSpec(snapshot, def.Agent),
		Metadata: map[string]string{
			"workflow_execution_id": snapshot.ExecutionID,
			"workflow_task_id":      taskID,
			"workflow_attempt":      fmt.Sprintf("%d", attemptNumber),
		},
		OnDone: func(outcome domain.OwnedRunOutcome) {
			select {
			case m.completions <- completion{runID: runID, outcome: outcome}:
			default:
				// The scheduler is the only consumer; a full buffer means it
				// stopped. Preserve the outcome for Stop's drain path.
				go func() { m.completions <- completion{runID: runID, outcome: outcome} }()
			}
		},
	})
	dispatched := m.now()
	if err != nil {
		attemptSlot.State = domain.WorkflowAttemptFailed
		attemptSlot.Reason = "dispatch_error"
		attemptSlot.Error = err.Error()
		completed := m.now()
		attemptSlot.CompletedAt = &completed
		task.State = domain.WorkflowTaskFailed
		_ = m.persistLocked(snapshot)
		return true
	}

	attemptSlot.State = domain.WorkflowAttemptRunning
	attemptSlot.DispatchedAt = &dispatched
	task.State = domain.WorkflowTaskRunning
	m.active.live[runID] = &liveAttempt{
		taskID:       taskID,
		attempt:      attemptNumber,
		runID:        runID,
		dispatchedAt: dispatched,
		timeout:      time.Duration(def.EffectiveTimeoutSeconds(snapshot.Definition.TaskTimeoutSeconds)) * time.Second,
	}
	_ = m.persistLocked(snapshot)
	return true
}

func (m *Manager) setStorageErrorLocked(err error) {
	if m.storageErr == nil {
		m.storageErr = err
		if m.active != nil {
			snapshot := m.active.snapshot
			snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, "storage_error")
			message := err.Error()
			snapshot.LastError = &message
			snapshot.State = domain.WorkflowNeedsAttention
			_ = m.store.SaveWorkflowSnapshot(snapshot)
		}
	}
}

// handleCompletion commits one worker-stopped outcome into the snapshot. The
// result file is written and verified before success is durably published.
// Verdict-task completions enter the judging phase here; handleJudgement
// commits their classification.
func (m *Manager) handleCompletion(c completion) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c.judgement != nil {
		m.handleJudgement(c.judgement)
		return
	}

	if m.active == nil {
		return
	}
	snapshot := m.active.snapshot
	live, ok := m.active.live[c.runID]
	if !ok {
		// Already finalized (for example as interrupted after an unconfirmed
		// cancellation): the outcome is ignored rather than resurrected.
		return
	}
	delete(m.active.live, c.runID)
	task := snapshot.Tasks[live.taskID]
	if task == nil {
		return
	}
	attempt := findAttempt(snapshot, live.taskID, live.attempt)
	if attempt == nil {
		return
	}
	now := m.now()
	completed := now
	attempt.CompletedAt = &completed

	// A cancellation we requested (timeout or whole-execution cancel) means
	// the worker stopped on our signal; classify by why we cancelled.
	timedOut := !live.cancelRequestedAt.IsZero() && snapshot.Mode != domain.WorkflowModeCancelling
	cancelRequested := !live.cancelRequestedAt.IsZero() || snapshot.Mode == domain.WorkflowModeCancelling
	hasOutput := c.outcome.Status == domain.RunCompleted && strings.TrimSpace(c.outcome.FinalMessage) != ""

	switch {
	case timedOut && hasOutput:
		attempt.State = domain.WorkflowAttemptFailed
		attempt.Reason = "timeout"
		attempt.Error = "task exceeded its timeout; a late response arrived after cancellation and was not counted"
	case timedOut:
		attempt.State = domain.WorkflowAttemptFailed
		attempt.Reason = "timeout"
		if c.outcome.Error != "" {
			attempt.Error = c.outcome.Error
		} else {
			attempt.Error = "task exceeded its timeout and its owned work was cancelled"
		}
	case hasOutput:
		path, size, sha, err := m.store.WriteWorkflowResponse(snapshot.ExecutionID, live.taskID, live.attempt, []byte(c.outcome.FinalMessage))
		if err != nil {
			m.setStorageErrorLocked(fmt.Errorf("persist response of %s: %w", c.runID, err))
			attempt.State = domain.WorkflowAttemptFailed
			attempt.Reason = "storage_error"
			attempt.Error = err.Error()
			break
		}
		attempt.ResultPath = path
		attempt.ResultSize = size
		attempt.ResultSHA256 = sha
		if len(snapshot.Definition.Tasks[live.taskID].Verdicts) > 0 {
			// The response is committed; classification settles the
			// attempt. Dependents wait for that settlement because a
			// judging attempt is not committed.
			attempt.State = domain.WorkflowAttemptJudging
			task := snapshot.Tasks[live.taskID]
			m.dispatchJudgementLocked(snapshot, task, attempt)
		} else {
			attempt.State = domain.WorkflowAttemptSucceeded
		}
	case cancelRequested && c.outcome.Status != domain.RunCompleted:
		attempt.State = domain.WorkflowAttemptCancelled
		attempt.Reason = "cancelled"
		if c.outcome.Error != "" {
			attempt.Error = c.outcome.Error
		}
	case c.outcome.Status == domain.RunCompleted:
		attempt.State = domain.WorkflowAttemptFailed
		attempt.Reason = "missing_output"
		attempt.Error = "backend completed without a nonempty final response"
	case c.outcome.Status == domain.RunInterrupted:
		attempt.State = domain.WorkflowAttemptInterrupted
		attempt.Reason = "interrupted"
		if c.outcome.Error != "" {
			attempt.Error = c.outcome.Error
		}
	default:
		attempt.State = domain.WorkflowAttemptFailed
		attempt.Reason = "backend_error"
		if c.outcome.Error != "" {
			attempt.Error = c.outcome.Error
		}
	}

	snapshot.Revision++
	snapshot.UpdatedAt = now
	if err := m.store.SaveWorkflowSnapshot(snapshot); err != nil {
		m.setStorageErrorLocked(err)
	}
	m.notifyRevisionLocked()
	m.Notify()
}

// refreshExecutionStateLocked recomputes attention reasons and the workflow
// state from task states and live work.
func (m *Manager) refreshExecutionStateLocked(snapshot *domain.WorkflowSnapshot) bool {
	previous := snapshot.State
	previousReasons := snapshot.AttentionReasons

	reasons := collectAttentionReasons(snapshot)
	if m.storageErr != nil {
		reasons = appendUniqueReason(reasons, "storage_error")
	}
	snapshot.AttentionReasons = reasons

	allSettled := len(m.active.live) == 0
	// A loop that has not reached its final iteration keeps the execution
	// non-terminal even when every task state currently looks settled.
	for _, loop := range snapshot.Loops {
		if loop.State != domain.WorkflowLoopDone {
			allSettled = false
			break
		}
	}
	if allSettled {
		for _, task := range snapshot.Tasks {
			if !task.State.Settled() {
				allSettled = false
				break
			}
		}
	}

	switch {
	case snapshot.Mode == domain.WorkflowModeCancelling:
		// applyCancellingLocked owns the transition to cancelled.
	case len(reasons) > 0:
		snapshot.State = domain.WorkflowNeedsAttention
	case snapshot.Mode == domain.WorkflowModePaused:
		snapshot.State = domain.WorkflowPaused
	case allSettled:
		snapshot.State = finalState(snapshot)
	default:
		snapshot.State = domain.WorkflowRunning
	}

	if snapshot.State != previous || len(previousReasons) != len(snapshot.AttentionReasons) {
		return true
	}
	return false
}

func collectAttentionReasons(snapshot *domain.WorkflowSnapshot) []string {
	var reasons []string
	for _, task := range snapshot.Tasks {
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			switch {
			case attempt.State == domain.WorkflowAttemptInterrupted && attempt.CleanupConfirmedAt == nil:
				reasons = append(reasons, fmt.Sprintf("interrupted_attempt:%s:%d", task.TaskID, attempt.Attempt))
			case attempt.State == domain.WorkflowAttemptJudging && attempt.Reason != "":
				// A held classification: the judge was unavailable or its
				// confidence fell below the threshold. The verdict record
				// keeps the distribution for inspection.
				reasons = append(reasons, fmt.Sprintf("%s:%s:%d", attempt.Reason, task.TaskID, attempt.Attempt))
			}
		}
	}
	// Durable loop failure/blocking holds take precedence over final-state
	// derivation and keep the execution interventionable.
	reasons = append(reasons, loopHoldReasons(snapshot)...)
	return reasons
}

// finalState derives the workflow verdict once all tasks settled with no
// uncertainty: blocked or non-tolerated failures fail the execution, tolerated
// failures degrade it, otherwise it succeeded.
func finalState(snapshot *domain.WorkflowSnapshot) domain.WorkflowState {
	tolerated := false
	for _, task := range snapshot.Tasks {
		switch task.State {
		case domain.WorkflowTaskBlocked, domain.WorkflowTaskCancelled:
			return domain.WorkflowFailed
		case domain.WorkflowTaskFailed:
			def := snapshot.Definition.Tasks[task.TaskID]
			if !def.AllowedToFail {
				return domain.WorkflowFailed
			}
			tolerated = true
		}
	}
	if tolerated {
		return domain.WorkflowCompletedWithError
	}
	return domain.WorkflowSucceeded
}

func (m *Manager) persistLocked(snapshot *domain.WorkflowSnapshot) error {
	snapshot.Revision++
	snapshot.UpdatedAt = m.now()
	if err := m.store.SaveWorkflowSnapshot(snapshot); err != nil {
		m.setStorageErrorLocked(err)
		return err
	}
	m.notifyRevisionLocked()
	return nil
}

func findAttempt(snapshot *domain.WorkflowSnapshot, taskID string, attemptNumber int) *domain.WorkflowAttempt {
	task := snapshot.Tasks[taskID]
	if task == nil {
		return nil
	}
	for i := range task.Attempts {
		if task.Attempts[i].Attempt == attemptNumber {
			return &task.Attempts[i]
		}
	}
	return nil
}

func lastCommittedAttempt(task *domain.WorkflowTaskExecution) *domain.WorkflowAttempt {
	for i := len(task.Attempts) - 1; i >= 0; i-- {
		if task.Attempts[i].State.Committed() {
			return &task.Attempts[i]
		}
	}
	return nil
}

func executionNumber(executionID string) string {
	if _, ok := strings.CutPrefix(executionID, "wf_"); ok {
		return strings.TrimPrefix(executionID, "wf_")
	}
	return executionID
}

func sortedTaskIDs(tasks map[string]domain.WorkflowTaskDefinition) []string {
	ids := make([]string, 0, len(tasks))
	for taskID := range tasks {
		ids = append(ids, taskID)
	}
	sort.Strings(ids)
	return ids
}

func topologicalOrder(def domain.WorkflowDefinition) []string {
	order := make([]string, 0, len(def.Tasks))
	visited := map[string]bool{}
	var visit func(taskID string)
	visit = func(taskID string) {
		if visited[taskID] {
			return
		}
		visited[taskID] = true
		for _, dep := range def.Tasks[taskID].Needs {
			visit(dep)
		}
		order = append(order, taskID)
	}
	for _, taskID := range sortedTaskIDs(def.Tasks) {
		visit(taskID)
	}
	return order
}

// buildManifest renders the ordered dependency manifest of one attempt.
// Same-loop dependencies resolve to the loop's current iteration; other
// dependencies keep single-attempt resolution. A body task dispatching after
// the first iteration additionally carries the prior iteration's settled
// body-task outcomes.
func buildManifest(snapshot *domain.WorkflowSnapshot, taskID string) domain.WorkflowInputManifest {
	def := snapshot.Definition.Tasks[taskID]
	nested := snapshot.Definition.HasNesting()
	manifest := domain.WorkflowInputManifest{
		ExecutionID:  snapshot.ExecutionID,
		TaskID:       taskID,
		Iteration:    currentIteration(snapshot, def),
		Dependencies: []domain.WorkflowDependencyInput{},
	}
	if nested && def.Loop != "" {
		manifest.IterationPath = loopCurrentPath(snapshot, def.Loop)
	}
	for _, depID := range sortedDependencyIDs(def.Needs) {
		dep := snapshot.Tasks[depID]
		if dep == nil {
			continue
		}
		attempt := dependencyAttempt(snapshot, taskID, depID)
		if attempt == nil {
			continue
		}
		entry := domain.WorkflowDependencyInput{
			TaskID:    depID,
			Agent:     dep.Agent,
			Attempt:   attempt.Attempt,
			Iteration: attempt.Iteration,
			RunID:     attempt.RunID,
			Status:    string(attempt.State),
			Error:     attempt.Error,
			Verdict:   attempt.Verdict,
		}
		if nested {
			entry.IterationPath = attempt.IterationPath
		}
		if attempt.State == domain.WorkflowAttemptSucceeded {
			entry.ResultPath = attempt.ResultPath
			entry.ResultSize = attempt.ResultSize
			entry.ResultSHA256 = attempt.ResultSHA256
		}
		manifest.Dependencies = append(manifest.Dependencies, entry)
	}
	if def.Loop != "" {
		loop := snapshot.Loops[def.Loop]
		if loop != nil {
			if nested {
				// The owner's own previous-iteration section exists only past
				// its first local iteration, but ancestor sections are computed
				// independently: an inner task at inner=1 under outer=2 still
				// receives the previous outer iteration's whole-subtree feedback.
				if loop.Iteration > 1 {
					summarized := decrementPath(loop.IterationPath)
					manifest.PreviousIterationPath = summarized
					manifest.PreviousIteration = nestedPreviousIterationOutcomes(snapshot, def.Loop, summarized)
				}
				manifest.AncestorPreviousIterations = nestedAncestorPreviousIterations(snapshot, def.Loop)
			} else if loop.Iteration > 1 {
				manifest.PreviousIteration = previousIterationInputs(snapshot, def.Loop, loop.Iteration-1)
			}
		}
	}
	return manifest
}

func sortedDependencyIDs(needs []string) []string {
	ids := append([]string(nil), needs...)
	sort.Strings(ids)
	return ids
}

// buildPromptLocked composes the exact task message: its own prompt plus a
// clearly delimited manifest of dependency outcomes and readable local result
// files. Responses are referenced, never pasted or truncated.
func (m *Manager) buildPromptLocked(snapshot *domain.WorkflowSnapshot, taskID string, manifest domain.WorkflowInputManifest) string {
	execDir, _ := m.store.WorkflowDir(snapshot.ExecutionID)
	prompt := strings.TrimSpace(snapshot.Definition.Tasks[taskID].Prompt)
	var builder strings.Builder
	builder.WriteString(prompt)
	if len(manifest.Dependencies) == 0 && len(manifest.PreviousIteration) == 0 && len(manifest.AncestorPreviousIterations) == 0 {
		return builder.String()
	}
	if len(manifest.Dependencies) > 0 {
		builder.WriteString("\n\n--- Workflow dependency inputs ---\n")
		fmt.Fprintf(&builder, "Execution %s, task %s.\n", snapshot.ExecutionID, taskID)
		builder.WriteString("All direct dependencies below have settled. Read each referenced local response file for the complete result; do not rely on this summary alone.\n")
		for _, dep := range manifest.Dependencies {
			switch dep.Status {
			case string(domain.WorkflowAttemptSucceeded):
				fmt.Fprintf(&builder, "- %s: succeeded (agent %s, attempt %d, run %s). Full response: %s\n",
					dep.TaskID, dep.Agent, dep.Attempt, dep.RunID, joinWorkflowPath(execDir, dep.ResultPath))
			default:
				errorText := dep.Error
				if errorText == "" {
					errorText = "no error detail recorded"
				}
				fmt.Fprintf(&builder, "- %s: %s (agent %s, attempt %d, run %s). Error: %s\n",
					dep.TaskID, dep.Status, dep.Agent, dep.Attempt, dep.RunID, errorText)
			}
		}
	}
	if len(manifest.PreviousIteration) > 0 {
		nested := snapshot.Definition.HasNesting()
		builder.WriteString("\n--- Previous iteration outcomes ---\n")
		if nested {
			fmt.Fprintf(&builder, "Loop iteration %s; the loop body settled in the previous invocation %s as follows. Read each referenced local response file for the complete result; do not rely on this summary alone.\n",
				domain.RenderIterationPath(manifest.IterationPath), domain.RenderIterationPath(manifest.PreviousIterationPath))
		} else {
			fmt.Fprintf(&builder, "Loop iteration %d; the loop body settled in iteration %d as follows. Read each referenced local response file for the complete result; do not rely on this summary alone.\n",
				manifest.Iteration, manifest.Iteration-1)
		}
		for _, entry := range manifest.PreviousIteration {
			switch entry.Status {
			case string(domain.WorkflowAttemptSucceeded):
				ref := joinWorkflowPath(execDir, entry.ResultPath)
				if entry.Verdict != nil {
					fmt.Fprintf(&builder, "- %s: succeeded, verdict %s (agent %s, attempt %d, %s, run %s). Full response: %s\n",
						entry.TaskID, entry.Verdict.Value, entry.Agent, entry.Attempt, carryOverContextLabel(entry), entry.RunID, ref)
				} else {
					fmt.Fprintf(&builder, "- %s: succeeded (agent %s, attempt %d, %s, run %s). Full response: %s\n",
						entry.TaskID, entry.Agent, entry.Attempt, carryOverContextLabel(entry), entry.RunID, ref)
				}
			default:
				errorText := entry.Error
				if errorText == "" {
					errorText = "no error detail recorded"
				}
				fmt.Fprintf(&builder, "- %s: %s (agent %s, attempt %d, %s, run %s). Error: %s\n",
					entry.TaskID, entry.Status, entry.Agent, entry.Attempt, carryOverContextLabel(entry), entry.RunID, errorText)
			}
		}
	}
	if len(manifest.AncestorPreviousIterations) > 0 {
		builder.WriteString("\n--- Enclosing loop previous-iteration outcomes ---\n")
		builder.WriteString("Each section below summarizes an enclosing loop's previous iteration over its whole subtree. Read each referenced local response file for the complete result; do not rely on this summary alone.\n")
		for _, section := range manifest.AncestorPreviousIterations {
			fmt.Fprintf(&builder, "\n[enclosing context %s]\n", domain.RenderIterationPath(section.IterationPath))
			for _, entry := range section.Outcomes {
				switch entry.Status {
				case string(domain.WorkflowAttemptSucceeded):
					ref := joinWorkflowPath(execDir, entry.ResultPath)
					if entry.Verdict != nil {
						fmt.Fprintf(&builder, "- %s: succeeded, verdict %s (agent %s, attempt %d, %s, run %s). Full response: %s\n",
							entry.TaskID, entry.Verdict.Value, entry.Agent, entry.Attempt, carryOverContextLabel(entry), entry.RunID, ref)
					} else {
						fmt.Fprintf(&builder, "- %s: succeeded (agent %s, attempt %d, %s, run %s). Full response: %s\n",
							entry.TaskID, entry.Agent, entry.Attempt, carryOverContextLabel(entry), entry.RunID, ref)
					}
				default:
					errorText := entry.Error
					if errorText == "" {
						errorText = "no error detail recorded"
					}
					fmt.Fprintf(&builder, "- %s: %s (agent %s, attempt %d, %s, run %s). Error: %s\n",
						entry.TaskID, entry.Status, entry.Agent, entry.Attempt, carryOverContextLabel(entry), entry.RunID, errorText)
				}
			}
		}
	}
	manifestPath := manifestRelativePath(taskID, manifest.Attempt)
	fmt.Fprintf(&builder, "Input manifest: %s\n", joinWorkflowPath(execDir, manifestPath))
	return builder.String()
}

// carryOverContextLabel names the context a carry-over entry was sourced from:
// the complete iteration path in a nested execution, otherwise the local
// iteration number, so an agent can tell repeated local counters apart.
func carryOverContextLabel(entry domain.WorkflowDependencyInput) string {
	if len(entry.IterationPath) > 0 {
		return "context " + domain.RenderIterationPath(entry.IterationPath)
	}
	return fmt.Sprintf("iteration %d", entry.Iteration)
}

// manifestRelativePath mirrors the store layout for attempt input artifacts.
func manifestRelativePath(taskID string, attempt int) string {
	return fmt.Sprintf("tasks/%s/attempts/%d/input-manifest.json", taskID, attempt)
}

// joinWorkflowPath renders an execution-relative artifact as a readable local
// path; malformed references fall back to the recorded relative form.
func joinWorkflowPath(execDir, relative string) string {
	if execDir == "" || relative == "" {
		return relative
	}
	if filepath.IsAbs(relative) {
		return relative
	}
	joined := filepath.Join(execDir, filepath.FromSlash(relative))
	cleaned := filepath.Clean(joined)
	if cleaned != execDir && !strings.HasPrefix(cleaned, execDir+string(filepath.Separator)) {
		return relative
	}
	return cleaned
}
