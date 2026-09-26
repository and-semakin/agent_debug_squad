package adapters

import (
	"context"
	"fmt"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/codex"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/cursor"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/fake"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/kimi"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/zcode"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

type AgentAdapter interface {
	Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
	Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error)
	Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error)
	Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
}

func New(spec domain.AgentSpec) (AgentAdapter, error) {
	switch spec.Backend {
	case "zcode":
		return zcode.New(spec), nil
	case "fake":
		return fake.New(spec), nil
	case "codex":
		return codex.New(spec), nil
	case "cursor":
		return cursor.New(spec), nil
	case "kimi":
		return kimi.New(spec), nil
	case "opencode":
		return nil, fmt.Errorf("opencode requires an owned runtime; construct it through the orchestrator")
	default:
		return nil, fmt.Errorf("unknown backend %q", spec.Backend)
	}
}

// PermissionReplier is optional; CLI adapters need not implement it.
type PermissionReplier interface {
	ReplyPermission(ctx context.Context, runID, requestID string, reply domain.PermissionReply) error
}
