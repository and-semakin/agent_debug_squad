package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	cfg := domain.SessionConfig{
		SessionName:  "ownership",
		SessionID:    "session_test",
		WorkspaceDir: t.TempDir(),
		StateDirName: ".agent-debug-squad",
	}
	return New(cfg)
}

func TestSessionOwnershipExcludesCompetingProcess(t *testing.T) {
	if isUnsupportedOwnershipPlatform() {
		t.Skip("exclusive ownership is unix-only in v1")
	}
	st := testStore(t)
	first, err := AcquireSessionOwnership(st.SessionDir())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := AcquireSessionOwnership(st.SessionDir()); err == nil {
		t.Fatal("second acquire must fail while the first owner is live")
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := AcquireSessionOwnership(st.SessionDir())
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

func TestSessionOwnershipReleasedOnProcessExit(t *testing.T) {
	if isUnsupportedOwnershipPlatform() {
		t.Skip("exclusive ownership is unix-only in v1")
	}
	st := testStore(t)
	if os.Getenv("GO_WANT_OWNERSHIP_HELPER") == "1" {
		ownership, err := AcquireSessionOwnership(os.Getenv("AGENT_DEBUG_SQUAD_OWNERSHIP_DIR"))
		if err != nil {
			t.Fatalf("helper acquire: %v", err)
		}
		defer ownership.Release()
		if ready := os.Getenv("AGENT_DEBUG_SQUAD_OWNERSHIP_READY"); ready != "" {
			if err := os.WriteFile(ready, []byte("ready"), 0o644); err != nil {
				t.Fatalf("helper ready signal: %v", err)
			}
		}
		time.Sleep(1 * time.Minute)
		return
	}

	helper := os.Args[0]
	readyPath := filepath.Join(t.TempDir(), "helper-ready")
	proc, err := os.StartProcess(helper, []string{helper, "-test.run", "^TestSessionOwnershipReleasedOnProcessExit$", "-test.v"},
		&os.ProcAttr{Env: append(os.Environ(),
			"GO_WANT_OWNERSHIP_HELPER=1",
			"AGENT_DEBUG_SQUAD_OWNERSHIP_DIR="+st.SessionDir(),
			"AGENT_DEBUG_SQUAD_OWNERSHIP_READY="+readyPath),
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr}})
	if err != nil {
		t.Skipf("cannot spawn helper process: %v", err)
	}
	defer func() { _ = proc.Kill() }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("helper did not signal readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := AcquireSessionOwnership(st.SessionDir()); err == nil {
		t.Fatal("competing owner must not acquire while helper is alive")
	}

	if err := proc.Signal(os.Kill); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill helper: %v", err)
	}
	_, _ = proc.Wait()

	deadline = time.Now().Add(5 * time.Second)
	for {
		ownership, err := AcquireSessionOwnership(st.SessionDir())
		if err == nil {
			_ = ownership.Release()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ownership must be released when the owning process dies: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func isUnsupportedOwnershipPlatform() bool {
	return ownershipUnsupported
}

func TestWorkflowSnapshotRoundTrip(t *testing.T) {
	st := testStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := domain.WorkflowSnapshot{
		SchemaVersion:  domain.WorkflowSnapshotSchemaVersion,
		ExecutionID:    "wf_000001",
		Revision:       3,
		Definition:     domain.WorkflowDefinition{Version: 1, Name: "chain", MaxParallel: 1, TaskTimeoutSeconds: 60, Tasks: map[string]domain.WorkflowTaskDefinition{"a": {Agent: "x", Prompt: "p"}}},
		DefinitionHash: "hash",
		Agents:         map[string]domain.AgentSpec{"x": {Name: "x", Backend: "fake"}},
		RequestID:      "req-1",
		State:          domain.WorkflowRunning,
		Mode:           domain.WorkflowModeRunning,
		Tasks: map[string]*domain.WorkflowTaskExecution{
			"a": {TaskID: "a", Agent: "x", State: domain.WorkflowTaskSucceeded, Attempts: []domain.WorkflowAttempt{{
				Attempt: 1, RunID: "wrun_000001_000001", State: domain.WorkflowAttemptSucceeded,
				ResultPath: "tasks/a/attempts/1/response.txt", ResultSize: 2, ResultSHA256: "aa",
				ReservedAt: &now, DispatchedAt: &now, CompletedAt: &now,
			}}},
		},
		NextRunSeq: 1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := st.SaveWorkflowSnapshot(&snapshot); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := st.LoadWorkflowSnapshot("wf_000001")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Revision != 3 || loaded.State != domain.WorkflowRunning || loaded.NextRunSeq != 1 {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	if loaded.Tasks["a"].Attempts[0].RunID != "wrun_000001_000001" {
		t.Fatalf("attempt mismatch: %+v", loaded.Tasks["a"])
	}

	ids, err := st.ListWorkflowExecutions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 1 || ids[0] != "wf_000001" {
		t.Fatalf("unexpected executions: %v", ids)
	}
	next, err := st.NextWorkflowExecutionID()
	if err != nil {
		t.Fatalf("next id: %v", err)
	}
	if next != "wf_000002" {
		t.Fatalf("next execution id: %s", next)
	}
}

func TestLoadWorkflowSnapshotRejectsUnsupportedVersion(t *testing.T) {
	st := testStore(t)
	dir, err := st.WorkflowDir("wf_000009")
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	corrupt := `{"schema_version": 99, "execution_id": "wf_000009"}`
	if err := os.WriteFile(filepath.Join(dir, workflowSnapshotFile), []byte(corrupt), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := st.LoadWorkflowSnapshot("wf_000009"); err == nil || !strings.Contains(err.Error(), "unsupported schema version") {
		t.Fatalf("unsupported version must fail closed, got %v", err)
	}
}

func TestLoadWorkflowSnapshotAcceptsSchema1(t *testing.T) {
	st := testStore(t)
	dir, err := st.WorkflowDir("wf_000008")
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-loops schema-1 snapshot: loopless, must load unchanged.
	legacy := `{"schema_version": 1, "execution_id": "wf_000008", "revision": 2,` +
		` "definition": {"version": 1, "name": "chain", "max_parallel": 1, "task_timeout_seconds": 60,` +
		`  "tasks": {"a": {"agent": "x", "prompt": "p"}}},` +
		` "definition_hash": "hash", "request_id": "req-1", "state": "running", "mode": "running",` +
		` "tasks": {"a": {"task_id": "a", "agent": "x", "state": "pending", "attempts": []}},` +
		` "next_run_seq": 1}`
	if err := os.WriteFile(filepath.Join(dir, workflowSnapshotFile), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := st.LoadWorkflowSnapshot("wf_000008")
	if err != nil {
		t.Fatalf("schema 1 must load upgrade-only, got %v", err)
	}
	if loaded.SchemaVersion != 1 || loaded.Loops != nil || loaded.Definition.Loops != nil {
		t.Fatalf("schema-1 load mismatch: %+v", loaded)
	}
	if loaded.Definition.Tasks["a"].Loop != "" {
		t.Fatalf("legacy task must load loopless: %+v", loaded.Definition.Tasks["a"])
	}

	// Freshly written snapshots carry the current schema and keep loop state.
	snapshot := domain.WorkflowSnapshot{
		SchemaVersion:  domain.WorkflowSnapshotSchemaVersion,
		ExecutionID:    "wf_000007",
		Definition:     domain.WorkflowDefinition{Version: 1, Name: "loop", MaxParallel: 1, TaskTimeoutSeconds: 60, Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 3}}, Tasks: map[string]domain.WorkflowTaskDefinition{"a": {Agent: "x", Prompt: "p", Loop: "refine"}}},
		State:          domain.WorkflowRunning,
		Mode:           domain.WorkflowModeRunning,
		Tasks:          map[string]*domain.WorkflowTaskExecution{"a": {TaskID: "a", Agent: "x", State: domain.WorkflowTaskSucceeded, Attempts: []domain.WorkflowAttempt{{Attempt: 1, Iteration: 2, State: domain.WorkflowAttemptSucceeded}}}},
		Loops:          map[string]*domain.WorkflowLoopExecution{"refine": {Iteration: 2, State: domain.WorkflowLoopRunning}},
		DefinitionHash: "hash",
	}
	if err := st.SaveWorkflowSnapshot(&snapshot); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := st.LoadWorkflowSnapshot("wf_000007")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.SchemaVersion != domain.WorkflowSnapshotSchemaVersion {
		t.Fatalf("new snapshots must persist the current schema, got %d", reloaded.SchemaVersion)
	}
	if reloaded.Loops["refine"] == nil || reloaded.Loops["refine"].Iteration != 2 || reloaded.Loops["refine"].State != domain.WorkflowLoopRunning {
		t.Fatalf("loop execution must round-trip: %+v", reloaded.Loops)
	}
	if reloaded.Tasks["a"].Attempts[0].Iteration != 2 {
		t.Fatalf("attempt iteration must round-trip: %+v", reloaded.Tasks["a"].Attempts[0])
	}
}

func TestLoadWorkflowSnapshotRejectsCorruption(t *testing.T) {
	st := testStore(t)
	dir, err := st.WorkflowDir("wf_000010")
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, workflowSnapshotFile), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := st.LoadWorkflowSnapshot("wf_000010"); err == nil {
		t.Fatal("corrupt snapshot must fail closed")
	}
}

func TestSaveWorkflowSnapshotInjectedFailures(t *testing.T) {
	st := testStore(t)
	snapshot := domain.WorkflowSnapshot{
		SchemaVersion: domain.WorkflowSnapshotSchemaVersion,
		ExecutionID:   "wf_000011",
		Definition:    domain.WorkflowDefinition{Version: 1, Name: "n", MaxParallel: 1, TaskTimeoutSeconds: 1, Tasks: map[string]domain.WorkflowTaskDefinition{"a": {Agent: "x", Prompt: "p"}}},
		State:         domain.WorkflowRunning,
		Mode:          domain.WorkflowModeRunning,
		Tasks:         map[string]*domain.WorkflowTaskExecution{},
	}
	for _, stage := range []string{"write", "sync", "rename", "dirsync"} {
		t.Run(stage, func(t *testing.T) {
			restore := atomicWriteHook
			atomicWriteHook = func(s string) error {
				if s == stage {
					return errors.New("injected " + stage + " failure")
				}
				return nil
			}
			defer func() { atomicWriteHook = restore }()
			if err := st.SaveWorkflowSnapshot(&snapshot); err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("stage %s must fail the save, got %v", stage, err)
			}
		})
	}
}

func TestWorkflowAttemptArtifactsAndVerification(t *testing.T) {
	st := testStore(t)
	executionID := "wf_000012"
	promptPath, manifestPath, err := st.WriteWorkflowAttemptInput(executionID, "verify", 2, []byte("exact prompt"), []byte(`{"dependencies":[]}`))
	if err != nil {
		t.Fatalf("write input: %v", err)
	}
	if want := "tasks/verify/attempts/2/prompt.txt"; filepath.ToSlash(promptPath) != want {
		t.Fatalf("prompt path: %s", promptPath)
	}
	if want := "tasks/verify/attempts/2/input-manifest.json"; filepath.ToSlash(manifestPath) != want {
		t.Fatalf("manifest path: %s", manifestPath)
	}
	prompt, err := st.ReadWorkflowArtifact(executionID, promptPath)
	if err != nil || string(prompt) != "exact prompt" {
		t.Fatalf("prompt read back: %q %v", prompt, err)
	}

	resultPath, size, sha, err := st.WriteWorkflowResponse(executionID, "verify", 2, []byte("final response"))
	if err != nil {
		t.Fatalf("write response: %v", err)
	}
	if size != int64(len("final response")) || sha == "" {
		t.Fatalf("size/hash mismatch: %d %s", size, sha)
	}
	if err := st.VerifyWorkflowArtifact(executionID, resultPath, size, sha); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := st.VerifyWorkflowArtifact(executionID, resultPath, size+1, sha); err == nil {
		t.Fatal("changed size must fail verification")
	}
	if err := st.VerifyWorkflowArtifact(executionID, resultPath, size, "0000"); err == nil {
		t.Fatal("changed hash must fail verification")
	}

	absolute, err := st.resolveWorkflowArtifact(executionID, resultPath)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := os.Remove(absolute); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := st.VerifyWorkflowArtifact(executionID, resultPath, size, sha); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing artifact must fail verification, got %v", err)
	}
}

func TestWorkflowArtifactRejectsEscapingPaths(t *testing.T) {
	st := testStore(t)
	for _, path := range []string{"../../secrets", "/etc/passwd", ""} {
		if _, err := st.ReadWorkflowArtifact("wf_000001", path); err == nil {
			t.Fatalf("path %q must be rejected", path)
		}
	}
}

func TestAppendWorkflowEventWritesJSONL(t *testing.T) {
	st := testStore(t)
	event := domain.WorkflowControlEvent{Type: "pause", At: time.Now().UTC()}
	if err := st.AppendWorkflowEvent("wf_000013", event); err != nil {
		t.Fatalf("append: %v", err)
	}
	dir, err := st.WorkflowDir("wf_000013")
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `"type":"pause"`) {
		t.Fatalf("event not persisted: %s", data)
	}
}

func TestWorkflowAttemptStatePathIsolated(t *testing.T) {
	st := testStore(t)
	path, err := st.WorkflowAttemptStatePath("wf_000014", "review_a", 3)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if want := "tasks/review_a/attempts/3/agent-state.json"; !strings.HasSuffix(filepath.ToSlash(path), want) {
		t.Fatalf("state path: %s", path)
	}
}
