package config

import (
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestValidateZCodeAgent(t *testing.T) {
	valid := []domain.AgentSpec{
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{}},
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"provider": "account:zai-individual-coding-plan"}},
		{Name: "a", Backend: "codex"},
	}
	for _, spec := range valid {
		if err := ValidateZCodeAgent(spec); err != nil {
			t.Fatalf("rejected valid %+v: %v", spec, err)
		}
	}
	invalid := []domain.AgentSpec{
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"plan_policy": "fixed"}},
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"plan_policy": "start-first"}},
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"provider": "account:zai-start-plan"}},
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"provider": "openai"}},
	}
	for _, spec := range invalid {
		err := ValidateZCodeAgent(spec)
		if err == nil {
			t.Fatalf("accepted invalid %+v", spec)
		}
	}
}

func TestValidateZCodeAgentNamesRemovedOption(t *testing.T) {
	spec := domain.AgentSpec{Name: "a", Backend: "zcode", StringOptions: map[string]string{"plan_policy": "start-first"}}
	err := ValidateZCodeAgent(spec)
	if err == nil || !strings.Contains(err.Error(), "plan_policy") {
		t.Fatalf("rejection must name the removed option: %v", err)
	}
}

func TestNormalizeAgentSpecRejectsPlanPolicy(t *testing.T) {
	spec := domain.AgentSpec{Name: "a", Backend: "zcode", StartupPrompt: "s", Options: map[string]any{"plan_policy": "fixed"}}
	if _, err := NormalizeAgentOptions(spec); err == nil {
		t.Fatal("normalization accepted the removed plan_policy option")
	}
	if _, err := NormalizeAgentOptions(domain.AgentSpec{Name: "a", Backend: "zcode", StartupPrompt: "s", Options: map[string]any{"provider": "openai"}}); err == nil {
		t.Fatal("normalization accepted a foreign provider")
	}
	accepted, err := NormalizeAgentOptions(domain.AgentSpec{Name: "a", Backend: "zcode", StartupPrompt: "s", Options: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, set := accepted.StringOptions["plan_policy"]; set {
		t.Fatal("plan policy must not survive normalization")
	}
}
