package codex

import (
	"context"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
)

// CheckInstallation reports whether the effective codex command resolves and
// its launcher interpreter is available under the effective child
// environment, without contacting any model provider.
func (a *Adapter) CheckInstallation(ctx context.Context, input domain.InstallationInput) domain.InstallationResult {
	return preflight.CheckCLI(a.spec, input, "codex", "codex", BuildEnv)
}
