package zcode

import (
	"fmt"
	"strings"
)

// Account plan routing policy. plan_policy is an agent-only option; the
// machine backend schema must not accept it. Omission stays fixed so existing
// configurations keep their provider selection and defaults.
const (
	PlanPolicyFixed      = "fixed"
	PlanPolicyStartFirst = "start-first"
)

// The two Z.AI account providers that participate in plan routing. Both IDs
// are real entries of the installed runtime's built-in provider configuration
// at the inspected upstream commit; an explicit start-first value designates
// the routing family, never a pinned plan.
const (
	ProviderIndividual = "account:zai-individual-coding-plan"
	ProviderStart      = "account:zai-start-plan"
)

// ValidatePlanPolicy enforces the plan_policy/provider combination. It runs
// at YAML load before preflight or run allocation and equivalently on direct
// adapter construction. Fixed retains the existing Individual-only provider
// restriction. Start-first accepts an omitted provider or exactly one of the
// two routing-family IDs; every other provider fails.
func ValidatePlanPolicy(policy, provider string) error {
	policy = strings.TrimSpace(policy)
	provider = strings.TrimSpace(provider)
	switch policy {
	case "", PlanPolicyFixed:
		if provider != "" && provider != ProviderIndividual {
			return fmt.Errorf("zcode plan_policy fixed supports only provider %s", ProviderIndividual)
		}
	case PlanPolicyStartFirst:
		if provider != "" && provider != ProviderStart && provider != ProviderIndividual {
			return fmt.Errorf("zcode plan_policy start-first accepts an omitted provider, %s or %s", ProviderStart, ProviderIndividual)
		}
	default:
		return fmt.Errorf("zcode plan_policy must be %q or %q", PlanPolicyFixed, PlanPolicyStartFirst)
	}
	return nil
}

// validateAgentOptions revalidates the plan policy and reasoning choices for a
// resolved agent specification. It is shared by adapter Init and the config
// normalization so YAML load and direct construction reject the same inputs.
func validateAgentOptions(policy, provider, reasoning string) error {
	if err := ValidatePlanPolicy(policy, provider); err != nil {
		return err
	}
	switch reasoning {
	case "low", "high", "max":
	default:
		return fmt.Errorf("zcode reasoning must be low, high, or max")
	}
	return nil
}
