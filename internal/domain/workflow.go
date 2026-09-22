package domain

import "time"

const (
	// WorkflowDefinitionVersion is the version of a resolved workflow YAML
	// definition. Loops are additive, so the definition version stays 1.
	WorkflowDefinitionVersion = 1
	// WorkflowSnapshotSchemaVersion is the current persisted snapshot layout.
	// Version 2 adds loop execution state and per-attempt iteration numbers.
	WorkflowSnapshotSchemaVersion = 2
	// WorkflowSnapshotMinSchemaVersion is the oldest snapshot layout this
	// binary can load. Schema 1 predates loops and recovers as a loopless
	// execution; compatibility is upgrade-only, never downgrade.
	WorkflowSnapshotMinSchemaVersion = 1

	DefaultWorkflowTaskTimeoutSec = 1800
	// DefaultConfidenceThreshold gates judge verdicts: the chosen verdict
	// applies only when judge confidence meets it.
	DefaultConfidenceThreshold = 0.8
	// ReservedVerdictName is the synthetic outcome for a judge answer below
	// the confidence threshold; declaring it is a validation error.
	ReservedVerdictName = "uncertain"
)

// Uncertainty policies for verdict resolution. The waiting policy is named
// after the execution state it produces; the former "hold" spelling is no
// longer accepted, with no alias or automatic migration.
const (
	WorkflowOnUncertainNeedsAttention = "needs_attention"
	WorkflowOnUncertainError          = "error"
)

// Loop condition actions: the mapped meaning of a condition verdict at an
// acceptable iteration settlement.
const (
	WorkflowLoopActionBreak          = "break"
	WorkflowLoopActionContinue       = "continue"
	WorkflowLoopActionNeedsAttention = "needs_attention"
)

// Loop exhaustion policies: what happens when the condition maps to continue
// at the effective iteration cap. needs_attention is the default.
const (
	WorkflowExhaustionNeedsAttention = "needs_attention"
	WorkflowExhaustionSucceed        = "succeed"
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

// WorkflowLoopState is the observed lifecycle of one bounded loop's execution.
type WorkflowLoopState string

const (
	WorkflowLoopRunning        WorkflowLoopState = "running"
	WorkflowLoopNeedsAttention WorkflowLoopState = "needs_attention"
	WorkflowLoopDone           WorkflowLoopState = "done"
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
	// Loops declares bounded static loops keyed by loop name. Unset must
	// marshal identically to definitions parsed before loops existed.
	Loops map[string]WorkflowLoopDefinition `json:"loops,omitempty" yaml:"loops,omitempty"`
	// ConfidenceThreshold gates judge verdicts: 0 means unset, which hashes
	// identically to definitions parsed before verdicts existed and resolves
	// to DefaultConfidenceThreshold at use.
	ConfidenceThreshold float64 `json:"confidence_threshold,omitempty" yaml:"-"`
	// OnUncertain selects the below-threshold policy: "" (unset, hashing as
	// absent) resolves to WorkflowOnUncertainNeedsAttention at use.
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
	return WorkflowOnUncertainNeedsAttention
}

type WorkflowTaskDefinition struct {
	Agent  string   `json:"agent" yaml:"agent"`
	Prompt string   `json:"prompt" yaml:"prompt"`
	Needs  []string `json:"needs,omitempty" yaml:"needs,omitempty"`
	// Loop names the innermost loop this task belongs to. Empty means the
	// task runs outside every loop. Unset must marshal identically to tasks
	// parsed before loops existed.
	Loop                      string `json:"loop,omitempty" yaml:"loop,omitempty"`
	AllowedToFail             bool   `json:"allowed_to_fail,omitempty" yaml:"allowed_to_fail,omitempty"`
	MinSuccessfulDependencies int    `json:"min_successful_dependencies,omitempty" yaml:"min_successful_dependencies,omitempty"`
	TimeoutSeconds            int    `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	// Verdicts declares the classification contract of this task's
	// responses: verdict name to optional human description. A non-empty
	// map routes completed attempts through the judging phase. Unset must
	// marshal identically to tasks parsed before verdicts existed.
	Verdicts map[string]string `json:"verdicts,omitempty" yaml:"-"`
}

// WorkflowLoopDefinition is a bounded loop: exactly MaxIterations is required
// and must be positive. Condition fields are optional; a loop with none of
// them stays fully static. When present, UntilTask names the loop's single
// condition task (the unique body sink), OnVerdict maps each of its declared
// verdicts to a continuation action, and OnExhaustion selects the policy for a
// continue action at the effective cap. Unset condition fields must marshal
// byte-identically to definitions parsed before conditions existed.
type WorkflowLoopDefinition struct {
	MaxIterations int               `json:"max_iterations" yaml:"max_iterations"`
	UntilTask     string            `json:"until_task,omitempty" yaml:"until_task,omitempty"`
	OnVerdict     map[string]string `json:"on_verdict,omitempty" yaml:"on_verdict,omitempty"`
	OnExhaustion  string            `json:"on_exhaustion,omitempty" yaml:"on_exhaustion,omitempty"`
}

// HasCondition reports whether the loop declares a condition. A loop is
// conditioned only when it names an until_task and a complete on_verdict map;
// validation enforces the pairing, so UntilTask alone is the reliable signal.
func (d WorkflowLoopDefinition) HasCondition() bool {
	return d.UntilTask != ""
}

// EffectiveOnExhaustion resolves the unset exhaustion policy to the default
// needs_attention hold.
func (d WorkflowLoopDefinition) EffectiveOnExhaustion() string {
	if d.OnExhaustion != "" {
		return d.OnExhaustion
	}
	return WorkflowExhaustionNeedsAttention
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
	// Loops is the runtime state of each declared loop, keyed by loop name.
	// Absent for loopless executions, including those loaded from schema 1.
	Loops         map[string]*WorkflowLoopExecution `json:"loops,omitempty"`
	NextRunSeq    int                               `json:"next_run_seq"`
	RetryRequests map[string]WorkflowRetryRecord    `json:"retry_requests,omitempty"`
	Controls      []WorkflowControlEvent            `json:"controls,omitempty"`
	CreatedAt     time.Time                         `json:"created_at"`
	UpdatedAt     time.Time                         `json:"updated_at"`
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

// WorkflowLoopExecution is the durable runtime state of one loop: its current
// 1-based iteration, observed lifecycle state, extensions granted through the
// explicit extend control, and a pending manual-stop intent. The extension
// counter and stop flag are additive (omitempty) so pre-condition snapshots
// and static loops persist byte-identically.
type WorkflowLoopExecution struct {
	Iteration int               `json:"iteration"`
	State     WorkflowLoopState `json:"state"`
	// ExtendedIterations is the cumulative count granted by extend controls;
	// the effective cap is the declared max plus this value.
	ExtendedIterations int `json:"extended_iterations,omitempty"`
	// StopRequested records a durable manual-break intent: the loop finishes
	// its current iteration and never starts another.
	StopRequested bool `json:"stop_requested,omitempty"`
}

// EffectiveCap returns the loop's iteration ceiling: the declared maximum plus
// extensions granted through the explicit extend control.
func (e *WorkflowLoopExecution) EffectiveCap(declaredMax int) int {
	return declaredMax + e.ExtendedIterations
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
	Attempt int `json:"attempt"`
	// Iteration is the 1-based loop iteration this attempt belongs to. Zero
	// means the task runs outside every loop.
	Iteration          int                  `json:"iteration,omitempty"`
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
	// Loop names the target loop for loop control events (extend/stop); it is
	// omitted for task-level and lifecycle events so their serialization is
	// unchanged.
	Loop string `json:"loop,omitempty"`
}

// WorkflowInputManifest describes every direct dependency of one attempt in
// sorted task ID order. Successful entries reference the verified response
// file; failed entries carry the error instead.
type WorkflowInputManifest struct {
	ExecutionID string `json:"execution_id"`
	TaskID      string `json:"task_id"`
	Attempt     int    `json:"attempt"`
	// Iteration is the 1-based loop iteration of a body task's attempt; zero
	// for tasks outside every loop.
	Iteration int `json:"iteration,omitempty"`
	// PreviousIteration carries the prior iteration's settled body-task
	// outcomes for a loop body task dispatching in iteration k > 1: the
	// file-based carry-over channel. Omitted for the first iteration and for
	// tasks outside loops.
	Dependencies      []WorkflowDependencyInput `json:"dependencies"`
	PreviousIteration []WorkflowDependencyInput `json:"previous_iteration,omitempty"`
}

type WorkflowDependencyInput struct {
	TaskID       string          `json:"task_id"`
	Agent        string          `json:"agent"`
	Attempt      int             `json:"attempt"`
	Iteration    int             `json:"iteration,omitempty"`
	RunID        string          `json:"run_id"`
	Status       string          `json:"status"`
	Error        string          `json:"error,omitempty"`
	Verdict      *AttemptVerdict `json:"verdict,omitempty"`
	ResultPath   string          `json:"result_path,omitempty"`
	ResultSize   int64           `json:"result_size,omitempty"`
	ResultSHA256 string          `json:"result_sha256,omitempty"`
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
	Iteration    int                  `json:"iteration,omitempty"`
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

// WorkflowLoopView exposes the observable state of one bounded loop. Condition
// fields are populated only for conditioned or extended loops so static-loop
// views remain byte-identical to pre-condition releases.
type WorkflowLoopView struct {
	Name          string            `json:"name"`
	Iteration     int               `json:"iteration"`
	MaxIterations int               `json:"max_iterations"`
	State         WorkflowLoopState `json:"state"`
	// UntilTask names the loop's condition task; omitted for static loops.
	UntilTask string `json:"until_task,omitempty"`
	// EffectiveMaxIterations is the declared maximum plus granted extensions;
	// omitted when equal to MaxIterations on a static loop.
	EffectiveMaxIterations int `json:"effective_max_iterations,omitempty"`
	// LastConditionVerdict is the latest settled current-iteration verdict of
	// the condition task; omitted when none has settled yet.
	LastConditionVerdict string `json:"last_condition_verdict,omitempty"`
	// StopRequested exposes a pending manual-stop intent; omitted when false.
	StopRequested bool `json:"stop_requested,omitempty"`
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
	Loops              []WorkflowLoopView          `json:"loops,omitempty"`
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
