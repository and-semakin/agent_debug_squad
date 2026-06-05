package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrey/agent-debug-squad/internal/adapters"
	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/store"
)

var (
	ErrAgentNotFound = errors.New("agent not found")
	ErrAgentBusy     = errors.New("agent busy")
	ErrRunNotFound   = errors.New("run not found")
	ErrWaitTimeout   = errors.New("wait timeout")
	ErrResetTimeout  = errors.New("reset timeout")
)

const forceResetTimeout = 5 * time.Second

type Orchestrator struct {
	cfg     domain.SessionConfig
	store   *store.Store
	execCtx context.Context

	mu       sync.Mutex
	runtimes map[string]*agentRuntime
	waiters  map[string]chan struct{}
	nextRun  int
	workerWG sync.WaitGroup
}

type agentRuntime struct {
	spec              domain.AgentSpec
	state             domain.AgentState
	adapter           adapters.AgentAdapter
	busy              bool
	resetting         bool
	activeRunID       string
	cancelActiveRun   context.CancelFunc
	activeRunDone     chan struct{}
	runInterruptible  bool
	interruptingRunID string
}

func New(ctx context.Context, cfg domain.SessionConfig, s *store.Store) (*Orchestrator, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.SaveConfig(cfg); err != nil {
		return nil, err
	}
	if err := s.MarkActiveRunsInterrupted(); err != nil {
		return nil, err
	}

	o := &Orchestrator{
		cfg:      cfg,
		store:    s,
		execCtx:  ctx,
		runtimes: map[string]*agentRuntime{},
		waiters:  map[string]chan struct{}{},
		nextRun:  1,
	}

	runs, err := s.ListRuns()
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if n, ok := parseRunNumber(run.RunID); ok && n >= o.nextRun {
			o.nextRun = n + 1
		}
	}

	for _, spec := range cfg.Agents {
		spec = agentSpecWithDefaults(cfg, spec)
		adapter, err := adapters.New(spec)
		if err != nil {
			return nil, err
		}

		state, err := s.LoadAgentState(spec.Name)
		if errors.Is(err, os.ErrNotExist) {
			state = domain.AgentState{
				Name:          spec.Name,
				Backend:       spec.Backend,
				Model:         spec.StringOptions["model"],
				StartupPrompt: spec.StartupPrompt,
				WorkspaceDir:  cfg.WorkspaceDir,
			}
		} else if err != nil {
			return nil, err
		}
		state.Model = spec.StringOptions["model"]
		state.WorkspaceDir = cfg.WorkspaceDir

		state, err = adapter.Init(ctx, spec, state)
		if err != nil {
			return nil, err
		}
		if state.WorkspaceDir == "" {
			state.WorkspaceDir = cfg.WorkspaceDir
		}
		if err := s.SaveAgentState(state); err != nil {
			return nil, err
		}

		o.runtimes[spec.Name] = &agentRuntime{
			spec:    spec,
			state:   state,
			adapter: adapter,
		}
	}

	return o, nil
}

func agentSpecWithDefaults(cfg domain.SessionConfig, spec domain.AgentSpec) domain.AgentSpec {
	if spec.Yolo == nil {
		yolo := cfg.AgentYolo(spec)
		spec.Yolo = &yolo
	}
	return spec
}

func (o *Orchestrator) Agents() []domain.AgentState {
	o.mu.Lock()
	defer o.mu.Unlock()

	states := make([]domain.AgentState, 0, len(o.cfg.Agents))
	for _, spec := range o.cfg.Agents {
		if rt, ok := o.runtimes[spec.Name]; ok {
			states = append(states, rt.state)
		}
	}
	return states
}

func (o *Orchestrator) Agent(name string) (domain.AgentState, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	rt, ok := o.runtimes[name]
	if !ok {
		return domain.AgentState{}, false
	}
	return rt.state, true
}

func (o *Orchestrator) SubmitRun(ctx context.Context, agentName, message string, metadata map[string]string) (domain.RunRecord, error) {
	o.mu.Lock()
	rt, ok := o.runtimes[agentName]
	if !ok {
		o.mu.Unlock()
		return domain.RunRecord{}, ErrAgentNotFound
	}
	if rt.busy || rt.resetting {
		o.mu.Unlock()
		return domain.RunRecord{}, ErrAgentBusy
	}

	runID := fmt.Sprintf("run_%06d", o.nextRun)
	o.nextRun++
	runCtx, cancel := context.WithCancel(o.execCtx)
	rt.busy = true
	rt.activeRunID = runID
	rt.cancelActiveRun = cancel
	rt.activeRunDone = make(chan struct{})
	rt.runInterruptible = true
	run := domain.RunRecord{
		RunID:     runID,
		Agent:     agentName,
		Status:    domain.RunQueued,
		Message:   message,
		Metadata:  cloneMetadata(metadata),
		CreatedAt: time.Now().UTC(),
	}
	waiter := o.waiterLocked(runID)
	o.mu.Unlock()

	if err := o.store.SaveRun(run); err != nil {
		cancel()
		o.releaseAgent(agentName)
		o.notify(runID)
		return domain.RunRecord{}, err
	}
	if err := o.store.AppendTranscript(domain.TranscriptEvent{
		Type:     "facilitator_message",
		RunID:    run.RunID,
		To:       agentName,
		Text:     message,
		Metadata: cloneMetadata(metadata),
		At:       run.CreatedAt,
	}); err != nil {
		cancel()
		o.releaseAgent(agentName)
		o.notify(runID)
		return domain.RunRecord{}, err
	}

	o.workerWG.Add(1)
	go o.runWorker(runCtx, agentName, run, waiter)
	return run, nil
}

func (o *Orchestrator) WaitForWorkers(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		o.workerWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Orchestrator) Wait(ctx context.Context, runID string, timeout time.Duration) (domain.RunRecord, error) {
	run, err := o.Run(ctx, runID)
	if err != nil {
		return run, err
	}
	if isTerminal(run.Status) {
		o.deleteWaiter(runID)
		return run, nil
	}

	o.mu.Lock()
	waiter, hasWaiter := o.waiters[runID]
	registered := false
	if !hasWaiter {
		waiter = make(chan struct{})
		o.waiters[runID] = waiter
		registered = true
	}
	o.mu.Unlock()

	run, err = o.Run(ctx, runID)
	if err != nil {
		return run, err
	}
	if isTerminal(run.Status) {
		if registered {
			o.closeRegisteredWaiter(runID, waiter)
		} else {
			o.deleteWaiter(runID)
		}
		return run, nil
	}

	var timeoutC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutC = timer.C
	}

	select {
	case <-waiter:
		return o.Run(ctx, runID)
	case <-timeoutC:
		latest, latestErr := o.Run(context.Background(), runID)
		if latestErr != nil {
			return latest, latestErr
		}
		return latest, ErrWaitTimeout
	case <-ctx.Done():
		latest, latestErr := o.Run(context.Background(), runID)
		if latestErr != nil {
			return latest, latestErr
		}
		return latest, ctx.Err()
	}
}

func (o *Orchestrator) Run(ctx context.Context, runID string) (domain.RunRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.RunRecord{}, err
	}
	run, err := o.store.LoadRun(runID)
	if errors.Is(err, os.ErrNotExist) {
		return domain.RunRecord{}, ErrRunNotFound
	}
	return run, err
}

func (o *Orchestrator) Runs(ctx context.Context) ([]domain.RunRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return o.store.ListRuns()
}

func (o *Orchestrator) Transcript(ctx context.Context) ([]domain.TranscriptEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return o.store.ReadTranscript()
}

func (o *Orchestrator) ResetAgent(ctx context.Context, agentName string, force bool) (domain.AgentState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return domain.AgentState{}, err
	}

	var activeRunID string
	var cancel context.CancelFunc
	var activeRunDone <-chan struct{}

	o.mu.Lock()
	rt, ok := o.runtimes[agentName]
	if !ok {
		o.mu.Unlock()
		return domain.AgentState{}, ErrAgentNotFound
	}
	if rt.resetting {
		o.mu.Unlock()
		return domain.AgentState{}, ErrAgentBusy
	}
	if rt.busy {
		if !force {
			o.mu.Unlock()
			return domain.AgentState{}, ErrAgentBusy
		}
		activeRunDone = rt.activeRunDone
		if rt.runInterruptible && rt.activeRunID != "" {
			activeRunID = rt.activeRunID
			cancel = rt.cancelActiveRun
			rt.interruptingRunID = activeRunID
		}
	}
	rt.resetting = true
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		if rt := o.runtimes[agentName]; rt != nil {
			rt.resetting = false
		}
		o.mu.Unlock()
	}()

	if activeRunID != "" && cancel != nil {
		cancel()
	}
	if activeRunDone != nil {
		if err := waitForWorkerDone(ctx, activeRunDone, forceResetTimeout); err != nil {
			return domain.AgentState{}, err
		}
	}

	o.mu.Lock()
	rt = o.runtimes[agentName]
	state := rt.state
	spec := rt.spec
	adapter := rt.adapter
	o.mu.Unlock()

	reset, err := adapter.Reset(ctx, spec, state)
	if err != nil {
		o.markAgentResetFailure(agentName, state, err)
		return domain.AgentState{}, err
	}
	if reset.WorkspaceDir == "" {
		reset.WorkspaceDir = o.cfg.WorkspaceDir
	}
	if err := o.store.SaveAgentState(reset); err != nil {
		o.markAgentResetFailure(agentName, reset, err)
		return domain.AgentState{}, err
	}
	if err := o.store.AppendTranscript(domain.TranscriptEvent{
		Type:     "agent_reset",
		Agent:    agentName,
		Status:   resetEventStatus(force, activeRunID),
		Metadata: resetMetadata(force, activeRunID),
		At:       time.Now().UTC(),
	}); err != nil {
		o.markAgentResetFailure(agentName, reset, err)
		return domain.AgentState{}, err
	}

	o.mu.Lock()
	if current := o.runtimes[agentName]; current != nil {
		current.state = reset
	}
	o.mu.Unlock()
	return reset, nil
}

func waitForWorkerDone(ctx context.Context, done <-chan struct{}, timeout time.Duration) error {
	var timeoutC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutC = timer.C
	}

	select {
	case <-done:
		return nil
	case <-timeoutC:
		return ErrResetTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Orchestrator) runWorker(ctx context.Context, agentName string, run domain.RunRecord, waiter chan struct{}) {
	defer func() {
		defer o.workerWG.Done()
		close(waiter)
		o.mu.Lock()
		delete(o.waiters, run.RunID)
		o.mu.Unlock()
		o.releaseAgent(agentName)
	}()

	started := time.Now().UTC()
	run.Status = domain.RunRunning
	run.StartedAt = &started
	_ = o.store.SaveRun(run)

	o.mu.Lock()
	rt := o.runtimes[agentName]
	rt.state.Status = domain.AgentRunning
	state := rt.state
	adapter := rt.adapter
	o.mu.Unlock()
	_ = o.store.SaveAgentState(state)

	sink := newRunSink(o.store, run, log.Default())
	result, newState, sendErr := adapter.Send(ctx, state, domain.RunRequest{
		RunID:    run.RunID,
		Agent:    run.Agent,
		Message:  run.Message,
		Metadata: cloneMetadata(run.Metadata),
	}, sink)
	if sinkErr := sink.Err(); sinkErr != nil && sendErr == nil {
		sendErr = sinkErr
	}

	completed := time.Now().UTC()
	run.CompletedAt = &completed
	if result.Stderr != "" {
		stderrPath, err := o.store.WriteRunStderr(run, result.Stderr)
		if err != nil && sendErr == nil {
			sendErr = fmt.Errorf("write run stderr: %w", err)
		}
		if stderrPath != "" && sendErr == nil && result.FinalMessage == "" {
			result.FinalMessage = stderrPath
		}
	}
	if result.FinalMessage != "" {
		outputPath, err := o.store.WriteAgentOutput(run, result.FinalMessage)
		if err != nil && sendErr == nil {
			sendErr = fmt.Errorf("write agent output: %w", err)
		}
		if outputPath != "" && err == nil {
			run.OutputPath = &outputPath
		}
	}

	interruptedByReset := o.finishRunInterruptible(agentName, run.RunID)
	if interruptedByReset {
		run.Status = domain.RunInterrupted
		message := "interrupted by force reset"
		run.Error = &message
		newState.Status = domain.AgentIdle
		newState.LastError = nil
	} else if sendErr != nil || result.ErrorMessage != "" {
		run.Status = domain.RunFailed
		message := result.ErrorMessage
		if message == "" {
			message = sendErr.Error()
		}
		run.Error = &message
		newState.Status = domain.AgentFailed
		newState.LastError = &message
	} else {
		run.Status = domain.RunCompleted
		newState.Status = domain.AgentIdle
		newState.LastError = nil
	}
	newState.LastRunID = run.RunID
	if newState.WorkspaceDir == "" {
		newState.WorkspaceDir = o.cfg.WorkspaceDir
	}

	if err := o.store.SaveAgentState(newState); err != nil {
		markPersistenceFailure(&run, &newState, fmt.Errorf("save agent state: %w", err))
	}
	if err := o.store.AppendTranscript(domain.TranscriptEvent{
		Type:       "agent_result",
		RunID:      run.RunID,
		Agent:      run.Agent,
		OutputPath: deref(run.OutputPath),
		Status:     run.Status,
		At:         completed,
	}); err != nil {
		markPersistenceFailure(&run, &newState, fmt.Errorf("append transcript: %w", err))
	}
	if err := o.store.SaveRun(run); err != nil {
		markPersistenceFailure(&run, &newState, fmt.Errorf("save run: %w", err))
		_ = o.store.SaveRun(run)
	}

	o.mu.Lock()
	if current := o.runtimes[agentName]; current != nil {
		current.state = newState
	}
	o.mu.Unlock()
}

func markPersistenceFailure(run *domain.RunRecord, state *domain.AgentState, err error) {
	message := err.Error()
	run.Status = domain.RunFailed
	run.Error = &message
	state.Status = domain.AgentFailed
	state.LastError = &message
}

func (o *Orchestrator) markAgentResetFailure(agentName string, state domain.AgentState, err error) {
	message := err.Error()
	state.Status = domain.AgentFailed
	state.LastError = &message

	o.mu.Lock()
	defer o.mu.Unlock()
	if rt := o.runtimes[agentName]; rt != nil {
		rt.state = state
		_ = o.store.SaveAgentState(state)
	}
}

func (o *Orchestrator) releaseAgent(agentName string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if rt, ok := o.runtimes[agentName]; ok {
		if rt.activeRunDone != nil {
			close(rt.activeRunDone)
		}
		rt.busy = false
		rt.activeRunID = ""
		rt.cancelActiveRun = nil
		rt.activeRunDone = nil
		rt.runInterruptible = false
		rt.interruptingRunID = ""
	}
}

func (o *Orchestrator) finishRunInterruptible(agentName, runID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	rt, ok := o.runtimes[agentName]
	if !ok || rt.activeRunID != runID {
		return false
	}
	rt.runInterruptible = false
	if rt.interruptingRunID != runID {
		return false
	}
	rt.interruptingRunID = ""
	return true
}

func (o *Orchestrator) notify(runID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if waiter, ok := o.waiters[runID]; ok {
		close(waiter)
		delete(o.waiters, runID)
	}
}

func (o *Orchestrator) deleteWaiter(runID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.waiters, runID)
}

func (o *Orchestrator) closeRegisteredWaiter(runID string, waiter chan struct{}) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if current, ok := o.waiters[runID]; ok && current == waiter {
		close(waiter)
		delete(o.waiters, runID)
	}
}

func (o *Orchestrator) waiterLocked(runID string) chan struct{} {
	waiter, ok := o.waiters[runID]
	if !ok {
		waiter = make(chan struct{})
		o.waiters[runID] = waiter
	}
	return waiter
}

func parseRunNumber(runID string) (int, bool) {
	value, ok := strings.CutPrefix(runID, "run_")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	return n, err == nil
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	clone := make(map[string]string, len(metadata))
	for k, v := range metadata {
		clone[k] = v
	}
	return clone
}

func resetMetadata(force bool, previousRunID string) map[string]string {
	metadata := map[string]string{"force": strconv.FormatBool(force)}
	if previousRunID != "" {
		metadata["previous_run_id"] = previousRunID
	}
	return metadata
}

func resetEventStatus(force bool, previousRunID string) domain.RunStatus {
	if force && previousRunID != "" {
		return domain.RunInterrupted
	}
	return ""
}

func isTerminal(status domain.RunStatus) bool {
	switch status {
	case domain.RunCompleted, domain.RunFailed, domain.RunInterrupted:
		return true
	default:
		return false
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
