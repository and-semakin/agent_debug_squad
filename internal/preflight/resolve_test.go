package preflight

import (
	"os"
	"path/filepath"
	"testing"
)

func tempExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func envWith(entries ...string) []string {
	return append(os.Environ(), entries...)
}

func TestResolveBareNameSearchesEffectivePATHInOrder(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	tempExecutable(t, dirB, "tool", "#!/bin/sh\n")
	env := envWith("PATH=" + dirB + string(os.PathListSeparator) + dirA)
	path, issue := ResolveCommand("tool", env, t.TempDir())
	if issue != nil {
		t.Fatalf("unexpected issue: %+v", issue)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(dirB, "tool"))
	if path != want {
		t.Fatalf("path = %q, want first PATH match %q", path, want)
	}
}

func TestResolveMissingPATHReportsNotFound(t *testing.T) {
	_, issue := ResolveCommand("tool", []string{"HOME=/x"}, t.TempDir())
	if issue == nil || issue.Code != "not_found" {
		t.Fatalf("want not_found, got %+v", issue)
	}
	_, issue = ResolveCommand("tool", []string{"PATH=:"}, t.TempDir())
	if issue == nil || issue.Code != "not_found" {
		t.Fatalf("empty entries want not_found, got %+v", issue)
	}
}

func TestMixedRelativePATHRejectsEntireBareLookup(t *testing.T) {
	dirA := t.TempDir()
	rel := t.TempDir()
	tempExecutable(t, dirA, "tool", "#!/bin/sh\n")
	env := envWith("PATH=" + dirA + string(os.PathListSeparator) + filepath.Base(rel))
	_, issue := ResolveCommand("tool", env, t.TempDir())
	if issue == nil || issue.Code != "invalid_search_path" {
		t.Fatalf("want invalid_search_path, got %+v", issue)
	}
	// An explicit absolute command bypasses PATH validation entirely.
	path, issue := ResolveCommand(filepath.Join(dirA, "tool"), env, t.TempDir())
	if issue != nil || path == "" {
		t.Fatalf("explicit path must still resolve, got %q %+v", path, issue)
	}
}

func TestExplicitRelativePathWithSpacesResolvesAgainstWorkspace(t *testing.T) {
	workspace := t.TempDir()
	tools := filepath.Join(workspace, "tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	tempExecutable(t, tools, "my agent", "#!/bin/sh\n")
	path, issue := ResolveCommand("./tools/my agent", os.Environ(), workspace)
	if issue != nil {
		t.Fatalf("unexpected issue: %+v", issue)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(tools, "my agent"))
	if path != want {
		t.Fatalf("path = %q, want literal workspace file %q", path, want)
	}
}

func TestExplicitMissingPathNeverFallsBack(t *testing.T) {
	dir := t.TempDir()
	tempExecutable(t, dir, "codex", "#!/bin/sh\n")
	env := envWith("PATH=" + dir)
	_, issue := ResolveCommand("/nonexistent/codex", env, t.TempDir())
	if issue == nil || issue.Code != "not_found" {
		t.Fatalf("want not_found without fallback, got %+v", issue)
	}
}

func TestSymlinkTargets(t *testing.T) {
	dir := t.TempDir()
	target := tempExecutable(t, dir, "real", "#!/bin/sh\n")
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, issue := ResolveCommand(link, os.Environ(), t.TempDir()); issue != nil {
		t.Fatalf("regular symlink target must pass: %+v", issue)
	}
	broken := filepath.Join(dir, "broken")
	if err := os.Symlink(filepath.Join(dir, "missing"), broken); err != nil {
		t.Fatal(err)
	}
	if _, issue := ResolveCommand(broken, os.Environ(), t.TempDir()); issue == nil {
		t.Fatal("broken symlink must fail")
	}
	if err := os.MkdirAll(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, issue := ResolveCommand(filepath.Join(dir, "adir"), os.Environ(), t.TempDir()); issue == nil {
		t.Fatal("directory target must fail")
	}
}

func TestMissingExecutePermission(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noexec")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, issue := ResolveCommand(path, os.Environ(), t.TempDir())
	if issue == nil || issue.Code != "not_executable" {
		t.Fatalf("want not_executable, got %+v", issue)
	}
}

func TestLauncherInterpreterChain(t *testing.T) {
	dir := t.TempDir()
	nodeDir := t.TempDir()
	stubNode := tempExecutable(t, nodeDir, "node", "#!/bin/sh\n")
	script := filepath.Join(dir, "launcher")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// node available: chain passes.
	env := envWith("PATH=" + nodeDir)
	if issue := CheckLauncher(script, env, t.TempDir()); issue != nil {
		t.Fatalf("satisfied interpreter chain must pass: %+v", issue)
	}
	// node absent: interpreter prerequisite reported without executing it.
	empty := t.TempDir()
	if issue := CheckLauncher(script, envWith("PATH="+empty), t.TempDir()); issue == nil || issue.Component != "interpreter" {
		t.Fatalf("want interpreter issue, got %+v", issue)
	}
	_ = stubNode
}

func TestUnsupportedShebangReportsUnsupportedLauncher(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "wrapper")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env -S node --max-old-space-size=4096\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if issue := CheckLauncher(script, os.Environ(), t.TempDir()); issue == nil || issue.Code != "unsupported_launcher" {
		t.Fatalf("want unsupported_launcher, got %+v", issue)
	}
}

func TestMaterializeEnvPreservesLaunchSemantics(t *testing.T) {
	ambient := []string{"PATH=/usr/bin"}
	if got := MaterializeEnv(nil, ambient); len(got) != 1 || got[0] != "PATH=/usr/bin" {
		t.Fatalf("nil builder output must become ambient, got %v", got)
	}
	explicit := []string{}
	if got := MaterializeEnv(explicit, ambient); got == nil || len(got) != 0 {
		t.Fatalf("explicit empty must stay explicit, got %v", got)
	}
}
