package config

import (
	"errors"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedZCodeAgent(t *testing.T) {
	for _, options := range []map[string]any{nil, {"model": "GLM-5.3"}, {"plan_policy": "fixed"}, {"invalid": 42}} {
		_, err := NormalizeAgentOptions(domain.AgentSpec{Name: "legacy", Backend: "zcode", StartupPrompt: "test", Options: options})
		if !errors.Is(err, domain.ErrZCodeRemoved) {
			t.Fatalf("normalization: %v", err)
		}
	}
	path := filepath.Join(t.TempDir(), "squad.yaml")
	for _, workflow := range []string{"", defaultWorkflowYAML} {
		yaml := strings.ReplaceAll(workflowAgentsYAML, "backend: fake", "backend: zcode") + workflow
		if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); !errors.Is(err, domain.ErrZCodeRemoved) {
			t.Fatalf("load: %v", err)
		}
	}
}

func TestRemovedZCodeMachineSection(t *testing.T) {
	for _, section := range []string{"zcode:\n", "zcode: {}\n", "zcode:\n  command: node\n", "zcode:\n  proxy_url: secret-sentinel\n"} {
		home := writeMachineBackends(t, "codex: {}\n"+section)
		_, err := LoadMachineBackends(home)
		if !errors.Is(err, domain.ErrZCodeRemoved) || !strings.Contains(err.Error(), MachineBackendsPath(home)) || !strings.Contains(err.Error(), "remove the zcode section") || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatalf("machine diagnostic: %v", err)
		}
	}
}
