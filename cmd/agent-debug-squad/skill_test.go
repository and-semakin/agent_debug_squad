package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/skillmanager"
)

func TestSkillCLIIsolatedHome(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	call := func(want int, args ...string) skillmanager.Report {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.Dir = home
		output, e := cmd.Output()
		code := 0
		if e != nil {
			var ee *exec.ExitError
			if !errors.As(e, &ee) {
				t.Fatal(e)
			}
			code = ee.ExitCode()
		}
		if code != want {
			t.Fatalf("%v exit %d: %s", args, code, output)
		}
		var r skillmanager.Report
		if e = json.Unmarshal(output, &r); e != nil {
			t.Fatalf("bad JSON %s: %v", output, e)
		}
		return r
	}
	call(0, "skill", "status", "--json")
	if _, e := os.Stat(filepath.Join(home, ".agent-debug-squad")); !os.IsNotExist(e) {
		t.Fatal("status wrote state")
	}
	r := call(0, "skill", "install", "--agent", "codex", "--allow-dev", "--json")
	p := r.Destinations[0].Path
	call(0, "skill", "sync", "--allow-dev", "--json")
	call(2, "update", "--no-auto-update", "--json")
	call(2, "update", "--json")
	data, e := os.ReadFile(filepath.Join(p, "SKILL.md"))
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(p, "SKILL.md"), append(data, []byte("\nlocal\n")...), 0600)
	call(1, "skill", "status", "--json")
	cmd := exec.Command(bin, "version")
	cmd.Env = append(os.Environ(), "HOME="+home)
	if e := cmd.Run(); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(p, "SKILL.md"))
	if len(after) != len(data)+7 {
		t.Fatal("version implicitly synchronized")
	}
	r = call(0, "skill", "sync", "--allow-dev", "--json")
	if r.Destinations[0].Backup == "" || !r.Destinations[0].Reread {
		t.Fatal("repair report missing")
	}
}
func TestServeMaintenanceOptOutAndRestartFailure(t *testing.T) {
	originalVersion := version
	originalRun := maintenanceRun
	originalRestart := restartProcess
	t.Cleanup(func() { version = originalVersion; maintenanceRun = originalRun; restartProcess = originalRestart })
	calls := 0
	maintenanceRun = func(context.Context) (skillmanager.Report, bool, error) {
		calls++
		r := skillmanager.NewReport(skillBuild())
		r.Binary = &skillmanager.BinaryResult{After: "v9.0.0", Status: "installed"}
		return r, true, nil
	}
	restartProcess = func(string) error { return errors.New("fixture restart failure") }
	t.Setenv(noAutoUpdateEnv, "")
	version = "1.0.0"
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if e := serve(context.Background(), []string{"--config", missing, "--no-auto-update"}); e == nil {
		t.Fatal("missing config accepted")
	}
	if calls != 0 {
		t.Fatal("flag opt-out ignored")
	}
	t.Setenv(noAutoUpdateEnv, "1")
	serve(context.Background(), []string{"--config", missing})
	if calls != 0 {
		t.Fatal("env opt-out ignored")
	}
	t.Setenv(noAutoUpdateEnv, "")
	version = "dev"
	serve(context.Background(), []string{"--config", missing})
	if calls != 0 {
		t.Fatal("dev auto-updated")
	}
	version = "1.0.0"
	e := serve(context.Background(), []string{"--config", missing})
	if e == nil || e.Error() != "fixture restart failure" || calls != 1 {
		t.Fatal("continued after failed reexec", e)
	}
}

func TestNonServeCommandsNeverMaintain(t *testing.T) {
	old := maintenanceRun
	oldVersion := version
	version = "1.0.0"
	t.Cleanup(func() { maintenanceRun = old; version = oldVersion })
	maintenanceRun = func(context.Context) (skillmanager.Report, bool, error) {
		t.Fatal("implicit maintenance")
		return skillmanager.Report{}, false, nil
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, args := range [][]string{{"version"}, {"skill", "status", "--json"}, {"models", "--not-a-flag"}, {"run", "--config", filepath.Join(home, "missing"), "--request-id", "test"}} {
		run(args)
	}
	if _, e := os.Stat(filepath.Join(home, ".agent-debug-squad", "skill-management")); !os.IsNotExist(e) {
		t.Fatal("implicit skill state mutation")
	}
}
func TestServeSkillFailureStillReachesConfiguration(t *testing.T) {
	old := maintenanceRun
	oldVersion := version
	version = "1.0.0"
	t.Cleanup(func() { maintenanceRun = old; version = oldVersion })
	t.Setenv(noAutoUpdateEnv, "")
	maintenanceRun = func(context.Context) (skillmanager.Report, bool, error) {
		r := skillmanager.NewReport(skillBuild())
		r.Error = skillmanager.Failure("fixture_skill_failure", "fixture")
		r.Finish()
		return r, false, nil
	}
	e := serve(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "missing.yaml")})
	if !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("configuration was not reached: %v", e)
	}
}

func TestSkillMaintenanceLeavesServingSessionIntact(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	cfg, _, port := workflowConfig(t, "single:\n  agent: alpha\n  prompt: fixture\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "serve", "--config", cfg, "--no-auto-update")
	cmd.Env = append(os.Environ(), "HOME="+home)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Signal(syscall.SIGTERM); cmd.Wait() }()
	client := &http.Client{Timeout: time.Second}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		resp, e := client.Get(base + "/health")
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("server did not start")
	}
	resp, e := client.Post(base+"/agents/alpha/runs?wait=true&timeout_seconds=5", "application/json", strings.NewReader(`{"message":"fixture turn"}`))
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatal("manual run rejected", resp.StatusCode)
	}
	get := func() []byte {
		t.Helper()
		resp, e := client.Get(base + "/agents")
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	before := get()
	update := exec.Command(bin, "skill", "install", "--agent", "codex", "--allow-dev", "--json")
	update.Env = append(os.Environ(), "HOME="+home)
	if out, e := update.CombinedOutput(); e != nil {
		t.Fatalf("install: %v %s", e, out)
	}
	after := get()
	if string(before) != string(after) {
		t.Fatalf("running session changed: %s -> %s", before, after)
	}
}
