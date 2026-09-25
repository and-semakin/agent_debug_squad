package oneshot

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
	"github.com/and-semakin/agent_debug_squad/internal/store"
	"github.com/and-semakin/agent_debug_squad/internal/workflow"
)

const runSummaryFile = "run-summary.json"

// isolateHome redirects the home directory so the machine backend config of
// the developer machine never leaks into the test.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// sessionStore builds a store over the YAML config used by Main.
func sessionStore(t *testing.T, cfgPath string) *store.Store {
	t.Helper()
	isolateHome(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return store.New(cfg)
}

func cfgBody(t *testing.T, cfgPath string) string {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return string(data)
}

// overrideDelay rewrites the fake backend delay in the config file.
func overrideDelay(t *testing.T, cfgPath, delayMS string) {
	t.Helper()
	body := cfgBody(t, cfgPath)
	body = replaceFirst(body, `delay_ms: "40"`, `delay_ms: "`+delayMS+`"`)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
}

func replaceFirst(s, old, new string) string {
	idx := indexOf(s, old)
	if idx < 0 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// blockOnInterruptedExecution leaves a nonterminal execution behind, as a
// crashed one-shot owner would: an attempt is reserved but never settles.
func blockOnInterruptedExecution(t *testing.T, cfgPath, requestID string) {
	t.Helper()
	isolateHome(t)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	st := store.New(cfg)
	ownership, err := store.AcquireSessionOwnership(st.SessionDir())
	if err != nil {
		t.Fatalf("ownership: %v", err)
	}
	orch, err := orchestrator.NewWorkflowOnly(context.Background(), cfg, st)
	if err != nil {
		_ = ownership.Release()
		t.Fatalf("orchestrator: %v", err)
	}
	m := workflow.NewManager(cfg, st, orch)
	if err := m.StartScoped(context.Background(), ""); err != nil {
		_ = ownership.Release()
		t.Fatalf("start: %v", err)
	}
	if _, _, err := m.CreateSelected(requestID); err != nil {
		_ = ownership.Release()
		t.Fatalf("create: %v", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = m.Stop(stopCtx)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer waitCancel()
	_ = orch.WaitForWorkers(waitCtx)
	if err := ownership.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}
