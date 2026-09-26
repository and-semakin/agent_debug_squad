package workflow

import (
	"context"
	"errors"
	"sort"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// admission tracks one in-flight workflow admission so identical concurrent
// requests wait for the owner's result (success or failed report) instead of
// racing the preflight window. The reservation is memory-only: a rejected
// admission consumes no request ID.
type admission struct {
	done    chan struct{}
	view    domain.WorkflowExecutionView
	created bool
	err     error
}

// admissionAgents returns every agent referenced anywhere in the definition,
// including downstream, loop and allowed-to-fail tasks. Graph position is not
// a reason to defer a prerequisite; unreferenced definitions never block.
func admissionAgents(def domain.WorkflowDefinition) []string {
	seen := map[string]bool{}
	var agents []string
	for _, task := range def.Tasks {
		if task.Agent != "" && !seen[task.Agent] {
			seen[task.Agent] = true
			agents = append(agents, task.Agent)
		}
	}
	sort.Strings(agents)
	return agents
}

// runnableAgents conservatively selects agents that can still execute in a
// recovered execution: tasks that are not permanently settled, plus tasks in
// any unfinished enclosing loop that may re-arm. Permanently completed
// workflow-scope tasks and done root subtrees are excluded; conservative
// inclusion inside unfinished loops is intentional.
func runnableAgents(snapshot *domain.WorkflowSnapshot) []string {
	def := snapshot.Definition
	seen := map[string]bool{}
	var agents []string
	for taskID := range snapshot.Tasks {
		task := snapshot.Tasks[taskID]
		taskDef, ok := def.Tasks[taskID]
		if !ok || task == nil || taskDef.Agent == "" || seen[taskDef.Agent] {
			continue
		}
		if task.State == domain.WorkflowTaskCancelled {
			continue
		}
		unfinished := false
		for _, loopName := range def.LoopAncestry(taskDef.Loop) {
			loop := snapshot.Loops[loopName]
			if loop == nil || loop.State != domain.WorkflowLoopDone {
				unfinished = true
				break
			}
		}
		if !unfinished && task.State.Settled() {
			// A settled task outside any unfinished loop will never run again.
			continue
		}
		seen[taskDef.Agent] = true
		agents = append(agents, taskDef.Agent)
	}
	sort.Strings(agents)
	return agents
}

// preflightFailureLocked records a failed pass as sanitized diagnostics plus
// the backend_preflight_failed attention reason, holding the execution in
// needs_attention. Persistence is the caller's responsibility so the reason
// can ride the same save as surrounding state changes.
func (m *Manager) preflightFailureLocked(snapshot *domain.WorkflowSnapshot, err error) {
	var report *domain.PreflightError
	if errors.As(err, &report) {
		snapshot.BackendPreflight = report.Report
	} else {
		snapshot.BackendPreflight = &domain.PreflightReport{Issues: []domain.PreflightIssue{{
			Phase:     domain.InstallationPhaseInstallation,
			Component: domain.ComponentExecutable,
			Code:      domain.CodeCheckFailed,
			Message:   "the installation check failed unexpectedly",
		}}}
	}
	snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, preflightAttentionReason)
}

// preflightSuccessLocked clears only backend preflight diagnostics and
// causes, reporting whether anything changed. A full pass clears the
// aggregate; a target-only pass clears at most issues attributable solely to
// that target and keeps the aggregate cause while any unrelated issue
// remains.
func (m *Manager) preflightSuccessLocked(snapshot *domain.WorkflowSnapshot, target string) bool {
	if snapshot.BackendPreflight == nil {
		return false
	}
	if target == "" {
		snapshot.BackendPreflight = nil
		snapshot.AttentionReasons = removeReason(snapshot.AttentionReasons, preflightAttentionReason)
		return true
	}
	remaining := make([]domain.PreflightIssue, 0, len(snapshot.BackendPreflight.Issues))
	for _, issue := range snapshot.BackendPreflight.Issues {
		for _, agent := range issue.Agents {
			if agent != target {
				remaining = append(remaining, issue)
				break
			}
		}
	}
	if len(remaining) == 0 {
		snapshot.BackendPreflight = nil
		snapshot.AttentionReasons = removeReason(snapshot.AttentionReasons, preflightAttentionReason)
		return true
	}
	snapshot.BackendPreflight.Issues = remaining
	return true
}

const preflightAttentionReason = "backend_preflight_failed"

// preflightTargetFailureLocked records a failed target-only retry check,
// merging its issues with any persisted aggregate so unrelated diagnostics
// survive. The result stays sanitized and memory/persistence is the caller's.
func (m *Manager) preflightTargetFailureLocked(snapshot *domain.WorkflowSnapshot, target string, err error) {
	var report *domain.PreflightError
	if !errors.As(err, &report) || report.Report == nil {
		return
	}
	issues := make([]domain.PreflightIssue, 0, len(report.Report.Issues)+len(snapshot.BackendPreflight.Issues))
	for _, issue := range snapshot.BackendPreflight.Issues {
		for _, agent := range issue.Agents {
			if agent != target {
				issues = append(issues, issue)
				break
			}
		}
	}
	issues = append(issues, report.Report.Issues...)
	snapshot.BackendPreflight = &domain.PreflightReport{Issues: issues}
	snapshot.BackendPreflight.Sorted()
	snapshot.AttentionReasons = appendUniqueReason(snapshot.AttentionReasons, preflightAttentionReason)
}

func removeReason(reasons []string, target string) []string {
	out := reasons[:0:0]
	for _, reason := range reasons {
		if reason != target {
			out = append(out, reason)
		}
	}
	return out
}

// runPreflight runs one admission preflight; caller cancellation surfaces as
// an error so the HTTP layer can drop the response without a fabricated body.
func (m *Manager) runPreflight(ctx context.Context, agents []string) error {
	if len(agents) == 0 {
		return nil
	}
	return m.exec.PreflightAgents(ctx, agents)
}

// Preflighter is the installation/readiness gate the executor provides.
// *orchestrator.Orchestrator implements it; workflow admission, recovery,
// resume, retry acceptance and every scheduling batch route through it.
type Preflighter interface {
	PreflightAgents(ctx context.Context, names []string) error
}
