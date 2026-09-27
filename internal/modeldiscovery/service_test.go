package modeldiscovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/adapters"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureLister struct {
	status              string
	count, active, peak *atomic.Int32
	delay               time.Duration
}

func (f fixtureLister) CheckInstallation(context.Context, domain.InstallationInput) domain.InstallationResult {
	return domain.InstallationResult{Status: "not_required"}
}
func (f fixtureLister) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	f.count.Add(1)
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for p := f.peak.Load(); n > p; p = f.peak.Load() {
		if f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	r := modelprobe.New("fake", "synthetic", "test", "synthetic", in)
	select {
	case <-ctx.Done():
		modelprobe.Fail(&r, modelprobe.ContextStatus(ctx, "error"), "context_ended")
	case <-time.After(f.delay):
		if f.status != "empty" {
			r.Models = []domain.CatalogModel{modelprobe.Model("fake", "test", "test")}
		}
	}
	return r
}
func TestBudgetsAndConcurrency(t *testing.T) {
	for n, want := range map[int]time.Duration{1: 60 * time.Second, 5: 75 * time.Second, 7: 110 * time.Second} {
		got, e := DefaultTimeout(n, 30*time.Second)
		if e != nil || got != want {
			t.Fatal(n, got, e)
		}
	}
	if _, e := DefaultTimeout(7, time.Duration(math.MaxInt64)); e == nil {
		t.Fatal("overflow")
	}
	if d, _ := DefaultTimeout(7, 10*time.Second); d != 60*time.Second {
		t.Fatal(d)
	}
	targets := []Target{}
	for i := 0; i < 7; i++ {
		targets = append(targets, Target{ID: fmt.Sprint(i), Spec: domain.AgentSpec{Backend: "fake", Name: fmt.Sprint(i)}})
	}
	var count, active, peak atomic.Int32
	factory := func(context.Context, domain.AgentSpec, domain.MachineBackends, domain.ModelDiscoveryInput) (adapters.ModelLister, func(), error) {
		return fixtureLister{count: &count, active: &active, peak: &peak, delay: 15 * time.Millisecond}, func() {}, nil
	}
	r := Discover(context.Background(), targets, domain.MachineBackends{}, 2*time.Second, 250*time.Millisecond, factory)
	if r.Status != "ok" || count.Load() != 7 || peak.Load() > 3 {
		t.Fatalf("%s count %d peak %d", r.Status, count.Load(), peak.Load())
	}
	shortFactory := func(_ context.Context, spec domain.AgentSpec, _ domain.MachineBackends, _ domain.ModelDiscoveryInput) (adapters.ModelLister, func(), error) {
		delay := 200 * time.Millisecond
		if spec.Name == "0" {
			delay = 0
		}
		return fixtureLister{count: &count, active: &active, peak: &peak, delay: delay}, func() {}, nil
	}
	r = Discover(context.Background(), targets, domain.MachineBackends{}, 50*time.Millisecond, time.Second, shortFactory)
	if r.Status != "partial" {
		t.Fatal(r.Status)
	}
	for _, x := range r.Results[6:] {
		if x.Status != "timeout" {
			t.Fatal(x)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = Discover(ctx, targets, domain.MachineBackends{}, time.Second, time.Second, factory)
	for _, x := range r.Results {
		if x.Status != "cancelled" {
			t.Fatal(x)
		}
	}
}
func TestResolutionAndCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "squad.yaml")
	data := "workspace_dir: " + dir + "\nagents:\n  - name: 'A,B'\n    backend: fake\n    startup_prompt: hi\n    options: {model: not-in-catalog}\n  - name: C\n    backend: fake\n    startup_prompt: different\n    options: {model: arbitrary}\n"
	if e := os.WriteFile(cfg, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	targets, e := Resolve(Options{ConfigPath: cfg}, domain.MachineBackends{}, []string{"HOME=" + home})
	if e != nil || len(targets) != 1 || targets[0].ID != "agents:fake:A%2CB,C" {
		t.Fatalf("%+v %v", targets, e)
	}
	targets, e = Resolve(Options{Backends: []string{"fake", "fake"}}, domain.MachineBackends{}, nil)
	if e != nil || len(targets) != 1 {
		t.Fatal(targets, e)
	}
	if _, e = Resolve(Options{ConfigPath: cfg, Workspace: home}, domain.MachineBackends{}, nil); e == nil {
		t.Fatal("workspace conflict")
	}
	for _, tt := range []struct {
		args      []string
		code      int
		errorCode string
	}{{[]string{"--backend", "fake", "--json"}, 0, ""}, {[]string{"--config", cfg, "--json"}, 0, ""}, {[]string{"--all", "--backend", "fake", "--json"}, 2, "invalid_arguments"}, {[]string{"--timeout", "0s", "--json"}, 2, "invalid_arguments"}, {[]string{"--config", cfg + "missing", "--json"}, 2, "invalid_configuration"}, {[]string{"--bogus", "secret-value", "--json"}, 2, "invalid_arguments"}} {
		var out, errout bytes.Buffer
		code := Main(context.Background(), tt.args, &out, &errout)
		if code != tt.code {
			t.Fatalf("%v: %d %s", tt.args, code, out.String())
		}
		var r domain.ModelCatalog
		if e := json.Unmarshal(out.Bytes(), &r); e != nil {
			t.Fatal(e, out.String())
		}
		if tt.errorCode != "" && (r.Error == nil || r.Error.Code != tt.errorCode) {
			t.Fatal(r)
		}
		if strings.Contains(out.String()+errout.String(), "secret-value") {
			t.Fatal("leak")
		}
	}
	var out, errout bytes.Buffer
	code := Main(context.Background(), []string{"--backend", "fake"}, &out, &errout)
	if code != 0 || !strings.Contains(out.String(), "inference was not checked") {
		t.Fatal(code, out.String())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("discovery wrote state", entries)
	}
}
func TestHumanEscapes(t *testing.T) {
	var out bytes.Buffer
	Human(&out, domain.ModelCatalog{Status: "partial", Results: []domain.ModelDiscoveryResult{{Backend: "fake", TargetID: "evil\x1b[2J", Status: "unsupported", Diagnostics: []domain.ModelDiagnostic{{Code: "x", Message: "\x1b[31m"}}}}})
	if strings.ContainsRune(out.String(), '\x1b') || !strings.Contains(out.String(), "partial;") {
		t.Fatal(out.String())
	}
}
