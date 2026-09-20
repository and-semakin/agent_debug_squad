package orchestrator

import (
	"context"

	"github.com/and-semakin/agent_debug_squad/internal/adapters"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func (o *Orchestrator) ReplyPermission(ctx context.Context, runID, requestID string, reply domain.PermissionReply) error {
	if err := reply.Validate(); err != nil {
		return err
	}
	run, err := o.Run(ctx, runID)
	if err != nil {
		return err
	}
	o.mu.Lock()
	rt := o.runtimes[runtimeKeyForRun(run)]
	if isTerminal(run.Status) || rt == nil || !rt.busy || rt.resetting || rt.activeRunID != runID {
		o.mu.Unlock()
		return domain.ErrPermissionInactive
	}
	replier, ok := rt.adapter.(adapters.PermissionReplier)
	o.mu.Unlock()
	if !ok {
		return domain.ErrPermissionUnsupported
	}
	return replier.ReplyPermission(ctx, runID, requestID, reply)
}
