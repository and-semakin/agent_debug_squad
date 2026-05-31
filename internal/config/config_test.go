package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigAppliesDefaultsAndParsesOptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
session_name: debug-quorum
workspace_dir: ` + dir + `
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review carefully.
    options:
      command: codex
      model: openai/gpt-5.5
      inherit_env:
        - OPENAI_API_KEY
        - CODEX_HOME
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionName != "debug-quorum" {
		t.Fatalf("SessionName = %q", cfg.SessionName)
	}
	if cfg.StateDirName != ".agent-debug-squad" {
		t.Fatalf("StateDirName = %q", cfg.StateDirName)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("Host = %q", cfg.Host)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d", cfg.Port)
	}
	if cfg.SessionID == "" {
		t.Fatal("SessionID is empty")
	}
	got := cfg.Agents[0].StringOptions["command"]
	if got != "codex" {
		t.Fatalf("command option = %q", got)
	}
	env := cfg.Agents[0].ListOptions["inherit_env"]
	if len(env) != 2 || env[0] != "OPENAI_API_KEY" || env[1] != "CODEX_HOME" {
		t.Fatalf("inherit_env = %#v", env)
	}
}

func TestLoadConfigRejectsDuplicateAgentNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
workspace_dir: ` + dir + `
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: One.
  - name: Reviewer
    backend: kimi
    startup_prompt: Two.
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected duplicate agent error")
	}
}

func TestLoadConfigRejectsUnsupportedOptionValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
workspace_dir: ` + dir + `
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review carefully.
    options:
      model: 123
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected unsupported option value error")
	}
	if !strings.Contains(err.Error(), `agent "Reviewer" option "model"`) {
		t.Fatalf("error = %q", err)
	}
}

func TestLoadConfigRejectsNonLoopbackHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
workspace_dir: ` + dir + `
host: 0.0.0.0
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review carefully.
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want non-loopback host error")
	}
	if !strings.Contains(err.Error(), "non-loopback host") {
		t.Fatalf("error = %q, want non-loopback host", err.Error())
	}
}

func TestLoadConfigAcceptsLocalhostHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
workspace_dir: ` + dir + `
host: localhost
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review carefully.
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "localhost" {
		t.Fatalf("Host = %q, want localhost", cfg.Host)
	}
}

func TestLoadProjectCodeReviewSquadConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "configs", "code-review-squad.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionName != "code-review-squad" {
		t.Fatalf("SessionName = %q, want code-review-squad", cfg.SessionName)
	}
	if cfg.StateDirName != ".agent-review-artifacts" {
		t.Fatalf("StateDirName = %q, want .agent-review-artifacts", cfg.StateDirName)
	}
	if cfg.Port != 8090 {
		t.Fatalf("Port = %d, want 8090", cfg.Port)
	}
	if len(cfg.Agents) != 4 {
		t.Fatalf("len(Agents) = %d, want 4", len(cfg.Agents))
	}

	agents := map[string]bool{}
	for _, agent := range cfg.Agents {
		agents[agent.Name] = true
	}
	for _, name := range []string{"Facilitator", "Implementer", "Critic", "Advocat"} {
		if !agents[name] {
			t.Fatalf("missing agent %q in %#v", name, agents)
		}
	}

	facilitator := cfg.Agents[0]
	if facilitator.Name != "Facilitator" {
		t.Fatalf("first agent = %q, want Facilitator", facilitator.Name)
	}
	if !containsString(facilitator.ListOptions["inherit_env"], "HTTP_PROXY") {
		t.Fatalf("Facilitator inherit_env = %#v, want HTTP_PROXY", facilitator.ListOptions["inherit_env"])
	}

	critic := cfg.Agents[2]
	if critic.Name != "Critic" {
		t.Fatalf("third agent = %q, want Critic", critic.Name)
	}
	if critic.StringOptions["model"] != "zai-coding-plan/glm-5.1" {
		t.Fatalf("Critic model = %q", critic.StringOptions["model"])
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
