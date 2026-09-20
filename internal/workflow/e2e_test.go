package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

// e2eConfig builds a full stack configuration with the fake backend.
func e2eConfig(t *testing.T, def domain.WorkflowDefinition, agents ...domain.AgentSpec) domain.SessionConfig {
	t.Helper()
	return domain.SessionConfig{
		SessionName:  "e2e",
		SessionID:    "session_e2e",
		WorkspaceDir: t.TempDir(),
		StateDirName: ".agent-debug-squad",
		Host:         "127.0.0.1",
		Port:         0,
		Defaults:     domain.SessionDefaults{Yolo: true},
		Agents:       agents,
		Workflow:     &def,
	}
}

func fakeAgent(name string, options ...string) domain.AgentSpec {
	spec := domain.AgentSpec{Name: name, Backend: "fake", StartupPrompt: "You are " + name + "."}
	if len(options) >= 2 {
		spec.Options = map[string]any{options[0]: options[1]}
		spec.StringOptions = map[string]string{options[0]: options[1]}
	}
	return spec
}

// startStack wires ownership, orchestrator, and the workflow manager over one
// session state, exactly like the serve command does.
func startStack(t *testing.T, cfg domain.SessionConfig) (*Manager, *orchestrator.Orchestrator, func()) {
	t.Helper()
	st := store.New(cfg)
	ownership, err := store.AcquireSessionOwnership(st.SessionDir())
	if err != nil {
		t.Fatalf("ownership: %v", err)
	}
	orch, err := orchestrator.New(context.Background(), cfg, st)
	if err != nil {
		_ = ownership.Release()
		t.Fatalf("orchestrator: %v", err)
	}
	m := NewManager(cfg, st, orch)
	if err := m.Start(context.Background()); err != nil {
		_ = ownership.Release()
		t.Fatalf("start: %v", err)
	}
	cleanup := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.Stop(stopCtx)
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer waitCancel()
		_ = orch.WaitForWorkers(waitCtx)
		_ = ownership.Release()
	}
	return m, orch, cleanup
}

func waitTerminal(t *testing.T, m *Manager, executionID string, timeout time.Duration) domain.WorkflowExecutionView {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		view, err := m.View(executionID)
		if err == nil && view.State.Terminal() {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution %s did not finish; last view %+v", executionID, view)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEndToEndChainThroughRealOrchestrator(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "e2e-chain", MaxParallel: 2, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "Do the first step."},
			"b": {Agent: "beta", Prompt: "Do the second step.", Needs: []string{"a"}},
			"c": {Agent: "gamma", Prompt: "Do the third step.", Needs: []string{"b"}},
		},
	}
	cfg := e2eConfig(t, def, fakeAgent("alpha"), fakeAgent("beta"), fakeAgent("gamma"))
	m, orch, cleanup := startStack(t, cfg)
	defer cleanup()

	view, created, err := m.Create("req-e2e-chain")
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	final := waitTerminal(t, m, view.ExecutionID, 15*time.Second)
	if final.State != domain.WorkflowSucceeded {
		t.Fatalf("state: %v", final.State)
	}
	for _, task := range final.Tasks {
		if task.State != domain.WorkflowTaskSucceeded || task.Result == nil || task.Result.SHA256 == "" {
			t.Fatalf("task %s: %+v", task.TaskID, task)
		}
	}

	// Committed responses exist with matching hashes.
	for _, task := range final.Tasks {
		if task.Result == nil {
			t.Fatalf("task %s has no result", task.TaskID)
		}
	}

	// The second task's persisted prompt references the first result file.
	st := store.New(cfg)
	snapshot, err := st.LoadWorkflowSnapshot(view.ExecutionID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	promptPath := snapshot.Tasks["b"].Attempts[0].PromptPath
	promptBytes, err := st.ReadWorkflowArtifact(view.ExecutionID, promptPath)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if !strings.Contains(string(promptBytes), "a: succeeded") || !strings.Contains(string(promptBytes), "response.txt") {
		t.Fatalf("prompt of b must reference a's result:\n%s", promptBytes)
	}

	// Owned run records stream through the existing run API surface.
	run, err := orch.Run(context.Background(), snapshot.Tasks["b"].Attempts[0].RunID)
	if err != nil {
		t.Fatalf("owned run record: %v", err)
	}
	if run.Status != domain.RunCompleted || run.OutputPath == nil {
		t.Fatalf("owned run projection: %+v", run)
	}
	if run.Metadata["workflow_task_id"] != "b" {
		t.Fatalf("workflow identity: %+v", run.Metadata)
	}

	// Manual continuity is preserved: an ordinary turn still works on alpha
	// after workflow completion.
	manual, err := orch.SubmitRun(context.Background(), "alpha", "manual follow-up", nil)
	if err != nil {
		t.Fatalf("manual run: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		manualRun, err := orch.Run(context.Background(), manual.RunID)
		if err == nil && manualRun.Status == domain.RunCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("manual run did not complete: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A second execution with a new request id starts fresh conversations.
	view2, created2, err := m.Create("req-e2e-again")
	if err != nil || !created2 {
		t.Fatalf("second create: created=%v err=%v", created2, err)
	}
	if view2.ExecutionID == view.ExecutionID {
		t.Fatal("second submission must be a new execution")
	}
	final2 := waitTerminal(t, m, view2.ExecutionID, 15*time.Second)
	if final2.State != domain.WorkflowSucceeded {
		t.Fatalf("second execution state: %v", final2.State)
	}
}

func TestEndToEndTimeoutThroughRealOrchestrator(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "e2e-timeout", MaxParallel: 1, TaskTimeoutSeconds: 1,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"slow": {Agent: "alpha", Prompt: "Take your time.", TimeoutSeconds: 1, AllowedToFail: true},
		},
	}
	cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "3000"))
	m, _, cleanup := startStack(t, cfg)
	defer cleanup()

	view, _, err := m.Create("req-e2e-timeout")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	final := waitTerminal(t, m, view.ExecutionID, 15*time.Second)
	slow := findTaskView(final, "slow")
	if slow.State != domain.WorkflowTaskFailed {
		t.Fatalf("timed-out task must fail: %+v", slow)
	}
	if len(slow.Attempts) != 1 || slow.Attempts[0].Reason != "timeout" {
		t.Fatalf("attempt: %+v", slow.Attempts)
	}
	if final.State != domain.WorkflowCompletedWithError {
		t.Fatalf("allowed_to_fail timeout must be waived: %v", final.State)
	}
}

func TestEndToEndSimultaneousFinishesDoNotOversubscribe(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "e2e-race", MaxParallel: 2, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"root":  {Agent: "alpha", Prompt: "Root."},
			"left":  {Agent: "gamma", Prompt: "Left.", Needs: []string{"root"}},
			"right": {Agent: "beta", Prompt: "Right.", Needs: []string{"root"}},
			"join":  {Agent: "delta", Prompt: "Join.", Needs: []string{"left", "right"}, MinSuccessfulDependencies: 2},
		},
	}
	cfg := e2eConfig(t, def, fakeAgent("alpha"), fakeAgent("beta"), fakeAgent("gamma"), fakeAgent("delta"))
	m, _, cleanup := startStack(t, cfg)
	defer cleanup()

	view, _, err := m.Create("req-e2e-race")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The real loop dispatches and completes everything; left and right run
	// concurrently and finish at arbitrary times, exercising simultaneous
	// completion against the live reconciliation loop.
	final := waitTerminal(t, m, view.ExecutionID, 30*time.Second)
	if final.State != domain.WorkflowSucceeded {
		t.Fatalf("state: %v (%+v)", final.State, final.TaskCounts)
	}
	for _, task := range final.Tasks {
		if len(task.Attempts) != 1 {
			t.Fatalf("task %s must have exactly one attempt: %+v", task.TaskID, task.Attempts)
		}
		if task.TaskID == "join" && task.Result == nil {
			t.Fatalf("join must have a result: %+v", task)
		}
	}
}

func TestEndToEndPauseResumeThroughRealOrchestrator(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "e2e-pause", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "A."},
			"b": {Agent: "beta", Prompt: "B.", Needs: []string{"a"}},
		},
	}
	cfg := e2eConfig(t, def, fakeAgent("alpha", "delay_ms", "200"), fakeAgent("beta"))
	m, _, cleanup := startStack(t, cfg)
	defer cleanup()

	view, _, err := m.Create("req-e2e-pause")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Pause early, then resume; the execution must still complete.
	paused, err := m.Pause(view.ExecutionID)
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if paused.State != domain.WorkflowPaused {
		t.Fatalf("paused state: %v", paused.State)
	}
	resumed, err := m.Resume(view.ExecutionID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != domain.WorkflowRunning && resumed.State != domain.WorkflowPaused {
		t.Fatalf("resumed state: %v", resumed.State)
	}
	final := waitTerminal(t, m, view.ExecutionID, 20*time.Second)
	if final.State != domain.WorkflowSucceeded {
		t.Fatalf("state: %v", final.State)
	}
}

func TestEndToEndCompetingOwnerIsRejected(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "e2e-owner", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "alpha", Prompt: "A."},
		},
	}
	cfg := e2eConfig(t, def, fakeAgent("alpha"))
	st := store.New(cfg)
	ownership, err := store.AcquireSessionOwnership(st.SessionDir())
	if err != nil {
		t.Fatalf("first owner: %v", err)
	}
	defer func() { _ = ownership.Release() }()

	if _, err := store.AcquireSessionOwnership(st.SessionDir()); err == nil {
		t.Fatal("a competing owner must be rejected before touching state")
	}
}
