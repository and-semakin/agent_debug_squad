package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
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
	groups := map[string]*preflight.Entry{}
	agents := append([]string(nil), names...)
	sort.Strings(agents)
	// Service readiness entries: managed OpenCode shares one owned server per
	// Squad, so managed groups collapse onto one readiness check regardless
	// of per-agent command differences; external groups key on the endpoint.
	managed := false
	var managedAgents []string
	external := map[string][]string{}

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
		entry, ok := groups[key]
		if !ok {
			checked := adapter
			entry = &preflight.Entry{
				Backend: spec.Backend,
				Check: func(ctx context.Context) domain.InstallationResult {
					return checked.CheckInstallation(ctx, domain.InstallationInput{
						WorkspaceDir: o.cfg.WorkspaceDir,
						AmbientEnv:   ambient,
					})
				},
			}
			groups[key] = entry
		}
		entry.Agents = append(entry.Agents, name)
		if spec.Backend == "opencode" {
			mode, baseURL := opencodeServiceInfo(adapter)
			switch mode {
			case "external":
				external[baseURL] = append(external[baseURL], name)
			default:
				managed = true
				managedAgents = append(managedAgents, name)
			}
		}
	}

	entries := make([]preflight.Entry, 0, len(groups))
	for _, entry := range groups {
		entries = append(entries, *entry)
	}
	report := preflight.Pass(ctx, installationBudget, entries)
	if len(report.Issues) > 0 {
		return &domain.PreflightError{Report: &report}
	}

	serviceIssues := o.readinessPass(ctx, ambient, managed, managedAgents, external)
	if len(serviceIssues) > 0 {
		return &domain.PreflightError{Report: &domain.PreflightReport{Issues: serviceIssues}}
	}
	return nil
}

// readinessPass asks the OpenCode lifecycle integration to ensure managed
// services and probes external health. It never starts a server when stage
// one failed (the caller guarantees that ordering) and never terminates
// unrelated or external servers.
func (o *Orchestrator) readinessPass(ctx context.Context, ambient []string, managed bool, managedAgents []string, external map[string][]string) []domain.PreflightIssue {
	phaseCtx, cancel := context.WithTimeout(ctx, readinessBudget)
	defer cancel()

	type serviceCheck struct {
		agents []string
		run    func(ctx context.Context) *domain.InstallationIssue
	}
	var checks []serviceCheck
	if managed {
		agents := append([]string(nil), managedAgents...)
		sort.Strings(agents)
		checks = append(checks, serviceCheck{
			agents: agents,
			run: func(ctx context.Context) *domain.InstallationIssue {
				return o.openCode.EnsureReadyIssue(ctx)
			},
		})
	}
	baseURLs := make([]string, 0, len(external))
	for url := range external {
		baseURLs = append(baseURLs, url)
	}
	sort.Strings(baseURLs)
	for _, url := range baseURLs {
		url := url
		agents := append([]string(nil), external[url]...)
		sort.Strings(agents)
		checks = append(checks, serviceCheck{
			agents: agents,
			run: func(ctx context.Context) *domain.InstallationIssue {
				return o.externalHealthIssue(ctx, url, ambient)
			},
		})
	}
	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var issues []domain.PreflightIssue
	for _, check := range checks {
		check := check
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-phaseCtx.Done():
				mu.Lock()
				issues = append(issues, domain.PreflightIssue{
					Phase:     domain.InstallationPhaseReadiness,
					Backend:   "opencode",
					Agents:    check.agents,
					Component: domain.ComponentService,
					Code:      phaseCode(phaseCtx.Err()),
					Message:   "the service readiness check did not finish within its budget",
				})
				mu.Unlock()
				return
			}
			issue := check.run(phaseCtx)
			mu.Lock()
			defer mu.Unlock()
			if issue != nil {
				issues = append(issues, domain.PreflightIssue{
					Phase:             issue.Phase,
					Backend:           "opencode",
					Agents:            check.agents,
					Component:         issue.Component,
					Code:              issue.Code,
					RestartRequired:   issue.RestartRequired,
					Message:           issue.Message,
					InstallationLinks: issue.InstallationLinks,
				})
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-phaseCtx.Done():
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		return strings.Join(issues[i].Agents, ",") < strings.Join(issues[j].Agents, ",")
	})
	return issues
}

func phaseCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.CodeTimedOut
	}
	return domain.CodeCancelled
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
	builder.WriteString(spec.Backend)
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

// externalHealthIssue probes one external OpenCode endpoint's health through
// the adapter's transport, so redirects, authentication and redaction follow
// the same contract as ordinary requests.
func (o *Orchestrator) externalHealthIssue(ctx context.Context, baseURL string, ambient []string) *domain.InstallationIssue {
	spec := domain.AgentSpec{
		Backend:       "opencode",
		StringOptions: map[string]string{"mode": "external", "base_url": baseURL},
	}
	adapter, err := o.openCode.Adapter(spec)
	if err != nil {
		return &domain.InstallationIssue{
			Phase:     domain.InstallationPhaseReadiness,
			Component: domain.ComponentService,
			Code:      domain.CodeServiceUnavailable,
			Message:   "the OpenCode service endpoint is not usable; verify the external server configuration",
			InstallationLinks: []domain.InstallationLink{
				{Label: "OpenCode server documentation", URL: domain.DocOpenCodeServer},
			},
		}
	}
	_ = ambient
	return adapter.ReadinessCheck(ctx)
}
