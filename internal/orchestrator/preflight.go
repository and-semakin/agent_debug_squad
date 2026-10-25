package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/adapters"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
)

// Phase budgets for the two-stage preflight pass. Stage one collects every
// local installation result; stage two runs only after it passes and owns the
// managed startup plus readiness budget, so a cold managed start is never
// truncated by the installation budget.
const (
	installationBudget = 30 * time.Second
	readinessBudget    = 60 * time.Second
)

// PreflightAgents verifies local installation for every named agent's
// effective backend, deduplicated by canonical installation configuration
// with all affected agent names attached. Only when every local check passes
// does it check OpenCode service readiness. It returns a *domain.PreflightError
// on any failure; nil means admission may proceed (disappearance before the
// actual spawn remains a launch-time failure).
func (o *Orchestrator) PreflightAgents(ctx context.Context, names []string) error {
	ambient := os.Environ()
	installations := map[string]*preflight.Entry{}
	services := map[string]*preflight.Entry{}
	agents := append([]string(nil), names...)
	sort.Strings(agents)
	for _, name := range agents {
		spec, ok := o.effectiveSpec(name)
		if !ok {
			continue
		}
		adapter, err := o.newAdapter(spec)
		if err != nil {
			return fmt.Errorf("prepare %s check: %w", spec.Backend, err)
		}
		key := installationKey(spec, o.cfg.WorkspaceDir)
		if installations[key] == nil {
			checked := adapter
			installations[key] = &preflight.Entry{Backend: spec.Backend, Check: func(ctx context.Context) domain.InstallationResult {
				return checked.CheckInstallation(ctx, domain.InstallationInput{WorkspaceDir: o.cfg.WorkspaceDir, AmbientEnv: ambient})
			}}
		}
		installations[key].Agents = append(installations[key].Agents, name)
		if !domain.IsOpenCode(spec.Backend) {
			continue
		}
		mode, baseURL := opencodeServiceInfo(adapter)
		generation := spec.Backend
		if generation == "opencode" {
			generation = "opencode1"
		}
		parts := []string{generation, mode}
		if mode == "external" {
			parts = append(parts, baseURL, spec.StringOptions["username"], spec.StringOptions["password"])
		}
		raw, _ := json.Marshal(parts)
		key = string(raw) // private, never logged
		if services[key] == nil {
			checked := adapter
			services[key] = &preflight.Entry{Backend: spec.Backend, Timeout: readinessBudget, Check: func(ctx context.Context) domain.InstallationResult {
				issue := checked.(interface {
					ReadinessCheck(context.Context) *domain.InstallationIssue
				}).ReadinessCheck(ctx)
				if issue != nil {
					return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: []domain.InstallationIssue{*issue}}
				}
				return domain.InstallationResult{Status: domain.InstallationStatusReady}
			}}
		}
		services[key].Agents = append(services[key].Agents, name)
	}
	entries := func(groups map[string]*preflight.Entry) []preflight.Entry {
		out := make([]preflight.Entry, 0, len(groups))
		for _, g := range groups {
			out = append(out, *g)
		}
		return out
	}
	report := preflight.Pass(ctx, installationBudget, entries(installations))
	if len(report.Issues) > 0 {
		return &domain.PreflightError{Report: &report}
	}
	report = preflight.Pass(ctx, readinessBudget, entries(services))
	for i := range report.Issues {
		report.Issues[i].Phase = domain.InstallationPhaseReadiness
	}
	if len(report.Issues) > 0 {
		return &domain.PreflightError{Report: &report}
	}
	return nil
}

// effectiveSpec resolves the normalized, machine-merged agent spec a check
// and its associated launch would both use.
func (o *Orchestrator) effectiveSpec(name string) (domain.AgentSpec, bool) {
	for _, candidate := range o.cfg.Agents {
		if candidate.Name == name {
			return agentSpecWithDefaults(o.cfg, candidate), true
		}
	}
	return domain.AgentSpec{}, false
}

// installationKey is the memory-only canonical key for deduplication: agent
// names, prompts and model choices are excluded because they are not
// installation requirements; different explicit paths, modes, workspaces or
// effective environments stay distinct.
func installationKey(spec domain.AgentSpec, workspace string) string {
	var builder strings.Builder
	backend := spec.Backend
	if backend == "opencode1" {
		backend = "opencode"
	}
	builder.WriteString(backend)
	builder.WriteString("\x00")
	for _, key := range []string{"mode", "command", "runtime_path", "base_url"} {
		builder.WriteString(spec.StringOptions[key])
		builder.WriteString("\x00")
	}
	builder.WriteString(workspace)
	builder.WriteString("\x00")
	if spec.ListOptions != nil {
		env := append(append([]string{}, spec.ListOptions["env"]...), spec.ListOptions["inherit_env"]...)
		sort.Strings(env)
		builder.WriteString(strings.Join(env, "\x01"))
	}
	return builder.String()
}

func opencodeServiceInfo(adapter adapters.AgentAdapter) (string, string) {
	type serviceInfo interface {
		EffectiveMode() string
		EffectiveBaseURL() string
	}
	if info, ok := adapter.(serviceInfo); ok {
		return info.EffectiveMode(), info.EffectiveBaseURL()
	}
	return "managed", ""
}
