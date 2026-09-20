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
			if attempt.State == domain.WorkflowAttemptQueued {
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
		if last := task.LastAttempt(); last != nil {
			switch last.State {
			case domain.WorkflowAttemptRunning, domain.WorkflowAttemptDispatching, domain.WorkflowAttemptCancelling:
				task.State = map[domain.WorkflowAttemptState]domain.WorkflowTaskState{
					domain.WorkflowAttemptRunning:     domain.WorkflowTaskRunning,
					domain.WorkflowAttemptDispatching: domain.WorkflowTaskDispatching,
					domain.WorkflowAttemptCancelling:  domain.WorkflowTaskRunning,
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

		// No live or committed attempt: evaluate dependencies.
		depDef := def.Tasks[taskID]
		allSettled := true
		acceptable := true
		blockedBy := ""
		successes := 0
		for _, depID := range depDef.Needs {
			dep := snapshot.Tasks[depID]
			if dep == nil {
				continue
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
			depTaskDef := def.Tasks[depID]
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

	// Verify every committed dependency result before dispatch.
	for _, depID := range def.Needs {
		dep := snapshot.Tasks[depID]
		if dep == nil {
			continue
		}
		attempt := lastCommittedAttempt(dep)
		if attempt == nil || attempt.State != domain.WorkflowAttemptSucceeded || attempt.ResultPath == "" {
			continue
		}
		if err := m.store.VerifyWorkflowArtifact(snapshot.ExecutionID, attempt.ResultPath, attempt.ResultSize, attempt.ResultSHA256); err != nil {
			snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, fmt.Sprintf("artifact:%s:%d", depID, attempt.Attempt))
			m.setStorageErrorLocked(fmt.Errorf("verify dependency %s: %w", depID, err))
			return false
		}
	}

	// A retry reserves its attempt record as queued before dispatch; a fresh
	// dispatch appends a new one.
	var attemptSlot *domain.WorkflowAttempt
	if last := task.LastAttempt(); last != nil && last.State == domain.WorkflowAttemptQueued {
		attemptSlot = last
	} else {
		task.Attempts = append(task.Attempts, domain.WorkflowAttempt{Attempt: len(task.Attempts) + 1})
		attemptSlot = &task.Attempts[len(task.Attempts)-1]
	}
	attemptNumber := attemptSlot.Attempt
	manifest := buildManifest(snapshot, taskID)
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
func (m *Manager) handleCompletion(c completion) {
	m.mu.Lock()
	defer m.mu.Unlock()

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
		attempt.State = domain.WorkflowAttemptSucceeded
		attempt.ResultPath = path
		attempt.ResultSize = size
		attempt.ResultSHA256 = sha
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
	for _, task := range snapshot.Tasks {
		if !task.State.Settled() {
			allSettled = false
			break
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
			}
		}
	}
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
func buildManifest(snapshot *domain.WorkflowSnapshot, taskID string) domain.WorkflowInputManifest {
	def := snapshot.Definition.Tasks[taskID]
	manifest := domain.WorkflowInputManifest{
		ExecutionID:  snapshot.ExecutionID,
		TaskID:       taskID,
		Dependencies: []domain.WorkflowDependencyInput{},
	}
	for _, depID := range sortedDependencyIDs(def.Needs) {
		dep := snapshot.Tasks[depID]
		if dep == nil {
			continue
		}
		attempt := lastCommittedAttempt(dep)
		if attempt == nil {
			continue
		}
		entry := domain.WorkflowDependencyInput{
			TaskID:  depID,
			Agent:   dep.Agent,
			Attempt: attempt.Attempt,
			RunID:   attempt.RunID,
			Status:  string(attempt.State),
			Error:   attempt.Error,
		}
		if attempt.State == domain.WorkflowAttemptSucceeded {
			entry.ResultPath = attempt.ResultPath
			entry.ResultSize = attempt.ResultSize
			entry.ResultSHA256 = attempt.ResultSHA256
		}
		manifest.Dependencies = append(manifest.Dependencies, entry)
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
	if len(manifest.Dependencies) == 0 {
		return builder.String()
	}
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
	manifestPath := manifestRelativePath(taskID, manifest.Attempt)
	fmt.Fprintf(&builder, "Input manifest: %s\n", joinWorkflowPath(execDir, manifestPath))
	return builder.String()
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
