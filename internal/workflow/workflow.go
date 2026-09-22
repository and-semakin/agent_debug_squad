// Package workflow implements the declarative workflow scheduler: a single
// serialized reconciliation loop over an authoritative persisted snapshot,
// dispatching attempts through an executor interface.
package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/judge"
)

var (
	ErrExecutionNotFound     = errors.New("workflow execution not found")
	ErrTaskNotFound          = errors.New("workflow task not found")
	ErrNoDefinition          = errors.New("no workflow is configured")
	ErrInvalidRequest        = errors.New("invalid workflow request")
	ErrDefinitionChanged     = errors.New("request id was already used with a different workflow definition")
	ErrExecutionActive       = errors.New("another nonterminal workflow execution is already active")
	ErrInvalidTransition     = errors.New("workflow state does not allow this operation")
	ErrRetryConflict         = errors.New("retry request conflicts with current task state")
	ErrUncertaintyUnresolved = errors.New("workflow has unresolved recovery or artifact uncertainty")
	ErrWorkerActive          = errors.New("a known active worker cannot be overridden by a cleanup assertion")
	ErrStorageDamaged        = errors.New("workflow storage error requires attention")
	ErrAttemptNotFound       = errors.New("workflow attempt not found")
	ErrVerdictConflict       = errors.New("verdict override conflicts with current attempt state")
	ErrLoopNotFound          = errors.New("workflow loop not found")
	ErrLoopControlConflict   = errors.New("loop control conflicts with current loop state")
	ErrUnsupportedPolicy     = errors.New("workflow definition uses an unsupported on_uncertain policy")
)

// Store is the persistence surface the scheduler needs; *store.Store
// implements it. Wrappers may inject faults in tests.
type Store interface {
	SaveWorkflowSnapshot(snapshot *domain.WorkflowSnapshot) error
	LoadWorkflowSnapshot(executionID string) (domain.WorkflowSnapshot, error)
	ListWorkflowExecutions() ([]string, error)
	NextWorkflowExecutionID() (string, error)
	WriteWorkflowAttemptInput(executionID, taskID string, attempt int, prompt, manifest []byte) (promptPath, manifestPath string, err error)
	WriteWorkflowResponse(executionID, taskID string, attempt int, content []byte) (path string, size int64, sha256hex string, err error)
	WriteWorkflowDecision(executionID, taskID string, attempt int, content []byte) (path string, err error)
	ReadWorkflowArtifact(executionID, relativePath string) ([]byte, error)
	VerifyWorkflowArtifact(executionID, relativePath string, size int64, sha256hex string) error
	AppendWorkflowEvent(executionID string, event domain.WorkflowControlEvent) error
	WorkflowAttemptStatePath(executionID, taskID string, attempt int) (string, error)
	WorkflowDir(executionID string) (string, error)
}

// Executor runs one workflow attempt on a fresh owned runtime.
// *orchestrator.Orchestrator implements it.
type Executor interface {
	SubmitOwnedRun(ctx context.Context, opts domain.OwnedRunOptions) error
	CancelOwnedRun(runID string) bool
	OwnedRunActive(runID string) bool
}

// RunObserver optionally exposes live run projections (pending permissions).
type RunObserver interface {
	Run(ctx context.Context, runID string) (domain.RunRecord, error)
}

const (
	defaultReconcileInterval = 500 * time.Millisecond
	defaultCancelGrace       = 30 * time.Second
	completionBuffer         = 64
)

type liveAttempt struct {
	taskID             string
	attempt            int
	runID              string
	dispatchedAt       time.Time
	timeout            time.Duration
	cancelRequestedAt  time.Time
	confirmedUncertain bool
}

type execution struct {
	snapshot *domain.WorkflowSnapshot
	live     map[string]*liveAttempt // keyed by run ID
}

type completion struct {
	runID   string
	outcome domain.OwnedRunOutcome
	// judgement, when set, marks this completion as a verdict
	// classification result rather than a worker outcome.
	judgement *judgement
}

// judgement carries one asynchronous judge result back into the serialized
// commit path.
type judgement struct {
	runID     string
	taskID    string
	attempt   int
	decision  judge.Decision
	err       error
	truncated bool
}

type Manager struct {
	cfg   domain.SessionConfig
	store Store
	exec  Executor
	// judge classifies verdict tasks; nil means classification is
	// unavailable and verdict attempts hold with judge_unavailable.
	judge judge.Judge
	// judging tracks run IDs with an in-flight classification, so recovery
	// and resume never double-dispatch the same judge call.
	judging map[string]bool
	// judgeCtx bounds in-flight classifications; set in Start.
	judgeCtx context.Context
	now      func() time.Time

	cancelGrace       time.Duration
	reconcileInterval time.Duration

	mu          sync.Mutex
	active      *execution
	completions chan completion
	wake        chan struct{}
	revisionC   chan struct{}
	storageErr  error
	stopping    bool
	stop        context.CancelFunc
	loopDone    chan struct{}
}

func NewManager(cfg domain.SessionConfig, st Store, exec Executor) *Manager {
	return &Manager{
		cfg:               cfg,
		store:             st,
		exec:              exec,
		now:               time.Now,
		cancelGrace:       defaultCancelGrace,
		reconcileInterval: defaultReconcileInterval,
		completions:       make(chan completion, completionBuffer),
		wake:              make(chan struct{}, 1),
		revisionC:         make(chan struct{}),
		loopDone:          make(chan struct{}),
	}
}

// SetJudge wires the verdict judge. It must be called before Start; the
// startup gating in the server wiring guarantees a judge whenever the
// configured workflow declares verdicts.
func (m *Manager) SetJudge(j judge.Judge) {
	m.mu.Lock()
	m.judge = j
	m.mu.Unlock()
}

// Start recovers persisted executions and begins scheduling. It never
// creates a new execution: submission is explicit.
func (m *Manager) Start(ctx context.Context) error {
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
	var nonterminal []domain.WorkflowSnapshot
	for _, id := range ids {
		snapshot, err := m.store.LoadWorkflowSnapshot(id)
		if err != nil {
			cancel()
			return fmt.Errorf("recover workflow %s: %w", id, err)
		}
		if !snapshot.State.Terminal() {
			nonterminal = append(nonterminal, snapshot)
		}
	}
	if len(nonterminal) > 1 {
		cancel()
		return fmt.Errorf("session state contains %d nonterminal workflow executions; at most one is allowed in v1", len(nonterminal))
	}
	if len(nonterminal) == 1 {
		if err := m.recoverExecution(&nonterminal[0]); err != nil {
			cancel()
			return err
		}
	}

	go m.loop(loopCtx)
	return nil
}

func (m *Manager) recoverExecution(snapshot *domain.WorkflowSnapshot) error {
	// Reject a saved definition using the removed "hold" uncertainty policy
	// before any scheduling or mutation: there is no alias, fallback, or
	// automatic migration, and the saved definition is neither rewritten nor
	// silently reinterpreted.
	if snapshot.Definition.OnUncertain == "hold" {
		return fmt.Errorf("%w: execution %s saved on_uncertain \"hold\", which is no longer supported; edit the definition to %q and resubmit",
			ErrUnsupportedPolicy, snapshot.ExecutionID, domain.WorkflowOnUncertainNeedsAttention)
	}
	now := m.now()
	uncertain := false
	for _, task := range snapshot.Tasks {
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			switch attempt.State {
			case domain.WorkflowAttemptQueued, domain.WorkflowAttemptDispatching, domain.WorkflowAttemptRunning, domain.WorkflowAttemptCancelling:
				attempt.State = domain.WorkflowAttemptInterrupted
				attempt.Reason = "recovery"
				completed := now
				attempt.CompletedAt = &completed
				uncertain = true
				task.State = domain.WorkflowTaskInterrupted
			}
		}
	}
	// Revalidate committed successful artifacts.
	for _, task := range snapshot.Tasks {
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if attempt.State != domain.WorkflowAttemptSucceeded || attempt.ResultPath == "" {
				continue
			}
			if err := m.store.VerifyWorkflowArtifact(snapshot.ExecutionID, attempt.ResultPath, attempt.ResultSize, attempt.ResultSHA256); err != nil {
				snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, fmt.Sprintf("artifact:%s:%d:%v", task.TaskID, attempt.Attempt, err))
				uncertain = true
			}
		}
	}
	snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, collectAttentionReasons(snapshot)...)

	if snapshot.Mode == domain.WorkflowModeCancelling {
		snapshot.State = domain.WorkflowCancelling
		if uncertain {
			snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, "interrupted_attempts_require_cleanup_confirmation")
		}
	} else if uncertain {
		snapshot.State = domain.WorkflowNeedsAttention
		if snapshot.Mode == domain.WorkflowModeRunning {
			snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, "interrupted_attempts_require_retry_or_cancel")
		}
	}

	snapshot.Revision++
	snapshot.UpdatedAt = now
	if err := m.store.SaveWorkflowSnapshot(snapshot); err != nil {
		return fmt.Errorf("persist recovery of workflow %s: %w", snapshot.ExecutionID, err)
	}
	m.mu.Lock()
	m.active = &execution{snapshot: snapshot, live: map[string]*liveAttempt{}}
	// Attempts caught mid-judging have committed backend work; their
	// side-effect-free classification simply re-runs.
	m.redispatchJudgingLocked(snapshot)
	m.mu.Unlock()
	m.Notify()
	return nil
}

// Stop halts scheduling, cancels owned runs, and waits for their worker-stopped
// boundaries within the given context. Attempts whose workers do not report
// back stay uncommitted in the snapshot so the next start treats them as
// interrupted instead of inventing an outcome.
func (m *Manager) Stop(ctx context.Context) error {
	if m.stop == nil {
		return nil
	}
	m.mu.Lock()
	m.stopping = true
	live := make([]*liveAttempt, 0, len(m.activeLiveLocked()))
	for _, attempt := range m.activeLiveLocked() {
		live = append(live, attempt)
	}
	m.mu.Unlock()

	for _, attempt := range live {
		m.exec.CancelOwnedRun(attempt.runID)
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		remaining := len(m.activeLiveLocked())
		m.mu.Unlock()
		if remaining == 0 {
			break
		}
		// Process completions that raced with shutdown.
		select {
		case c := <-m.completions:
			m.handleCompletion(c)
			continue
		default:
		}
		select {
		case <-ctx.Done():
			m.stop()
			<-m.loopDone
			return fmt.Errorf("workflow shutdown left %d owned workers running", remaining)
		case <-ticker.C:
		}
	}

	m.stop()
	select {
	case <-m.loopDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (m *Manager) activeLiveLocked() map[string]*liveAttempt {
	if m.active == nil {
		return nil
	}
	return m.active.live
}

// Notify wakes the reconciliation loop without blocking the caller.
func (m *Manager) Notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) loop(ctx context.Context) {
	defer close(m.loopDone)
	ticker := time.NewTicker(m.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
			m.reconcile()
		case c := <-m.completions:
			m.handleCompletion(c)
			m.reconcile()
		case <-ticker.C:
			m.reconcile()
		}
	}
}

func (m *Manager) notifyRevisionLocked() {
	close(m.revisionC)
	m.revisionC = make(chan struct{})
}

func (m *Manager) revisionChannel() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revisionC
}

func appendUniqueReason(reasons []string, add ...string) []string {
	seen := map[string]bool{}
	for _, reason := range reasons {
		seen[reason] = true
	}
	for _, reason := range add {
		if !seen[reason] {
			reasons = append(reasons, reason)
			seen[reason] = true
		}
	}
	return reasons
}

// HashWorkflowDefinition fingerprints the resolved definition together with
// the agent options it will execute with, so replayed submissions can be
// compared byte-for-byte.
func HashWorkflowDefinition(def domain.WorkflowDefinition, agents map[string]domain.AgentSpec) string {
	payload := struct {
		Definition domain.WorkflowDefinition   `json:"definition"`
		Agents     map[string]domain.AgentSpec `json:"agents"`
	}{Definition: def, Agents: agents}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// resolvedAgents returns copies of the configured agent specs referenced by a
// definition, so executions keep an immutable agent configuration. The
// effective Yolo flag is pinned here: an agent without its own override must
// keep the global default that was active at creation time, even if the
// server's defaults change before a recovery restart.
func resolvedAgents(cfg domain.SessionConfig, def domain.WorkflowDefinition) map[string]domain.AgentSpec {
	agents := map[string]domain.AgentSpec{}
	for _, spec := range cfg.Agents {
		for _, task := range def.Tasks {
			if task.Agent == spec.Name {
				if spec.Yolo == nil {
					yolo := cfg.AgentYolo(spec)
					spec.Yolo = &yolo
				}
				agents[spec.Name] = spec
				break
			}
		}
	}
	return agents
}

// Create submits the configured definition under a request ID. It returns the
// execution view and whether a new execution was created (versus an idempotent
// replay).
func (m *Manager) Create(requestID string) (domain.WorkflowExecutionView, bool, error) {
	if requestID == "" {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}
	if m.cfg.Workflow == nil {
		return domain.WorkflowExecutionView{}, false, ErrNoDefinition
	}
	def := *m.cfg.Workflow
	if err := config.ValidateWorkflowDefinition(def, m.cfg.Agents); err != nil {
		return domain.WorkflowExecutionView{}, false, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	agents := resolvedAgents(m.cfg, def)
	hash := HashWorkflowDefinition(def, agents)

	m.mu.Lock()
	defer m.mu.Unlock()

	ids, err := m.store.ListWorkflowExecutions()
	if err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	for _, id := range ids {
		snapshot, err := m.store.LoadWorkflowSnapshot(id)
		if err != nil {
			return domain.WorkflowExecutionView{}, false, err
		}
		if snapshot.RequestID != requestID {
			continue
		}
		if snapshot.DefinitionHash == hash {
			return m.buildViewLocked(&snapshot), false, nil
		}
		return domain.WorkflowExecutionView{}, false, ErrDefinitionChanged
	}

	if m.active != nil {
		return domain.WorkflowExecutionView{}, false, ErrExecutionActive
	}
	for _, id := range ids {
		snapshot, err := m.store.LoadWorkflowSnapshot(id)
		if err != nil {
			return domain.WorkflowExecutionView{}, false, err
		}
		if !snapshot.State.Terminal() {
			return domain.WorkflowExecutionView{}, false, ErrExecutionActive
		}
	}

	executionID, err := m.store.NextWorkflowExecutionID()
	if err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	now := m.now()
	tasks := make(map[string]*domain.WorkflowTaskExecution, len(def.Tasks))
	for taskID, task := range def.Tasks {
		tasks[taskID] = &domain.WorkflowTaskExecution{
			TaskID: taskID,
			Agent:  task.Agent,
			State:  domain.WorkflowTaskPending,
		}
	}
	snapshot := &domain.WorkflowSnapshot{
		SchemaVersion:  domain.WorkflowSnapshotSchemaVersion,
		ExecutionID:    executionID,
		Revision:       1,
		Definition:     def,
		DefinitionHash: hash,
		Agents:         agents,
		RequestID:      requestID,
		State:          domain.WorkflowRunning,
		Mode:           domain.WorkflowModeRunning,
		Tasks:          tasks,
		Loops:          newLoopExecutions(def),
		RetryRequests:  map[string]domain.WorkflowRetryRecord{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := m.store.SaveWorkflowSnapshot(snapshot); err != nil {
		return domain.WorkflowExecutionView{}, false, err
	}
	_ = m.store.AppendWorkflowEvent(executionID, domain.WorkflowControlEvent{Type: "create", At: now, RequestID: requestID})
	m.active = &execution{snapshot: snapshot, live: map[string]*liveAttempt{}}
	m.notifyRevisionLocked()
	m.Notify()
	return m.buildViewLocked(snapshot), true, nil
}

func (m *Manager) findSnapshotLocked(executionID string) (*domain.WorkflowSnapshot, bool) {
	if m.active != nil && m.active.snapshot.ExecutionID == executionID {
		return m.active.snapshot, true
	}
	return nil, false
}

func (m *Manager) loadSnapshot(executionID string) (*domain.WorkflowSnapshot, error) {
	m.mu.Lock()
	snapshot, active := m.findSnapshotLocked(executionID)
	m.mu.Unlock()
	if active {
		return snapshot, nil
	}
	loaded, err := m.store.LoadWorkflowSnapshot(executionID)
	if err != nil {
		return nil, ErrExecutionNotFound
	}
	return &loaded, nil
}

// List returns summaries of every persisted execution, newest last.
func (m *Manager) List() ([]domain.WorkflowExecutionSummary, error) {
	ids, err := m.store.ListWorkflowExecutions()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	summaries := make([]domain.WorkflowExecutionSummary, 0, len(ids))
	for _, id := range ids {
		snapshot, ok := m.findSnapshotLocked(id)
		if !ok {
			loaded, err := m.store.LoadWorkflowSnapshot(id)
			if err != nil {
				return nil, err
			}
			snapshot = &loaded
		}
		summaries = append(summaries, buildSummary(snapshot))
	}
	return summaries, nil
}

func buildSummary(snapshot *domain.WorkflowSnapshot) domain.WorkflowExecutionSummary {
	return domain.WorkflowExecutionSummary{
		ExecutionID: snapshot.ExecutionID,
		Name:        snapshot.Definition.Name,
		RequestID:   snapshot.RequestID,
		State:       snapshot.State,
		Revision:    snapshot.Revision,
		TaskCounts:  countTaskStates(snapshot),
		CreatedAt:   snapshot.CreatedAt,
		UpdatedAt:   snapshot.UpdatedAt,
	}
}

// View renders the current execution state for observation.
func (m *Manager) View(executionID string) (domain.WorkflowExecutionView, error) {
	snapshot, err := m.loadSnapshot(executionID)
	if err != nil {
		return domain.WorkflowExecutionView{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buildViewLocked(snapshot), nil
}
