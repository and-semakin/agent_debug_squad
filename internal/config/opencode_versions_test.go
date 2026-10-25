package config

import (
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"testing"
)

func TestOpenCodeVersionSections(t *testing.T) {
	cfg, err := LoadMachineBackends(writeMachineBackends(t, "opencode1:\n  command: /v1\nopencode2:\n  command: /v2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.For("opencode").Command != "/v1" || cfg.For("opencode1") != cfg.OpenCode || cfg.For("opencode2").Command != "/v2" {
		t.Fatal(cfg)
	}
	for _, body := range []string{"opencode: {}\nopencode1: {}\n", "opencode2:\n  snapshot: 'false'\n", "opencode2:\n  mode: external\n  base_url: http://localhost\n  command: ''\n"} {
		if _, err := LoadMachineBackends(writeMachineBackends(t, body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	for _, backend := range []string{"opencode", "opencode1", "opencode2"} {
		spec := domain.AgentSpec{Backend: backend, Options: map[string]any{"env": []string{"SECRET=1"}}}
		if err := ValidateOpenCodeAgent(spec); err == nil {
			t.Fatalf("process override allowed for %s", backend)
		}
	}
}
