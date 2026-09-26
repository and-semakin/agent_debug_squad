package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/judge"
)

// scriptedJudge is a deterministic in-process Judge: it records every
// request and answers through a replaceable responder. A responder may block
// on a channel to hold classification in flight.
type scriptedJudge struct {
	mu       sync.Mutex
	requests []judge.Request
	respond  func(judge.Request) (judge.Decision, error)
}

func (s *scriptedJudge) Decide(_ context.Context, req judge.Request) (judge.Decision, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	respond := s.respond
	s.mu.Unlock()
	if respond == nil {
		return judge.Decision{}, errors.New("scripted judge: no response configured")
	}
	return respond(req)
}

func (s *scriptedJudge) setRespond(respond func(judge.Request) (judge.Decision, error)) {
	s.mu.Lock()
	s.respond = respond
	s.mu.Unlock()
}

func (s *scriptedJudge) lastRequest() judge.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return judge.Request{}
	}
	return s.requests[len(s.requests)-1]
}

func (s *scriptedJudge) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func passedDecision(confidence float64) judge.Decision {
	return judge.Decision{
		Choice:        "passed",
		Confidence:    confidence,
		Probabilities: map[string]float64{"passed": confidence, "failed": 1 - confidence},
		Model:         "typesafe/jev-1.13-20260917",
		Raw:           json.RawMessage(`{"model":"typesafe/jev-1.13-20260917","answers":{"verdict":{"type":"choice","choice":"passed"}}}`),
	}
}

type judgedFixture struct {
	*managerFixture
	judge *scriptedJudge
}

func newJudgedFixture(t *testing.T, def domain.WorkflowDefinition, agentNames ...string) *judgedFixture {
	t.Helper()
	fx := newManagerFixture(t, def, agentNames...)
	j := &scriptedJudge{}
	fx.m.SetJudge(j)
	return &judgedFixture{managerFixture: fx, judge: j}
}

// restartJudged simulates a process restart over the same session state
// with a fresh judged manager running the real reconciliation loop.
func restartJudged(t *testing.T, fx *judgedFixture) *judgedFixture {
	t.Helper()
	exec := newFakeExecutor()
	m := NewManager(fx.m.cfg, fx.st, exec)
	m.cancelGrace = fx.m.cancelGrace
	j := &scriptedJudge{}
	m.SetJudge(j)
	return &judgedFixture{managerFixture: &managerFixture{m: m, exec: exec, st: fx.st}, judge: j}
}

func verdictChainDefinition() domain.WorkflowDefinition {
	def := chainDefinition()
	taskA := def.Tasks["a"]
	taskA.Verdicts = map[string]string{"passed": "work accepted", "failed": "work rejected"}
	def.Tasks["a"] = taskA
	return def
}

// pumpUntil drives deterministic reconciliation rounds until cond holds or
// the timeout expires; classification goroutines post asynchronously, so the
// condition is polled between pumps.
func (fx *managerFixture) pumpUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fx.pump()
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	fx.pump()
	return cond()
}

func attemptState(t *testing.T, fx *managerFixture, taskID string) (domain.WorkflowAttemptState, *domain.WorkflowAttemptView) {
	t.Helper()
	view, err := fx.m.View("wf_000001")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	for _, task := range view.Tasks {
		if task.TaskID != taskID {
			continue
		}
		if len(task.Attempts) == 0 {
			return "", nil
		}
		last := task.Attempts[len(task.Attempts)-1]
		return last.State, &last
	}
	t.Fatalf("task %s not found", taskID)
	return "", nil
}

func TestVerdictSettlesAfterJudgingAndDependentsWait(t *testing.T) {
	fx := newJudgedFixture(t, verdictChainDefinition(), "a1", "a2", "a3")
	gate := make(chan struct{})
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		<-gate
		return passedDecision(0.93), nil
	})
	if _, _, err := fx.m.Create(context.Background(), "req-verdict"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	runA := fx.exec.liveRunIDs()[0]
	fx.exec.releaseSuccess(runA, "all good")
	fx.pump()

	// The response is committed, but the dependent waits for the verdict.
	state, _ := attemptState(t, fx.managerFixture, "a")
	if state != domain.WorkflowAttemptJudging {
		t.Fatalf("attempt state after response: %s", state)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("dependent dispatched before verdict settlement: %v", fx.exec.dispatched())
	}
	if response, err := fx.st.ReadWorkflowArtifact("wf_000001", "tasks/a/attempts/1/response.txt"); err != nil || string(response) != "all good" {
		t.Fatalf("response artifact: %q %v", response, err)
	}

	close(gate)
	if !fx.pumpUntil(3*time.Second, func() bool {
		state, _ := attemptState(t, fx.managerFixture, "a")
		return state == domain.WorkflowAttemptSucceeded
	}) {
		t.Fatal("attempt never settled with a verdict")
	}
	_, attemptView := attemptState(t, fx.managerFixture, "a")
	verdict := attemptView.Verdict
	if verdict == nil || verdict.Value != "passed" || verdict.Source != "judge" ||
		verdict.Model != "typesafe/jev-1.13-20260917" || verdict.Threshold != domain.DefaultConfidenceThreshold {
		t.Fatalf("verdict record: %+v", verdict)
	}
	if verdict.Probabilities["passed"] != 0.93 || len(verdict.Probabilities) != 2 {
		t.Fatalf("distribution: %v", verdict.Probabilities)
	}
	if decision, err := fx.st.ReadWorkflowArtifact("wf_000001", "tasks/a/attempts/1/decision.json"); err != nil || len(decision) == 0 {
		t.Fatalf("decision artifact: %q %v", decision, err)
	}

	// The chain continues and finishes as an ordinary workflow.
	for len(fx.exec.liveRunIDs()) > 0 {
		fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "next")
		fx.pump()
	}
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, err := fx.m.View("wf_000001")
		return err == nil && view.State == domain.WorkflowSucceeded
	}) {
		t.Fatal("workflow did not succeed")
	}

	// The verdict survives snapshot persistence.
	snapshot, err := fx.st.LoadWorkflowSnapshot("wf_000001")
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	persisted := snapshot.Tasks["a"].Attempts[0].Verdict
	if persisted == nil || persisted.Value != "passed" || persisted.Source != "judge" {
		t.Fatalf("persisted verdict: %+v", persisted)
	}
}

func TestUncertainVerdictHoldsForInspection(t *testing.T) {
	fx := newJudgedFixture(t, verdictChainDefinition(), "a1", "a2", "a3")
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return passedDecision(0.45), nil
	})
	if _, _, err := fx.m.Create(context.Background(), "req-hold"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "ambiguous output")
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, err := fx.m.View("wf_000001")
		return err == nil && view.State == domain.WorkflowNeedsAttention
	}) {
		t.Fatal("uncertain verdict must hold with needs_attention")
	}
	state, attempt := attemptState(t, fx.managerFixture, "a")
	if state != domain.WorkflowAttemptJudging {
		t.Fatalf("held attempt state: %s", state)
	}
	if attempt.Verdict == nil || attempt.Verdict.Value != domain.ReservedVerdictName {
		t.Fatalf("held verdict record: %+v", attempt.Verdict)
	}
	if attempt.Verdict.Probabilities["passed"] != 0.45 {
		t.Fatalf("distribution must be exposed while held: %v", attempt.Verdict.Probabilities)
	}
	view, _ := fx.m.View("wf_000001")
	found := false
	for _, reason := range view.AttentionReasons {
		if strings.HasPrefix(reason, "uncertain_verdict:a:1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("attention reasons: %v", view.AttentionReasons)
	}
	if len(fx.exec.dispatched()) != 1 {
		t.Fatalf("dependent dispatched during hold: %v", fx.exec.dispatched())
	}
}

func TestUncertainVerdictErrorFailsAttempt(t *testing.T) {
	def := verdictChainDefinition()
	def.OnUncertain = domain.WorkflowOnUncertainError
	fx := newJudgedFixture(t, def, "a1", "a2", "a3")
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return passedDecision(0.5), nil
	})
	if _, _, err := fx.m.Create(context.Background(), "req-uncertain-error"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "ambiguous")
	if !fx.pumpUntil(3*time.Second, func() bool {
		state, _ := attemptState(t, fx.managerFixture, "a")
		return state == domain.WorkflowAttemptFailed
	}) {
		t.Fatal("on_uncertain: error must fail the attempt")
	}
	_, attempt := attemptState(t, fx.managerFixture, "a")
	if attempt.Reason != "uncertain_verdict" {
		t.Fatalf("failure reason: %q", attempt.Reason)
	}
	if attempt.Verdict == nil || attempt.Verdict.Value != domain.ReservedVerdictName || attempt.Verdict.Probabilities == nil {
		t.Fatalf("uncertain failure keeps the distribution: %+v", attempt.Verdict)
	}
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, err := fx.m.View("wf_000001")
		return err == nil && view.State == domain.WorkflowFailed
	}) {
		t.Fatal("mandatory uncertainty failure must fail the execution")
	}
}

func TestConfidenceThresholdGating(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		threshold, machine, confidence float64
		policy                         string
		want                           domain.WorkflowAttemptState
	}{
		{"default_equal", 0, 0, 0.70, "", domain.WorkflowAttemptSucceeded},
		{"default_072", 0, 0, 0.72, "", domain.WorkflowAttemptSucceeded},
		{"default_078", 0, 0, 0.78, "", domain.WorkflowAttemptSucceeded},
		{"default_079", 0, 0, 0.79, "", domain.WorkflowAttemptSucceeded},
		{"default_068_attention", 0, 0, 0.68, "", domain.WorkflowAttemptJudging},
		{"default_068_error", 0, 0, 0.68, domain.WorkflowOnUncertainError, domain.WorkflowAttemptFailed},
		{"explicit_08_holds", 0.8, 0, 0.79, "", domain.WorkflowAttemptJudging},
		{"explicit_08_equal", 0.8, 0, 0.8, "", domain.WorkflowAttemptSucceeded},
		{"machine_065_applies", 0, 0.6, 0.65, "", domain.WorkflowAttemptSucceeded},
		{"machine_equal", 0, 0.6, 0.6, "", domain.WorkflowAttemptSucceeded},
		{"machine_059_attention", 0, 0.6, 0.59, "", domain.WorkflowAttemptJudging},
		{"machine_059_error", 0, 0.6, 0.59, domain.WorkflowOnUncertainError, domain.WorkflowAttemptFailed},
		{"workflow_wins_machine", 0.8, 0.6, 0.7, "", domain.WorkflowAttemptJudging},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := verdictChainDefinition()
			def.ConfidenceThreshold = tc.threshold
			def.OnUncertain = tc.policy
			fx := newJudgedFixture(t, def, "a1", "a2", "a3")
			if tc.machine > 0 {
				fx.m.cfg.MachineBackends.Judge = &domain.MachineBackendSettings{ConfidenceThreshold: &tc.machine}
			}
			fx.judge.setRespond(func(judge.Request) (judge.Decision, error) { return passedDecision(tc.confidence), nil })
			if _, _, err := fx.m.Create(context.Background(), "req-threshold"); err != nil {
				t.Fatal(err)
			}
			fx.pump()
			fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "threshold case")
			if !fx.pumpUntil(3*time.Second, func() bool {
				state, attempt := attemptState(t, fx.managerFixture, "a")
				return state == tc.want && attempt.Verdict != nil
			}) {
				t.Fatalf("expected classified state %s", tc.want)
			}
			_, attempt := attemptState(t, fx.managerFixture, "a")
			threshold := tc.threshold
			if threshold == 0 {
				threshold = tc.machine
			}
			if threshold == 0 {
				threshold = domain.DefaultConfidenceThreshold
			}
			if attempt.Verdict.Threshold != threshold {
				t.Fatalf("recorded threshold: %+v", attempt.Verdict)
			}
			if tc.want != domain.WorkflowAttemptSucceeded {
				if attempt.Verdict.Value != domain.ReservedVerdictName || attempt.Reason != "uncertain_verdict" {
					t.Fatalf("uncertain outcome: %+v", attempt)
				}
				if tc.want == domain.WorkflowAttemptJudging {
					view, err := fx.m.View("wf_000001")
					if err != nil || view.State != domain.WorkflowNeedsAttention {
						t.Fatalf("expected attention: %+v, %v", view, err)
					}
				}
			} else if attempt.Verdict.Value != "passed" {
				t.Fatalf("verdict: %+v", attempt.Verdict)
			}
		})
	}
}

func TestMachineConfidenceThresholdDoesNotChangeSavedDefinition(t *testing.T) {
	fx := newJudgedFixture(t, verdictChainDefinition(), "a1", "a2", "a3")
	machineThreshold := 0.6
	fx.m.cfg.MachineBackends.Judge = &domain.MachineBackendSettings{ConfidenceThreshold: &machineThreshold}
	if _, _, err := fx.m.Create(context.Background(), "req-machine-default-identity"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fx.st.LoadWorkflowSnapshot("wf_000001")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Definition.ConfidenceThreshold != 0 {
		t.Fatalf("machine default was copied into saved workflow definition: %+v", snapshot.Definition)
	}
	encoded, err := json.Marshal(snapshot.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "confidence_threshold") {
		t.Fatalf("machine threshold leaked into saved definition: %s", encoded)
	}
	if got := HashWorkflowDefinition(snapshot.Definition, snapshot.Agents); got != snapshot.DefinitionHash {
		t.Fatalf("machine default changed definition hash: got %s, saved %s", got, snapshot.DefinitionHash)
	}
}

func TestJudgeUnavailableHoldsAndResumeReclassifies(t *testing.T) {
	fx := newJudgedFixture(t, verdictChainDefinition(), "a1", "a2", "a3")
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return judge.Decision{}, errors.New("provider unreachable")
	})
	if _, _, err := fx.m.Create(context.Background(), "req-unavailable"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "result")
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, err := fx.m.View("wf_000001")
		return err == nil && view.State == domain.WorkflowNeedsAttention
	}) {
		t.Fatal("judge unavailability must hold with needs_attention")
	}
	state, attempt := attemptState(t, fx.managerFixture, "a")
	if state != domain.WorkflowAttemptJudging || attempt.Reason != "judge_unavailable" {
		t.Fatalf("held attempt: %s %q", state, attempt.Reason)
	}

	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return passedDecision(0.95), nil
	})
	if _, err := fx.m.Resume(context.Background(), "wf_000001"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !fx.pumpUntil(3*time.Second, func() bool {
		state, _ := attemptState(t, fx.managerFixture, "a")
		return state == domain.WorkflowAttemptSucceeded
	}) {
		t.Fatal("resume must re-classify the held attempt")
	}
	if len(fx.exec.dispatched()) != 2 {
		t.Fatalf("dependent must dispatch after re-classification: %v", fx.exec.dispatched())
	}
}

func TestJudgeInputTruncationAndArtifactIntegrity(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "truncation", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {
				Agent:  "a1",
				Prompt: "Produce output.",
				Verdicts: map[string]string{
					"passed": "accepted",
					"failed": "rejected",
				},
			},
		},
	}
	fx := newJudgedFixture(t, def, "a1")
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return passedDecision(0.99), nil
	})
	if _, _, err := fx.m.Create(context.Background(), "req-truncate"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	huge := strings.Repeat("x", judgeMaxResponseBytes+2048)
	fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], huge)
	if !fx.pumpUntil(3*time.Second, func() bool {
		state, _ := attemptState(t, fx.managerFixture, "a")
		return state == domain.WorkflowAttemptSucceeded
	}) {
		t.Fatal("attempt never settled")
	}

	request := fx.judge.lastRequest()
	excerpt := request.State["agent_response"]
	if !strings.Contains(excerpt, "[...truncated ") || len(excerpt) > judgeMaxResponseBytes+128 {
		t.Fatalf("judge input not truncated: %d bytes, contains marker: %v", len(excerpt), strings.Contains(excerpt, "[...truncated"))
	}
	if request.Question.Type != "choice" || request.Question.Criteria["passed"] != "accepted" {
		t.Fatalf("question: %+v", request.Question)
	}
	if request.State["agent_response"] == "" || request.State["task_id"] != "a" || request.State["agent"] != "a1" {
		t.Fatalf("evidence state: %+v", request.State)
	}
	_, attempt := attemptState(t, fx.managerFixture, "a")
	if attempt.Verdict == nil || !attempt.Verdict.Truncated {
		t.Fatalf("truncation flag: %+v", attempt.Verdict)
	}
	if attempt.Result != nil && attempt.Result.Size != int64(len(huge)) {
		t.Fatalf("response size must be complete: %d", attempt.Result.Size)
	}
	if err := fx.st.VerifyWorkflowArtifact("wf_000001", "tasks/a/attempts/1/response.txt", int64(len(huge)), mustHash(t, huge)); err != nil {
		t.Fatalf("response artifact must be unmodified: %v", err)
	}
}

func mustHash(t *testing.T, content string) string {
	t.Helper()
	sum := sha256Hex(content)
	return sum
}

func TestManualOverrideResolvesHold(t *testing.T) {
	fx := newJudgedFixture(t, verdictChainDefinition(), "a1", "a2", "a3")
	machineThreshold := 0.6
	fx.m.cfg.MachineBackends.Judge = &domain.MachineBackendSettings{ConfidenceThreshold: &machineThreshold}
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return passedDecision(0.3), nil
	})
	if _, _, err := fx.m.Create(context.Background(), "req-override"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "needs a human")
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, err := fx.m.View("wf_000001")
		return err == nil && view.State == domain.WorkflowNeedsAttention
	}) {
		t.Fatal("expected an uncertain hold")
	}

	// Undeclared verdict names are rejected.
	if _, _, err := fx.m.OverrideVerdict("wf_000001", "a", 1, OverrideVerdictRequest{RequestID: "ovr-bad", Verdict: "nope"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("undeclared verdict: %v", err)
	}
	// Unknown identifiers.
	if _, _, err := fx.m.OverrideVerdict("wf_000001", "zzz", 1, OverrideVerdictRequest{RequestID: "ovr-task", Verdict: "passed"}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("unknown task: %v", err)
	}
	if _, _, err := fx.m.OverrideVerdict("wf_000001", "a", 9, OverrideVerdictRequest{RequestID: "ovr-attempt", Verdict: "passed"}); !errors.Is(err, ErrAttemptNotFound) {
		t.Fatalf("unknown attempt: %v", err)
	}

	view, created, err := fx.m.OverrideVerdict("wf_000001", "a", 1, OverrideVerdictRequest{RequestID: "ovr-1", Verdict: "passed"})
	if err != nil || !created {
		t.Fatalf("override: created=%v err=%v", created, err)
	}
	if view.State == domain.WorkflowNeedsAttention {
		t.Fatalf("override must clear the hold: %s", view.State)
	}
	_, attempt := attemptState(t, fx.managerFixture, "a")
	if attempt.State != domain.WorkflowAttemptSucceeded || attempt.Verdict == nil ||
		attempt.Verdict.Source != "manual" || attempt.Verdict.Value != "passed" || attempt.Verdict.Threshold != 0.6 {
		t.Fatalf("overridden attempt: %+v", attempt)
	}

	// Idempotent replay.
	_, created, err = fx.m.OverrideVerdict("wf_000001", "a", 1, OverrideVerdictRequest{RequestID: "ovr-1", Verdict: "passed"})
	if err != nil || created {
		t.Fatalf("replay: created=%v err=%v", created, err)
	}
	// Conflicting replay with the same request ID.
	if _, _, err := fx.m.OverrideVerdict("wf_000001", "a", 1, OverrideVerdictRequest{RequestID: "ovr-1", Verdict: "failed"}); !errors.Is(err, ErrVerdictConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	// Settled attempts reject overrides.
	if _, _, err := fx.m.OverrideVerdict("wf_000001", "a", 1, OverrideVerdictRequest{RequestID: "ovr-2", Verdict: "failed"}); !errors.Is(err, ErrVerdictConflict) {
		t.Fatalf("settled attempt: %v", err)
	}

	if !fx.pumpUntil(3*time.Second, func() bool {
		return len(fx.exec.dispatched()) >= 2
	}) {
		t.Fatal("dependent must dispatch after the override")
	}
}

func TestCancellationDuringJudging(t *testing.T) {
	fx := newJudgedFixture(t, verdictChainDefinition(), "a1", "a2", "a3")
	gate := make(chan struct{})
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		<-gate
		return passedDecision(0.9), nil
	})
	if _, _, err := fx.m.Create(context.Background(), "req-cancel-judging"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "cancelled mid-judging")
	fx.pump()
	if _, err := fx.m.Cancel("wf_000001", CancelOptions{}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, err := fx.m.View("wf_000001")
		return err == nil && view.State == domain.WorkflowCancelled
	}) {
		t.Fatal("execution must cancel with a judging attempt")
	}
	state, attempt := attemptState(t, fx.managerFixture, "a")
	if state != domain.WorkflowAttemptCancelled {
		t.Fatalf("judging attempt after cancel: %s", state)
	}
	if attempt.Verdict != nil {
		t.Fatalf("no verdict is recorded for a cancelled attempt: %+v", attempt.Verdict)
	}

	// A late classification result is dropped, not resurrected.
	close(gate)
	fx.pump()
	state, _ = attemptState(t, fx.managerFixture, "a")
	if state != domain.WorkflowAttemptCancelled {
		t.Fatalf("late judgement must not resurrect the attempt: %s", state)
	}
}

func TestRecoveryReclassifiesJudgingAttempts(t *testing.T) {
	fx := newJudgedFixture(t, verdictChainDefinition(), "a1", "a2", "a3")
	gate := make(chan struct{})
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		<-gate
		return passedDecision(0.9), nil
	})
	if _, _, err := fx.m.Create(context.Background(), "req-recovery-judging"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "recovered response")
	fx.pump()
	state, _ := attemptState(t, fx.managerFixture, "a")
	if state != domain.WorkflowAttemptJudging {
		t.Fatalf("precondition: attempt must be mid-judging, got %s", state)
	}

	// Crash: a fresh judged manager recovers over the same session state.
	second := restartJudged(t, fx)
	machineThreshold := 0.6
	second.m.cfg.MachineBackends.Judge = &domain.MachineBackendSettings{ConfidenceThreshold: &machineThreshold}
	second.judge.setRespond(func(judge.Request) (judge.Decision, error) {
		return passedDecision(0.65), nil
	})
	if err := second.m.Start(context.Background()); err != nil {
		close(gate)
		t.Fatalf("start recovered manager: %v", err)
	}
	// The first fixture's blocked classification reads from gate; closing it
	// lets both fixtures' goroutines drain so the test can stop cleanly.
	defer close(gate)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _ = recoveryAttemptState(second)
		if state == domain.WorkflowAttemptSucceeded {
			break
		}
		if state == domain.WorkflowAttemptInterrupted {
			second.m.Stop(stopContext())
			t.Fatal("recovery must not interrupt a mid-judging attempt")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if state != domain.WorkflowAttemptSucceeded {
		second.m.Stop(stopContext())
		t.Fatalf("recovered attempt state: %s", state)
	}

	_, recovered := recoveryAttemptState(second)
	if recovered.Verdict == nil || recovered.Verdict.Threshold != 0.6 {
		second.m.Stop(stopContext())
		t.Fatalf("recovered omitted threshold must use current machine 0.6: %+v", recovered)
	}

	// The agent work never repeats: the recovered executor dispatches only
	// b and then c (max_parallel: 1), never a again.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(second.exec.dispatched()) < 1 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := len(second.exec.dispatched()); got != 1 {
		second.m.Stop(stopContext())
		t.Fatalf("recovery must not re-run the agent, dispatched=%d", got)
	}
	// Release the recovered chain's runs so Stop joins cleanly under the
	// fake executor, which never completes cancelled work on its own.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(second.exec.dispatched()) < 2 {
		if live := second.exec.liveRunIDs(); len(live) > 0 {
			second.exec.releaseSuccess(live[0], "done")
		}
		time.Sleep(2 * time.Millisecond)
	}
	second.m.Stop(stopContext())
	if got := len(second.exec.dispatched()); got != 2 {
		t.Fatalf("recovered chain must dispatch exactly b and c, dispatched=%d", got)
	}
}

// stopContext bounds manager Stop calls in tests so a fake executor that
// never reports cancelled work cannot hang a test forever.
func stopContext() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	// The manager has drained by then or the test already failed.
	time.AfterFunc(6*time.Second, cancel)
	return ctx
}

// recoveryAttemptState reads the last attempt state of task "a" through the
// recovered manager's live loop.
func recoveryAttemptState(fx *judgedFixture) (domain.WorkflowAttemptState, *domain.WorkflowAttemptView) {
	view, err := fx.m.View("wf_000001")
	if err != nil {
		return "", nil
	}
	for _, task := range view.Tasks {
		if task.TaskID == "a" && len(task.Attempts) > 0 {
			last := task.Attempts[len(task.Attempts)-1]
			return last.State, &last
		}
	}
	return "", nil
}

func TestVerdictWithoutJudgeHolds(t *testing.T) {
	// A wired server always has a judge for verdict tasks; the manager
	// still fails safe rather than wedging silently.
	fx := newManagerFixture(t, verdictChainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-no-judge"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()
	fx.exec.releaseSuccess(fx.exec.liveRunIDs()[0], "result")
	if !fx.pumpUntil(3*time.Second, func() bool {
		view, err := fx.m.View("wf_000001")
		return err == nil && view.State == domain.WorkflowNeedsAttention
	}) {
		t.Fatal("missing judge must hold with needs_attention")
	}
	state, attempt := attemptState(t, fx, "a")
	if state != domain.WorkflowAttemptJudging || attempt.Reason != "judge_unavailable" {
		t.Fatalf("held attempt: %s %q", state, attempt.Reason)
	}
}

func TestHashWorkflowDefinitionStableWithoutVerdictSettings(t *testing.T) {
	def, agents := ephemeralHashDefinition()
	// Golden pin shared with the ephemeral stability test: definitions
	// without verdict settings hash byte-identically to binaries predating
	// the verdict fields.
	const golden = "3e484ddd3e24e42aa4bae9c16b4f4268ca00ddfdeed4b22e5f74715a2f5deaf2"
	if got := HashWorkflowDefinition(def, agents); got != golden {
		t.Fatalf("hash drift without verdict settings: got %s, want %s", got, golden)
	}

	withVerdicts := def
	withVerdicts.Tasks = map[string]domain.WorkflowTaskDefinition{
		"a": {Agent: "alpha", Prompt: "Do the single step.", Verdicts: map[string]string{"passed": "", "failed": ""}},
	}
	if got := HashWorkflowDefinition(withVerdicts, agents); got == golden {
		t.Fatal("declaring verdicts must change the definition hash")
	}

	withThreshold := def
	withThreshold.ConfidenceThreshold = 0.9
	if got := HashWorkflowDefinition(withThreshold, agents); got == golden {
		t.Fatal("setting confidence_threshold must change the definition hash")
	}

	withPolicy := def
	withPolicy.OnUncertain = domain.WorkflowOnUncertainError
	if got := HashWorkflowDefinition(withPolicy, agents); got == golden {
		t.Fatal("setting on_uncertain must change the definition hash")
	}
}

// sha256Hex mirrors the store's artifact hashing for integrity assertions.
func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
