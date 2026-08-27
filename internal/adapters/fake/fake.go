package fake

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

type Adapter struct {
	spec domain.AgentSpec
}

func New(spec domain.AgentSpec) *Adapter {
	return &Adapter{spec: spec}
}

func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if state.Name == "" {
		state = domain.AgentState{
			Name:             spec.Name,
			Backend:          spec.Backend,
			StartupPrompt:    spec.StartupPrompt,
			BackendSessionID: "fake_" + spec.Name,
			Status:           domain.AgentIdle,
			CreatedAt:        time.Now().UTC(),
		}
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error) {
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	sink.StdoutLine(fmt.Sprintf("%s received run %s", state.Name, run.RunID))
	select {
	case <-ctx.Done():
		return domain.RunResult{}, state, ctx.Err()
	case <-time.After(a.delay()):
	}
	state.Status = domain.AgentIdle
	return domain.RunResult{
		FinalMessage: fmt.Sprintf("%s received: %s", state.Name, run.Message),
	}, state, nil
}

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = spec.StringOptions["model"]
	state.StartupPrompt = spec.StartupPrompt
	state.BackendSessionID = "fake_" + spec.Name + "_reset"
	state.Status = domain.AgentIdle
	state.LastRunID = ""
	state.LastError = nil
	return state, nil
}

func (a *Adapter) delay() time.Duration {
	value := strings.TrimSpace(a.spec.StringOptions["delay_ms"])
	if value == "" {
		return 50 * time.Millisecond
	}
	ms, err := strconv.Atoi(value)
	if err != nil || ms < 0 {
		return 50 * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}
