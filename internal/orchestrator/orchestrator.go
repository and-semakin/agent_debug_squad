package orchestrator

import (
	"context"
	"errors"
	"fmt"
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
)

type Orchestrator struct {
	cfg     domain.SessionConfig
	store   *store.Store
	execCtx context.Context

	mu       sync.Mutex
	runtimes map[string]*agentRuntime
	waiters  map[string]chan struct{}
	nextRun  int
}

type agentRuntime struct {
	spec    domain.AgentSpec
	state   domain.AgentState
	adapter adapters.AgentAdapter
	busy    bool
}

func New(ctx context.Context, cfg domain.SessionConfig, s *store.Store) (*Orchestrator, error) {
	if err := s.SaveConfig(cfg); err != nil {
		return nil, err
	}
	if err := s.MarkActiveRunsInterrupted(); err != nil {
		return nil, err
	}

	o := &Orchestrator{
		cfg:      cfg,
		store:    s,
		execCtx:  context.Background(),
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
		adapter, err := adapters.New(spec)
		if err != nil {
			return nil, err
		}

		state, err := s.LoadAgentState(spec.Name)
		if errors.Is(err, os.ErrNotExist) {
			state = domain.AgentState{
				Name:          spec.Name,
				Backend:       spec.Backend,
				StartupPrompt: spec.StartupPrompt,
				WorkspaceDir:  cfg.WorkspaceDir,
			}
		} else if err != nil {
			return nil, err
		}
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
	if rt.busy {
		o.mu.Unlock()
		return domain.RunRecord{}, ErrAgentBusy
	}

	runID := fmt.Sprintf("run_%06d", o.nextRun)
	o.nextRun++
	rt.busy = true
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
		o.releaseAgent(agentName)
		o.notify(runID)
		return domain.RunRecord{}, err
	}

	go o.runWorker(o.execCtx, agentName, run, waiter)
	return run, nil
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

func (o *Orchestrator) runWorker(ctx context.Context, agentName string, run domain.RunRecord, waiter chan struct{}) {
	defer func() {
		o.releaseAgent(agentName)
		close(waiter)
		o.mu.Lock()
		delete(o.waiters, run.RunID)
		o.mu.Unlock()
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

	result, newState, sendErr := adapter.Send(ctx, state, domain.RunRequest{
		RunID:    run.RunID,
		Agent:    run.Agent,
		Message:  run.Message,
		Metadata: cloneMetadata(run.Metadata),
	})

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

	if sendErr != nil || result.ErrorMessage != "" {
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

func (o *Orchestrator) releaseAgent(agentName string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if rt, ok := o.runtimes[agentName]; ok {
		rt.busy = false
	}
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
