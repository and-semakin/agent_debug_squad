package kimi

import (
	"context"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
)

// CheckInstallation reports whether the effective kimi command resolves. It
// follows the same ambient-versus-constrained environment rule as the
// associated launch: childEnv returns nil when the spec carries no
// environment entries, which means the captured ambient environment.
func (a *Adapter) CheckInstallation(ctx context.Context, input domain.InstallationInput) domain.InstallationResult {
	return preflight.CheckCLI(a.spec, input, "kimi", "kimi", childEnv)
}
