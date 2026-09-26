package cursor

import (
	"context"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
)

// CheckInstallation reports whether the effective cursor command resolves
// under the effective child environment. The documented upstream launcher is
// `agent`; the repository default stays `cursor-agent`, so an installed
// upstream distribution needs an explicit command override — a missing
// default never falls back to another alias.
func (a *Adapter) CheckInstallation(ctx context.Context, input domain.InstallationInput) domain.InstallationResult {
	return preflight.CheckCLI(a.spec, input, "cursor", "cursor-agent", BuildEnv)
}
