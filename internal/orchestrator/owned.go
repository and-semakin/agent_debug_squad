package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/adapters"
	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// SubmitOwnedRun executes one workflow attempt on a fresh, workflow-owned
// runtime reserved under the caller-provided run ID. The runtime never loads
// or mutates the manual agent state; its backend session starts empty. OnDone
// fires exactly once, after the worker has stopped and its projections are
// persisted.
func (o *Orchestrator) SubmitOwnedRun(ctx context.Context, opts domain.OwnedRunOptions) error {
	if opts.RunID == "" {
		return ErrRunIDInvalid
	}
	if !IsWorkflowRunID(opts.RunID) {
		return fmt.Errorf("%w: workflow run IDs must use the %s prefix", ErrRunIDInvalid, workflowRunIDPrefix)
	}
	if opts.OnDone == nil {
		return errors.New("owned run requires an OnDone callback")
	}

	o.mu.Lock()
	key := ownedRuntimeKey(opts.RunID)
	if _, exists := o.runtimes[key]; exists {
		o.mu.Unlock()
		return ErrRunIDReserved
	}
	var spec domain.AgentSpec
	if opts.Spec != nil {
		// A workflow execution carries its own immutable agent configuration;
		// use it instead of the server's current YAML so recovery after a
		// config edit keeps the original backend, model, and options.
		spec = *opts.Spec
		if spec.Name == "" {
			spec.Name = opts.Agent
		}
		normalized, err := config.NormalizeAgentOptions(spec)
		if err != nil {
			o.mu.Unlock()
			return fmt.Errorf("resolve saved agent %q: %w", opts.Agent, err)
		}
		spec = normalized
		spec = agentSpecWithDefaults(o.cfg, spec)
	} else {
		found := false
		for _, candidate := range o.cfg.Agents {
			if candidate.Name == opts.Agent {
				spec = candidate
				found = true
				break
			}
		}
		if !found {
			o.mu.Unlock()
			return ErrAgentNotFound
		}
		spec = agentSpecWithDefaults(o.cfg, spec)
	}
	adapter, err := adapters.New(spec)
	if err != nil {
		o.mu.Unlock()
		return err
	}

	runCtx, cancel := context.WithCancel(o.execCtx)
	if ctx.Err() != nil {
		cancel()
		o.mu.Unlock()
		return ctx.Err()
	}
	rt := &agentRuntime{
		spec:            spec,
		adapter:         adapter,
		busy:            true,
		activeRunID:     opts.RunID,
		cancelActiveRun: cancel,
		activeRunDone:   make(chan struct{}),
		owned:           true,
		statePath:       opts.StatePath,
	}
	o.runtimes[key] = rt
	run := domain.RunRecord{
		RunID:     opts.RunID,
		Agent:     opts.Agent,
		Status:    domain.RunQueued,
		Message:   opts.Message,
		Metadata:  cloneMetadata(opts.Metadata),
		CreatedAt: time.Now().UTC(),
	}
	waiter := o.waiterLocked(opts.RunID)
	o.mu.Unlock()

	if err := o.store.SaveRun(run); err != nil {
		o.removeOwnedRuntime(key, cancel)
		o.notify(opts.RunID)
		return err
	}
	if err := o.store.AppendTranscript(domain.TranscriptEvent{
		Type:     "workflow_message",
		RunID:    run.RunID,
		To:       opts.Agent,
		Text:     opts.Message,
		Metadata: cloneMetadata(opts.Metadata),
		At:       run.CreatedAt,
	}); err != nil {
		o.removeOwnedRuntime(key, cancel)
		o.notify(opts.RunID)
		return err
	}
	o.logLifecycle("run=%s agent=%s status=%s owned=true", run.RunID, run.Agent, run.Status)

	o.workerWG.Add(1)
	go func() {
		// An empty identity is the "fresh conversation" signal for Init: the
		// adapter allocates a new backend session instead of reusing one. The
		// workspace is set up front because CLI adapters run child processes
		// with state.WorkspaceDir as their working directory.
		initialized, initErr := adapter.Init(ctx, spec, domain.AgentState{
			Backend:      spec.Backend,
			WorkspaceDir: o.cfg.WorkspaceDir,
		})
		if initErr != nil {
			fresh := domain.AgentState{
				Name:          opts.Agent,
				Backend:       spec.Backend,
				Model:         spec.StringOptions["model"],
				StartupPrompt: spec.StartupPrompt,
				WorkspaceDir:  o.cfg.WorkspaceDir,
				CreatedAt:     time.Now().UTC(),
			}
			o.mu.Lock()
			rt.state = fresh
			o.mu.Unlock()
			// The worker never reached runWorker, whose defer owns Done on
			// the success path; release the reservation here so shutdown
			// joins promptly.
			o.workerWG.Done()
			o.failOwnedRun(run, key, cancel, opts.OnDone, fmt.Errorf("init owned runtime: %w", initErr))
			return
		}
		if initialized.Name == "" {
			initialized.Name = opts.Agent
		}
		if initialized.Backend == "" {
			initialized.Backend = spec.Backend
		}
		if initialized.WorkspaceDir == "" {
			initialized.WorkspaceDir = o.cfg.WorkspaceDir
		}
		o.mu.Lock()
		rt.state = initialized
		o.mu.Unlock()
		o.runWorker(runCtx, key, run, waiter, opts.OnDone)
	}()
	return nil
}

func (o *Orchestrator) failOwnedRun(run domain.RunRecord, key string, cancel context.CancelFunc, onDone func(domain.OwnedRunOutcome), err error) {
	message := err.Error()
	completed := time.Now().UTC()
	run.Status = domain.RunFailed
	run.CompletedAt = &completed
	run.Progress = &domain.RunProgress{Phase: domain.RunPhaseFailed, LastActivityAt: completed}
	run.Error = &message
	_ = o.store.SaveRun(run)
	o.logLifecycle("run=%s agent=%s status=%s owned=true", run.RunID, run.Agent, run.Status)
	o.removeOwnedRuntime(key, cancel)
	o.notify(run.RunID)
	onDone(domain.OwnedRunOutcome{
		RunID:  run.RunID,
		Status: domain.RunFailed,
		Error:  message,
	})
}

func (o *Orchestrator) removeOwnedRuntime(key string, cancel context.CancelFunc) {
	o.mu.Lock()
	if rt := o.runtimes[key]; rt != nil {
		if rt.activeRunDone != nil {
			close(rt.activeRunDone)
		}
		delete(o.runtimes, key)
	}
	o.mu.Unlock()
	cancel()
}

// CancelOwnedRun requests cancellation of a live owned run and reports whether
// an active worker was found.
func (o *Orchestrator) CancelOwnedRun(runID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	rt := o.runtimes[ownedRuntimeKey(runID)]
	if rt == nil || !rt.busy || rt.activeRunID != runID || rt.cancelActiveRun == nil {
		return false
	}
	rt.cancelActiveRun()
	return true
}

// OwnedRunActive reports whether an owned run still has a live worker.
func (o *Orchestrator) OwnedRunActive(runID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	rt := o.runtimes[ownedRuntimeKey(runID)]
	return rt != nil && rt.busy && rt.activeRunID == runID
}
