package domain

import (
	"encoding/json"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestWorkflowDefinitionJSONRoundTrip(t *testing.T) {
	def := WorkflowDefinition{
		Version:            WorkflowSchemaVersion,
		Name:               "review-and-verify",
		MaxParallel:        2,
		TaskTimeoutSeconds: DefaultWorkflowTaskTimeoutSec,
		Tasks: map[string]WorkflowTaskDefinition{
			"verify": {
				Agent:                     "verifier",
				Prompt:                    "Validate findings.",
				Needs:                     []string{"review_a", "review_b"},
				MinSuccessfulDependencies: 1,
			},
		},
	}

	data, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded WorkflowDefinition
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Name != def.Name || decoded.MaxParallel != def.MaxParallel || decoded.TaskTimeoutSeconds != def.TaskTimeoutSeconds {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
	got := decoded.Tasks["verify"]
	if got.Agent != "verifier" || got.MinSuccessfulDependencies != 1 || len(got.Needs) != 2 {
		t.Fatalf("task round trip mismatch: %+v", got)
	}
	if got.AllowedToFail {
		t.Fatal("allowed_to_fail must default to false when omitted")
	}
}

func TestWorkflowTaskDefinitionYAMLDefaultOmissions(t *testing.T) {
	body := "agent: reviewer\nprompt: Review.\n"
	var task WorkflowTaskDefinition
	if err := yaml.Unmarshal([]byte(body), &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if task.Needs != nil {
		t.Fatalf("needs must default to nil, got %v", task.Needs)
	}
	if task.AllowedToFail {
		t.Fatal("allowed_to_fail must default to false")
	}
	if task.MinSuccessfulDependencies != 0 {
		t.Fatalf("min_successful_dependencies must default to 0, got %d", task.MinSuccessfulDependencies)
	}
	if task.TimeoutSeconds != 0 {
		t.Fatalf("timeout_seconds must default to 0 (inherit workflow), got %d", task.TimeoutSeconds)
	}
}

func TestEffectiveTimeoutSecondsPrefersTaskOverride(t *testing.T) {
	task := WorkflowTaskDefinition{TimeoutSeconds: 30}
	if got := task.EffectiveTimeoutSeconds(1800); got != 30 {
		t.Fatalf("override not applied: %d", got)
	}
	plain := WorkflowTaskDefinition{}
	if got := plain.EffectiveTimeoutSeconds(DefaultWorkflowTaskTimeoutSec); got != 1800 {
		t.Fatalf("fallback not applied: %d", got)
	}
	if got := plain.EffectiveTimeoutSeconds(0); got != 0 {
		t.Fatalf("zero fallback must stay zero, got %d", got)
	}
}

func TestWorkflowSnapshotJSONRoundTrip(t *testing.T) {
	now := parseTestTime(t, "2026-01-02T03:04:05Z")
	snap := WorkflowSnapshot{
		SchemaVersion:    WorkflowSchemaVersion,
		ExecutionID:      "wf_000001",
		Revision:         7,
		Definition:       WorkflowDefinition{Version: 1, Name: "review", MaxParallel: 1, Tasks: map[string]WorkflowTaskDefinition{"a": {Agent: "x", Prompt: "p"}}},
		DefinitionHash:   "abc",
		Agents:           map[string]AgentSpec{"x": {Name: "x", Backend: "fake"}},
		RequestID:        "req-1",
		State:            WorkflowNeedsAttention,
		Mode:             WorkflowModeRunning,
		AttentionReasons: []string{"interrupted_attempt:a:1"},
		Tasks: map[string]*WorkflowTaskExecution{
			"a": {
				TaskID: "a",
				Agent:  "x",
				State:  WorkflowTaskInterrupted,
				Attempts: []WorkflowAttempt{{
					Attempt:      1,
					RunID:        "wrun_000001_000001",
					State:        WorkflowAttemptInterrupted,
					ResultPath:   "tasks/a/attempts/1/response.txt",
					ResultSize:   3,
					ResultSHA256: "ff",
					ReservedAt:   &now,
				}},
			},
		},
		NextRunSeq: 1,
		RetryRequests: map[string]WorkflowRetryRecord{
			"retry-1": {RequestID: "retry-1", TaskID: "a", Attempt: 2, CreatedAt: now},
		},
		Controls:  []WorkflowControlEvent{{Type: "pause", At: now}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded WorkflowSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ExecutionID != snap.ExecutionID || decoded.Revision != snap.Revision || decoded.NextRunSeq != snap.NextRunSeq {
		t.Fatalf("snapshot round trip mismatch: %+v", decoded)
	}
	task := decoded.Tasks["a"]
	if task == nil || task.State != WorkflowTaskInterrupted || len(task.Attempts) != 1 {
		t.Fatalf("task round trip mismatch: %+v", task)
	}
	attempt := task.LastAttempt()
	if attempt == nil || attempt.RunID != "wrun_000001_000001" || attempt.ReservedAt == nil || !attempt.ReservedAt.Equal(now) {
		t.Fatalf("attempt round trip mismatch: %+v", attempt)
	}
	if decoded.RetryRequests["retry-1"].TaskID != "a" {
		t.Fatalf("retry records lost: %+v", decoded.RetryRequests)
	}
}

func TestWorkflowStateTerminal(t *testing.T) {
	terminal := []WorkflowState{WorkflowCancelled, WorkflowSucceeded, WorkflowCompletedWithError, WorkflowFailed}
	for _, state := range terminal {
		if !state.Terminal() {
			t.Fatalf("%s must be terminal", state)
		}
	}
	nonTerminal := []WorkflowState{WorkflowRunning, WorkflowPaused, WorkflowNeedsAttention, WorkflowCancelling}
	for _, state := range nonTerminal {
		if state.Terminal() {
			t.Fatalf("%s must not be terminal", state)
		}
	}
}

func TestWorkflowTaskStateSettled(t *testing.T) {
	settled := []WorkflowTaskState{WorkflowTaskSucceeded, WorkflowTaskFailed, WorkflowTaskBlocked, WorkflowTaskCancelled}
	for _, state := range settled {
		if !state.Settled() {
			t.Fatalf("%s must be settled", state)
		}
	}
	unsettled := []WorkflowTaskState{WorkflowTaskPending, WorkflowTaskReady, WorkflowTaskDispatching, WorkflowTaskRunning, WorkflowTaskInterrupted}
	for _, state := range unsettled {
		if state.Settled() {
			t.Fatalf("%s must not be settled", state)
		}
	}
}

func TestWorkflowAttemptStateCommitted(t *testing.T) {
	committed := []WorkflowAttemptState{WorkflowAttemptSucceeded, WorkflowAttemptFailed, WorkflowAttemptCancelled}
	for _, state := range committed {
		if !state.Committed() {
			t.Fatalf("%s must be committed", state)
		}
	}
	uncertain := []WorkflowAttemptState{WorkflowAttemptQueued, WorkflowAttemptDispatching, WorkflowAttemptRunning, WorkflowAttemptCancelling, WorkflowAttemptInterrupted}
	for _, state := range uncertain {
		if state.Committed() {
			t.Fatalf("%s must not be committed", state)
		}
	}
}

func parseTestTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return parsed
}
