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
