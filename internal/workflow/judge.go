package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/judge"
)

const (
	// judgeMaxResponseBytes caps the agent response handed to the judge;
	// longer responses are truncated head-and-tail. The saved response file
	// is never modified.
	judgeMaxResponseBytes = 64 << 10
	judgeQuestionName     = "verdict"
	judgeInstructions     = "Classify the outcome of this agent task into exactly one of the declared verdicts."
	verdictSourceJudge    = "judge"
	verdictSourceManual   = "manual"
)

// dispatchJudgementLocked hands one verdict classification to the judge
// outside the manager lock. Callers hold the lock; the goroutine only reads
// immutable request data and posts its result through the completions
// channel, where it is committed in a serialized pass like any completion.
func (m *Manager) dispatchJudgementLocked(snapshot *domain.WorkflowSnapshot, task *domain.WorkflowTaskExecution, attempt *domain.WorkflowAttempt) {
	runID := attempt.RunID
	if m.judging == nil {
		m.judging = map[string]bool{}
	}
	if m.judging[runID] {
		return
	}
	if m.judge == nil {
		// Startup gating makes this unreachable in a wired server; keep the
		// hold observable rather than crashing the execution.
		attempt.Reason = "judge_unavailable"
		return
	}
	m.judging[runID] = true
	request, truncated := m.buildJudgeRequest(snapshot, task, attempt)
	taskID := task.TaskID
	attemptNumber := attempt.Attempt
	go func() {
		decision, err := m.judge.Decide(m.judgeContext(), request)
		result := &judgement{
			runID:     runID,
			taskID:    taskID,
			attempt:   attemptNumber,
			decision:  decision,
			err:       err,
			truncated: truncated,
		}
		select {
		case m.completions <- completion{runID: runID, judgement: result}:
		default:
			// The scheduler is the only consumer; a full buffer means it
			// stopped. Preserve the outcome for Stop's drain path.
			go func() { m.completions <- completion{runID: runID, judgement: result} }()
		}
	}()
}

// judgeContext returns the classification deadline context, or Background
// when the loop has not started (deterministic test drives).
func (m *Manager) judgeContext() context.Context {
	m.mu.Lock()
	ctx := m.judgeCtx
	m.mu.Unlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// buildJudgeRequest assembles the decision request for one attempt: the
// task's declared verdict options as choice criteria, and the evidence —
// task identity, prompt, and final response (deterministically truncated
// beyond the cap).
func (m *Manager) buildJudgeRequest(snapshot *domain.WorkflowSnapshot, task *domain.WorkflowTaskExecution, attempt *domain.WorkflowAttempt) (judge.Request, bool) {
	taskDef := snapshot.Definition.Tasks[task.TaskID]
	response, truncated := truncateForJudge(m.readAttemptResponse(snapshot, attempt))
	state := map[string]string{
		"execution_id":   snapshot.ExecutionID,
		"task_id":        task.TaskID,
		"agent":          task.Agent,
		"task_prompt":    strings.TrimSpace(taskDef.Prompt),
		"agent_response": response,
	}
	return judge.Request{
		State:        state,
		QuestionName: judgeQuestionName,
		Question: judge.Question{
			Type:         string(judge.QuestionChoice),
			Instructions: judgeInstructions,
			Criteria:     taskDef.Verdicts,
		},
	}, truncated
}

// readAttemptResponse loads the committed response artifact of the attempt.
// The artifact was verified before judging; an unreadable file leaves the
// judge an empty response, which settlement handles as an uncertain outcome.
func (m *Manager) readAttemptResponse(snapshot *domain.WorkflowSnapshot, attempt *domain.WorkflowAttempt) string {
	if attempt.ResultPath == "" {
		return ""
	}
	content, err := m.store.ReadWorkflowArtifact(snapshot.ExecutionID, attempt.ResultPath)
	if err != nil {
		return ""
	}
	return string(content)
}

// truncateForJudge caps text head-and-tail for the judge input only. The
// marker records how many bytes were elided.
func truncateForJudge(text string) (string, bool) {
	if len(text) <= judgeMaxResponseBytes {
		return text, false
	}
	elided := len(text) - judgeMaxResponseBytes
	half := judgeMaxResponseBytes / 2
	head := text[:half]
	tail := text[len(text)-half:]
	return head + fmt.Sprintf("\n[...truncated %d bytes...]\n", elided) + tail, true
}

// handleJudgement commits one classification result into the snapshot under
// the manager lock, exactly like a worker completion.
func (m *Manager) handleJudgement(j *judgement) {
	if m.active == nil {
		return
	}
	snapshot := m.active.snapshot
	delete(m.judging, j.runID)
	task := snapshot.Tasks[j.taskID]
	if task == nil {
		return
	}
	attempt := findAttempt(snapshot, j.taskID, j.attempt)
	if attempt == nil || attempt.State != domain.WorkflowAttemptJudging {
		// Settled meanwhile (manual override or cancellation); the
		// classification is dropped.
		return
	}
	taskDef := snapshot.Definition.Tasks[j.taskID]
	threshold := m.effectiveConfidenceThreshold(snapshot.Definition)

	if j.err != nil {
		// Transport failure after the client's bounded retries: hold for
		// intervention rather than failing the task. The attempt keeps its
		// committed response artifact.
		now := m.now()
		attempt.Reason = "judge_unavailable"
		attempt.Error = j.err.Error()
		snapshot.Revision++
		snapshot.UpdatedAt = now
		if err := m.store.SaveWorkflowSnapshot(snapshot); err != nil {
			m.setStorageErrorLocked(err)
		}
		m.notifyRevisionLocked()
		m.Notify()
		return
	}
	if _, declared := taskDef.Verdicts[j.decision.Choice]; declared && j.decision.Confidence >= threshold {
		m.settleVerdictLocked(snapshot, task, attempt, j, threshold)
		return
	}
	m.settleUncertainLocked(snapshot, task, attempt, j, threshold)
}

// settleVerdictLocked commits a confident, declared verdict as the
// attempt's successful settlement.
func (m *Manager) settleVerdictLocked(snapshot *domain.WorkflowSnapshot, task *domain.WorkflowTaskExecution, attempt *domain.WorkflowAttempt, j *judgement, threshold float64) {
	// Publish the raw decision as a readable audit artifact before
	// committing the outcome.
	if len(j.decision.Raw) > 0 {
		if _, err := m.store.WriteWorkflowDecision(snapshot.ExecutionID, task.TaskID, attempt.Attempt, j.decision.Raw); err != nil {
			m.setStorageErrorLocked(fmt.Errorf("persist decision of %s: %w", j.runID, err))
			attempt.Reason = "judge_unavailable"
			attempt.Error = err.Error()
			return
		}
	}
	now := m.now()
	attempt.State = domain.WorkflowAttemptSucceeded
	attempt.Reason = ""
	attempt.Error = ""
	attempt.Verdict = &domain.AttemptVerdict{
		Value:         j.decision.Choice,
		Confidence:    j.decision.Confidence,
		Probabilities: j.decision.Probabilities,
		Model:         j.decision.Model,
		Threshold:     threshold,
		Source:        verdictSourceJudge,
		Truncated:     j.truncated,
		JudgedAt:      now,
	}
	snapshot.Revision++
	snapshot.UpdatedAt = now
	if err := m.store.SaveWorkflowSnapshot(snapshot); err != nil {
		m.setStorageErrorLocked(err)
	}
	m.notifyRevisionLocked()
	m.Notify()
}

// settleUncertainLocked applies the below-threshold (or undeclared-choice)
// policy: error fails the attempt, retryable through the retry API; hold
// keeps it in judging with needs_attention and the distribution exposed.
func (m *Manager) settleUncertainLocked(snapshot *domain.WorkflowSnapshot, task *domain.WorkflowTaskExecution, attempt *domain.WorkflowAttempt, j *judgement, threshold float64) {
	now := m.now()
	// The distribution is worth auditing even when the outcome is a
	// failure.
	if len(j.decision.Raw) > 0 {
		if _, err := m.store.WriteWorkflowDecision(snapshot.ExecutionID, task.TaskID, attempt.Attempt, j.decision.Raw); err != nil {
			m.setStorageErrorLocked(fmt.Errorf("persist decision of %s: %w", j.runID, err))
			attempt.Reason = "judge_unavailable"
			attempt.Error = err.Error()
			return
		}
	}
	attempt.Verdict = &domain.AttemptVerdict{
		Value:         domain.ReservedVerdictName,
		Confidence:    j.decision.Confidence,
		Probabilities: j.decision.Probabilities,
		Model:         j.decision.Model,
		Threshold:     threshold,
		Source:        verdictSourceJudge,
		Truncated:     j.truncated,
		JudgedAt:      now,
	}
	attempt.Reason = "uncertain_verdict"
	if snapshot.Definition.EffectiveOnUncertain() == domain.WorkflowOnUncertainError {
		attempt.State = domain.WorkflowAttemptFailed
		attempt.Error = fmt.Sprintf("judge confidence %.2f below threshold %.2f", j.decision.Confidence, threshold)
	} else {
		attempt.Error = fmt.Sprintf("judge confidence %.2f below threshold %.2f; override manually or resume to re-classify", j.decision.Confidence, threshold)
	}
	snapshot.Revision++
	snapshot.UpdatedAt = now
	if err := m.store.SaveWorkflowSnapshot(snapshot); err != nil {
		m.setStorageErrorLocked(err)
	}
	m.notifyRevisionLocked()
	m.Notify()
}

// redispatchJudgingLocked re-runs classification for attempts left in the
// judging phase: in-flight crashes (recovery) and held outcomes (resume).
// Callers hold the lock.
func (m *Manager) redispatchJudgingLocked(snapshot *domain.WorkflowSnapshot) {
	for _, task := range snapshot.Tasks {
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if attempt.State != domain.WorkflowAttemptJudging {
				continue
			}
			attempt.Reason = ""
			attempt.Error = ""
			m.dispatchJudgementLocked(snapshot, task, attempt)
		}
	}
}

// resumeJudgingLocked re-attempts only held verdict classifications (a
// judging attempt carrying a reason) whose own response artifact verifies.
// Unlike the recovery path it preserves each attempt's attention hold until
// the committed outcome resolves it, so a resumed classification cannot
// prematurely release ordinary task dispatch. Concurrent calls deduplicate
// through the in-flight run set: a repeated resume never starts a second
// judge call for the same attempt. Callers hold the lock.
func (m *Manager) resumeJudgingLocked(snapshot *domain.WorkflowSnapshot) {
	for _, task := range snapshot.Tasks {
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if attempt.State != domain.WorkflowAttemptJudging || attempt.Reason == "" {
				continue
			}
			if attempt.ResultPath != "" {
				if err := m.store.VerifyWorkflowArtifact(snapshot.ExecutionID, attempt.ResultPath, attempt.ResultSize, attempt.ResultSHA256); err != nil {
					// The response artifact is damaged: the classification
					// stays held and the artifact reason keeps blocking.
					continue
				}
			}
			m.dispatchJudgementLocked(snapshot, task, attempt)
		}
	}
}
