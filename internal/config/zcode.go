package config

import (
	"fmt"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/zcode"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// ValidateZCodeAgent rejects invalid ZCode plan-policy/provider combinations
// during YAML load and workflow validation, before preflight or run
// allocation. plan_policy is agent-only: the machine backend schema never
// accepts it. Direct adapter construction enforces the same rule in Init.
func ValidateZCodeAgent(spec domain.AgentSpec) error {
	if spec.Backend != "zcode" {
		return nil
	}
	policy := spec.StringOptions["plan_policy"]
	provider := spec.StringOptions["provider"]
	if err := zcode.ValidatePlanPolicy(policy, provider); err != nil {
		return fmt.Errorf("agent %q: %w", spec.Name, err)
	}
	return nil
}
