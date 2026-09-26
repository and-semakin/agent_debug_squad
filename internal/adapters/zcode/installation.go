package zcode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/cursor"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
	"github.com/and-semakin/agent_debug_squad/internal/procgroup"
)

// providerConfigEnv is the effective override variable for the built-in
// provider configuration; the bundle-relative default matches host.cjs.
const providerConfigEnv = "ZCODE_BUILTIN_PROVIDER_CONFIG_FILE"

// CheckInstallation independently verifies the effective Node executable,
// the readable runtime bundle, and the readable built-in provider
// configuration, then runs the embedded probe-only inspector for structural
// compatibility. It never loads/executes the runtime bundle, reads account
// or credential files, or spawns app-server. Missing Node and missing
// runtime/config are independent issues; the probe is skipped when either is
// missing, so no derivative duplicates appear.
func (a *Adapter) CheckInstallation(ctx context.Context, input domain.InstallationInput) domain.InstallationResult {
	links := []domain.InstallationLink{preflight.OfficialLink("zcode"), preflight.OfficialLink("node")}
	if runtime.GOOS == "windows" {
		return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: []domain.InstallationIssue{{
			Phase:             domain.InstallationPhaseInstallation,
			Component:         domain.ComponentPlatform,
			Code:              domain.CodeUnsupportedPlatform,
			Message:           "native Windows local backend checks are not supported by this build; use macOS, Linux or WSL",
			InstallationLinks: links,
		}}}
	}

	effective := preflight.MaterializeEnv(cursor.BuildEnv(a.spec, input.AmbientEnv), input.AmbientEnv)

	var issues []domain.InstallationIssue
	nodePath, nodeIssue := preflight.ResolveCommand(a.option("command", "node"), effective, input.WorkspaceDir)
	if nodeIssue != nil {
		issues = append(issues, *nodeIssue)
	}
	runtimePath := a.option("runtime_path", defaultRuntime)
	runtimeIssue := preflight.ReadableFile(runtimePath, domain.ComponentRuntime,
		"the ZCode desktop runtime bundle was not found at the configured or default location; install ZCode desktop or configure runtime_path explicitly",
		"the ZCode desktop runtime bundle is not a readable file; repair the installation or configure runtime_path explicitly",
		links)
	if runtimeIssue != nil {
		issues = append(issues, *runtimeIssue)
	}
	providerPath := a.providerConfigPath(effective, runtimePath, input.WorkspaceDir)
	providerIssue := preflight.ReadableFile(providerPath, domain.ComponentProviderConfig,
		"the built-in ZCode provider configuration was not found next to the runtime bundle; reinstall ZCode desktop or point ZCODE_BUILTIN_PROVIDER_CONFIG_FILE at the configuration",
		"the built-in ZCode provider configuration is not a readable file; repair the installation or point ZCODE_BUILTIN_PROVIDER_CONFIG_FILE at the configuration",
		links)
	if providerIssue != nil {
		issues = append(issues, *providerIssue)
	}
	if len(issues) > 0 {
		return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: issues}
	}
	if issue := a.runStructuralProbe(ctx, nodePath, runtimePath, providerPath, effective); issue != nil {
		return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: []domain.InstallationIssue{*issue}}
	}
	return domain.InstallationResult{Status: domain.InstallationStatusReady}
}

// providerConfigPath resolves the built-in provider configuration: a nonempty
// ZCODE_BUILTIN_PROVIDER_CONFIG_FILE override (relative overrides resolve
// against the effective workspace) or the bundle-relative default.
func (a *Adapter) providerConfigPath(effective []string, runtimePath, workspaceDir string) string {
	if override, ok := preflight.EnvLookup(effective, providerConfigEnv); ok && override != "" {
		if filepath.IsAbs(override) {
			return override
		}
		if workspaceDir != "" {
			return filepath.Join(workspaceDir, override)
		}
		return override
	}
	return filepath.Join(filepath.Dir(runtimePath), "..", "config", "provider", "zcode-builtin.json")
}

// runStructuralProbe runs the embedded host in probe-only mode with the
// resolved Node executable and a minimal inspection environment: preload
// hooks such as NODE_OPTIONS are omitted without touching the eventual
// backend child environment. Probe output is bounded and never forwarded raw.
func (a *Adapter) runStructuralProbe(ctx context.Context, nodePath, runtimePath, providerPath string, effective []string) *domain.InstallationIssue {
	links := []domain.InstallationLink{preflight.OfficialLink("zcode")}
	cmd := exec.Command(nodePath, "-e", hostSource, runtimePath, providerPath)
	// SQUAD_PROBE selects the probe-only entry in the inspector environment;
	// it is never part of the eventual backend child environment.
	cmd.Env = append(minimalInspectionEnv(effective), "SQUAD_PROBE=1")
	procgroup.Prepare(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return probeFailure(links)
	}
	// The caller's per-check deadline (five seconds) bounds the probe; the
	// process group is killed when the context ends or the call returns.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			procgroup.Kill(cmd)
		case <-done:
		}
	}()
	if err := cmd.Wait(); err != nil {
		return probeFailure(links)
	}
	var probe struct {
		OK   bool   `json:"ok"`
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &probe); err != nil || !probe.OK {
		return probeFailure(links)
	}
	return nil
}

func probeFailure(links []domain.InstallationLink) *domain.InstallationIssue {
	return &domain.InstallationIssue{
		Phase:             domain.InstallationPhaseInstallation,
		Component:         domain.ComponentRuntime,
		Code:              domain.CodeUnsupportedRuntime,
		Message:           "the installed ZCode desktop runtime bundle is missing required structures or is otherwise unsupported; update agent-debug-squad or reinstall a compatible ZCode desktop 3.x",
		InstallationLinks: links,
	}
}

// minimalInspectionEnv retains only the values the inspector needs and drops
// Node preload hooks, so environment-driven code cannot execute during
// inspection.
func minimalInspectionEnv(effective []string) []string {
	keep := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "TMP": true, "TEMP": true}
	var out []string
	for _, item := range effective {
		if key, _, ok := strings.Cut(item, "="); ok && keep[key] {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		out = []string{"PATH=" + os.Getenv("PATH")}
	}
	return out
}
