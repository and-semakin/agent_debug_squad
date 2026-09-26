package fake

import (
	"context"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// CheckInstallation reports that the fake backend has no local installation
// requirement: no filesystem discovery and no network requests.
func (a *Adapter) CheckInstallation(ctx context.Context, input domain.InstallationInput) domain.InstallationResult {
	return domain.InstallationResult{Status: domain.InstallationStatusNotRequired}
}
