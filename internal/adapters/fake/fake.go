package fake

import (
	"context"
	"fmt"
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
	case <-time.After(50 * time.Millisecond):
	}
	state.LastRunID = run.RunID
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
