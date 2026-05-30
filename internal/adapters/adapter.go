package adapters

import (
	"context"
	"fmt"

	"github.com/andrey/agent-debug-squad/internal/adapters/codex"
	"github.com/andrey/agent-debug-squad/internal/adapters/fake"
	"github.com/andrey/agent-debug-squad/internal/domain"
)

type AgentAdapter interface {
	Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
	Send(ctx context.Context, state domain.AgentState, run domain.RunRequest) (domain.RunResult, domain.AgentState, error)
	Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error)
}

func New(spec domain.AgentSpec) (AgentAdapter, error) {
	switch spec.Backend {
	case "fake":
		return fake.New(spec), nil
	case "codex":
		return codex.New(spec), nil
	case "opencode", "kimi":
		return nil, fmt.Errorf("backend %q is wired in a later implementation task", spec.Backend)
	default:
		return nil, fmt.Errorf("unknown backend %q", spec.Backend)
	}
}
