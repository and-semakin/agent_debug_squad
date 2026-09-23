package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// nestedDef is the smallest two-level nesting used across these recovery tests:
// an inner loop (implement -> review) nested inside an outer loop (exit).
func nestedDef() domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		Version: 1, Name: "nested", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer": {MaxIterations: 2},
			"inner": {MaxIterations: 2, Parent: "outer"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"implement": {Agent: "x", Prompt: "p", Loop: "inner"},
			"review":    {Agent: "x", Prompt: "p", Loop: "inner", Needs: []string{"implement"}},
			"exit":      {Agent: "x", Prompt: "p", Loop: "outer", Needs: []string{"review"}},
		},
	}
}

// validNestedSnapshot builds a schema-3 snapshot that satisfies every recovery
// invariant: outer is at iteration 1, inner at outer=1/inner=1, implement has
// committed its first pass, and review is the single live attempt running at
// the inner loop's current path.
func validNestedSnapshot() domain.WorkflowSnapshot {
	outerPath := []domain.IterationEntry{{Loop: "outer", Iteration: 1}}
	innerPath := []domain.IterationEntry{{Loop: "outer", Iteration: 1}, {Loop: "inner", Iteration: 1}}
	return domain.WorkflowSnapshot{
		SchemaVersion: domain.WorkflowSnapshotNestedSchemaVersion,
		ExecutionID:   "wf_000001",
		Definition:    nestedDef(),
		State:         domain.WorkflowRunning,
		Mode:          domain.WorkflowModeRunning,
		Loops: map[string]*domain.WorkflowLoopExecution{
			"outer": {Iteration: 1, State: domain.WorkflowLoopRunning, IterationPath: outerPath},
			"inner": {Iteration: 1, State: domain.WorkflowLoopRunning, IterationPath: innerPath},
		},
		Tasks: map[string]*domain.WorkflowTaskExecution{
			"implement": {TaskID: "implement", State: domain.WorkflowTaskSucceeded, Attempts: []domain.WorkflowAttempt{
				{Attempt: 1, Iteration: 1, IterationPath: innerPath, State: domain.WorkflowAttemptSucceeded},
			}},
			"review": {TaskID: "review", State: domain.WorkflowTaskRunning, Attempts: []domain.WorkflowAttempt{
				{Attempt: 1, Iteration: 1, IterationPath: innerPath, State: domain.WorkflowAttemptRunning},
			}},
			"exit": {TaskID: "exit", State: domain.WorkflowTaskPending},
		},
	}
}

func mustReject(t *testing.T, snapshot domain.WorkflowSnapshot, want string) {
	t.Helper()
	err := validateWorkflowSnapshot(&snapshot)
	if err == nil {
		t.Fatalf("contradictory snapshot must be rejected before scheduling (want %q)", want)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("rejection reason mismatch: got %q, want substring %q", err, want)
	}
}

// --- P2 #4: contradictory live contexts are rejected before scheduling -------

// TestValidateRejectsChildPathOutsideParent: the current child loop's path must
// extend the current parent path; a child pointing at a different outer
// invocation is contradictory.
func TestValidateRejectsChildPathOutsideParent(t *testing.T) {
	s := validNestedSnapshot()
	s.Loops["inner"].IterationPath = []domain.IterationEntry{{Loop: "outer", Iteration: 2}, {Loop: "inner", Iteration: 1}}
	s.Tasks["review"].Attempts[0].IterationPath = s.Loops["inner"].IterationPath
	mustReject(t, s, "inconsistent with parent")
}

// TestValidateRejectsLiveAttemptOutsideOwnerPath: a running attempt belonging to
// a different inner invocation than the loop's current path is contradictory.
func TestValidateRejectsLiveAttemptOutsideOwnerPath(t *testing.T) {
	s := validNestedSnapshot()
	s.Tasks["review"].Attempts[0].Iteration = 2
	s.Tasks["review"].Attempts[0].IterationPath = []domain.IterationEntry{{Loop: "outer", Iteration: 1}, {Loop: "inner", Iteration: 2}}
	mustReject(t, s, "outside owner")
}

// TestValidateRejectsUnresolvedInterruptionOutsideOwnerPath: the latest
// unresolved interruption must belong to the owner's current path.
func TestValidateRejectsUnresolvedInterruptionOutsideOwnerPath(t *testing.T) {
	s := validNestedSnapshot()
	s.Tasks["review"].Attempts = []domain.WorkflowAttempt{
		{Attempt: 1, Iteration: 1, IterationPath: []domain.IterationEntry{{Loop: "outer", Iteration: 1}, {Loop: "inner", Iteration: 1}}, State: domain.WorkflowAttemptSucceeded},
		{Attempt: 2, Iteration: 2, IterationPath: []domain.IterationEntry{{Loop: "outer", Iteration: 1}, {Loop: "inner", Iteration: 2}}, State: domain.WorkflowAttemptInterrupted},
	}
	mustReject(t, s, "outside owner")
}

// TestValidateRejectsCompetingLiveContexts: one loop cannot have unfinished work
// from two different invocations at once; only one live attempt can match the
// owner's current path, the other is rejected.
func TestValidateRejectsCompetingLiveContexts(t *testing.T) {
	s := validNestedSnapshot()
	s.Tasks["review"].Attempts = []domain.WorkflowAttempt{
		{Attempt: 1, Iteration: 1, IterationPath: []domain.IterationEntry{{Loop: "outer", Iteration: 1}, {Loop: "inner", Iteration: 1}}, State: domain.WorkflowAttemptRunning},
		{Attempt: 2, Iteration: 1, IterationPath: []domain.IterationEntry{{Loop: "outer", Iteration: 2}, {Loop: "inner", Iteration: 1}}, State: domain.WorkflowAttemptRunning},
	}
	mustReject(t, s, "outside owner")
}

// TestValidateRejectsDoneParentWithUnfinishedChild: a done parent loop must not
// contain an unfinished child invocation.
func TestValidateRejectsDoneParentWithUnfinishedChild(t *testing.T) {
	s := validNestedSnapshot()
	s.Loops["outer"].State = domain.WorkflowLoopDone
	mustReject(t, s, "is done but child loop")
}

// TestValidateAcceptsHistoricalInterruptionSupersededAfterAdvance is the
// required positive case: an interruption superseded by an accepted retry stays
// historical, and after the outer loop advances the new live work under the
// fresh ancestor context must still recover cleanly.
func TestValidateAcceptsHistoricalInterruptionSupersededAfterAdvance(t *testing.T) {
	oldInner := []domain.IterationEntry{{Loop: "outer", Iteration: 1}, {Loop: "inner", Iteration: 1}}
	newOuter := []domain.IterationEntry{{Loop: "outer", Iteration: 2}}
	newInner := []domain.IterationEntry{{Loop: "outer", Iteration: 2}, {Loop: "inner", Iteration: 1}}
	s := validNestedSnapshot()
	s.Loops["outer"].Iteration = 2
	s.Loops["outer"].IterationPath = newOuter
	s.Loops["inner"].IterationPath = newInner
	// implement: interrupted at the old context, superseded by an accepted retry
	// that succeeded, then the current live attempt after the outer advanced.
	s.Tasks["implement"].Attempts = []domain.WorkflowAttempt{
		{Attempt: 1, Iteration: 1, IterationPath: oldInner, State: domain.WorkflowAttemptInterrupted},
		{Attempt: 2, Iteration: 1, IterationPath: oldInner, State: domain.WorkflowAttemptSucceeded},
		{Attempt: 3, Iteration: 1, IterationPath: newInner, State: domain.WorkflowAttemptRunning},
	}
	s.Tasks["implement"].Attempts[2].Iteration = 1
	// review's live attempt tracks the advanced inner current path.
	s.Tasks["review"].Attempts[0].IterationPath = newInner
	if err := validateWorkflowSnapshot(&s); err != nil {
		t.Fatalf("historical interruption after an ancestor advance must not block recovery: %v", err)
	}
}

// --- P2 #5: re-saving a recovered schema-1 snapshot upgrades it to schema 2 ---

func TestResavingRecoveredSchema1UpgradesToSchema2(t *testing.T) {
	st := testStore(t)
	dir, err := st.WorkflowDir("wf_000020")
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{"schema_version": 1, "execution_id": "wf_000020", "revision": 2,` +
		` "definition": {"version": 1, "name": "chain", "max_parallel": 1, "task_timeout_seconds": 60,` +
		`  "tasks": {"a": {"agent": "x", "prompt": "p"}}},` +
		` "definition_hash": "hash", "request_id": "req-1", "state": "running", "mode": "running",` +
		` "tasks": {"a": {"task_id": "a", "agent": "x", "state": "pending", "attempts": []}},` +
		` "next_run_seq": 1}`
	if err := os.WriteFile(filepath.Join(dir, workflowSnapshotFile), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := st.LoadWorkflowSnapshot("wf_000020")
	if err != nil {
		t.Fatalf("load schema 1: %v", err)
	}
	if loaded.SchemaVersion != 1 {
		t.Fatalf("load alone must not rewrite the schema, got %d", loaded.SchemaVersion)
	}
	// Re-saving through the normal commit path upgrades the nonnested snapshot.
	if err := st.SaveWorkflowSnapshot(&loaded); err != nil {
		t.Fatalf("resave: %v", err)
	}
	reloaded, err := st.LoadWorkflowSnapshot("wf_000020")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.SchemaVersion != domain.WorkflowSnapshotNonNestedSchemaVersion {
		t.Fatalf("resaved nonnested snapshot must be schema 2, got %d", reloaded.SchemaVersion)
	}
	if reloaded.Loops != nil {
		t.Fatalf("upgraded snapshot must carry no nesting fields: %+v", reloaded.Loops)
	}
}

func TestSavingNestedSnapshotKeepsSchema3(t *testing.T) {
	st := testStore(t)
	snapshot := validNestedSnapshot()
	snapshot.ExecutionID = "wf_000021"
	// Even if a nested snapshot is mislabeled at the nonnested schema, the
	// commit point normalizes it up to the nested schema.
	snapshot.SchemaVersion = domain.WorkflowSnapshotNonNestedSchemaVersion
	if err := st.SaveWorkflowSnapshot(&snapshot); err != nil {
		t.Fatalf("save nested: %v", err)
	}
	reloaded, err := st.LoadWorkflowSnapshot("wf_000021")
	if err != nil {
		t.Fatalf("reload nested: %v", err)
	}
	if reloaded.SchemaVersion != domain.WorkflowSnapshotNestedSchemaVersion {
		t.Fatalf("nested snapshot must persist schema 3, got %d", reloaded.SchemaVersion)
	}
}

// --- review 3 #2: a duplicate loop record in the raw JSON is rejected ---------

// TestLoadRejectsDuplicateLoopRecord writes a schema-3 snapshot whose loops
// object repeats the "inner" key. encoding/json collapses duplicate keys to the
// last value before validation can see them, so this corruption is only
// catchable on the raw bytes; it cannot be expressed through a Go map. Recovery
// must fail closed rather than choose a latest record.
func TestLoadRejectsDuplicateLoopRecord(t *testing.T) {
	st := testStore(t)
	dir, err := st.WorkflowDir("wf_000030")
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := `{
	  "schema_version": 3,
	  "execution_id": "wf_000030",
	  "definition": {"version":1,"name":"nested","max_parallel":1,"task_timeout_seconds":60,
	    "loops":{"outer":{"max_iterations":2},"inner":{"max_iterations":2,"parent":"outer"}},
	    "tasks":{"a":{"agent":"x","prompt":"p","loop":"inner"}}},
	  "state":"running","mode":"running",
	  "loops":{
	    "outer":{"iteration":1,"state":"running","iteration_path":[{"loop":"outer","iteration":1}]},
	    "inner":{"iteration":1,"state":"running","iteration_path":[{"loop":"outer","iteration":1},{"loop":"inner","iteration":1}]},
	    "inner":{"iteration":2,"state":"running","iteration_path":[{"loop":"outer","iteration":1},{"loop":"inner","iteration":2}]}
	  },
	  "tasks":{"a":{"task_id":"a","agent":"x","state":"running"}}
	}`
	if err := os.WriteFile(filepath.Join(dir, workflowSnapshotFile), []byte(raw), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := st.LoadWorkflowSnapshot("wf_000030"); err == nil || !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "inner") {
		t.Fatalf("duplicate loop record must fail recovery, got %v", err)
	}
}

// Malformed values must terminate scanning, including errors nested in arrays.
func TestLoadRejectsMalformedSnapshotJSON(t *testing.T) {
	for _, raw := range []string{
		`{"tasks":[tru]}`,
		`{"tasks":[{"attempts":[tru]}]}`,
		`{"tasks":[{"attempts":}]}`,
		`{"tasks":[`,
		`{"tasks":[{"attempts":[1`,
		`{"tasks":{}} trailing`,
	} {
		t.Run(raw, func(t *testing.T) {
			st := testStore(t)
			dir, err := st.WorkflowDir("wf_000031")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, workflowSnapshotFile), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := st.LoadWorkflowSnapshot("wf_000031"); err == nil {
				t.Fatal("malformed JSON must fail recovery")
			}
		})
	}
}

func TestSnapshotDuplicateKeyScan(t *testing.T) {
	for _, tc := range []struct{ raw, path, key string }{
		{`{"a":1,"\u0061":2}`, "", "a"},
		{`{"tasks":[{"id":1,"id":2}]}`, "tasks", "id"},
		{`{"tasks":[{"id":1},{"id":2}]}`, "", ""},
		{`{"definition":{"loops":{"inner":{},"inner":{}}}}`, "definition.loops", "inner"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			path, key, found, err := firstDuplicateJSONKey([]byte(tc.raw))
			if err != nil || found != (tc.key != "") || path != tc.path || key != tc.key {
				t.Fatalf("got (%q, %q, %v, %v), want (%q, %q)", path, key, found, err, tc.path, tc.key)
			}
		})
	}
}
