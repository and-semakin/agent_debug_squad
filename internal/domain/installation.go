package domain

import (
	"fmt"
	"sort"
	"strings"
)

// Installation check vocabulary shared by every adapter and the preflight
// coordinator. Expected failures are data, not errors, so one pass collects
// every independent component failure.
const (
	InstallationStatusReady       = "ready"
	InstallationStatusNotRequired = "not_required"
	InstallationStatusFailed      = "failed"

	InstallationPhaseInstallation = "installation"
	InstallationPhaseReadiness    = "readiness"

	ComponentExecutable     = "executable"
	ComponentInterpreter    = "interpreter"
	ComponentRuntime        = "runtime"
	ComponentProviderConfig = "provider_config"
	ComponentPlatform       = "platform"
	ComponentService        = "service"

	CodeNotFound            = "not_found"
	CodeNotExecutable       = "not_executable"
	CodeNotReadable         = "not_readable"
	CodeInvalidSearchPath   = "invalid_search_path"
	CodeUnsupportedLauncher = "unsupported_launcher"
	CodeUnsupportedRuntime  = "unsupported_runtime"
	CodeUnsupportedPlatform = "unsupported_platform"
	CodeTimedOut            = "timed_out"
	CodeCancelled           = "cancelled"
	CodeCheckFailed         = "check_failed"
	CodeServiceUnavailable  = "service_unavailable"
	CodeServiceIncompatible = "service_incompatible"
	CodeStartFailed         = "start_failed"
	CodeRestartRequired     = "restart_required"

	PreflightErrorCode = "backend_preflight_failed"
)

// Official documentation links reported with installation and service
// failures. OS-specific choices are delegated to these pages.
const (
	DocCodexCLI       = "https://learn.chatgpt.com/docs/codex/cli"
	DocCursorCLI      = "https://cursor.com/docs/cli/installation"
	DocKimiCLI        = "https://www.kimi.com/code/docs/en/"
	DocOpenCodeCLI    = "https://opencode.ai/docs/"
	DocOpenCodeServer = "https://opencode.ai/docs/server/"
	DocZCodeDesktop   = "https://zcode.z.ai/en/docs/install"
	DocNodeDownload   = "https://nodejs.org/en/download"
)

// InstallationLink is one labeled documentation entry in a report.
type InstallationLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// InstallationInput carries what an adapter cannot know by itself: the
// absolute working directory an associated launch would use and one captured
// ambient environment snapshot. The adapter already holds the normalized,
// machine-merged agent spec.
type InstallationInput struct {
	WorkspaceDir string
	AmbientEnv   []string
}

// InstallationIssue is one failure of one component of one backend check.
// Backend and agent attribution is attached by the preflight coordinator.
type InstallationIssue struct {
	Phase             string             `json:"phase"`
	Component         string             `json:"component"`
	Code              string             `json:"code"`
	RestartRequired   bool               `json:"restart_required"`
	Message           string             `json:"message"`
	InstallationLinks []InstallationLink `json:"installation_links,omitempty"`
}

// InstallationResult is one adapter's installation check outcome.
type InstallationResult struct {
	Status string              `json:"status"`
	Issues []InstallationIssue `json:"issues,omitempty"`
}

// PreflightIssue is an aggregated issue with its affected backend and the
// sorted agent names that share the failing configuration.
type PreflightIssue struct {
	Phase             string             `json:"phase"`
	Backend           string             `json:"backend"`
	Agents            []string           `json:"agents"`
	Component         string             `json:"component"`
	Code              string             `json:"code"`
	RestartRequired   bool               `json:"restart_required"`
	Message           string             `json:"message"`
	InstallationLinks []InstallationLink `json:"installation_links,omitempty"`
}

// PreflightReport is the sanitized, persistable aggregate. It is diagnostics,
// never admission authority: absence means unchecked and success is never
// stored as reusable evidence.
type PreflightReport struct {
	Issues []PreflightIssue `json:"issues,omitempty"`
}

// Sorted orders issues deterministically by phase, backend, agent list,
// component, and code.
func (r *PreflightReport) Sorted() {
	sort.SliceStable(r.Issues, func(i, j int) bool {
		a, b := r.Issues[i], r.Issues[j]
		if a.Phase != b.Phase {
			return a.Phase < b.Phase
		}
		if a.Backend != b.Backend {
			return a.Backend < b.Backend
		}
		if ka, kb := strings.Join(a.Agents, ","), strings.Join(b.Agents, ","); ka != kb {
			return ka < kb
		}
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		return a.Code < b.Code
	})
	for _, issue := range r.Issues {
		sort.Strings(issue.Agents)
	}
}

// AggregateText renders the concise public message for HTTP and CLI
// reporting.
func (r *PreflightReport) AggregateText() string {
	parts := make([]string, 0, len(r.Issues))
	for _, issue := range r.Issues {
		parts = append(parts, fmt.Sprintf("%s [%s] (%s): %s", issue.Backend, issue.Code, issue.Component, issue.Message))
	}
	if len(parts) == 0 {
		return "backend preflight failed"
	}
	return strings.Join(parts, "; ")
}

// RestartRequired reports whether any issue requires an explicit Squad
// restart before the runtime becomes usable again.
func (r *PreflightReport) RestartRequired() bool {
	for _, issue := range r.Issues {
		if issue.RestartRequired {
			return true
		}
	}
	return false
}

// PreflightError carries a failed admission report through errors.As so the
// HTTP layer can render structured 503 bodies without touching internals.
type PreflightError struct {
	Report *PreflightReport
}

func (e *PreflightError) Error() string {
	if e.Report == nil {
		return "backend preflight failed"
	}
	return e.Report.AggregateText()
}
