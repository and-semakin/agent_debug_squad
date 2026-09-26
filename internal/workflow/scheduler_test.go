package workflow

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

// fakeExecutor records dispatches and lets tests release worker-stopped
// outcomes deterministically.
type fakeExecutor struct {
	mu          sync.Mutex
	opts        map[string]domain.OwnedRunOptions
	order       []string
	cancels     []string
	submitErr   map[string]error
	completed   map[string]bool
	runs        map[string]domain.RunRecord
	preflights  [][]string
	brokenAgent string
}

func newFakeExecutor() *fakeExecutor {
	return &fakeExecutor{
		opts:      map[string]domain.OwnedRunOptions{},
		submitErr: map[string]error{},
		completed: map[string]bool{},
		runs:      map[string]domain.RunRecord{},
	}
}

func (f *fakeExecutor) SubmitOwnedRun(_ context.Context, opts domain.OwnedRunOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.submitErr[opts.Agent]; err != nil {
		return err
	}
	f.opts[opts.RunID] = opts
	f.order = append(f.order, opts.RunID)
	return nil
}

// PreflightAgents records the gated agent sets; fake installations always
// pass unless a test pins a broken agent.
func (f *fakeExecutor) PreflightAgents(_ context.Context, names []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.preflights = append(f.preflights, append([]string(nil), names...))
	for _, name := range names {
		if name == f.brokenAgent {
			return &fakePreflightFailure{agent: name}
		}
	}
	return nil
}

// fakePreflightFailure is the typed rejection the manager publishes as
// backend_preflight diagnostics.
type fakePreflightFailure struct {
	agent string
}

func (e *fakePreflightFailure) Error() string {
	return "installation for " + e.agent + " is missing"
}

func (f *fakeExecutor) CancelOwnedRun(runID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.opts[runID]; !ok {
		return false
	}
	f.cancels = append(f.cancels, runID)
	return true
}

func (f *fakeExecutor) OwnedRunActive(runID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, registered := f.opts[runID]
	return registered && !f.completed[runID]
}

// Run implements RunObserver for permission observation tests.
func (f *fakeExecutor) Run(_ context.Context, runID string) (domain.RunRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.runs[runID]
	if !ok {
		return domain.RunRecord{}, fmt.Errorf("run %s not found", runID)
	}
	return run, nil
}

func (f *fakeExecutor) release(runID string, outcome domain.OwnedRunOutcome) {
	f.mu.Lock()
	opts, ok := f.opts[runID]
	if ok {
		delete(f.opts, runID)
		f.completed[runID] = true
	}
	f.mu.Unlock()
	if ok {
		opts.OnDone(outcome)
	}
}

func (f *fakeExecutor) releaseSuccess(runID, message string) {
	f.release(runID, domain.OwnedRunOutcome{RunID: runID, Status: domain.RunCompleted, FinalMessage: message})
}

func (f *fakeExecutor) releaseFailure(runID, message string) {
	f.release(runID, domain.OwnedRunOutcome{RunID: runID, Status: domain.RunFailed, Error: message})
}

func (f *fakeExecutor) liveRunIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.opts))
	for runID := range f.opts {
		ids = append(ids, runID)
	}
	return ids
}

func (f *fakeExecutor) dispatched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

func (f *fakeExecutor) wasCancelled(runID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.cancels {
		if id == runID {
			return true
		}
	}
	return false
}

func (f *fakeExecutor) options(runID string) (domain.OwnedRunOptions, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	opts, ok := f.opts[runID]
	return opts, ok
}

// recordingStore wraps the real store with counters and injectable faults.
type recordingStore struct {
	*store.Store
	mu               sync.Mutex
	saveErr          error
	verifyErr        error
	writeResponseErr error
	writeInputErr    error
	saves            int
}

func newRecordingStore(t *testing.T) *recordingStore {
	t.Helper()
	cfg := domain.SessionConfig{
		SessionName:  "workflow-test",
		SessionID:    "session_wftest",
		WorkspaceDir: t.TempDir(),
		StateDirName: ".agent-debug-squad",
	}
	return &recordingStore{Store: store.New(cfg)}
}

func (r *recordingStore) SaveWorkflowSnapshot(snapshot *domain.WorkflowSnapshot) error {
	r.mu.Lock()
	r.saves++
	err := r.saveErr
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return r.Store.SaveWorkflowSnapshot(snapshot)
}

func (r *recordingStore) VerifyWorkflowArtifact(executionID, relativePath string, size int64, sha256hex string) error {
	r.mu.Lock()
	err := r.verifyErr
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return r.Store.VerifyWorkflowArtifact(executionID, relativePath, size, sha256hex)
}

func (r *recordingStore) WriteWorkflowResponse(executionID, taskID string, attempt int, content []byte) (string, int64, string, error) {
	r.mu.Lock()
	err := r.writeResponseErr
	r.mu.Unlock()
	if err != nil {
		return "", 0, "", err
	}
	return r.Store.WriteWorkflowResponse(executionID, taskID, attempt, content)
}

func (r *recordingStore) WriteWorkflowAttemptInput(executionID, taskID string, attempt int, prompt, manifest []byte) (string, string, error) {
	r.mu.Lock()
	err := r.writeInputErr
	r.mu.Unlock()
	if err != nil {
		return "", "", err
	}
	return r.Store.WriteWorkflowAttemptInput(executionID, taskID, attempt, prompt, manifest)
}

func (r *recordingStore) saveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saves
}

type managerFixture struct {
	m    *Manager
	exec *fakeExecutor
	st   *recordingStore
}

func testWorkflowConfig(def domain.WorkflowDefinition, agentNames ...string) domain.SessionConfig {
	agents := make([]domain.AgentSpec, 0, len(agentNames))
	for _, name := range agentNames {
		agents = append(agents, domain.AgentSpec{Name: name, Backend: "fake", StartupPrompt: "You are " + name})
	}
	return domain.SessionConfig{
		SessionName:  "workflow-test",
		SessionID:    "session_wftest",
		WorkspaceDir: "/tmp/agent-debug-squad-workflow-test",
		StateDirName: ".agent-debug-squad",
		Host:         "127.0.0.1",
		Port:         8080,
		Defaults:     domain.SessionDefaults{Yolo: true},
		Agents:       agents,
		Workflow:     &def,
	}
}

func newManagerFixture(t *testing.T, def domain.WorkflowDefinition, agentNames ...string) *managerFixture {
	t.Helper()
	st := newRecordingStore(t)
	cfg := testWorkflowConfig(def, agentNames...)
	cfg.WorkspaceDir = t.TempDir()
	exec := newFakeExecutor()
	m := NewManager(cfg, st, exec)
	m.cancelGrace = 50 * time.Millisecond
	return &managerFixture{m: m, exec: exec, st: st}
}

// restart simulates a process restart over the same session state: a fresh
// manager and executor sharing the persisted store.
func (fx *managerFixture) restart() *managerFixture {
	exec := newFakeExecutor()
	m := NewManager(fx.m.cfg, fx.st, exec)
	m.cancelGrace = fx.m.cancelGrace
	return &managerFixture{m: m, exec: exec, st: fx.st}
}

// pump drains pending completions and runs one deterministic reconciliation
// pass, replacing the asynchronous loop.
// awaitRecoveryPreflight waits for the asynchronous recovery installation
// pass to settle so the deterministic pump observes its outcome.
func (fx *managerFixture) awaitRecoveryPreflight() {
	for i := 0; i < 5000; i++ {
		fx.m.mu.Lock()
		checking := fx.m.recoveryChecking
		fx.m.mu.Unlock()
		if !checking {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (fx *managerFixture) pump() {
	// The asynchronous recovery installation pass must settle before the
	// deterministic reconciliation pass observes its outcome.
	fx.awaitRecoveryPreflight()
	for {
		select {
		case c := <-fx.m.completions:
			fx.m.handleCompletion(c)
			continue
		default:
		}
		fx.m.reconcile()
		select {
		case c := <-fx.m.completions:
			fx.m.handleCompletion(c)
			continue
		default:
			return
		}
	}
}

func chainDefinition() domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		Version:            1,
		Name:               "chain",
		MaxParallel:        1,
		TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "Do A."},
			"b": {Agent: "a2", Prompt: "Do B.", Needs: []string{"a"}},
			"c": {Agent: "a3", Prompt: "Do C.", Needs: []string{"b"}},
		},
	}
}

func TestChainExecutesWithoutCoordinatorTurns(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-chain"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	released := map[string]string{}
	for i := 0; i < 3; i++ {
		live := fx.exec.liveRunIDs()
		if len(live) != 1 {
			t.Fatalf("step %d: want exactly one live attempt, got %v", i, live)
		}
		runID := live[0]
		fx.exec.releaseSuccess(runID, "result of "+runID)
		released[runID] = "done"
		fx.pump()
	}

	view, err := fx.m.View("wf_000001")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v", view.State)
	}
	if view.TaskCounts.Succeeded != 3 {
		t.Fatalf("task counts: %+v", view.TaskCounts)
	}
	if len(fx.exec.dispatched()) != 3 {
		t.Fatalf("dispatch order: %v", fx.exec.dispatched())
	}
}

func TestDiamondFanOutFanInAndManifestOrder(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "diamond", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "Root."},
			"b": {Agent: "a2", Prompt: "B.", Needs: []string{"a"}},
			"c": {Agent: "a3", Prompt: "C.", Needs: []string{"a"}},
			"d": {Agent: "a4", Prompt: "D.", Needs: []string{"b", "c"}},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2", "a3", "a4")
	if _, _, err := fx.m.Create(context.Background(), "req-diamond"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	fx.exec.releaseSuccess("wrun_000001_000001", "root result")
	fx.pump()
	if got := len(fx.exec.liveRunIDs()); got != 2 {
		t.Fatalf("fan-out must run b and c concurrently, live=%v", fx.exec.liveRunIDs())
	}

	// Reverse completion order: c settles first, b later.
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "c"), "c result")
	fx.pump()
	if got := len(fx.exec.liveRunIDs()); got != 1 {
		t.Fatalf("d must wait for b, live=%v", fx.exec.liveRunIDs())
	}
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx, "b"), "b result")
	fx.pump()

	dRun := mustFindRunForTask(t, fx, "d")
	opts, _ := fx.exec.options(dRun)
	if !containsSubstrings(opts.Message, "b: succeeded", "c: succeeded") {
		t.Fatalf("d prompt must reference both results in order:\n%s", opts.Message)
	}
	bIdx, cIdx := indexOf(opts.Message, "b: succeeded"), indexOf(opts.Message, "c: succeeded")
	if bIdx > cIdx {
		t.Fatalf("manifest must be in sorted task order (b before c):\n%s", opts.Message)
	}
	if !containsSubstrings(opts.Message, "response.txt") {
		t.Fatalf("d prompt must reference result files:\n%s", opts.Message)
	}

	fx.exec.releaseSuccess(dRun, "d result")
	fx.pump()
	view, _ := fx.m.View("wf_000001")
	if view.State != domain.WorkflowSucceeded {
		t.Fatalf("final state: %v", view.State)
	}

	// The persisted manifest keeps the same ordered identities.
	snapshot, err := fx.st.LoadWorkflowSnapshot("wf_000001")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	manifestPath := snapshot.Tasks["d"].Attempts[0].ManifestPath
	manifestBytes, err := fx.st.ReadWorkflowArtifact("wf_000001", manifestPath)
	if err != nil {
		t.Fatalf("manifest read: %v", err)
	}
	if !containsSubstrings(string(manifestBytes), `"task_id": "b"`, `"task_id": "c"`) {
		t.Fatalf("manifest content:\n%s", manifestBytes)
	}
}

func mustFindRunForTask(t *testing.T, fx *managerFixture, taskID string) string {
	t.Helper()
	view, err := fx.m.View("wf_000001")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	for _, task := range view.Tasks {
		if task.TaskID != taskID {
			continue
		}
		for _, attempt := range task.Attempts {
			if attempt.RunID != "" && fx.exec.OwnedRunActive(attempt.RunID) {
				return attempt.RunID
			}
		}
	}
	t.Fatalf("no live run for task %s", taskID)
	return ""
}

func containsSubstrings(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if indexOf(haystack, needle) < 0 {
			return false
		}
	}
	return true
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestMaxParallelSlotsCountPermissionWaits(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "slots", MaxParallel: 2, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"x": {Agent: "a1", Prompt: "X."},
			"y": {Agent: "a2", Prompt: "Y."},
			"z": {Agent: "a3", Prompt: "Z."},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-slots"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	live := fx.exec.liveRunIDs()
	if len(live) != 2 {
		t.Fatalf("two slots must be occupied, live=%v", live)
	}
	// Leave both attempts waiting (for example on permissions).
	fx.pump()
	if got := len(fx.exec.liveRunIDs()); got != 2 {
		t.Fatalf("permission waits must hold slots, live=%v", fx.exec.liveRunIDs())
	}

	fx.exec.releaseSuccess(live[0], "first")
	fx.pump()
	if got := len(fx.exec.liveRunIDs()); got != 2 {
		t.Fatalf("a freed slot must dispatch z, live=%v", fx.exec.liveRunIDs())
	}
}

func TestReadyTieBreakIsLexicographic(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "ties", MaxParallel: 1, TaskTimeoutSeconds: 300,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"c": {Agent: "a1", Prompt: "C."},
			"a": {Agent: "a2", Prompt: "A."},
			"b": {Agent: "a3", Prompt: "B."},
		},
	}
	fx := newManagerFixture(t, def, "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-ties"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.pump()

	view, _ := fx.m.View("wf_000001")
	runID := ""
	for _, task := range view.Tasks {
		if task.TaskID == "a" && len(task.Attempts) == 1 {
			runID = task.Attempts[0].RunID
		}
	}
	if runID == "" {
		t.Fatalf("task a must be dispatched first, tasks=%+v", view.Tasks)
	}
}

func TestDuplicateAndLostWakeUpsAreHarmless(t *testing.T) {
	fx := newManagerFixture(t, chainDefinition(), "a1", "a2", "a3")
	if _, _, err := fx.m.Create(context.Background(), "req-wake"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fx.m.Notify()
	fx.m.Notify()
	fx.pump()
	fx.pump()

	live := fx.exec.liveRunIDs()
	if len(live) != 1 {
		t.Fatalf("duplicate wake-ups must not duplicate dispatch, live=%v", live)
	}
	fx.exec.releaseSuccess(live[0], "a done")

	// The completion is durably saved, but its scheduler wake-up is lost:
	// only the periodic reconciliation runs afterwards.
	c := <-fx.m.completions
	fx.m.handleCompletion(c)
	fx.m.reconcile()
	if got := len(fx.exec.liveRunIDs()); got != 1 {
		t.Fatalf("lost wake-up must be recovered by periodic reconcile, live=%v", fx.exec.liveRunIDs())
	}
}
