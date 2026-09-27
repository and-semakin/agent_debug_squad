package modeldiscovery

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/adapters"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
)

type outcomeLister struct {
	result domain.ModelDiscoveryResult
	issue  *domain.InstallationIssue
	calls  *int
}

func (l outcomeLister) CheckInstallation(context.Context, domain.InstallationInput) domain.InstallationResult {
	if l.issue != nil {
		return domain.InstallationResult{Status: domain.InstallationStatusFailed, Issues: []domain.InstallationIssue{*l.issue}}
	}
	return domain.InstallationResult{Status: domain.InstallationStatusNotRequired}
}
func (l outcomeLister) ListModels(context.Context, domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	*l.calls++
	return l.result
}
func TestEveryOutcomeAndRefresh(t *testing.T) {
	for _, status := range []string{"ok", "empty", "partial", "not_installed", "not_configured", "auth_required", "offline", "unsupported", "timeout", "cancelled", "error"} {
		t.Run(status, func(t *testing.T) {
			calls := 0
			r := modelprobe.New("fake", "synthetic", "test", "synthetic", domain.ModelDiscoveryInput{})
			old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			r.Sources[0].Freshness = domain.ModelFreshness{State: "stale", AsOf: &old, Cached: modelprobe.Bool(true)}
			if status == "ok" || status == "partial" {
				r.Models = append(r.Models, modelprobe.Model("fake", "m", "test"))
			}
			if status != "ok" && status != "empty" {
				modelprobe.Fail(&r, status, "fixture")
			}
			factory := func(context.Context, domain.AgentSpec, domain.MachineBackends, domain.ModelDiscoveryInput) (adapters.ModelLister, func(), error) {
				return outcomeLister{result: r, calls: &calls}, func() {}, nil
			}
			for i := 0; i < 2; i++ {
				report := Discover(context.Background(), []Target{{ID: "machine:fake", Spec: domain.AgentSpec{Backend: "fake"}}}, domain.MachineBackends{}, time.Second, time.Second, factory)
				want := "failed"
				if status == "ok" || status == "empty" {
					want = "ok"
				}
				if status == "partial" {
					want = "partial"
				}
				if report.Status != want || report.Results[0].Status != status || !report.Results[0].Sources[0].Freshness.AsOf.Equal(old) {
					t.Fatal(report)
				}
			}
			if calls != 2 {
				t.Fatal("catalog reused", calls)
			}
		})
	}
}
func TestInstallationFailureSkipsListing(t *testing.T) {
	calls := 0
	factory := func(context.Context, domain.AgentSpec, domain.MachineBackends, domain.ModelDiscoveryInput) (adapters.ModelLister, func(), error) {
		return outcomeLister{issue: &domain.InstallationIssue{Code: "not_found"}, calls: &calls}, func() {}, nil
	}
	r := Discover(context.Background(), []Target{{Spec: domain.AgentSpec{Backend: "fake"}}}, domain.MachineBackends{}, time.Second, time.Second, factory)
	if calls != 0 || r.Status != "failed" || r.Results[0].Status != "not_installed" {
		t.Fatal(r, calls)
	}
}
func TestFactoryCoverageAndNoState(t *testing.T) {
	dir := t.TempDir()
	for _, backend := range append(append([]string{}, RealBackends...), "fake") {
		l, close, e := adapters.NewModelLister(context.Background(), domain.AgentSpec{Backend: backend}, domain.MachineBackends{}, domain.ModelDiscoveryInput{WorkspaceDir: dir, AmbientEnv: []string{}})
		if e != nil || l == nil {
			t.Fatal(backend, e)
		}
		close()
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
}
func TestCLIExitCancellationAndDegradation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/definitely-not-installed")
	for _, tc := range []struct {
		backend string
		cancel  bool
		code    int
	}{{"fake", true, 130}, {"codex", false, 1}} {
		ctx, cancel := context.WithCancel(context.Background())
		if tc.cancel {
			cancel()
		}
		var out, errout bytes.Buffer
		code := Main(ctx, []string{"--backend", tc.backend, "--json"}, &out, &errout)
		cancel()
		if code != tc.code || errout.Len() != 0 {
			t.Fatal(code, errout.String())
		}
		var r domain.ModelCatalog
		if e := json.Unmarshal(out.Bytes(), &r); e != nil || len(r.Results) != 1 {
			t.Fatal(e, out.String())
		}
	}
}
func TestSelectorModesAndPrivateDeduplication(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "squad.yaml")
	body := "workspace_dir: " + dir + "\nagents:\n  - name: a\n    backend: codex\n    startup_prompt: unused\n    options:\n      model: arbitrary\n      env: ['HOME=/first', 'PATH=/bin', 'API_KEY=secret-a']\n  - name: b\n    backend: codex\n    startup_prompt: unused\n    options:\n      env: ['HOME=/second', 'PATH=/bin', 'API_KEY=secret-b']\n"
	if e := os.WriteFile(cfg, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		o Options
		n int
	}{{Options{}, 5}, {Options{ConfigPath: cfg}, 2}, {Options{ConfigPath: cfg, All: true}, 6}, {Options{ConfigPath: cfg, Backends: []string{"fake"}}, 1}, {Options{ConfigPath: cfg, Backends: []string{"codex", "codex"}}, 2}} {
		targets, e := Resolve(tc.o, domain.MachineBackends{}, nil)
		if e != nil || len(targets) != tc.n {
			t.Fatal(targets, e)
		}
		for _, target := range targets {
			if target.ID != "agents:codex:a" && target.ID != "agents:codex:b" && len(target.Agents) > 0 {
				t.Fatal(target.ID)
			}
		}
	}
}

func TestCleanupFailurePreservesRows(t *testing.T) {
	calls := 0
	r := modelprobe.New("fake", "synthetic", "test", "synthetic", domain.ModelDiscoveryInput{})
	r.Models = []domain.CatalogModel{modelprobe.Model("fake", "m", "test")}
	factory := func(context.Context, domain.AgentSpec, domain.MachineBackends, domain.ModelDiscoveryInput) (adapters.ModelLister, func(), error) {
		return outcomeLister{result: r, calls: &calls}, func() { panic("SECRET") }, nil
	}
	report := Discover(context.Background(), []Target{{Spec: domain.AgentSpec{Backend: "fake"}}}, domain.MachineBackends{}, time.Second, time.Second, factory)
	if report.Status != "partial" || report.Results[0].Diagnostics[0].Code != "cleanup_failed" {
		t.Fatal(report)
	}
}
