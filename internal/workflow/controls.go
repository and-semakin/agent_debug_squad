package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// Pause durably stops new dispatch reservations; already dispatched attempts
// finish and their outcomes are preserved.
func (m *Manager) Pause(executionID string) (domain.WorkflowExecutionView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, err := m.loadSnapshotLocked(executionID)
	if err != nil {
		return domain.WorkflowExecutionView{}, err
	}
	if snapshot.State.Terminal() {
		return domain.WorkflowExecutionView{}, fmt.Errorf("%w: execution is %s", ErrInvalidTransition, snapshot.State)
	}
	if snapshot.Mode == domain.WorkflowModeCancelling {
		return domain.WorkflowExecutionView{}, fmt.Errorf("%w: execution is cancelling", ErrInvalidTransition)
	}
	if snapshot.Mode == domain.WorkflowModePaused {
		return m.buildViewLocked(snapshot), nil
	}

	snapshot.Mode = domain.WorkflowModePaused
	if len(snapshot.AttentionReasons) == 0 {
		snapshot.State = domain.WorkflowPaused
	}
	_ = m.store.AppendWorkflowEvent(executionID, domain.WorkflowControlEvent{Type: "pause", At: m.now()})
	if err := m.persistLocked(snapshot); err != nil {
		return domain.WorkflowExecutionView{}, err
	}
	m.Notify()
	return m.buildViewLocked(snapshot), nil
}

// Resume revalidates storage and artifacts and continues a paused or
// needs_attention execution once no uncertainty remains.
func (m *Manager) Resume(executionID string) (domain.WorkflowExecutionView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, err := m.loadSnapshotLocked(executionID)
	if err != nil {
		return domain.WorkflowExecutionView{}, err
	}
	if snapshot.State.Terminal() || snapshot.Mode == domain.WorkflowModeCancelling {
		return domain.WorkflowExecutionView{}, fmt.Errorf("%w: execution is %s", ErrInvalidTransition, snapshot.State)
	}
	if snapshot.State != domain.WorkflowPaused && snapshot.State != domain.WorkflowNeedsAttention {
		return domain.WorkflowExecutionView{}, fmt.Errorf("%w: execution is %s", ErrInvalidTransition, snapshot.State)
	}
	// Revalidate artifacts before consulting the recorded storage error: a
	// transient verification failure (for example a committed result that
	// was briefly unreadable and later restored) must not wedge resume
	// until process restart.
	reasons, err := m.revalidateLocked(snapshot)
	if err != nil {
		snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, fmt.Sprintf("revalidation_failed:%v", err))
		snapshot.State = domain.WorkflowNeedsAttention
		_ = m.persistLocked(snapshot)
		m.Notify()
		return domain.WorkflowExecutionView{}, fmt.Errorf("%w: %v", ErrUncertaintyUnresolved, err)
	}
	if len(reasons) > 0 {
		snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, reasons...)
		snapshot.State = domain.WorkflowNeedsAttention
		_ = m.persistLocked(snapshot)
		m.Notify()
		return domain.WorkflowExecutionView{}, fmt.Errorf("%w: %v", ErrUncertaintyUnresolved, reasons)
	}
	if m.storageErr != nil {
		// The recorded failure was resolvable: its cause is gone and every
		// artifact verifies, so scheduling may resume without a restart.
		m.storageErr = nil
	}

	snapshot.Mode = domain.WorkflowModeRunning
	snapshot.State = domain.WorkflowRunning
	snapshot.AttentionReasons = nil
	_ = m.store.AppendWorkflowEvent(executionID, domain.WorkflowControlEvent{Type: "resume", At: m.now()})
	if err := m.persistLocked(snapshot); err != nil {
		return domain.WorkflowExecutionView{}, err
	}
	// Held classifications get a fresh judge round before scheduling
	// continues; their attention reasons are gone with the hold.
	m.redispatchJudgingLocked(snapshot)
	m.Notify()
	return m.buildViewLocked(snapshot), nil
}

// revalidateLocked checks committed artifacts and unresolved interruptions.
func (m *Manager) revalidateLocked(snapshot *domain.WorkflowSnapshot) ([]string, error) {
	var reasons []string
	for _, task := range snapshot.Tasks {
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			switch {
			case attempt.State == domain.WorkflowAttemptInterrupted && attempt.CleanupConfirmedAt == nil:
				// A confirmed cleanup already closed the uncertainty; only
				// unconfirmed interruptions block resume, consistent with
				// collectAttentionReasons.
				reasons = append(reasons, fmt.Sprintf("interrupted_attempt:%s:%d requires retry or cancellation", task.TaskID, attempt.Attempt))
			case attempt.State == domain.WorkflowAttemptSucceeded && attempt.ResultPath != "":
				if err := m.store.VerifyWorkflowArtifact(snapshot.ExecutionID, attempt.ResultPath, attempt.ResultSize, attempt.ResultSHA256); err != nil {
					reasons = append(reasons, fmt.Sprintf("artifact:%s:%d:%v", task.TaskID, attempt.Attempt, err))
				}
			}
		}
	}
	return reasons, nil
}

type CancelOptions struct {
	ConfirmPreviousStopped bool
}

// Cancel durably records cancelling intent, cancels live owned runs, and marks
// unstarted tasks cancelled. It reaches cancelled only after owned workers
// stop, or after an explicit cleanup assertion for interrupted attempts.
func (m *Manager) Cancel(executionID string, opts CancelOptions) (domain.WorkflowExecutionView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, err := m.loadSnapshotLocked(executionID)
	if err != nil {
		return domain.WorkflowExecutionView{}, err
	}
	if snapshot.State.Terminal() && snapshot.State != domain.WorkflowCancelled {
		return domain.WorkflowExecutionView{}, fmt.Errorf("%w: execution is %s", ErrInvalidTransition, snapshot.State)
	}
	if snapshot.Mode == domain.WorkflowModeCancelling || snapshot.State == domain.WorkflowCancelled {
		if opts.ConfirmPreviousStopped {
			if err := m.applyCleanupAssertionLocked(snapshot); err != nil {
				return domain.WorkflowExecutionView{}, err
			}
			m.reconcileLocked()
		}
		return m.buildViewLocked(snapshot), nil
	}

	now := m.now()
	snapshot.Mode = domain.WorkflowModeCancelling
	snapshot.State = domain.WorkflowCancelling
	_ = m.store.AppendWorkflowEvent(executionID, domain.WorkflowControlEvent{
		Type: "cancel", At: now, ConfirmPreviousStopped: opts.ConfirmPreviousStopped,
	})
	if err := m.persistLocked(snapshot); err != nil {
		return domain.WorkflowExecutionView{}, err
	}

	for runID := range m.activeLiveForSnapshot(snapshot) {
		m.exec.CancelOwnedRun(runID)
	}

	if opts.ConfirmPreviousStopped {
		if err := m.applyCleanupAssertionLocked(snapshot); err != nil {
			return domain.WorkflowExecutionView{}, err
		}
	}
	m.reconcileLocked()
	m.Notify()
	return m.buildViewLocked(snapshot), nil
}

func (m *Manager) activeLiveForSnapshot(snapshot *domain.WorkflowSnapshot) map[string]*liveAttempt {
	if m.active != nil && m.active.snapshot == snapshot {
		return m.active.live
	}
	return nil
}

// applyCleanupAssertionLocked records a caller's assertion that interrupted
// backend work has stopped. It never overrides a worker known to be live in
// this process.
func (m *Manager) applyCleanupAssertionLocked(snapshot *domain.WorkflowSnapshot) error {
	now := m.now()
	// A cleanup assertion never overrides a worker this process knows is live.
	for runID := range m.activeLiveForSnapshot(snapshot) {
		return fmt.Errorf("%w: run %s is still active", ErrWorkerActive, runID)
	}
	for _, task := range snapshot.Tasks {
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if attempt.State != domain.WorkflowAttemptInterrupted {
				continue
			}
			if m.exec.OwnedRunActive(attempt.RunID) {
				return fmt.Errorf("%w: run %s is still active", ErrWorkerActive, attempt.RunID)
			}
		}
	}
	confirmed := false
	for _, task := range snapshot.Tasks {
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if attempt.State == domain.WorkflowAttemptInterrupted && attempt.CleanupConfirmedAt == nil {
				at := now
				attempt.CleanupConfirmedAt = &at
				confirmed = true
			}
		}
	}
	if confirmed {
		_ = m.store.AppendWorkflowEvent(snapshot.ExecutionID, domain.WorkflowControlEvent{
			Type: "cleanup_confirmed", At: now,
		})
		_ = m.persistLocked(snapshot)
	}
	return nil
}

type RetryRequest struct {
	RequestID              string
	ExpectedAttempt        int
	ConfirmPreviousStopped bool
}

type OverrideVerdictRequest struct {
	RequestID string
	Verdict   string
}

// OverrideVerdict settles a held judging attempt with a human-chosen
// verdict, recorded as manually sourced. It is idempotent per request ID
// through the snapshot's control history.
func (m *Manager) OverrideVerdict(executionID, taskID string, attemptNumber int, req OverrideVerdictRequest) (domain.WorkflowExecutionView, bool, error) {
	if req.RequestID == "" {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}
	if req.Verdict == "" {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: verdict is required", ErrInvalidRequest)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, err := m.loadSnapshotLocked(executionID)
	if err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	for _, event := range snapshot.Controls {
		if event.Type != "verdict_override" || event.RequestID != req.RequestID {
			continue
		}
		if event.TaskID == taskID && event.Attempt == attemptNumber && event.Detail == req.Verdict {
			return m.buildViewLocked(snapshot), false, nil
		}
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: request id already used", ErrVerdictConflict)
	}

	task := snapshot.Tasks[taskID]
	if task == nil {
		return domain.WorkflowExecutionView{}, false, ErrTaskNotFound
	}
	taskDef := snapshot.Definition.Tasks[taskID]
	if _, declared := taskDef.Verdicts[req.Verdict]; !declared {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: verdict %q is not declared by task %q", ErrInvalidRequest, req.Verdict, taskID)
	}
	attempt := findAttempt(snapshot, taskID, attemptNumber)
	if attempt == nil {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: task %q has no attempt %d", ErrAttemptNotFound, taskID, attemptNumber)
	}
	if attempt.State != domain.WorkflowAttemptJudging {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: attempt %d of task %q is %s, not judging", ErrVerdictConflict, attemptNumber, taskID, attempt.State)
	}

	now := m.now()
	attempt.State = domain.WorkflowAttemptSucceeded
	attempt.Reason = ""
	attempt.Error = ""
	attempt.Verdict = &domain.AttemptVerdict{
		Value:     req.Verdict,
		Threshold: snapshot.Definition.EffectiveConfidenceThreshold(),
		Source:    verdictSourceManual,
		JudgedAt:  now,
	}
	event := domain.WorkflowControlEvent{
		Type: "verdict_override", At: now, RequestID: req.RequestID,
		TaskID: taskID, Attempt: attemptNumber, Detail: req.Verdict,
	}
	snapshot.Controls = append(snapshot.Controls, event)
	_ = m.store.AppendWorkflowEvent(executionID, event)
	snapshot.AttentionReasons = collectAttentionReasons(snapshot)
	if len(snapshot.AttentionReasons) == 0 && !snapshot.State.Terminal() {
		snapshot.State = stateForMode(snapshot)
	}
	if err := m.persistLocked(snapshot); err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	m.reconcileLocked()
	m.Notify()
	return m.buildViewLocked(snapshot), true, nil
}

// RetryTask reserves a fresh queued attempt for a failed or interrupted task
// before any descendant has consumed its outcome.
func (m *Manager) RetryTask(executionID, taskID string, req RetryRequest) (domain.WorkflowExecutionView, bool, error) {
	if req.RequestID == "" {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}
	if req.ExpectedAttempt < 1 {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: expected_attempt is required", ErrInvalidRequest)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, err := m.loadSnapshotLocked(executionID)
	if err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	task := snapshot.Tasks[taskID]
	if task == nil {
		return domain.WorkflowExecutionView{}, false, ErrTaskNotFound
	}

	if snapshot.RetryRequests == nil {
		snapshot.RetryRequests = map[string]domain.WorkflowRetryRecord{}
	}
	if previous, ok := snapshot.RetryRequests[req.RequestID]; ok {
		if previous.TaskID == taskID && previous.Attempt == req.ExpectedAttempt+1 {
			return m.buildViewLocked(snapshot), false, nil
		}
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: request id already used", ErrRetryConflict)
	}

	if snapshot.Mode == domain.WorkflowModeCancelling || snapshot.State == domain.WorkflowCancelled {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: execution is %s", ErrRetryConflict, snapshot.State)
	}
	if snapshot.State.Terminal() {
		if snapshot.State != domain.WorkflowFailed && snapshot.State != domain.WorkflowCompletedWithError {
			return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: execution is %s", ErrRetryConflict, snapshot.State)
		}
		if m.active != nil && m.active.snapshot != snapshot {
			return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: another execution is active", ErrRetryConflict)
		}
	}

	if task.State != domain.WorkflowTaskFailed && task.State != domain.WorkflowTaskInterrupted {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: task is %s", ErrRetryConflict, task.State)
	}
	if req.ExpectedAttempt != len(task.Attempts) {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: expected_attempt %d but last attempt is %d", ErrRetryConflict, req.ExpectedAttempt, len(task.Attempts))
	}
	if snapshot.State == domain.WorkflowNeedsAttention && !onlyInterruptionsRemain(snapshot) {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: other uncertainty remains", ErrRetryConflict)
	}

	if err := assertNoDescendantAttempts(snapshot, taskID); err != nil {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: %v", ErrRetryConflict, err)
	}

	last := task.LastAttempt()
	if last != nil && last.State == domain.WorkflowAttemptInterrupted {
		if !req.ConfirmPreviousStopped {
			return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: interrupted retry requires confirm_previous_stopped", ErrRetryConflict)
		}
		if m.exec.OwnedRunActive(last.RunID) {
			return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: run %s is still active", ErrWorkerActive, last.RunID)
		}
		if last.CleanupConfirmedAt == nil {
			at := m.now()
			last.CleanupConfirmedAt = &at
		}
	}

	now := m.now()
	attemptNumber := len(task.Attempts) + 1
	task.Attempts = append(task.Attempts, domain.WorkflowAttempt{
		Attempt:    attemptNumber,
		State:      domain.WorkflowAttemptQueued,
		Reason:     "retry_reserved",
		ReservedAt: &now,
	})
	task.State = domain.WorkflowTaskPending
	task.BlockedReason = ""
	snapshot.RetryRequests[req.RequestID] = domain.WorkflowRetryRecord{
		RequestID:              req.RequestID,
		TaskID:                 taskID,
		Attempt:                attemptNumber,
		CreatedAt:              now,
		ConfirmPreviousStopped: req.ConfirmPreviousStopped,
	}

	// Reopen a terminal execution; a paused one stays paused after retry.
	if snapshot.State.Terminal() {
		snapshot.Mode = domain.WorkflowModeRunning
	}
	snapshot.AttentionReasons = collectAttentionReasons(snapshot)
	if len(snapshot.AttentionReasons) == 0 {
		snapshot.State = stateForMode(snapshot)
	} else {
		snapshot.State = domain.WorkflowNeedsAttention
	}

	_ = m.store.AppendWorkflowEvent(executionID, domain.WorkflowControlEvent{
		Type: "retry", At: now, RequestID: req.RequestID, TaskID: taskID,
		ConfirmPreviousStopped: req.ConfirmPreviousStopped,
	})
	if err := m.persistLocked(snapshot); err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	// A retried terminal execution becomes schedulable again.
	if m.active == nil {
		m.active = &execution{snapshot: snapshot, live: map[string]*liveAttempt{}}
	}
	if snapshot.State != domain.WorkflowPaused {
		m.Notify()
	}
	return m.buildViewLocked(snapshot), true, nil
}

func stateForMode(snapshot *domain.WorkflowSnapshot) domain.WorkflowState {
	switch snapshot.Mode {
	case domain.WorkflowModePaused:
		return domain.WorkflowPaused
	case domain.WorkflowModeCancelling:
		return domain.WorkflowCancelling
	default:
		return domain.WorkflowRunning
	}
}

// onlyInterruptionsRemain reports whether every unresolved attention reason
// is interruption uncertainty: conditions a caller can retire attempt by
// attempt with confirmed retries. It deliberately allows retrying one
// interrupted task while independent siblings are still interrupted—each
// retry queues its attempt, and dispatch stays held until the last
// uncertainty is resolved. Artifact or storage damage is different: it must
// be repaired and revalidated through resume before new work.
func onlyInterruptionsRemain(snapshot *domain.WorkflowSnapshot) bool {
	for _, reason := range snapshot.AttentionReasons {
		if !strings.HasPrefix(reason, "interrupted_attempt:") &&
			!strings.HasPrefix(reason, "interrupted_attempts_require") {
			return false
		}
	}
	return true
}

func assertNoDescendantAttempts(snapshot *domain.WorkflowSnapshot, taskID string) error {
	descendants := map[string]bool{}
	var mark func(string)
	mark = func(current string) {
		for otherID, other := range snapshot.Definition.Tasks {
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
	for descendant := range descendants {
		if task := snapshot.Tasks[descendant]; task != nil && len(task.Attempts) > 0 {
			return fmt.Errorf("descendant %s already has attempts", descendant)
		}
	}
	return nil
}

// reconcileLocked runs one reconciliation pass; callers hold the manager lock.
func (m *Manager) reconcileLocked() {
	if m.active == nil || m.stopping {
		return
	}
	snapshot := m.active.snapshot
	changed := false
	if m.storageErr == nil {
		changed = m.enforceTimeoutsLocked(snapshot) || changed
	}
	changed = m.applyCancellingLocked(snapshot) || changed
	changed = m.recomputeTaskStatesLocked(snapshot) || changed
	// Refresh the execution state before dispatching so that attention
	// conditions established above block new work in the same pass.
	changed = m.refreshExecutionStateLocked(snapshot) || changed
	if m.storageErr == nil && snapshot.Mode == domain.WorkflowModeRunning && snapshot.State == domain.WorkflowRunning {
		changed = m.dispatchReadyTasksLocked(snapshot) || changed
	}
	if changed {
		m.persistLocked(snapshot)
	}
	if snapshot.State.Terminal() {
		// Historical executions are read from the store; the active slot is
		// free for a new submission.
		m.active = nil
	}
}

// Wait blocks until the execution reaches a terminal state, an intervention
// condition, or the timeout elapses. Expiry returns the current state and
// never cancels work; a client disconnect does not stop execution either.
func (m *Manager) Wait(ctx context.Context, executionID string, timeout time.Duration) (domain.WorkflowExecutionView, error) {
	deadline := time.Now().Add(timeout)
	observer := m.runObserver()
	for {
		view, err := m.View(executionID)
		if err != nil {
			return view, err
		}
		if waitConditionMet(view, observer) {
			return view, nil
		}
		if ctx.Err() != nil {
			return view, ctx.Err()
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return view, nil
		}
		wait := 150 * time.Millisecond
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			// The caller went away; execution continues unaffected.
			return m.View(executionID)
		case <-timer.C:
		}
	}
}

func (m *Manager) runObserver() RunObserver {
	if observer, ok := m.exec.(RunObserver); ok {
		return observer
	}
	return nil
}

// waitConditionMet reports terminal or intervention states, including pending
// permissions and failed auto-approvals of live attempts.
func waitConditionMet(view domain.WorkflowExecutionView, observer RunObserver) bool {
	if view.State.Terminal() || view.State == domain.WorkflowNeedsAttention {
		return true
	}
	if len(view.PendingPermissions) > 0 {
		return true
	}
	_ = observer
	return false
}

func countTaskStates(snapshot *domain.WorkflowSnapshot) domain.WorkflowTaskCounts {
	counts := domain.WorkflowTaskCounts{}
	for _, task := range snapshot.Tasks {
		switch task.State {
		case domain.WorkflowTaskPending:
			counts.Pending++
		case domain.WorkflowTaskReady:
			counts.Ready++
		case domain.WorkflowTaskDispatching, domain.WorkflowTaskRunning:
			counts.Active++
		case domain.WorkflowTaskSucceeded:
			counts.Succeeded++
		case domain.WorkflowTaskFailed:
			counts.Failed++
		case domain.WorkflowTaskBlocked:
			counts.Blocked++
		case domain.WorkflowTaskInterrupted:
			counts.Interrupted++
		case domain.WorkflowTaskCancelled:
			counts.Cancelled++
		}
	}
	return counts
}

func (m *Manager) loadSnapshotLocked(executionID string) (*domain.WorkflowSnapshot, error) {
	if snapshot, ok := m.findSnapshotLocked(executionID); ok {
		return snapshot, nil
	}
	snapshot, err := m.store.LoadWorkflowSnapshot(executionID)
	if err != nil {
		return nil, ErrExecutionNotFound
	}
	return &snapshot, nil
}

func (m *Manager) buildViewLocked(snapshot *domain.WorkflowSnapshot) domain.WorkflowExecutionView {
	view := domain.WorkflowExecutionView{
		ExecutionID:      snapshot.ExecutionID,
		Definition:       snapshot.Definition,
		DefinitionHash:   snapshot.DefinitionHash,
		RequestID:        snapshot.RequestID,
		Revision:         snapshot.Revision,
		State:            snapshot.State,
		Mode:             snapshot.Mode,
		AttentionReasons: snapshot.AttentionReasons,
		LastError:        snapshot.LastError,
		TaskCounts:       countTaskStates(snapshot),
		CreatedAt:        snapshot.CreatedAt,
		UpdatedAt:        snapshot.UpdatedAt,
	}
	execDir, _ := m.store.WorkflowDir(snapshot.ExecutionID)

	taskIDs := make([]string, 0, len(snapshot.Tasks))
	for taskID := range snapshot.Tasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	for _, taskID := range taskIDs {
		task := snapshot.Tasks[taskID]
		taskView := domain.WorkflowTaskView{
			TaskID:        task.TaskID,
			Agent:         task.Agent,
			State:         task.State,
			BlockedReason: task.BlockedReason,
			Attempts:      make([]domain.WorkflowAttemptView, 0, len(task.Attempts)),
		}
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			attemptView := domain.WorkflowAttemptView{
				Attempt:      attempt.Attempt,
				RunID:        attempt.RunID,
				State:        attempt.State,
				Reason:       attempt.Reason,
				Error:        attempt.Error,
				ReservedAt:   attempt.ReservedAt,
				DispatchedAt: attempt.DispatchedAt,
				CompletedAt:  attempt.CompletedAt,
				Verdict:      attempt.Verdict,
			}
			if attempt.ResultPath != "" {
				attemptView.Result = &domain.WorkflowResultRef{
					Path:   joinWorkflowPath(execDir, attempt.ResultPath),
					Size:   attempt.ResultSize,
					SHA256: attempt.ResultSHA256,
				}
			}
			taskView.Attempts = append(taskView.Attempts, attemptView)
			if attempt.State == domain.WorkflowAttemptSucceeded {
				taskView.Result = attemptView.Result
			}
		}
		view.Tasks = append(view.Tasks, taskView)
	}

	if m.active != nil && m.active.snapshot == snapshot {
		if observer := m.runObserver(); observer != nil {
			for _, live := range m.active.live {
				run, err := observer.Run(context.Background(), live.runID)
				if err != nil || run.Progress == nil {
					continue
				}
				for i := range run.Progress.PendingPermissions {
					request := run.Progress.PendingPermissions[i]
					if !request.AutoApproving || request.AutoApproveError != "" {
						view.PendingPermissions = append(view.PendingPermissions, domain.WorkflowPendingPermission{
							TaskID:  live.taskID,
							Attempt: live.attempt,
							RunID:   live.runID,
							Request: request,
						})
					}
				}
			}
		}
	}
	return view
}
