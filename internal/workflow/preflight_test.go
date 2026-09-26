package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// chainWithFailedConsumer builds an execution whose predecessor succeeded and
// whose consumer b failed, leaving b retryable.
func chainWithFailedConsumer(t *testing.T) *managerFixture {
	t.Helper()
	def := domain.WorkflowDefinition{
		Version: 1, Name: "target-only", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "A."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2")
	if _, _, err := fx.m.Create(context.Background(), "req-target"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "a"), "a result")
	c := <-fx.m.completions
	fx.m.handleCompletion(c)
	fx.pump()
	fx.exec.releaseFailure(mustFindRunForTask(t, fx, "b"), "b failed")
	c = <-fx.m.completions
	fx.m.handleCompletion(c)
	fx.pump()
	return fx
}

// TestRetryAcceptanceChecksOnlyItsTarget proves an unrelated agent's broken
// installation cannot reject an otherwise eligible retry.
func TestRetryAcceptanceChecksOnlyItsTarget(t *testing.T) {
	fx := chainWithFailedConsumer(t)
	// Agent a1's installation is broken; b's retry (agent a2) targets itself
	// and must still be accepted, queued behind the unrelated hold.
	fx.exec.brokenAgent = "a1"
	view, created, err := fx.m.RetryTask(context.Background(), "wf_000001", "b", RetryRequest{
		RequestID:       "req-retry-b",
		ExpectedAttempt: 1,
	})
	if err != nil || !created {
		t.Fatalf("eligible retry must be accepted despite unrelated failure: created=%v err=%v", created, err)
	}
	if task := findTaskView(view, "b"); task.State != domain.WorkflowTaskPending && task.State != domain.WorkflowTaskReady {
		t.Fatalf("queued retry must make the task schedulable-again candidate, got %v", task.State)
	}
}

// TestRetryTargetFailureDoesNotConsumeRequestID proves a broken target is
// rejected and the same ID is admissible after repair.
func TestRetryTargetFailureDoesNotConsumeRequestID(t *testing.T) {
	fx := chainWithFailedConsumer(t)
	fx.exec.brokenAgent = "a2"
	_, _, err := fx.m.RetryTask(context.Background(), "wf_000001", "b", RetryRequest{
		RequestID:       "req-repair",
		ExpectedAttempt: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "a2") {
		t.Fatalf("broken target must be rejected naming its own failure, got %v", err)
	}
	fx.exec.brokenAgent = ""
	if _, created, err := fx.m.RetryTask(context.Background(), "wf_000001", "b", RetryRequest{
		RequestID:       "req-repair",
		ExpectedAttempt: 1,
	}); err != nil || !created {
		t.Fatalf("same request ID must be admissible after repair: created=%v err=%v", created, err)
	}
}

// TestSnapshotDiagnosticsExposeRestartGuidance covers the observation
// contract: a latched issue carries restart_required and the derived reasons
// hold the execution.
func TestSnapshotDiagnosticsExposeRestartGuidance(t *testing.T) {
	snapshot := &domain.WorkflowSnapshot{
		BackendPreflight: &domain.PreflightReport{Issues: []domain.PreflightIssue{{
			Phase:           domain.InstallationPhaseReadiness,
			Backend:         "opencode",
			Agents:          []string{"Reviewer"},
			Component:       domain.ComponentService,
			Code:            domain.CodeStartFailed,
			RestartRequired: true,
			Message:         "the owned OpenCode server is not available; correct the cause and restart Squad explicitly",
		}}},
	}
	reasons := collectAttentionReasons(snapshot)
	found := false
	for _, reason := range reasons {
		if reason == "backend_preflight_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("backend_preflight_failed must hold the execution, got %v", reasons)
	}
}
