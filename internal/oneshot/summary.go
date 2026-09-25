package oneshot

import (
	"fmt"
	"sort"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// Cleanup statuses reported in the summary.
const (
	CleanupComplete   = "complete"
	CleanupIncomplete = "incomplete"
	CleanupNotStarted = "not_started"
)

// Exit reasons reported in the summary.
const (
	ReasonTerminalOutcome = "terminal_outcome"
	ReasonSignal          = "signal_cancellation"
	ReasonStartupFailure  = "startup_failure"
	ReasonFatalError      = "fatal_error"
)

// Summary is the version-1 machine-readable report of one `run` invocation.
// It is a derived, latest-invocation report: never read for scheduling or
// recovery, and replaceable on replay.
type Summary struct {
	SummaryVersion   int                       `json:"summary_version"`
	RequestID        string                    `json:"request_id"`
	ExecutionID      *string                   `json:"execution_id"`
	WorkflowState    *string                   `json:"workflow_state"`
	Revision         *int64                    `json:"revision"`
	ExitCode         int                       `json:"exit_code"`
	ExitReason       string                    `json:"exit_reason"`
	TriggeringSignal *string                   `json:"triggering_signal"`
	TaskCounts       domain.WorkflowTaskCounts `json:"task_counts"`
	FailedBlocked    []TaskIssue               `json:"failed_blocked_tasks"`
	Verdicts         []VerdictEntry            `json:"verdicts"`
	AttentionReasons []string                  `json:"attention_reasons,omitempty"`
	SessionDir       string                    `json:"session_dir,omitempty"`
	WorkflowDir      string                    `json:"workflow_dir,omitempty"`
	SnapshotPath     string                    `json:"snapshot_path,omitempty"`
	ResponsePaths    []string                  `json:"response_paths,omitempty"`
	DecisionPaths    []string                  `json:"decision_paths,omitempty"`
	Cleanup          CleanupReport             `json:"cleanup"`
	SummaryPersisted bool                      `json:"summary_persisted"`
}

// TaskIssue describes one failed or blocked task's durable outcome.
type TaskIssue struct {
	TaskID        string `json:"task_id"`
	State         string `json:"state"`
	Attempt       int    `json:"attempt,omitempty"`
	Iteration     int    `json:"iteration,omitempty"`
	IterationPath string `json:"iteration_path,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`
	Error         string `json:"error,omitempty"`
}

// VerdictEntry identifies one recorded verdict with its task, attempt, and
// iteration-path identity instead of inventing a single workflow verdict.
type VerdictEntry struct {
	TaskID        string  `json:"task_id"`
	Attempt       int     `json:"attempt"`
	Iteration     int     `json:"iteration,omitempty"`
	IterationPath string  `json:"iteration_path,omitempty"`
	Value         string  `json:"value"`
	Confidence    float64 `json:"confidence"`
	Source        string  `json:"source"`
}

// CleanupReport reports the teardown outcome and any unresolved owned work.
type CleanupReport struct {
	Status            string   `json:"status"`
	OutstandingRunIDs []string `json:"outstanding_run_ids,omitempty"`
	Errors            []string `json:"errors,omitempty"`
}

// buildSummary projects the durable snapshot and the invocation's process
// outcome into the version-1 report. Identity and state stay null when the
// failure happened before selection.
func buildSummary(snapshot *domain.WorkflowSnapshot, counts domain.WorkflowTaskCounts, exitCode int, exitReason string, signal string, cleanup CleanupReport, sessionDir, workflowDir, snapshotPath string) *Summary {
	s := &Summary{
		SummaryVersion: 1,
		RequestID:      snapshot.RequestID,
		ExitCode:       exitCode,
		ExitReason:     exitReason,
		TaskCounts:     counts,
		FailedBlocked:  []TaskIssue{},
		Verdicts:       []VerdictEntry{},
		Cleanup:        cleanup,
	}
	if signal != "" {
		s.TriggeringSignal = &signal
	}
	if snapshot.ExecutionID == "" {
		return s
	}
	executionID := snapshot.ExecutionID
	s.ExecutionID = &executionID
	state := string(snapshot.State)
	s.WorkflowState = &state
	revision := snapshot.Revision
	s.Revision = &revision
	s.AttentionReasons = snapshot.AttentionReasons
	s.SessionDir = sessionDir
	s.WorkflowDir = workflowDir
	s.SnapshotPath = snapshotPath

	taskIDs := make([]string, 0, len(snapshot.Tasks))
	for taskID := range snapshot.Tasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	for _, taskID := range taskIDs {
		task := snapshot.Tasks[taskID]
		if task.State != domain.WorkflowTaskFailed && task.State != domain.WorkflowTaskBlocked {
			continue
		}
		issue := TaskIssue{
			TaskID:        task.TaskID,
			State:         string(task.State),
			BlockedReason: task.BlockedReason,
		}
		if last := task.LastAttempt(); last != nil {
			issue.Attempt = last.Attempt
			issue.Iteration = last.Iteration
			issue.IterationPath = iterationPathToken(last.IterationPath)
			issue.Error = last.Error
		}
		s.FailedBlocked = append(s.FailedBlocked, issue)
	}
	for _, taskID := range taskIDs {
		task := snapshot.Tasks[taskID]
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if attempt.ResultPath != "" {
				s.ResponsePaths = append(s.ResponsePaths, attempt.ResultPath)
			}
			if attempt.Verdict == nil {
				continue
			}
			s.Verdicts = append(s.Verdicts, VerdictEntry{
				TaskID:        taskID,
				Attempt:       attempt.Attempt,
				Iteration:     attempt.Iteration,
				IterationPath: iterationPathToken(attempt.IterationPath),
				Value:         attempt.Verdict.Value,
				Confidence:    attempt.Verdict.Confidence,
				Source:        attempt.Verdict.Source,
			})
		}
	}
	return s
}

func iterationPathToken(path []domain.IterationEntry) string {
	if len(path) == 0 {
		return ""
	}
	parts := make([]string, 0, len(path))
	for _, entry := range path {
		parts = append(parts, fmt.Sprintf("%s=%d", entry.Loop, entry.Iteration))
	}
	return strings.Join(parts, "/")
}

// countTasks recounts task states from the durable snapshot for paths without
// a live manager (terminal replay, startup failures).
func countTasks(snapshot *domain.WorkflowSnapshot) domain.WorkflowTaskCounts {
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
