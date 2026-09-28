package config

import (
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestValidateZCodeAgent(t *testing.T) {
	valid := []domain.AgentSpec{
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{}},
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"plan_policy": "start-first", "provider": "account:zai-start-plan"}},
		{Name: "a", Backend: "codex"},
	}
	for _, spec := range valid {
		if err := ValidateZCodeAgent(spec); err != nil {
			t.Fatalf("rejected valid %+v: %v", spec, err)
		}
	}
	invalid := []domain.AgentSpec{
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"plan_policy": "start-first", "provider": "openai"}},
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"plan_policy": "auto"}},
		{Name: "a", Backend: "zcode", StringOptions: map[string]string{"plan_policy": "fixed", "provider": "account:zai-start-plan"}},
	}
	for _, spec := range invalid {
		if err := ValidateZCodeAgent(spec); err == nil {
			t.Fatalf("accepted invalid %+v", spec)
		}
	}
}

func TestNormalizeAgentSpecRejectsForeignStartFirstProvider(t *testing.T) {
	spec := domain.AgentSpec{Name: "a", Backend: "zcode", StartupPrompt: "s", Options: map[string]any{"plan_policy": "start-first", "provider": "openai"}}
	if _, err := NormalizeAgentOptions(spec); err == nil {
		t.Fatal("normalization accepted a foreign provider under start-first")
	}
	accepted, err := NormalizeAgentOptions(domain.AgentSpec{Name: "a", Backend: "zcode", StartupPrompt: "s", Options: map[string]any{"plan_policy": "start-first"}})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.StringOptions["plan_policy"] != "start-first" {
		t.Fatal("plan policy lost")
	}
}
