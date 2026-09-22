package workflow

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// cloneWorkflowSnapshot returns a deep, independent copy of snapshot via a JSON
// round-trip. The domain snapshot carries no json:"-" fields, so the copy is
// lossless; it lets a control prepare and persist a candidate mutation without
// touching the live in-memory pointer until the write is confirmed.
func cloneWorkflowSnapshot(snapshot *domain.WorkflowSnapshot) (*domain.WorkflowSnapshot, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	clone := &domain.WorkflowSnapshot{}
	if err := json.Unmarshal(raw, clone); err != nil {
		return nil, err
	}
	return clone, nil
}

// commitControlLocked persists a prepared control mutation and, only after the
// write succeeds, adopts the copy as the live snapshot for its execution. A
// failed save therefore leaves the active in-memory state and its idempotency
// records untouched, so a replayed request cannot falsely report success for an
// uncommitted change that would then vanish on restart. Callers hold the lock.
func (m *Manager) commitControlLocked(executionID string, updated *domain.WorkflowSnapshot) error {
	updated.Revision++
	updated.UpdatedAt = m.now()
	if err := m.store.SaveWorkflowSnapshot(updated); err != nil {
		m.setStorageErrorLocked(err)
		return err
	}
	if m.active != nil && m.active.snapshot.ExecutionID == executionID {
		m.active.snapshot = updated
	}
	m.notifyRevisionLocked()
	return nil
}

// ExtendRequest raises a loop's effective iteration cap beyond its declared
// max_iterations. It is a manual, durable, audited action: the system itself
// never loops unboundedly, so every cap increase is an explicit request bound
// to a request ID and idempotent on replay.
type ExtendRequest struct {
	RequestID     string
	AddIterations int
}

// StopRequest durably records the intent to finish a loop's current iteration
// without starting another. It is a manual break: it neither cancels running
// workers nor waives failure, interruption, or unresolved-classification holds.
type StopRequest struct {
	RequestID string
}

// ExtendLoop raises the effective cap of an unfinished loop by a positive
// number of iterations. Identical accepted requests replay without a second
// increase even after the loop or execution finishes or the process restarts;
// reusing the request ID for a different loop or amount conflicts. The replay
// lookup precedes lifecycle checks, while new requests against done loops or
// terminal/cancelling executions conflict. Raising the cap lets the next
// reconcile clear a loop_exhausted hold, so no special un-hold path exists.
func (m *Manager) ExtendLoop(executionID, loopName string, req ExtendRequest) (domain.WorkflowExecutionView, bool, error) {
	if req.RequestID == "" {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}
	if req.AddIterations < 1 {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: add_iterations must be a positive integer, got %d", ErrInvalidRequest, req.AddIterations)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, err := m.loadSnapshotLocked(executionID)
	if err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	if _, declared := snapshot.Definition.Loops[loopName]; !declared {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: loop %q", ErrLoopNotFound, loopName)
	}

	// Accepted-request replay and conflict detection precede lifecycle
	// checks so an identical extension still succeeds after completion.
	amount := strconv.Itoa(req.AddIterations)
	for _, event := range snapshot.Controls {
		if event.Type != "loop_extend" || event.RequestID != req.RequestID {
			continue
		}
		if event.Loop == loopName && event.Detail == amount {
			return m.buildViewLocked(snapshot), false, nil
		}
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: extend request id already used for another loop or amount", ErrLoopControlConflict)
	}

	loop := snapshot.Loops[loopName]
	if loop == nil {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: loop %q", ErrLoopNotFound, loopName)
	}
	if snapshot.Mode == domain.WorkflowModeCancelling || snapshot.State.Terminal() {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: execution is %s", ErrLoopControlConflict, snapshot.State)
	}
	if loop.State == domain.WorkflowLoopDone {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: loop %q is done", ErrLoopControlConflict, loopName)
	}

	// Prepare the extension on a deep copy and adopt it only after the save
	// succeeds, so a failed write leaves neither an uncommitted cap increase nor
	// its replay record on the live snapshot.
	updated, err := cloneWorkflowSnapshot(snapshot)
	if err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	now := m.now()
	updated.Loops[loopName].ExtendedIterations += req.AddIterations
	event := domain.WorkflowControlEvent{
		Type: "loop_extend", At: now, RequestID: req.RequestID,
		Loop: loopName, Detail: amount,
	}
	updated.Controls = append(updated.Controls, event)
	updated.AttentionReasons = collectAttentionReasons(updated)
	if len(updated.AttentionReasons) == 0 && !updated.State.Terminal() {
		updated.State = stateForMode(updated)
	}
	if err := m.commitControlLocked(executionID, updated); err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	_ = m.store.AppendWorkflowEvent(executionID, event)
	m.reconcileLocked()
	m.Notify()
	return m.buildViewLocked(updated), true, nil
}

// StopLoop records a durable manual-break intent for an unfinished loop. It
// lets the current iteration finish under normal dependency and pause rules,
// forbids the next iteration, and the loop is marked done by the reconcile
// pass only once its current iteration settles acceptably. Intent survives
// recovery and retries in the same iteration. Repeated accepted requests
// replay without new effects; a new request while intent is pending succeeds
// without advancing. New requests against done loops or terminal/cancelling
// executions conflict.
func (m *Manager) StopLoop(executionID, loopName string, req StopRequest) (domain.WorkflowExecutionView, bool, error) {
	if req.RequestID == "" {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, err := m.loadSnapshotLocked(executionID)
	if err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	if _, declared := snapshot.Definition.Loops[loopName]; !declared {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: loop %q", ErrLoopNotFound, loopName)
	}

	// Accepted-request replay precedes lifecycle checks so a lost response can
	// be retried safely even after the loop or execution finished; reuse of the
	// request ID for another loop conflicts.
	for _, event := range snapshot.Controls {
		if event.Type != "loop_stop" || event.RequestID != req.RequestID {
			continue
		}
		if event.Loop == loopName {
			return m.buildViewLocked(snapshot), false, nil
		}
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: stop request id already used for another loop", ErrLoopControlConflict)
	}

	loop := snapshot.Loops[loopName]
	if loop == nil {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: loop %q", ErrLoopNotFound, loopName)
	}
	if snapshot.Mode == domain.WorkflowModeCancelling || snapshot.State.Terminal() {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: execution is %s", ErrLoopControlConflict, snapshot.State)
	}
	if loop.State == domain.WorkflowLoopDone {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: loop %q is done", ErrLoopControlConflict, loopName)
	}

	// Record the intent on a deep copy and adopt it only after the save
	// succeeds, so a failed write leaves neither an uncommitted stop flag nor
	// its replay record on the live snapshot.
	updated, err := cloneWorkflowSnapshot(snapshot)
	if err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	now := m.now()
	changed := !loop.StopRequested
	updated.Loops[loopName].StopRequested = true
	event := domain.WorkflowControlEvent{
		Type: "loop_stop", At: now, RequestID: req.RequestID, Loop: loopName,
	}
	updated.Controls = append(updated.Controls, event)
	updated.AttentionReasons = collectAttentionReasons(updated)
	if len(updated.AttentionReasons) == 0 && !updated.State.Terminal() {
		updated.State = stateForMode(updated)
	}
	if err := m.commitControlLocked(executionID, updated); err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	_ = m.store.AppendWorkflowEvent(executionID, event)
	// Attempt local resolution now; if the iteration has not settled
	// acceptably the intent stays pending until a later reconcile.
	m.reconcileLocked()
	m.Notify()
	return m.buildViewLocked(updated), changed, nil
}
