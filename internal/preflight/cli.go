package preflight

import (
	"os"
	"runtime"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// OfficialLink returns the single product documentation link for a backend.
func OfficialLink(backend string) domain.InstallationLink {
	return domain.InstallationLink{Label: productLabel(backend), URL: officialLinkURL(backend)}
}

// CheckCLI is the shared executable-and-launcher check for Codex, Cursor and
// Kimi. The adapter supplies its own environment builder so the constrained
// environment rules stay in the adapter that owns them; nil builder output
// means the captured ambient environment, exactly as exec treats it at
// launch. A self-contained CLI never gains a Node/Python requirement here.
func CheckCLI(spec domain.AgentSpec, input domain.InstallationInput, backend, defaultCommand string, buildEnv func(spec domain.AgentSpec, ambient []string) []string) domain.InstallationResult {
	if runtime.GOOS == "windows" {
		return failedResult(&domain.InstallationIssue{
			Phase:             domain.InstallationPhaseInstallation,
			Component:         domain.ComponentPlatform,
			Code:              domain.CodeUnsupportedPlatform,
			Message:           "native Windows local backend checks are not supported by this build; use macOS, Linux or WSL",
			InstallationLinks: []domain.InstallationLink{OfficialLink(backend)},
		})
	}
	command := spec.StringOptions["command"]
	if command == "" {
		command = defaultCommand
	}
	effective := MaterializeEnv(buildEnv(spec, input.AmbientEnv), input.AmbientEnv)
	resolved, issue := ResolveCommand(command, effective, input.WorkspaceDir)
	if issue != nil {
		if len(issue.InstallationLinks) == 0 {
			issue.InstallationLinks = []domain.InstallationLink{OfficialLink(backend)}
		}
		return failedResult(issue)
	}
	if issue := CheckLauncher(resolved, effective, input.WorkspaceDir); issue != nil {
		if len(issue.InstallationLinks) == 0 {
			issue.InstallationLinks = []domain.InstallationLink{OfficialLink(backend)}
		}
		return failedResult(issue)
	}
	return domain.InstallationResult{Status: domain.InstallationStatusReady}
}

func failedResult(issue *domain.InstallationIssue) domain.InstallationResult {
	if issue == nil {
		return domain.InstallationResult{Status: domain.InstallationStatusFailed}
	}
	return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: []domain.InstallationIssue{*issue}}
}

// ReadableFile classifies a required file: nil when it exists as a readable
// regular file, otherwise an issue whose message never includes the path.
func ReadableFile(path, component, missingMessage, unreadableMessage string, links []domain.InstallationLink) *domain.InstallationIssue {
	info, err := os.Stat(path)
	if err != nil {
		return &domain.InstallationIssue{Phase: domain.InstallationPhaseInstallation, Component: component, Code: domain.CodeNotFound, Message: missingMessage, InstallationLinks: links}
	}
	if !info.Mode().IsRegular() {
		return &domain.InstallationIssue{Phase: domain.InstallationPhaseInstallation, Component: component, Code: domain.CodeNotReadable, Message: unreadableMessage, InstallationLinks: links}
	}
	file, err := os.Open(path)
	if err != nil {
		return &domain.InstallationIssue{Phase: domain.InstallationPhaseInstallation, Component: component, Code: domain.CodeNotReadable, Message: unreadableMessage, InstallationLinks: links}
	}
	_ = file.Close()
	return nil
}
