package zcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// validFixture lives in host_test.go; the probe must stop after structural
// discovery, so the fixture carries a top-level throw: compilation would
// execute it and fail, while a probe-only pass never compiles.
const probeFixture = validFixture + "\nthrow Error('bundle was compiled');"

// providerConfigRoot builds the bundle-relative layout the adapter resolves
// by default: <root>/runtime/bundle.cjs next to <root>/config/provider/.
func providerConfigRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "config", "provider")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return runtimeDir, configDir
}

func TestCheckInstallationProbeStopsBeforeCompilation(t *testing.T) {
	node := requireNode(t)
	runtimeDir, configDir := providerConfigRoot(t)
	bundle := filepath.Join(runtimeDir, "bundle.cjs")
	if err := os.WriteFile(bundle, []byte(probeFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(configDir, "zcode-builtin.json")
	if err := os.WriteFile(provider, []byte(`{"zcodeBuiltinRevision":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := domain.AgentSpec{
		Name:          "Z",
		Backend:       "zcode",
		StringOptions: map[string]string{"command": node, "runtime_path": bundle},
	}
	result := New(spec).CheckInstallation(context.Background(), domain.InstallationInput{
		WorkspaceDir: t.TempDir(),
		AmbientEnv:   os.Environ(),
	})
	if result.Status != domain.InstallationStatusReady {
		t.Fatalf("probe should pass on a structurally compatible bundle without compiling it: %+v", result.Issues)
	}
}

func TestCheckInstallationRejectsUnsupportedBundle(t *testing.T) {
	node := requireNode(t)
	runtimeDir, configDir := providerConfigRoot(t)
	bundle := filepath.Join(runtimeDir, "bundle.cjs")
	if err := os.WriteFile(bundle, []byte("throw Error('unreadable');"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(configDir, "zcode-builtin.json")
	if err := os.WriteFile(provider, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := domain.AgentSpec{
		Name:          "Z",
		Backend:       "zcode",
		StringOptions: map[string]string{"command": node, "runtime_path": bundle},
	}
	result := New(spec).CheckInstallation(context.Background(), domain.InstallationInput{
		WorkspaceDir: t.TempDir(),
		AmbientEnv:   os.Environ(),
	})
	if result.Status != domain.InstallationStatusFailed || len(result.Issues) != 1 ||
		result.Issues[0].Code != domain.CodeUnsupportedRuntime || result.Issues[0].Component != domain.ComponentRuntime {
		t.Fatalf("want one unsupported_runtime issue, got %+v", result)
	}
}

func TestCheckInstallationReportsIndependentFailures(t *testing.T) {
	// Pin every input to a guaranteed-missing path so the host's ambient
	// environment cannot make the check platform- or machine-dependent.
	spec := domain.AgentSpec{
		Name:    "Z",
		Backend: "zcode",
		StringOptions: map[string]string{
			"command":      "/nonexistent/node",
			"runtime_path": "/nonexistent/bundle.cjs",
		},
		ListOptions: map[string][]string{
			"env": {`ZCODE_BUILTIN_PROVIDER_CONFIG_FILE=/nonexistent/provider.json`},
		},
	}
	result := New(spec).CheckInstallation(context.Background(), domain.InstallationInput{
		WorkspaceDir: t.TempDir(),
		AmbientEnv:   []string{"PATH=/usr/bin:/bin"},
	})
	if result.Status != domain.InstallationStatusFailed || len(result.Issues) != 3 {
		t.Fatalf("want all three independent failures, got %+v", result)
	}
	components := map[string]bool{}
	for _, issue := range result.Issues {
		components[issue.Component] = true
	}
	for _, want := range []string{domain.ComponentExecutable, domain.ComponentRuntime, domain.ComponentProviderConfig} {
		if !components[want] {
			t.Fatalf("missing component issue %s: %+v", want, result.Issues)
		}
	}
	links := 0
	for _, issue := range result.Issues {
		links += len(issue.InstallationLinks)
	}
	if links == 0 {
		t.Fatal("installation links must be present")
	}
}

func TestCheckInstallationSuppressesPreloadHooksDuringInspection(t *testing.T) {
	node := requireNode(t)
	workspace := t.TempDir()
	hook := filepath.Join(workspace, "hook.js")
	marker := filepath.Join(workspace, "marker")
	if err := os.WriteFile(hook, []byte("require('node:fs').writeFileSync("+strconvQuotes(marker)+", 'x')"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtimeDir, configDir := providerConfigRoot(t)
	bundle := filepath.Join(runtimeDir, "bundle.cjs")
	if err := os.WriteFile(bundle, []byte(probeFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(configDir, "zcode-builtin.json")
	if err := os.WriteFile(provider, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := domain.AgentSpec{
		Name:          "Z",
		Backend:       "zcode",
		StringOptions: map[string]string{"command": node, "runtime_path": bundle},
	}
	ambient := append(os.Environ(), "NODE_OPTIONS=--require "+hook)
	result := New(spec).CheckInstallation(context.Background(), domain.InstallationInput{
		WorkspaceDir: workspace,
		AmbientEnv:   ambient,
	})
	if result.Status != domain.InstallationStatusReady {
		t.Fatalf("probe should pass: %+v", result.Issues)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("preload hook executed during inspection")
	}
}

func strconvQuotes(value string) string {
	quoted := strings.ReplaceAll(value, `\`, `\\`)
	quoted = strings.ReplaceAll(quoted, `"`, `\"`)
	return `"` + quoted + `"`
}

func TestCheckInstallationProviderConfigOverride(t *testing.T) {
	node := requireNode(t)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(runtimeDir, "bundle.cjs")
	if err := os.WriteFile(bundle, []byte(probeFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	// The bundle-relative default is intentionally absent; the override wins.
	provider := filepath.Join(t.TempDir(), "custom-provider.json")
	if err := os.WriteFile(provider, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	spec := domain.AgentSpec{
		Name:    "Z",
		Backend: "zcode",
		StringOptions: map[string]string{
			"command":      node,
			"runtime_path": bundle,
		},
		ListOptions: map[string][]string{
			"env": {`ZCODE_BUILTIN_PROVIDER_CONFIG_FILE=` + provider},
		},
	}
	result := New(spec).CheckInstallation(context.Background(), domain.InstallationInput{
		WorkspaceDir: workspace,
		AmbientEnv:   os.Environ(),
	})
	if result.Status != domain.InstallationStatusReady {
		t.Fatalf("override must be honored: %+v", result.Issues)
	}
}
