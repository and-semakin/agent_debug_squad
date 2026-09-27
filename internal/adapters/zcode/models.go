package zcode

import (
	"context"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
)

// Listing through host.main would load credentials and bootstrap account state.
// Until a read-only bootstrap is verified, never load the runtime for discovery.
func (a *Adapter) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("zcode", "zcode_registry", "registry", "configured", in)
	modelprobe.Fail(&r, modelprobe.ContextStatus(ctx, "unsupported"), "read_only_catalog_unavailable")
	return r
}
