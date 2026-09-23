package orchestrator

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

func TestNewAppliesMachineBackendDefaults(t *testing.T) {
	cfg := testConfig(t, "Coder")
	cfg.Agents[0].Backend = "codex"
	cfg.MachineBackends = domain.MachineBackends{
		Codex: &domain.MachineBackendSettings{
			Command:    "/opt/codex",
			ProxyURL:   "http://proxy.example:8080",
			NoProxy:    "localhost",
			CACertFile: "/etc/ca.pem",
		},
	}

	o, err := New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rt := o.runtimes["Coder"]
	if rt == nil {
		t.Fatal("runtime for Coder not found")
	}
	if got := rt.spec.StringOptions["command"]; got != "/opt/codex" {
		t.Fatalf("command = %q, want machine default", got)
	}
	// ca_cert_file has no codex translation and must not leak into options.
	if _, ok := rt.spec.StringOptions["ca_cert_file"]; ok {
		t.Fatal("codex must not receive ca_cert_file as an option")
	}
	wantEnv := []string{
		"HTTP_PROXY=http://proxy.example:8080",
		"HTTPS_PROXY=http://proxy.example:8080",
		"NO_PROXY=localhost",
	}
	if !reflect.DeepEqual(rt.spec.ListOptions["env"], wantEnv) {
		t.Fatalf("env = %v, want %v", rt.spec.ListOptions["env"], wantEnv)
	}
	// The pre-existing yolo default must keep applying alongside machine
	// defaults.
	if rt.spec.Yolo == nil || !*rt.spec.Yolo {
		t.Fatalf("runtime spec Yolo = %v, want true from defaults", rt.spec.Yolo)
	}
}

func TestNewKeepsAgentCommandOverMachineDefault(t *testing.T) {
	cfg := testConfig(t, "Coder")
	cfg.Agents[0].Backend = "codex"
	cfg.Agents[0].Options = map[string]any{"command": "/custom/codex"}
	normalized, err := config.NormalizeAgentOptions(cfg.Agents[0])
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	cfg.Agents[0] = normalized
	cfg.MachineBackends = domain.MachineBackends{
		Codex: &domain.MachineBackendSettings{Command: "/opt/codex"},
	}

	o, err := New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rt := o.runtimes["Coder"]
	if got := rt.spec.StringOptions["command"]; got != "/custom/codex" {
		t.Fatalf("command = %q, want agent value", got)
	}
}

func TestSubmitOwnedRunAppliesMachineDefaultsToSnapshotSpec(t *testing.T) {
	cfg := testConfig(t, "Worker")
	cfg.MachineBackends = domain.MachineBackends{
		Codex: &domain.MachineBackendSettings{
			Command:  "/bin/sleep",
			ProxyURL: "http://proxy.example:8080",
		},
	}
	o, err := New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// A workflow snapshot spec: raw options only, no resolved option maps
	// and no machine values.
	snapshot, err := config.NormalizeAgentOptions(domain.AgentSpec{
		Name:          "Worker",
		Backend:       "codex",
		StartupPrompt: "You are Worker",
		Options:       map[string]any{"model": "gpt-test"},
	})
	if err != nil {
		t.Fatalf("normalize snapshot: %v", err)
	}

	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000099_000001",
		Agent:     "Worker",
		Message:   "work",
		Spec:      &snapshot,
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}

	waitUntilOwnedActive(t, o, "wrun_000099_000001")
	rt := o.runtimes[ownedRuntimeKey("wrun_000099_000001")]
	if rt == nil {
		t.Fatal("owned runtime not found")
	}
	if got := rt.spec.StringOptions["command"]; got != "/bin/sleep" {
		t.Fatalf("command = %q, want machine default applied to snapshot spec", got)
	}
	wantEnv := []string{
		"HTTP_PROXY=http://proxy.example:8080",
		"HTTPS_PROXY=http://proxy.example:8080",
	}
	if !reflect.DeepEqual(rt.spec.ListOptions["env"], wantEnv) {
		t.Fatalf("env = %v, want %v", rt.spec.ListOptions["env"], wantEnv)
	}

	// The caller's snapshot spec must stay free of machine values: persisted
	// workflow snapshots keep storing only the workflow's own options.
	if got := snapshot.StringOptions["command"]; got != "" {
		t.Fatalf("snapshot spec command mutated to %q", got)
	}
	if len(snapshot.ListOptions["env"]) != 0 {
		t.Fatalf("snapshot spec env mutated: %v", snapshot.ListOptions["env"])
	}

	if !o.CancelOwnedRun("wrun_000099_000001") {
		t.Fatal("cancel must find the active owned run")
	}
	<-outcomes
}
