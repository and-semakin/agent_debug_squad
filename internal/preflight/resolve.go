// Package preflight holds the shared installation-check machinery: effective
// environment materialization, executable resolution with explicit path
// semantics, shebang interpreter inspection, and the bounded aggregate pass.
// It imports domain only, so every adapter can share it without cycles.
package preflight

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// syscallAccess reports whether the calling user may execute path. syscall
// has no portable X_OK constant across all build targets, so the raw call is
// isolated here.
func syscallAccess(path string) error {
	return syscallAccessOS(path)
}

// maxInterpreterDepth bounds shebang interpreter chains; cycles fail instead.
const maxInterpreterDepth = 8

// MaterializeEnv applies the actual launch semantics to a check: the existing
// CLI environment builders return nil when no effective entry survives, which
// exec interprets as full ambient inheritance. A non-nil explicit slice stays
// as-is, even when empty.
func MaterializeEnv(explicit []string, ambient []string) []string {
	if explicit != nil {
		return explicit
	}
	out := make([]string, len(ambient))
	copy(out, ambient)
	return out
}

// EnvLookup returns the last effective value of a variable in an environment
// slice and whether any entry was present.
func EnvLookup(env []string, key string) (string, bool) {
	value := ""
	found := false
	for _, item := range env {
		if k, v, ok := strings.Cut(item, "="); ok && k == key {
			value, found = v, true
		}
	}
	return value, found
}

// ResolveCommand resolves one configured command the way the associated
// launch would: single executable token, no shell/expansion, PATH search only
// for bare names against the effective child environment, explicit paths
// relative to the workspace. It returns the resolved absolute path or a
// public issue; raw filesystem errors never reach the message.
func ResolveCommand(command string, env []string, workspaceDir string) (string, *domain.InstallationIssue) {
	links := officialLinksFor("")
	if strings.ContainsRune(command, '/') {
		path := command
		if !filepath.IsAbs(path) {
			if workspaceDir == "" {
				return "", fail(domain.ComponentExecutable, domain.CodeNotFound, links, "the command is a relative path but no workspace is configured")
			}
			path = filepath.Join(workspaceDir, path)
		}
		return checkExecutableFile(path, links)
	}
	return resolveBareName(command, env, links)
}

func resolveBareName(name string, env []string, links []domain.InstallationLink) (string, *domain.InstallationIssue) {
	raw, present := EnvLookup(env, "PATH")
	if !present || strings.TrimSpace(raw) == "" {
		return "", fail(domain.ComponentExecutable, domain.CodeNotFound, links,
			"no usable PATH in the effective child environment; inherit PATH or configure an absolute command")
	}
	entries := strings.Split(raw, string(os.PathListSeparator))
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		if !filepath.IsAbs(entry) {
			// One relative entry rejects the entire bare-name lookup, even if
			// an earlier absolute directory contains the executable; an
			// explicit ./tool command remains available.
			return "", fail(domain.ComponentExecutable, domain.CodeInvalidSearchPath, links,
				"the effective child PATH contains a relative directory, so bare command names are not searched; repair PATH or configure an absolute command")
		}
		dirs = append(dirs, entry)
	}
	if len(dirs) == 0 {
		return "", fail(domain.ComponentExecutable, domain.CodeNotFound, links,
			"the effective child PATH is empty, so bare command names cannot be found; inherit PATH or configure an absolute command")
	}
	for _, dir := range dirs {
		candidate := filepath.Join(dir, name)
		if path, issue := checkExecutableFile(candidate, links); issue == nil {
			return path, nil
		} else if issue.Code != domain.CodeNotFound {
			return "", issue
		}
	}
	return "", fail(domain.ComponentExecutable, domain.CodeNotFound, links,
		"the configured command was not found in the effective child PATH; inspect the command and PATH settings and install the missing backend")
}

// checkExecutableFile follows symlinks and accepts only a usable regular
// file with effective-user execute access.
func checkExecutableFile(path string, links []domain.InstallationLink) (string, *domain.InstallationIssue) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fail(domain.ComponentExecutable, domain.CodeNotFound, links,
			"the configured command does not exist at its effective location; inspect the command and PATH settings and install the missing backend")
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fail(domain.ComponentExecutable, domain.CodeNotFound, links,
			"the configured command does not exist at its effective location; inspect the command and PATH settings and install the missing backend")
	}
	if !info.Mode().IsRegular() {
		return "", fail(domain.ComponentExecutable, domain.CodeNotFound, links,
			"the configured command is not a regular executable file")
	}
	if err := accessExecutable(resolved, info.Mode()); err != nil {
		return "", fail(domain.ComponentExecutable, domain.CodeNotExecutable, links,
			"the configured command is not executable by the server user; add execute permission or repair the installation")
	}
	return resolved, nil
}

func accessExecutable(path string, mode os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if syscallAccess(path) == nil {
		return nil
	}
	if mode.Perm()&0111 == 0 {
		return fmt.Errorf("no execute bit")
	}
	// access(2) can reject what the effective user may run (for example as
	// root); the mode-bit check above keeps those cases usable.
	return nil
}

// CheckLauncher inspects the file at resolvedPath for a shebang and verifies
// its recognized interpreter chain under the same child environment, without
// executing any wrapper content. Recognized forms: an absolute interpreter, a
// bare interpreter resolved on PATH, `/usr/bin/env NAME`, and the simple
// `/usr/bin/env -S NAME` form. A nil result means no recognized shebang (a
// native binary) or a fully satisfied chain.
func CheckLauncher(resolvedPath string, env []string, workspaceDir string) *domain.InstallationIssue {
	seen := map[string]bool{}
	links := officialLinksFor("")
	path := resolvedPath
	for depth := 0; depth < maxInterpreterDepth; depth++ {
		if seen[path] {
			return fail(domain.ComponentInterpreter, domain.CodeUnsupportedLauncher, links,
				"the launcher interpreter chain contains a cycle")
		}
		seen[path] = true
		interpreter, arg, hasShebang, err := readShebang(path)
		if err != nil || !hasShebang {
			return nil // native binary or unreadable tail: launch errors stay launch-time
		}
		var (
			next  string
			issue *domain.InstallationIssue
		)
		switch {
		case filepath.Base(interpreter) == "env":
			rest := strings.TrimSpace(arg)
			if rest == "" || strings.ContainsAny(rest, " \t") && !strings.HasPrefix(rest, "-S ") {
				return fail(domain.ComponentInterpreter, domain.CodeUnsupportedLauncher, links,
					"the launcher uses a shebang form this checker does not cover; invoke an explicit interpreter or simplify the shebang")
			}
			if strings.HasPrefix(rest, "-S ") {
				rest = strings.TrimSpace(strings.TrimPrefix(rest, "-S "))
				if rest == "" || strings.ContainsAny(rest, " \t") {
					return fail(domain.ComponentInterpreter, domain.CodeUnsupportedLauncher, links,
						"the launcher uses a shebang form this checker does not cover; invoke an explicit interpreter or simplify the shebang")
				}
			}
			next, issue = ResolveCommand(rest, env, workspaceDir)
		case arg != "":
			return fail(domain.ComponentInterpreter, domain.CodeUnsupportedLauncher, links,
				"the launcher uses a shebang form this checker does not cover; invoke an explicit interpreter or simplify the shebang")
		default:
			next, issue = ResolveCommand(interpreter, env, workspaceDir)
		}
		if issue != nil {
			issue.Component = domain.ComponentInterpreter
			return issue
		}
		path = next
	}
	return fail(domain.ComponentInterpreter, domain.CodeUnsupportedLauncher, links,
		"the launcher interpreter chain is too deep to verify")
}

func readShebang(path string) (interpreter, arg string, hasShebang bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", false, err
	}
	defer file.Close()
	head := make([]byte, 256)
	n, err := file.Read(head)
	if err != nil && n == 0 {
		return "", "", false, err
	}
	line := head[:n]
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if !bytes.HasPrefix(line, []byte("#!")) {
		return "", "", false, nil
	}
	fields := strings.SplitN(strings.TrimSpace(string(line[2:])), " ", 2)
	interpreter = strings.TrimSpace(fields[0])
	if len(fields) == 2 {
		arg = strings.TrimSpace(fields[1])
	}
	return interpreter, arg, true, nil
}

func fail(component, code string, links []domain.InstallationLink, message string) *domain.InstallationIssue {
	return &domain.InstallationIssue{
		Phase:             domain.InstallationPhaseInstallation,
		Component:         component,
		Code:              code,
		Message:           message,
		InstallationLinks: links,
	}
}

// officialLinksFor returns the documentation links relevant to a backend; an
// empty backend yields no product link (callers attach their own).
func officialLinksFor(backend string) []domain.InstallationLink {
	if url := officialLinkURL(backend); url != "" {
		return []domain.InstallationLink{{Label: productLabel(backend), URL: url}}
	}
	return nil
}

func officialLinkURL(backend string) string {
	switch backend {
	case "codex":
		return domain.DocCodexCLI
	case "cursor":
		return domain.DocCursorCLI
	case "kimi":
		return domain.DocKimiCLI
	case "opencode":
		return domain.DocOpenCodeCLI
	case "zcode":
		return domain.DocZCodeDesktop
	case "node":
		return domain.DocNodeDownload
	default:
		return ""
	}
}

func productLabel(backend string) string {
	switch backend {
	case "codex":
		return "Codex CLI installation"
	case "cursor":
		return "Cursor CLI installation"
	case "kimi":
		return "Kimi CLI installation"
	case "opencode":
		return "OpenCode installation"
	case "zcode":
		return "ZCode installation"
	case "node":
		return "Node.js installation"
	default:
		return "installation documentation"
	}
}

// Deadline bounds one unique check.
const perCheckDeadline = 5 * time.Second

// Entry is one unique installation configuration with its affected agents.
type Entry struct {
	Backend string
	Agents  []string
	Check   func(ctx context.Context) domain.InstallationResult
}

// Pass runs bounded, concurrent installation checks: five seconds per unique
// check, at most four at a time, with the caller's context able to cut the
// pass short. Completed failures are preserved; unfinished groups are
// classified cancelled or timed_out instead of silently passing.
func Pass(ctx context.Context, budget time.Duration, entries []Entry) domain.PreflightReport {
	var report domain.PreflightReport
	if len(entries) == 0 {
		return report
	}
	phaseCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, entry := range entries {
		entry := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-phaseCtx.Done():
				mu.Lock()
				report.Issues = append(report.Issues, incompleteIssues(entry, phaseCtx.Err())...)
				mu.Unlock()
				return
			}
			checkCtx, checkCancel := context.WithTimeout(phaseCtx, perCheckDeadline)
			result := entry.Check(checkCtx)
			checkCancel()
			mu.Lock()
			defer mu.Unlock()
			switch result.Status {
			case domain.InstallationStatusFailed:
				for _, issue := range result.Issues {
					report.Issues = append(report.Issues, domain.PreflightIssue{
						Phase:             issue.Phase,
						Backend:           entry.Backend,
						Agents:            append([]string(nil), entry.Agents...),
						Component:         issue.Component,
						Code:              issue.Code,
						RestartRequired:   issue.RestartRequired,
						Message:           issue.Message,
						InstallationLinks: issue.InstallationLinks,
					})
				}
			case domain.InstallationStatusReady, domain.InstallationStatusNotRequired, "":
			default:
				report.Issues = append(report.Issues, domain.PreflightIssue{
					Phase:     domain.InstallationPhaseInstallation,
					Backend:   entry.Backend,
					Agents:    append([]string(nil), entry.Agents...),
					Component: domain.ComponentExecutable,
					Code:      domain.CodeCheckFailed,
					Message:   "the installation check failed unexpectedly",
				})
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-phaseCtx.Done():
		// Cancellation stops new work; probes still running observe their own
		// check contexts. Wait briefly for completion so in-flight results
		// are preserved, then classify whatever remains unfinished.
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	report.Sorted()
	return report
}

// incompleteIssues classifies groups that never produced a verdict.
func incompleteIssues(entry Entry, cause error) []domain.PreflightIssue {
	code := domain.CodeCancelled
	if cause != nil && cause.Error() == context.DeadlineExceeded.Error() {
		code = domain.CodeTimedOut
	}
	return []domain.PreflightIssue{{
		Phase:     domain.InstallationPhaseInstallation,
		Backend:   entry.Backend,
		Agents:    append([]string(nil), entry.Agents...),
		Component: domain.ComponentExecutable,
		Code:      code,
		Message:   "the installation check did not finish within its budget",
	}}
}
