package opencode

import (
	"context"
	"runtime"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
)

// CheckInstallation is mode-aware. External mode has no local installation
// requirement: no CLI lookup, no spawning, no filesystem runtime checks.
// Managed mode resolves its effective serve command under the baseline
// allowlist environment the owned server would launch with, without starting
// any server here.
func (a *Adapter) CheckInstallation(ctx context.Context, input domain.InstallationInput) domain.InstallationResult {
	mode := a.settings.Mode
	if mode == "" {
		mode = "managed"
	}
	if mode == "external" {
		return domain.InstallationResult{Status: domain.InstallationStatusNotRequired}
	}
	if runtime.GOOS == "windows" {
		return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: []domain.InstallationIssue{{
			Phase:             domain.InstallationPhaseInstallation,
			Component:         domain.ComponentPlatform,
			Code:              domain.CodeUnsupportedPlatform,
			Message:           "native Windows local backend checks are not supported by this build; use macOS, Linux or WSL",
			InstallationLinks: []domain.InstallationLink{preflight.OfficialLink("opencode")},
		}}}
	}
	command := a.settings.Command
	if command == "" {
		command = "opencode"
	}
	effective := baselineLookupEnv(a.settings, input.AmbientEnv)
	resolved, issue := preflight.ResolveCommand(command, effective, input.WorkspaceDir)
	if issue != nil {
		return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: []domain.InstallationIssue{*issue}}
	}
	if issue := preflight.CheckLauncher(resolved, effective, input.WorkspaceDir); issue != nil {
		return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: []domain.InstallationIssue{*issue}}
	}
	return domain.InstallationResult{Status: domain.InstallationStatusReady}
}

// baselineLookupEnv materializes the owned server's baseline allowlist
// environment: the fixed baseline names plus machine inherit_env, copied
// from the captured ambient snapshot. Managed OpenCode keeps this separate
// contract and is never forced into the CLI builders' ambient rules.
func baselineLookupEnv(s domain.MachineBackendSettings, ambient []string) []string {
	source := map[string]string{}
	for _, item := range ambient {
		if k, v, ok := strings.Cut(item, "="); ok {
			source[k] = v
		}
	}
	allowed := map[string]bool{}
	for _, key := range baselineEnv {
		allowed[key] = true
	}
	for _, key := range s.InheritEnv {
		allowed[key] = true
	}
	var env []string
	for key := range allowed {
		if value, ok := source[key]; ok {
			env = append(env, key+"="+value)
		}
	}
	if env == nil {
		env = []string{}
	}
	return env
}

// EffectiveMode reports the resolved mode for readiness wiring.
func (a *Adapter) EffectiveMode() string {
	if a.settings.Mode == "" {
		return "managed"
	}
	return a.settings.Mode
}

// EffectiveBaseURL reports the effective endpoint for external readiness.
func (a *Adapter) EffectiveBaseURL() string {
	if url := a.settings.BaseURL; url != "" {
		return url
	}
	return defaultBaseURL
}
