package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/skillmanager"
)

func maintenanceFixture(t *testing.T) (Maintenance, string) {
	t.Helper()
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	bundle, e := skillmanager.Embedded()
	if e != nil {
		t.Fatal(e)
	}
	m, e := skillmanager.New(home, home, nil, skillmanager.Build{Version: "v1.0.0"}, bundle)
	if e != nil {
		t.Fatal(e)
	}
	r, code := m.Execute(context.Background(), skillmanager.Options{Command: "install", Agents: []string{"codex"}})
	if code != 0 {
		t.Fatal(r)
	}
	executable := filepath.Join(home, "squad")
	if e = os.WriteFile(executable, []byte("old image"), 0700); e != nil {
		t.Fatal(e)
	}
	u := Maintenance{Manager: m, Executable: executable, Check: func(context.Context) (*Release, error) { return nil, nil }}
	return u, r.Destinations[0].Path
}
func TestMaintenanceCurrentAndOfflineRepair(t *testing.T) {
	for _, networkFailed := range []bool{false, true} {
		t.Run(map[bool]string{true: "offline", false: "current"}[networkFailed], func(t *testing.T) {
			u, p := maintenanceFixture(t)
			os.RemoveAll(p)
			if networkFailed {
				u.Check = func(context.Context) (*Release, error) { return nil, errors.New("network secret") }
			}
			r, updated := u.Run(context.Background())
			if updated || len(r.Destinations) != 1 || !r.Destinations[0].Changed {
				t.Fatalf("missing repair %+v", r)
			}
			if networkFailed && (r.Status != "partial" || r.Binary.Error == nil) {
				t.Fatalf("lost network failure %+v", r)
			}
		})
	}
}
func TestMaintenanceNewImageAndFailedHandoff(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{true: "failed", false: "new"}[failed], func(t *testing.T) {
			u, _ := maintenanceFixture(t)
			u.Check = func(context.Context) (*Release, error) { return &Release{Version: "v1.1.0"}, nil }
			u.Install = func(_ context.Context, _ Release, p string) error { return os.WriteFile(p, []byte("new image"), 0700) }
			u.Handoff = func(ctx context.Context, p, v string) (skillmanager.Report, error) {
				b, _ := os.ReadFile(p)
				if string(b) != "new image" {
					t.Fatal("old image")
				}
				if failed {
					return skillmanager.Report{}, errors.New("failed")
				}
				bundle, e := skillmanager.LoadBundle(fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: agent-debug-squad\ndescription: New release\n---\nnew release instructions\n")}}, nil)
				if e != nil {
					t.Fatal(e)
				}
				m, e := skillmanager.New(u.Manager.Home, u.Manager.Home, nil, skillmanager.Build{Version: v}, bundle)
				if e != nil {
					t.Fatal(e)
				}
				r, _ := m.Execute(ctx, skillmanager.Options{Command: "sync"})
				return r, nil
			}
			r, updated := u.Run(context.Background())
			if !updated || r.Binary.Status != "installed" {
				t.Fatal("lost installed state")
			}
			if failed {
				if r.Error == nil || r.Error.Code != "skill_handoff_failed" {
					t.Fatal("false success")
				}
			} else if r.Program.Version != "v1.1.0" || !r.Destinations[0].Changed {
				t.Fatal("old bundle used")
			}
		})
	}
}
func TestMaintenanceUncertainInstall(t *testing.T) {
	u, _ := maintenanceFixture(t)
	u.Check = func(context.Context) (*Release, error) { return &Release{Version: "v1.1.0"}, nil }
	u.Install = func(_ context.Context, _ Release, p string) error {
		os.WriteFile(p, []byte("uncertain new bytes"), 0700)
		return errors.New("failure")
	}
	r, _ := u.Run(context.Background())
	if r.Binary.Status != "unknown" || r.Error == nil || len(r.Destinations) != 0 {
		t.Fatal("synced old bundle after uncertain install")
	}
}
func TestMaintenanceLock(t *testing.T) {
	u, _ := maintenanceFixture(t)
	unlock, e := u.Manager.UpdateLock(u.Executable)
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	r, _ := u.Run(context.Background())
	if r.Error == nil || r.Error.Code != "busy" {
		t.Fatal("raced update")
	}
}
func TestSyncInstalledReportValidation(t *testing.T) {
	u, _ := maintenanceFixture(t)
	r := skillmanager.NewReport(u.Manager.Build)
	b, _ := json.Marshal(r)
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{{"valid", "printf '%s\\n' '" + string(b) + "'", true}, {"invalid", "echo not-json", false}, {"wrong-exit", "printf '%s\\n' '" + string(b) + "'; exit 1", false}, {"extra", "printf '%s\\n' '" + string(b) + "'; echo extra", false}} {
		t.Run(tc.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "child")
			os.WriteFile(script, []byte("#!/bin/sh\n"+tc.body+"\n"), 0700)
			_, e := SyncInstalled(context.Background(), script, "v1.0.0")
			if (e == nil) != tc.ok {
				t.Fatal(e)
			}
		})
	}
}
func TestContinuationIsOneUseAndVersionBound(t *testing.T) {
	t.Setenv(ContinuationEnv, "")
	env := ContinuationEnvironment("1.0.0")
	for _, s := range env {
		if strings.HasPrefix(s, ContinuationEnv+"=") {
			os.Setenv(ContinuationEnv, strings.TrimPrefix(s, ContinuationEnv+"="))
		}
	}
	if !ConsumeContinuation("v1.0.0") || ConsumeContinuation("1.0.0") {
		t.Fatal("continuation not one use")
	}
}

func TestRealNewBinaryHandoff(t *testing.T) {
	u, p := maintenanceFixture(t)
	old, e := skillmanager.LoadBundle(fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: agent-debug-squad\ndescription: Old fixture\n---\nold bundle\n")}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	// Replace the fixture registration with a distinct old bundle at a new release.
	oldManager, e := skillmanager.New(u.Manager.Home, u.Manager.Home, nil, skillmanager.Build{Version: "v1.1.0"}, old)
	if e != nil {
		t.Fatal(e)
	}
	if r, c := oldManager.Execute(context.Background(), skillmanager.Options{Command: "sync"}); c != 0 {
		t.Fatal(r)
	}
	u.Manager = oldManager
	next := filepath.Join(t.TempDir(), "new-squad")
	cmd := exec.Command("go", "build", "-ldflags=-X main.version=1.2.0", "-o", next, "../../cmd/agent-debug-squad")
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, output)
	}
	t.Setenv("HOME", u.Manager.Home)
	u.Check = func(context.Context) (*Release, error) { return &Release{Version: "v1.2.0"}, nil }
	u.Install = func(_ context.Context, _ Release, p string) error {
		b, e := os.ReadFile(next)
		if e != nil {
			return e
		}
		return os.WriteFile(p, b, 0700)
	}
	r, updated := u.Run(context.Background())
	if !updated || r.Status != "ok" || r.Program.Version != "v1.2.0" || r.Program.BundleHash == old.Hash {
		t.Fatalf("new-image sync failed: %+v", r)
	}
	data, e := os.ReadFile(filepath.Join(p, "SKILL.md"))
	if e != nil || strings.Contains(string(data), "old bundle") {
		t.Fatal("old image payload was used", e)
	}
}

func TestHandoffDeadlineAndCommittedRecoveryReport(t *testing.T) {
	script := filepath.Join(t.TempDir(), "child")
	os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 20\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, e := SyncInstalled(ctx, script, "v1.0.0"); e == nil {
		t.Fatal("deadline ignored")
	}
	u, _ := maintenanceFixture(t)
	u.Check = func(context.Context) (*Release, error) { return &Release{Version: "v1.1.0"}, nil }
	u.Install = func(context.Context, Release, string) error { return nil }
	u.Handoff = func(ctx context.Context, p, v string) (skillmanager.Report, error) {
		bundle, e := skillmanager.LoadBundle(fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: agent-debug-squad\ndescription: New content\n---\nnew\n")}}, nil)
		if e != nil {
			t.Fatal(e)
		}
		m, e := skillmanager.New(u.Manager.Home, u.Manager.Home, nil, skillmanager.Build{Version: v}, bundle)
		if e != nil {
			t.Fatal(e)
		}
		if r, c := m.Execute(ctx, skillmanager.Options{Command: "sync"}); c != 0 {
			t.Fatal(r)
		}
		return skillmanager.Report{}, errors.New("lost output after commit")
	}
	r, updated := u.Run(context.Background())
	if !updated || r.Error == nil || len(r.Destinations) != 1 || !r.Destinations[0].Changed || !r.Destinations[0].Reread {
		t.Fatalf("lost confirmed commit: %+v", r)
	}
}
