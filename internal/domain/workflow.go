package domain

import "time"

const (
	WorkflowSchemaVersion         = 1
	DefaultWorkflowTaskTimeoutSec = 1800
	// DefaultConfidenceThreshold gates judge verdicts: the chosen verdict
	// applies only when judge confidence meets it.
	DefaultConfidenceThreshold = 0.8
	// ReservedVerdictName is the synthetic outcome for a judge answer below
	// the confidence threshold; declaring it is a validation error.
	ReservedVerdictName = "uncertain"
)

// Uncertainty policies for verdict resolution.
const (
	WorkflowOnUncertainHold  = "hold"
	WorkflowOnUncertainError = "error"
)

type WorkflowState string

const (
	WorkflowRunning            WorkflowState = "running"
	WorkflowPaused             WorkflowState = "paused"
	WorkflowNeedsAttention     WorkflowState = "needs_attention"
	WorkflowCancelling         WorkflowState = "cancelling"
	WorkflowCancelled          WorkflowState = "cancelled"
	WorkflowSucceeded          WorkflowState = "succeeded"
	WorkflowCompletedWithError WorkflowState = "completed_with_errors"
	WorkflowFailed             WorkflowState = "failed"
)

func (s WorkflowState) Terminal() bool {
	switch s {
	case WorkflowCancelled, WorkflowSucceeded, WorkflowCompletedWithError, WorkflowFailed:
		return true
	default:
		return false
	}
}

// WorkflowMode is the durable scheduling intent of an execution, kept
// separately from the observed state so recovery can distinguish "paused by
// the user" from "held for attention".
type WorkflowMode string

const (
	WorkflowModeRunning    WorkflowMode = "running"
	WorkflowModePaused     WorkflowMode = "paused"
	WorkflowModeCancelling WorkflowMode = "cancelling"
)

type WorkflowTaskState string

const (
	WorkflowTaskPending     WorkflowTaskState = "pending"
	WorkflowTaskReady       WorkflowTaskState = "ready"
	WorkflowTaskDispatching WorkflowTaskState = "dispatching"
	WorkflowTaskRunning     WorkflowTaskState = "running"
	WorkflowTaskSucceeded   WorkflowTaskState = "succeeded"
	WorkflowTaskFailed      WorkflowTaskState = "failed"
	WorkflowTaskInterrupted WorkflowTaskState = "interrupted"
	WorkflowTaskBlocked     WorkflowTaskState = "blocked"
	WorkflowTaskCancelled   WorkflowTaskState = "cancelled"
)

func (s WorkflowTaskState) Settled() bool {
	switch s {
	case WorkflowTaskSucceeded, WorkflowTaskFailed, WorkflowTaskBlocked, WorkflowTaskCancelled:
		return true
	default:
		return false
	}
}

type WorkflowAttemptState string

const (
	WorkflowAttemptQueued      WorkflowAttemptState = "queued"
	WorkflowAttemptDispatching WorkflowAttemptState = "dispatching"
	WorkflowAttemptRunning     WorkflowAttemptState = "running"
	// WorkflowAttemptJudging is the post-response classification phase of a
	// verdict task: the backend work is committed, the response artifact is
	// saved, and the attempt settles only once its verdict is resolved. A
	// held judging attempt carries a Reason of "uncertain_verdict" or
	// "judge_unavailable".
	WorkflowAttemptJudging     WorkflowAttemptState = "judging"
	WorkflowAttemptCancelling  WorkflowAttemptState = "cancelling"
	WorkflowAttemptSucceeded   WorkflowAttemptState = "succeeded"
	WorkflowAttemptFailed      WorkflowAttemptState = "failed"
	WorkflowAttemptInterrupted WorkflowAttemptState = "interrupted"
	WorkflowAttemptCancelled   WorkflowAttemptState = "cancelled"
)

func (s WorkflowAttemptState) Committed() bool {
	switch s {
	case WorkflowAttemptSucceeded, WorkflowAttemptFailed, WorkflowAttemptCancelled:
		return true
	default:
		return false
	}
}

// WorkflowDefinition is the resolved, immutable graph of one execution.
type WorkflowDefinition struct {
	Version            int                               `json:"version" yaml:"version"`
	Name               string                            `json:"name" yaml:"name"`
	MaxParallel        int                               `json:"max_parallel" yaml:"max_parallel"`
	TaskTimeoutSeconds int                               `json:"task_timeout_seconds" yaml:"task_timeout_seconds"`
	Tasks              map[string]WorkflowTaskDefinition `json:"tasks" yaml:"tasks"`
	// ConfidenceThreshold gates judge verdicts: 0 means unset, which hashes
	// identically to definitions parsed before verdicts existed and resolves
	// to DefaultConfidenceThreshold at use.
	ConfidenceThreshold float64 `json:"confidence_threshold,omitempty" yaml:"-"`
	// OnUncertain selects the below-threshold policy: "" (unset, hashing as
	// absent) resolves to WorkflowOnUncertainHold at use.
	OnUncertain string `json:"on_uncertain,omitempty" yaml:"-"`
}

func (d WorkflowDefinition) EffectiveConfidenceThreshold() float64 {
	if d.ConfidenceThreshold > 0 {
		return d.ConfidenceThreshold
	}
	return DefaultConfidenceThreshold
}

func (d WorkflowDefinition) EffectiveOnUncertain() string {
	if d.OnUncertain != "" {
		return d.OnUncertain
	}
	return WorkflowOnUncertainHold
}

type WorkflowTaskDefinition struct {
	Agent                     string   `json:"agent" yaml:"agent"`
	Prompt                    string   `json:"prompt" yaml:"prompt"`
	Needs                     []string `json:"needs,omitempty" yaml:"needs,omitempty"`
	AllowedToFail             bool     `json:"allowed_to_fail,omitempty" yaml:"allowed_to_fail,omitempty"`
	MinSuccessfulDependencies int      `json:"min_successful_dependencies,omitempty" yaml:"min_successful_dependencies,omitempty"`
	TimeoutSeconds            int      `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	// Verdicts declares the classification contract of this task's
	// responses: verdict name to optional human description. A non-empty
	// map routes completed attempts through the judging phase. Unset must
	// marshal identically to tasks parsed before verdicts existed.
	Verdicts map[string]string `json:"verdicts,omitempty" yaml:"-"`
}

func (d WorkflowTaskDefinition) EffectiveTimeoutSeconds(fallback int) int {
	if d.TimeoutSeconds > 0 {
		return d.TimeoutSeconds
	}
	return fallback
}

// WorkflowSnapshot is the authoritative persisted state of one execution.
// Everything the scheduler decides lives here; run records, transcripts, and
// result files are projections and artifacts, not a second source of truth.
type WorkflowSnapshot struct {
	SchemaVersion    int                               `json:"schema_version"`
	ExecutionID      string                            `json:"execution_id"`
	Revision         int64                             `json:"revision"`
	Definition       WorkflowDefinition                `json:"definition"`
	DefinitionHash   string                            `json:"definition_hash"`
	Agents           map[string]AgentSpec              `json:"agents"`
	RequestID        string                            `json:"request_id"`
	State            WorkflowState                     `json:"state"`
	Mode             WorkflowMode                      `json:"mode"`
	AttentionReasons []string                          `json:"attention_reasons,omitempty"`
	LastError        *string                           `json:"last_error,omitempty"`
	Tasks            map[string]*WorkflowTaskExecution `json:"tasks"`
	NextRunSeq       int                               `json:"next_run_seq"`
	RetryRequests    map[string]WorkflowRetryRecord    `json:"retry_requests,omitempty"`
	Controls         []WorkflowControlEvent            `json:"controls,omitempty"`
	CreatedAt        time.Time                         `json:"created_at"`
	UpdatedAt        time.Time                         `json:"updated_at"`
}

type WorkflowTaskExecution struct {
	TaskID        string            `json:"task_id"`
	Agent         string            `json:"agent"`
	State         WorkflowTaskState `json:"state"`
	BlockedReason string            `json:"blocked_reason,omitempty"`
	Attempts      []WorkflowAttempt `json:"attempts"`
}

func (t *WorkflowTaskExecution) LastAttempt() *WorkflowAttempt {
	if len(t.Attempts) == 0 {
		return nil
	}
	return &t.Attempts[len(t.Attempts)-1]
}

// AttemptVerdict records the resolved classification of one attempt, plus
// everything needed to audit the decision: the full probability
// distribution, the model that served it, the threshold it was judged
// against, and whether the judge saw a truncated response.
type AttemptVerdict struct {
	Value         string             `json:"value"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Model         string             `json:"model,omitempty"`
	Threshold     float64            `json:"threshold"`
	Source        string             `json:"source"` // judge | manual
	Truncated     bool               `json:"truncated,omitempty"`
	JudgedAt      time.Time          `json:"judged_at"`
}

type WorkflowAttempt struct {
	Attempt            int                  `json:"attempt"`
	RunID              string               `json:"run_id"`
	State              WorkflowAttemptState `json:"state"`
	Reason             string               `json:"reason,omitempty"`
	Error              string               `json:"error,omitempty"`
	PromptPath         string               `json:"prompt_path,omitempty"`
	ManifestPath       string               `json:"manifest_path,omitempty"`
	ResultPath         string               `json:"result_path,omitempty"`
	ResultSize         int64                `json:"result_size,omitempty"`
	ResultSHA256       string               `json:"result_sha256,omitempty"`
	Verdict            *AttemptVerdict      `json:"verdict,omitempty"`
	ReservedAt         *time.Time           `json:"reserved_at,omitempty"`
	DispatchedAt       *time.Time           `json:"dispatched_at,omitempty"`
	CompletedAt        *time.Time           `json:"completed_at,omitempty"`
	CancelRequestedAt  *time.Time           `json:"cancel_requested_at,omitempty"`
	CleanupConfirmedAt *time.Time           `json:"cleanup_confirmed_at,omitempty"`
}

type WorkflowRetryRecord struct {
	RequestID              string    `json:"request_id"`
	TaskID                 string    `json:"task_id"`
	Attempt                int       `json:"attempt"`
	CreatedAt              time.Time `json:"created_at"`
	ConfirmPreviousStopped bool      `json:"confirm_previous_stopped,omitempty"`
}

type WorkflowControlEvent struct {
	Type                   string    `json:"type"`
	At                     time.Time `json:"at"`
	RequestID              string    `json:"request_id,omitempty"`
	TaskID                 string    `json:"task_id,omitempty"`
	Attempt                int       `json:"attempt,omitempty"`
	Detail                 string    `json:"detail,omitempty"`
	ConfirmPreviousStopped bool      `json:"confirm_previous_stopped,omitempty"`
}

// WorkflowInputManifest describes every direct dependency of one attempt in
// sorted task ID order. Successful entries reference the verified response
// file; failed entries carry the error instead.
type WorkflowInputManifest struct {
	ExecutionID  string                    `json:"execution_id"`
	TaskID       string                    `json:"task_id"`
	Attempt      int                       `json:"attempt"`
	Dependencies []WorkflowDependencyInput `json:"dependencies"`
}

type WorkflowDependencyInput struct {
	TaskID       string `json:"task_id"`
	Agent        string `json:"agent"`
	Attempt      int    `json:"attempt"`
	RunID        string `json:"run_id"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	ResultPath   string `json:"result_path,omitempty"`
	ResultSize   int64  `json:"result_size,omitempty"`
	ResultSHA256 string `json:"result_sha256,omitempty"`
}

// OwnedRunOptions asks the orchestrator to execute one workflow attempt on a
// fresh, workflow-owned runtime under a caller-reserved run ID.
type OwnedRunOptions struct {
	RunID     string
	Agent     string
	Message   string
	Metadata  map[string]string
	StatePath string
	// Spec is the execution's immutable saved agent configuration. When set,
	// the executor must use it instead of the server's current YAML so a
	// config edit and restart cannot change a recovered workflow's backend,
	// model, or permissions.
	Spec   *AgentSpec
	OnDone func(OwnedRunOutcome)
}

// OwnedRunOutcome reports the worker-stopped completion boundary of an owned
// run: the raw final response is passed through unmodified.
type OwnedRunOutcome struct {
	RunID        string
	Status       RunStatus
	FinalMessage string
	Error        string
}

type WorkflowResultRef struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type WorkflowAttemptView struct {
	Attempt      int                  `json:"attempt"`
	RunID        string               `json:"run_id"`
	State        WorkflowAttemptState `json:"state"`
	Reason       string               `json:"reason,omitempty"`
	Error        string               `json:"error,omitempty"`
	ReservedAt   *time.Time           `json:"reserved_at,omitempty"`
	DispatchedAt *time.Time           `json:"dispatched_at,omitempty"`
	CompletedAt  *time.Time           `json:"completed_at,omitempty"`
	Result       *WorkflowResultRef   `json:"result,omitempty"`
	Verdict      *AttemptVerdict      `json:"verdict,omitempty"`
}

type WorkflowTaskView struct {
	TaskID        string                `json:"task_id"`
	Agent         string                `json:"agent"`
	State         WorkflowTaskState     `json:"state"`
	BlockedReason string                `json:"blocked_reason,omitempty"`
	Attempts      []WorkflowAttemptView `json:"attempts"`
	Result        *WorkflowResultRef    `json:"result,omitempty"`
}

type WorkflowTaskCounts struct {
	Pending     int `json:"pending"`
	Ready       int `json:"ready"`
	Active      int `json:"active"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Blocked     int `json:"blocked"`
	Interrupted int `json:"interrupted"`
	Cancelled   int `json:"cancelled"`
}

type WorkflowPendingPermission struct {
	TaskID  string            `json:"task_id"`
	Attempt int               `json:"attempt"`
	RunID   string            `json:"run_id"`
	Request PermissionRequest `json:"request"`
}

type WorkflowExecutionView struct {
	ExecutionID        string                      `json:"execution_id"`
	Definition         WorkflowDefinition          `json:"definition"`
	DefinitionHash     string                      `json:"definition_hash"`
	RequestID          string                      `json:"request_id"`
	Revision           int64                       `json:"revision"`
	State              WorkflowState               `json:"state"`
	Mode               WorkflowMode                `json:"mode"`
	AttentionReasons   []string                    `json:"attention_reasons,omitempty"`
	LastError          *string                     `json:"last_error,omitempty"`
	TaskCounts         WorkflowTaskCounts          `json:"task_counts"`
	Tasks              []WorkflowTaskView          `json:"tasks"`
	PendingPermissions []WorkflowPendingPermission `json:"pending_permissions,omitempty"`
	CreatedAt          time.Time                   `json:"created_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
}

type WorkflowExecutionSummary struct {
	ExecutionID string             `json:"execution_id"`
	Name        string             `json:"name"`
	RequestID   string             `json:"request_id"`
	State       WorkflowState      `json:"state"`
	Revision    int64              `json:"revision"`
	TaskCounts  WorkflowTaskCounts `json:"task_counts"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}
