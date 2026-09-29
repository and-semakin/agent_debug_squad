package config

import (
	"fmt"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/zcode"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// ValidateZCodeAgent rejects removed or unsupported ZCode options during YAML
// load and workflow validation, before preflight or run allocation. The
// plan_policy option was removed with Start Plan routing and now fails with an
// actionable error; the provider option accepts only the Individual Coding
// Plan ID. Direct adapter construction enforces the same rules in Init.
func ValidateZCodeAgent(spec domain.AgentSpec) error {
	if spec.Backend != "zcode" {
		return nil
	}
	if _, set := spec.StringOptions["plan_policy"]; set {
		return fmt.Errorf("agent %q: zcode plan_policy was removed; remove it from the agent options", spec.Name)
	}
	if provider := spec.StringOptions["provider"]; provider != "" && provider != zcode.ProviderIndividual {
		return fmt.Errorf("agent %q: zcode supports only provider %s", spec.Name, zcode.ProviderIndividual)
	}
	return nil
}
