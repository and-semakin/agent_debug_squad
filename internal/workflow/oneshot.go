package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// ErrExecutionSealed reports that a one-shot fence rejected a control: the
// selected execution durably reached terminal state and can no longer be
// reopened by this process.
var ErrExecutionSealed = errors.New("workflow execution is sealed after terminal completion")

// SetOneShotTarget installs the immutable one-shot fence target. Once the
// named execution durably commits a terminal state, state-changing controls
// that could reopen it are rejected for the lifetime of this manager. The
// target cannot be changed or cleared.
func (m *Manager) SetOneShotTarget(executionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.oneShotID == "" {
		m.oneShotID = executionID
	}
}

// oneShotSealLocked seals the fence when a terminal snapshot of the selected
// execution has been durably persisted. Called from the serialized commit
// paths, so a control serialized before the commit proceeds under its normal
// rules and one serialized after is rejected.
func (m *Manager) oneShotSealLocked(snapshot *domain.WorkflowSnapshot) {
	if m.oneShotID != "" && snapshot.ExecutionID == m.oneShotID && snapshot.State.Terminal() {
		m.oneShotSealed = true
	}
}

// oneShotReopenGuardLocked rejects controls that could reopen the sealed
// selected execution.
func (m *Manager) oneShotReopenGuardLocked(executionID string) error {
	if m.oneShotID != "" && executionID == m.oneShotID && m.oneShotSealed {
		return fmt.Errorf("%w: %s", ErrExecutionSealed, executionID)
	}
	return nil
}

// OwnsRun reports whether a run ID belongs to the one-shot selected
// execution. Workflow run IDs embed the execution number, so completed runs
// of the selected execution still resolve after their workers stopped.
func (m *Manager) OwnsRun(runID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.oneShotID == "" || !strings.HasPrefix(runID, "wrun_") {
		return false
	}
	if m.active != nil && m.active.snapshot.ExecutionID == m.oneShotID {
		if _, live := m.active.live[runID]; live {
			return true
		}
	}
	return runID == selectedRunPrefix(m.oneShotID) || strings.HasPrefix(runID, selectedRunPrefix(m.oneShotID))
}

// selectedRunPrefix returns the run-ID prefix shared by every run of the
// selected execution: wrun_<execution number>_.
func selectedRunPrefix(executionID string) string {
	return fmt.Sprintf("wrun_%s_", executionNumber(executionID))
}

// OutstandingRunIDs lists the selected execution's run IDs whose workers have
// not reported back, so a failed teardown can name the unresolved work.
func (m *Manager) OutstandingRunIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.activeLiveLocked()))
	for runID := range m.activeLiveLocked() {
		ids = append(ids, runID)
	}
	return ids
}

// FatalError exposes the scheduler's first persistence failure so a one-shot
// observer can stop waiting instead of mistaking the resulting attention hold
// for ordinary intervention.
func (m *Manager) FatalError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storageErr
}

// StartScoped is the one-shot variant of Start: it recovers only the selected
// execution and fails before any mutation when another nonterminal execution
// exists. An empty selectedID starts the loop without recovering anything;
// the caller then creates the selected execution explicitly.
func (m *Manager) StartScoped(ctx context.Context, selectedID string) error {
	loopCtx, cancel := context.WithCancel(ctx)
	m.stop = cancel
	m.mu.Lock()
	m.judgeCtx = loopCtx
	m.mu.Unlock()

	ids, err := m.store.ListWorkflowExecutions()
	if err != nil {
		cancel()
		return fmt.Errorf("list workflow executions: %w", err)
	}
	var selected *domain.WorkflowSnapshot
	for _, id := range ids {
		snapshot, err := m.store.LoadWorkflowSnapshot(id)
		if err != nil {
			cancel()
			return fmt.Errorf("inspect workflow %s: %w", id, err)
		}
		if snapshot.State.Terminal() {
			continue
		}
		if selectedID == "" || snapshot.ExecutionID != selectedID {
			cancel()
			return fmt.Errorf("refusing one-shot startup: execution %s is nonterminal and not selected by this run", snapshot.ExecutionID)
		}
		selected = &snapshot
	}
	if selectedID != "" && selected == nil {
		cancel()
		return fmt.Errorf("%w: selected execution %s is missing or terminal", ErrExecutionNotFound, selectedID)
	}
	if selected != nil {
		if err := m.recoverExecution(selected); err != nil {
			cancel()
			return err
		}
		m.beginRecoveryPreflight()
	}

	go m.loop(loopCtx)
	return nil
}

// CreateSelected is the one-shot variant of Create: the created (or replayed)
// execution becomes the fence target inside the same serialized step, so no
// control can observe the execution before the fence is installed.
func (m *Manager) CreateSelected(ctx context.Context, requestID string) (domain.WorkflowExecutionView, bool, error) {
	return m.create(ctx, requestID, true)
}

// ObserveTerminal waits for the selected execution's durable terminal state
// without treating intervention, pause, cancellation-in-progress, pending
// permissions, or worker counts as completion. It returns the coherent view
// plus a fatal persistence error: a storage failure stops scheduling and is
// surfaced here instead of ever reading as a successful outcome. onUpdate
// receives each changed view (nonterminal included) for intervention notices.
// Context cancellation returns the current view with nil error; the caller
// owns the reason it stopped waiting.
func (m *Manager) ObserveTerminal(ctx context.Context, executionID string, onUpdate func(domain.WorkflowExecutionView)) (domain.WorkflowExecutionView, error) {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	lastRevision := int64(-1)
	for {
		view, err := m.View(executionID)
		if err != nil {
			return view, err
		}
		if view.Revision != lastRevision {
			lastRevision = view.Revision
			if onUpdate != nil {
				onUpdate(view)
			}
		}
		if view.State.Terminal() {
			return view, nil
		}
		if ferr := m.FatalError(); ferr != nil {
			return view, ferr
		}
		if ctx.Err() != nil {
			return view, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}
}
