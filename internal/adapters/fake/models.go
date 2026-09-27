package fake

import (
	"context"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
)

func (a *Adapter) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("fake", "synthetic", "synthetic", "synthetic", in)
	if ctx.Err() != nil {
		modelprobe.Fail(&r, modelprobe.ContextStatus(ctx, "error"), "context_ended")
		return r
	}
	r.Models = append(r.Models, modelprobe.Model("fake", "fake", "synthetic"))
	return r
}
