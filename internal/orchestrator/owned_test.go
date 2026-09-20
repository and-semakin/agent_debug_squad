package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

func newOwnedTestOrchestrator(t *testing.T, agentNames ...string) (*Orchestrator, *store.Store) {
	t.Helper()
	return newOwnedTestOrchestratorWithDelay(t, 0, agentNames...)
}

// newOwnedTestOrchestratorWithDelay configures the fake backend delay so owned
// runs stay observable while live.
func newOwnedTestOrchestratorWithDelay(t *testing.T, delayMS int, agentNames ...string) (*Orchestrator, *store.Store) {
	t.Helper()
	cfg := testConfig(t, agentNames...)
	if delayMS > 0 {
		for i := range cfg.Agents {
			cfg.Agents[i].Options = map[string]any{"delay_ms": fmt.Sprintf("%d", delayMS)}
			cfg.Agents[i].StringOptions = map[string]string{"delay_ms": fmt.Sprintf("%d", delayMS)}
		}
	}
	st := store.New(cfg)
	o, err := New(context.Background(), cfg, st)
	if err != nil {
		t.Fatalf("orchestrator: %v", err)
	}
	return o, st
}

func readFileJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func TestSubmitOwnedRunRejectsDuplicateRunIDWhileLive(t *testing.T) {
	o, _ := newOwnedTestOrchestratorWithDelay(t, 2000, "Reviewer")
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000001_000001",
		Agent:     "Reviewer",
		Message:   "slow",
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}
	waitUntilOwnedActive(t, o, "wrun_000001_000001")

	err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:   "wrun_000001_000001",
		Agent:   "Reviewer",
		Message: "duplicate",
		OnDone:  func(domain.OwnedRunOutcome) {},
	})
	if !errors.Is(err, ErrRunIDReserved) {
		t.Fatalf("duplicate dispatch must be rejected while live, got %v", err)
	}

	if !o.CancelOwnedRun("wrun_000001_000001") {
		t.Fatal("cancel must reach the live run")
	}
	<-outcomes
}

func TestSubmitOwnedRunRequiresWorkflowNamespace(t *testing.T) {
	o, _ := newOwnedTestOrchestrator(t, "Reviewer")
	err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:   "run_000001",
		Agent:   "Reviewer",
		Message: "x",
		OnDone:  func(domain.OwnedRunOutcome) {},
	})
	if !errors.Is(err, ErrRunIDInvalid) {
		t.Fatalf("manual namespace must be rejected, got %v", err)
	}
}

func TestSubmitOwnedRunUnknownAgentFails(t *testing.T) {
	o, _ := newOwnedTestOrchestrator(t, "Reviewer")
	err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:   "wrun_000001_000001",
		Agent:   "Ghost",
		Message: "x",
		OnDone:  func(domain.OwnedRunOutcome) {},
	})
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("unknown agent must fail, got %v", err)
	}
}

func TestSubmitOwnedRunCompletesWithRawFinalMessage(t *testing.T) {
	o, st := newOwnedTestOrchestrator(t, "Reviewer")
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	statePath := filepath.Join(t.TempDir(), "owned-state.json")
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:   "wrun_000001_000001",
		Agent:   "Reviewer",
		Message: "hello",
		Metadata: map[string]string{
			"workflow_execution_id": "wf_000001",
			"workflow_task_id":      "review",
			"workflow_attempt":      "1",
		},
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: statePath,
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}

	select {
	case outcome := <-outcomes:
		if outcome.Status != domain.RunCompleted {
			t.Fatalf("status: %v", outcome.Status)
		}
		if outcome.FinalMessage == "" || outcome.Error != "" {
			t.Fatalf("outcome: %+v", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned run did not complete")
	}

	run, err := o.Run(context.Background(), "wrun_000001_000001")
	if err != nil {
		t.Fatalf("run record: %v", err)
	}
	if run.Status != domain.RunCompleted || run.OutputPath == nil {
		t.Fatalf("run projection: %+v", run)
	}
	if run.Metadata["workflow_execution_id"] != "wf_000001" || run.Metadata["workflow_task_id"] != "review" {
		t.Fatalf("workflow identity missing: %+v", run.Metadata)
	}

	var state domain.AgentState
	readFileJSON(t, statePath, &state)
	if state.BackendSessionID == "" {
		t.Fatal("owned state must record a backend session id")
	}
	if _, err := st.LoadAgentState("Reviewer"); err != nil {
		t.Fatalf("manual agent state must remain readable: %v", err)
	}
}

func TestSubmitOwnedRunUsesFreshSessionPerAttempt(t *testing.T) {
	o, st := newOwnedTestOrchestrator(t, "Reviewer")
	outcomes := make(chan domain.OwnedRunOutcome, 4)
	var mu sync.Mutex
	statePaths := make([]string, 0, 3)

	submit := func(runID string) {
		statePath := filepath.Join(t.TempDir(), "state.json")
		mu.Lock()
		statePaths = append(statePaths, statePath)
		mu.Unlock()
		if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
			RunID:     runID,
			Agent:     "Reviewer",
			Message:   "attempt",
			OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
			StatePath: statePath,
		}); err != nil {
			t.Errorf("submit %s: %v", runID, err)
		}
	}

	submit("wrun_000001_000001")
	<-outcomes
	submit("wrun_000001_000002")
	<-outcomes

	manual, err := o.SubmitRun(context.Background(), "Reviewer", "manual turn", nil)
	if err != nil {
		t.Fatalf("manual run must still work: %v", err)
	}
	submit("wrun_000002_000001")
	<-outcomes
	waitForTerminalRun(t, o, manual.RunID)

	mu.Lock()
	defer mu.Unlock()
	sessionIDs := map[string]bool{}
	for _, path := range statePaths {
		var state domain.AgentState
		readFileJSON(t, path, &state)
		if state.BackendSessionID == "" {
			t.Fatalf("owned state %s has no backend session id", path)
		}
		if sessionIDs[state.BackendSessionID] {
			t.Fatalf("owned attempts shared backend session %q", state.BackendSessionID)
		}
		sessionIDs[state.BackendSessionID] = true
	}

	manualState, err := st.LoadAgentState("Reviewer")
	if err != nil {
		t.Fatalf("manual state: %v", err)
	}
	if sessionIDs[manualState.BackendSessionID] {
		t.Fatalf("manual session %q reused by an owned runtime", manualState.BackendSessionID)
	}
	if manualState.LastRunID != manual.RunID {
		t.Fatalf("manual continuity broken: last run %q", manualState.LastRunID)
	}
}

func waitForTerminalRun(t *testing.T, o *Orchestrator, runID string) domain.RunRecord {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		run, err := o.Run(context.Background(), runID)
		if err == nil && isTerminal(run.Status) {
			return run
		}
		select {
		case <-deadline:
			t.Fatalf("run %s did not finish", runID)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestManualMutationOfWorkflowRuntimeRejected(t *testing.T) {
	o, _ := newOwnedTestOrchestratorWithDelay(t, 2000, "Reviewer")
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000003_000001",
		Agent:     "Reviewer",
		Message:   "slow",
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}
	waitUntilOwnedActive(t, o, "wrun_000003_000001")

	if _, err := o.SubmitRun(context.Background(), "wrun_000003_000001", "manual hijack", nil); !errors.Is(err, ErrWorkflowOwned) {
		t.Fatalf("manual run on owned runtime must return ErrWorkflowOwned, got %v", err)
	}
	if _, err := o.ResetAgent(context.Background(), "wrun_000003_000001", true); !errors.Is(err, ErrWorkflowOwned) {
		t.Fatalf("manual reset on owned runtime must return ErrWorkflowOwned, got %v", err)
	}
	if !o.CancelOwnedRun("wrun_000003_000001") {
		t.Fatal("cancel must reach the live run")
	}
	<-outcomes
}

func TestCancelOwnedRunFailsOutcomeWithContextError(t *testing.T) {
	o, _ := newOwnedTestOrchestratorWithDelay(t, 2000, "Reviewer")
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000004_000001",
		Agent:     "Reviewer",
		Message:   "cancel me",
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}

	waitUntilOwnedActive(t, o, "wrun_000004_000001")
	if !o.CancelOwnedRun("wrun_000004_000001") {
		t.Fatal("cancel must find the active owned run")
	}

	select {
	case outcome := <-outcomes:
		if outcome.Status != domain.RunFailed {
			t.Fatalf("cancelled owned run status: %v", outcome.Status)
		}
		if outcome.Error == "" {
			t.Fatal("cancelled owned run must carry an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled owned run did not report completion")
	}
	if o.OwnedRunActive("wrun_000004_000001") {
		t.Fatal("owned run must stop after cancellation")
	}
}

func TestReplyPermissionRoutesToOwnedRuntime(t *testing.T) {
	o, _ := newOwnedTestOrchestratorWithDelay(t, 2000, "Reviewer")
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000007_000001",
		Agent:     "Reviewer",
		Message:   "held",
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}
	waitUntilOwnedActive(t, o, "wrun_000007_000001")

	// The run record exists and the live owned runtime is addressed through
	// the run-scoped reply path. The fake backend does not implement the
	// PermissionReplier interface, so the routing itself is observable as
	// "unsupported backend" (409) rather than run-not-found or inactive.
	reply := domain.PermissionReply{Reply: "once"}
	err := o.ReplyPermission(context.Background(), "wrun_000007_000001", "per_x", reply)
	if !errors.Is(err, domain.ErrPermissionUnsupported) {
		t.Fatalf("reply must reach the live owned runtime and report unsupported backend, got %v", err)
	}

	if !o.CancelOwnedRun("wrun_000007_000001") {
		t.Fatal("cancel must reach the live run")
	}
	<-outcomes

	// After the worker stops the run is inactive for permission replies.
	if err := o.ReplyPermission(context.Background(), "wrun_000007_000001", "per_x", reply); !errors.Is(err, domain.ErrPermissionInactive) {
		t.Fatalf("finished owned run must report inactive, got %v", err)
	}
}

func waitUntilOwnedActive(t *testing.T, o *Orchestrator, runID string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for !o.OwnedRunActive(runID) {
		select {
		case <-deadline:
			t.Fatalf("owned run %s never became active", runID)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestOwnedRunsDoNotDisturbManualNumbering(t *testing.T) {
	o, _ := newOwnedTestOrchestrator(t, "Reviewer")
	outcomes := make(chan domain.OwnedRunOutcome, 2)

	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000001_000001",
		Agent:     "Reviewer",
		Message:   "owned",
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}
	<-outcomes

	manual, err := o.SubmitRun(context.Background(), "Reviewer", "manual", nil)
	if err != nil {
		t.Fatalf("manual run: %v", err)
	}
	if manual.RunID != "run_000001" {
		t.Fatalf("manual numbering must be unaffected by workflow run IDs, got %s", manual.RunID)
	}
	waitForTerminalRun(t, o, manual.RunID)
}

func TestStartupInterruptionSkipsWorkflowRuns(t *testing.T) {
	cfg := testConfig(t, "Reviewer")
	st := store.New(cfg)
	o, err := New(context.Background(), cfg, st)
	if err != nil {
		t.Fatalf("orchestrator: %v", err)
	}
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000001_000001",
		Agent:     "Reviewer",
		Message:   "owned",
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}
	<-outcomes
	run, err := o.Run(context.Background(), "wrun_000001_000001")
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	run.Status = domain.RunRunning
	run.CompletedAt = nil
	if err := st.SaveRun(run); err != nil {
		t.Fatalf("save run: %v", err)
	}

	if _, err := New(context.Background(), cfg, st); err != nil {
		t.Fatalf("restart: %v", err)
	}
	after, err := st.LoadRun("wrun_000001_000001")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Status != domain.RunRunning {
		t.Fatalf("workflow-owned projection must not be interrupted on startup, got %v", after.Status)
	}
}

func TestOwnedRunStreamsArtifacts(t *testing.T) {
	o, st := newOwnedTestOrchestrator(t, "Reviewer")
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000005_000001",
		Agent:     "Reviewer",
		Message:   "stream",
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}
	<-outcomes

	eventsPath := filepath.Join(st.SessionDir(), "runs", "wrun_000005_000001", "Reviewer.events.jsonl")
	if _, err := os.Stat(eventsPath); err != nil {
		t.Fatalf("streaming events must be written for owned runs: %v", err)
	}
}

// TestSubmitOwnedRunRunsBackendInConfiguredWorkspace reproduces the reviewer
// check with a stub CLI executable: the child process must run inside the
// configured workspace, never the server's inherited directory.
func TestSubmitOwnedRunRunsBackendInConfiguredWorkspace(t *testing.T) {
	binDir := t.TempDir()
	script := filepath.Join(binDir, "codex-cwd.sh")
	body := "#!/bin/sh\n" +
		"cwd=\"$(pwd -P)\"\n" +
		"printf '{\"type\":\"thread.started\",\"session_id\":\"stub-thread\"}\\n'\n" +
		"printf '{\"type\":\"item.completed\",\"item\":{\"role\":\"assistant\",\"text\":\"cwd %s\"}}\\n' \"$cwd\"\n" +
		"printf '{\"type\":\"turn.completed\"}\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	cfg := testConfig(t, "Reviewer")
	workspace, err := filepath.EvalSymlinks(cfg.WorkspaceDir)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	st := store.New(cfg)
	o, err := New(context.Background(), cfg, st)
	if err != nil {
		t.Fatalf("orchestrator: %v", err)
	}
	saved := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "codex",
		StartupPrompt: "Report your working directory.",
		Options:       map[string]any{"command": script},
	}
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000006_000001",
		Agent:     "Reviewer",
		Message:   "where are you",
		Spec:      &saved,
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}

	select {
	case outcome := <-outcomes:
		if outcome.Status != domain.RunCompleted {
			t.Fatalf("owned codex run status: %v (%s)", outcome.Status, outcome.Error)
		}
		if !strings.Contains(outcome.FinalMessage, workspace) {
			t.Fatalf("backend child process ran in %q, want configured workspace %q", outcome.FinalMessage, workspace)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("owned run did not complete")
	}
}

// TestSubmitOwnedRunUsesSavedAgentSpec verifies the executor prefers the
// immutable saved configuration over the server's current YAML.
func TestSubmitOwnedRunUsesSavedAgentSpec(t *testing.T) {
	o, _ := newOwnedTestOrchestrator(t, "Reviewer")
	// The saved spec is a different role than the live "Reviewer" agent:
	// name, prompt, and backend options must all come from the saved copy.
	saved := domain.AgentSpec{
		Name:          "SavedRole",
		Backend:       "fake",
		StartupPrompt: "Saved role prompt.",
		Options:       map[string]any{"delay_ms": "10"},
	}
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000009_000001",
		Agent:     "Reviewer",
		Message:   "saved turn",
		Spec:      &saved,
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}
	select {
	case outcome := <-outcomes:
		if outcome.Status != domain.RunCompleted {
			t.Fatalf("status: %v (%s)", outcome.Status, outcome.Error)
		}
		if outcome.FinalMessage != "SavedRole received: saved turn" {
			t.Fatalf("runtime must use the saved spec identity, got %q", outcome.FinalMessage)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned run did not complete")
	}
}

// TestSubmitOwnedRunRejectsUnresolvableSavedSpec verifies a saved spec that
// cannot be normalized fails before any reservation is left behind.
func TestSubmitOwnedRunRejectsUnresolvableSavedSpec(t *testing.T) {
	o, _ := newOwnedTestOrchestrator(t, "Reviewer")
	saved := domain.AgentSpec{Name: "Reviewer", Backend: "fake"}
	err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:   "wrun_000010_000001",
		Agent:   "Reviewer",
		Message: "x",
		Spec:    &saved,
		OnDone:  func(domain.OwnedRunOutcome) {},
	})
	if err == nil || !strings.Contains(err.Error(), "startup_prompt") {
		t.Fatalf("saved spec without a prompt must fail resolution, got %v", err)
	}
}

// TestWaitForWorkersJoinsAfterOwnedInitFailure guards the WaitGroup leak: an
// owned run whose adapter fails during Init must still release its worker
// reservation so shutdown joins promptly.
func TestWaitForWorkersJoinsAfterOwnedInitFailure(t *testing.T) {
	o, _ := newOwnedTestOrchestrator(t, "Reviewer")
	// The zcode adapter rejects an unknown reasoning level during Init.
	saved := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "zcode",
		StartupPrompt: "Will never start.",
		Options:       map[string]any{"reasoning": "ultra"},
	}
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(context.Background(), domain.OwnedRunOptions{
		RunID:     "wrun_000011_000001",
		Agent:     "Reviewer",
		Message:   "x",
		Spec:      &saved,
		OnDone:    func(outcome domain.OwnedRunOutcome) { outcomes <- outcome },
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}); err != nil {
		t.Fatalf("submit owned: %v", err)
	}
	select {
	case outcome := <-outcomes:
		if outcome.Status != domain.RunFailed {
			t.Fatalf("init failure must surface as a failed run, got %v", outcome.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failed owned run did not report completion")
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := o.WaitForWorkers(waitCtx); err != nil {
		t.Fatalf("WaitForWorkers must join after an owned Init failure, got %v", err)
	}
}
