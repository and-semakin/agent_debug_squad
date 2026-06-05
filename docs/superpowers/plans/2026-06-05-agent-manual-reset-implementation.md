# Agent Manual Reset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manual REST API operation that resets one configured agent's backend session, with safe default behavior and explicit force cancellation for stuck runs.

**Architecture:** Extend the adapter boundary with an intentional `Reset` operation, then add orchestrator lifecycle support for per-run cancellation and reset serialization. The API remains thin: parse `force=true`, call `ResetAgent`, and map domain errors to HTTP responses.

**Tech Stack:** Go standard library HTTP server, context cancellation, file-backed store, existing fake/codex/kimi/opencode adapters, `go test`.

---

## File Structure

- Modify `internal/adapters/adapter.go`: extend `AgentAdapter` with `Reset`.
- Modify `internal/adapters/fake/fake.go`: implement reset and configurable fake delay for deterministic cancellation tests.
- Modify `internal/adapters/codex/codex.go`: implement reset by clearing logical session continuity.
- Modify `internal/adapters/kimi/kimi.go`: implement reset by clearing logical session continuity.
- Modify `internal/adapters/opencode/opencode.go`: implement reset by creating a fresh OpenCode HTTP session.
- Modify adapter tests in `internal/adapters/*/*_test.go`: verify backend-specific reset behavior.
- Modify `internal/orchestrator/orchestrator.go`: add reset errors, runtime reset state, per-run cancel, forced interruption handling, transcript event.
- Modify `internal/orchestrator/orchestrator_test.go`: cover idle reset, busy conflict, force reset interruption, and root cancellation regression.
- Modify `internal/api/server.go`: add `POST /agents/{name}/reset` route and error mapping.
- Modify `internal/api/server_test.go`: cover API success, conflict, force reset, and not found.
- Modify `README.md`: document the new endpoint and behavior.

## Task 1: Adapter Reset Contract

**Files:**
- Modify: `internal/adapters/adapter.go`
- Modify: `internal/adapters/fake/fake.go`
- Modify: `internal/adapters/codex/codex.go`
- Modify: `internal/adapters/kimi/kimi.go`
- Modify: `internal/adapters/opencode/opencode.go`
- Test: `internal/adapters/codex/codex_test.go`
- Test: `internal/adapters/kimi/kimi_test.go`
- Test: `internal/adapters/opencode/opencode_test.go`

- [ ] **Step 1: Extend the adapter interface**

In `internal/adapters/adapter.go`, change the interface to:

```go
type AgentAdapter interface {
	Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
	Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error)
	Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error)
	Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
}
```

- [ ] **Step 2: Add focused Codex reset test**

Append this test to `internal/adapters/codex/codex_test.go`:

```go
func TestResetClearsSessionContinuity(t *testing.T) {
	spec := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "codex",
		StartupPrompt: "Review carefully.",
		StringOptions: map[string]string{"model": "openai/gpt-5"},
	}
	errText := "old error"
	state := domain.AgentState{
		Name:             "Reviewer",
		Backend:          "codex",
		Model:            "old-model",
		StartupPrompt:    "Review carefully.",
		WorkspaceDir:     t.TempDir(),
		BackendSessionID: "thread_old",
		Status:           domain.AgentFailed,
		LastRunID:        "run_000123",
		LastError:        &errText,
	}

	reset, err := New(spec).Reset(context.Background(), spec, state)
	if err != nil {
		t.Fatal(err)
	}
	if reset.BackendSessionID != "" {
		t.Fatalf("BackendSessionID = %q, want empty", reset.BackendSessionID)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}
	if reset.LastError != nil {
		t.Fatalf("LastError = %v, want nil", reset.LastError)
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
	if reset.Model != "openai/gpt-5" {
		t.Fatalf("Model = %q, want openai/gpt-5", reset.Model)
	}
}
```

- [ ] **Step 3: Add focused Kimi reset test**

Append this test to `internal/adapters/kimi/kimi_test.go`:

```go
func TestResetClearsSessionContinuity(t *testing.T) {
	spec := domain.AgentSpec{
		Name:          "Implementer",
		Backend:       "kimi",
		StartupPrompt: "Implement carefully.",
	}
	errText := "old error"
	state := domain.AgentState{
		Name:             "Implementer",
		Backend:          "kimi",
		StartupPrompt:    "Implement carefully.",
		WorkspaceDir:     t.TempDir(),
		BackendSessionID: "kimi_old",
		Status:           domain.AgentFailed,
		LastRunID:        "run_000123",
		LastError:        &errText,
	}

	reset, err := New(spec).Reset(context.Background(), spec, state)
	if err != nil {
		t.Fatal(err)
	}
	if reset.BackendSessionID != "" {
		t.Fatalf("BackendSessionID = %q, want empty", reset.BackendSessionID)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}
	if reset.LastError != nil {
		t.Fatalf("LastError = %v, want nil", reset.LastError)
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
}
```

- [ ] **Step 4: Add focused OpenCode reset test**

Append this test to `internal/adapters/opencode/opencode_test.go`:

```go
func TestResetCreatesNewSessionAndClearsContinuity(t *testing.T) {
	var sessionCreates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session" {
			t.Fatalf("request = %s %s, want POST /session", r.Method, r.URL.Path)
		}
		sessionCreates++
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "session_new"})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"base_url": server.URL},
	}
	errText := "old error"
	state := domain.AgentState{
		Name:             "Skeptic",
		Backend:          "opencode",
		StartupPrompt:    "Challenge assumptions.",
		WorkspaceDir:     t.TempDir(),
		BackendSessionID: "session_old",
		Status:           domain.AgentFailed,
		LastRunID:        "run_000123",
		LastError:        &errText,
	}

	reset, err := New(spec).Reset(context.Background(), spec, state)
	if err != nil {
		t.Fatal(err)
	}
	if sessionCreates != 1 {
		t.Fatalf("sessionCreates = %d, want 1", sessionCreates)
	}
	if reset.BackendSessionID != "session_new" {
		t.Fatalf("BackendSessionID = %q, want session_new", reset.BackendSessionID)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}
	if reset.LastError != nil {
		t.Fatalf("LastError = %v, want nil", reset.LastError)
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
}
```

- [ ] **Step 5: Run adapter tests and verify they fail**

Run:

```bash
go test ./internal/adapters/...
```

Expected: FAIL because the new interface requires `Reset` and the concrete adapters do not implement it yet.

- [ ] **Step 6: Implement reset helpers in each adapter**

Add this helper shape to each adapter file and keep behavior backend-specific:

In `internal/adapters/codex/codex.go`:

```go
func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = spec.StringOptions["model"]
	state.StartupPrompt = spec.StartupPrompt
	state.BackendSessionID = ""
	state.Status = domain.AgentIdle
	state.LastRunID = ""
	state.LastError = nil
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	return state, nil
}
```

In `internal/adapters/kimi/kimi.go`:

```go
func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = spec.StringOptions["model"]
	state.StartupPrompt = spec.StartupPrompt
	state.BackendSessionID = ""
	state.Status = domain.AgentIdle
	state.LastRunID = ""
	state.LastError = nil
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	return state, nil
}
```

In `internal/adapters/opencode/opencode.go`:

```go
func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	id, err := a.createSession(ctx)
	if err != nil {
		return state, err
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = spec.StringOptions["model"]
	state.StartupPrompt = spec.StartupPrompt
	state.BackendSessionID = id
	state.Status = domain.AgentIdle
	state.LastRunID = ""
	state.LastError = nil
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	return state, nil
}
```

In `internal/adapters/fake/fake.go`, add imports `strconv` and `strings`, then implement:

```go
func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = spec.StringOptions["model"]
	state.StartupPrompt = spec.StartupPrompt
	state.BackendSessionID = "fake_" + spec.Name + "_reset"
	state.Status = domain.AgentIdle
	state.LastRunID = ""
	state.LastError = nil
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	return state, nil
}

func (a *Adapter) delay() time.Duration {
	value := strings.TrimSpace(a.spec.StringOptions["delay_ms"])
	if value == "" {
		return 50 * time.Millisecond
	}
	ms, err := strconv.Atoi(value)
	if err != nil || ms < 0 {
		return 50 * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}
```

Then replace the hard-coded fake sleep in `Send`:

```go
select {
case <-ctx.Done():
	return domain.RunResult{}, state, ctx.Err()
case <-time.After(a.delay()):
}
```

- [ ] **Step 7: Run adapter tests and verify they pass**

Run:

```bash
go test ./internal/adapters/...
```

Expected: PASS.

- [ ] **Step 8: Commit adapter contract work**

Run:

```bash
git add internal/adapters
git commit -m "feat: add agent adapter reset contract"
```

## Task 2: Orchestrator Reset Lifecycle

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`
- Modify: `internal/orchestrator/orchestrator_test.go`

- [ ] **Step 1: Write orchestrator idle reset test**

Append this test to `internal/orchestrator/orchestrator_test.go`:

```go
func TestResetAgentClearsIdleAgentContinuity(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "first", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	if _, err := o.Wait(ctx, run.RunID, time.Second); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	reset, err := o.ResetAgent(ctx, "Reviewer", false)
	if err != nil {
		t.Fatalf("ResetAgent() error = %v", err)
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}
	if reset.LastError != nil {
		t.Fatalf("LastError = %v, want nil", reset.LastError)
	}
	if reset.BackendSessionID != "fake_Reviewer_reset" {
		t.Fatalf("BackendSessionID = %q, want fake_Reviewer_reset", reset.BackendSessionID)
	}

	events, err := o.Transcript(ctx)
	if err != nil {
		t.Fatalf("Transcript() error = %v", err)
	}
	last := events[len(events)-1]
	if last.Type != "agent_reset" || last.Agent != "Reviewer" {
		t.Fatalf("last transcript event = %#v, want agent_reset for Reviewer", last)
	}
	if last.Metadata["force"] != "false" {
		t.Fatalf("force metadata = %q, want false", last.Metadata["force"])
	}
}
```

- [ ] **Step 2: Write busy reset conflict test**

Append:

```go
func TestResetAgentBusyWithoutForceReturnsConflict(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	cfg.Agents[0].StringOptions = map[string]string{"delay_ms": "5000"}
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "long run", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	waitForAgentStatus(t, o, "Reviewer", domain.AgentRunning)

	_, err = o.ResetAgent(ctx, "Reviewer", false)
	if !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("ResetAgent() error = %v, want %v", err, ErrAgentBusy)
	}

	if _, err := o.ResetAgent(ctx, "Reviewer", true); err != nil {
		t.Fatalf("ResetAgent(force) cleanup error = %v", err)
	}
	completed, err := o.Run(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if completed.Status != domain.RunInterrupted {
		t.Fatalf("Status = %q, want %q", completed.Status, domain.RunInterrupted)
	}
}
```

- [ ] **Step 3: Write force reset interruption test**

Append:

```go
func TestResetAgentForceInterruptsActiveRun(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	cfg.Agents[0].StringOptions = map[string]string{"delay_ms": "5000"}
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "long run", map[string]string{"kind": "test"})
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	waitForAgentStatus(t, o, "Reviewer", domain.AgentRunning)

	reset, err := o.ResetAgent(ctx, "Reviewer", true)
	if err != nil {
		t.Fatalf("ResetAgent(force) error = %v", err)
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}

	interrupted, err := o.Run(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if interrupted.Status != domain.RunInterrupted {
		t.Fatalf("Status = %q, want %q; error = %v", interrupted.Status, domain.RunInterrupted, interrupted.Error)
	}
	if interrupted.Error == nil || *interrupted.Error != "interrupted by force reset" {
		t.Fatalf("Error = %v, want force reset message", interrupted.Error)
	}
}
```

- [ ] **Step 4: Add orchestrator test helper**

Add this helper near existing orchestrator test helpers:

```go
func waitForAgentStatus(t *testing.T, o *Orchestrator, agentName string, want domain.AgentStatus) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, agent := range o.Agents() {
			if agent.Name == agentName && agent.Status == want {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("agent %q did not reach status %q", agentName, want)
		case <-ticker.C:
		}
	}
}
```

- [ ] **Step 5: Run orchestrator tests and verify reset tests fail**

Run:

```bash
go test ./internal/orchestrator
```

Expected: FAIL because `ResetAgent` and reset lifecycle fields do not exist.

- [ ] **Step 6: Add reset errors and runtime lifecycle fields**

In `internal/orchestrator/orchestrator.go`, extend vars and runtime:

```go
var (
	ErrAgentNotFound = errors.New("agent not found")
	ErrAgentBusy     = errors.New("agent busy")
	ErrRunNotFound   = errors.New("run not found")
	ErrWaitTimeout   = errors.New("wait timeout")
	ErrResetTimeout  = errors.New("reset timeout")
)

const forceResetTimeout = 5 * time.Second

type agentRuntime struct {
	spec             domain.AgentSpec
	state            domain.AgentState
	adapter          adapters.AgentAdapter
	busy             bool
	resetting        bool
	activeRunID      string
	cancelActiveRun  context.CancelFunc
	interruptingRunID string
}
```

Run `gofmt` later; spacing in the struct can be normalized by `gofmt`.

- [ ] **Step 7: Wire per-run cancellation in SubmitRun**

In `SubmitRun`, reject resetting runtimes and create a per-run context before launching the worker:

```go
if rt.busy || rt.resetting {
	o.mu.Unlock()
	return domain.RunRecord{}, ErrAgentBusy
}

runID := fmt.Sprintf("run_%06d", o.nextRun)
o.nextRun++
runCtx, cancel := context.WithCancel(o.execCtx)
rt.busy = true
rt.activeRunID = runID
rt.cancelActiveRun = cancel
```

On every pre-worker error path after this point, call `cancel()` before `o.releaseAgent(agentName)`.

Launch the worker with the per-run context:

```go
o.workerWG.Add(1)
go o.runWorker(runCtx, agentName, run, waiter)
```

- [ ] **Step 8: Clear active runtime fields on release**

Replace `releaseAgent` with:

```go
func (o *Orchestrator) releaseAgent(agentName string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if rt, ok := o.runtimes[agentName]; ok {
		rt.busy = false
		rt.activeRunID = ""
		rt.cancelActiveRun = nil
	}
}
```

- [ ] **Step 9: Add interruption marker helpers**

Add:

```go
func (o *Orchestrator) markRunInterrupting(agentName, runID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if rt, ok := o.runtimes[agentName]; ok && rt.activeRunID == runID {
		rt.interruptingRunID = runID
	}
}

func (o *Orchestrator) consumeRunInterrupting(agentName, runID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	rt, ok := o.runtimes[agentName]
	if !ok || rt.interruptingRunID != runID {
		return false
	}
	rt.interruptingRunID = ""
	return true
}
```

- [ ] **Step 10: Make runWorker record forced cancellation as interrupted**

In `runWorker`, after adapter `Send` and sink error handling, compute:

```go
interruptedByReset := o.consumeRunInterrupting(agentName, run.RunID)
```

Then make the status decision:

```go
if interruptedByReset {
	run.Status = domain.RunInterrupted
	message := "interrupted by force reset"
	run.Error = &message
	newState.Status = domain.AgentIdle
	newState.LastError = nil
} else if sendErr != nil || result.ErrorMessage != "" {
	run.Status = domain.RunFailed
	message := result.ErrorMessage
	if message == "" {
		message = sendErr.Error()
	}
	run.Error = &message
	newState.Status = domain.AgentFailed
	newState.LastError = &message
} else {
	run.Status = domain.RunCompleted
	newState.Status = domain.AgentIdle
	newState.LastError = nil
}
```

Keep `newState.LastRunID = run.RunID` after this block. `ResetAgent` will clear it after the worker finishes.

- [ ] **Step 11: Implement ResetAgent**

Add:

```go
func (o *Orchestrator) ResetAgent(ctx context.Context, agentName string, force bool) (domain.AgentState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return domain.AgentState{}, err
	}

	var activeRunID string
	var cancel context.CancelFunc

	o.mu.Lock()
	rt, ok := o.runtimes[agentName]
	if !ok {
		o.mu.Unlock()
		return domain.AgentState{}, ErrAgentNotFound
	}
	if rt.resetting {
		o.mu.Unlock()
		return domain.AgentState{}, ErrAgentBusy
	}
	if rt.busy {
		if !force {
			o.mu.Unlock()
			return domain.AgentState{}, ErrAgentBusy
		}
		activeRunID = rt.activeRunID
		cancel = rt.cancelActiveRun
		rt.interruptingRunID = activeRunID
	}
	rt.resetting = true
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		if rt := o.runtimes[agentName]; rt != nil {
			rt.resetting = false
		}
		o.mu.Unlock()
	}()

	if activeRunID != "" {
		if cancel != nil {
			cancel()
		}
		if _, err := o.Wait(ctx, activeRunID, forceResetTimeout); errors.Is(err, ErrWaitTimeout) {
			return domain.AgentState{}, ErrResetTimeout
		} else if err != nil {
			return domain.AgentState{}, err
		}
	}

	o.mu.Lock()
	rt = o.runtimes[agentName]
	state := rt.state
	spec := rt.spec
	adapter := rt.adapter
	o.mu.Unlock()

	reset, err := adapter.Reset(ctx, spec, state)
	if err != nil {
		o.markAgentResetFailure(agentName, err)
		return domain.AgentState{}, err
	}
	if reset.WorkspaceDir == "" {
		reset.WorkspaceDir = o.cfg.WorkspaceDir
	}
	if err := o.store.SaveAgentState(reset); err != nil {
		o.markAgentResetFailure(agentName, err)
		return domain.AgentState{}, err
	}
	if err := o.store.AppendTranscript(domain.TranscriptEvent{
		Type:   "agent_reset",
		Agent:  agentName,
		Status: resetEventStatus(force, activeRunID),
		Metadata: resetMetadata(force, activeRunID),
		At:     time.Now().UTC(),
	}); err != nil {
		o.markAgentResetFailure(agentName, err)
		return domain.AgentState{}, err
	}

	o.mu.Lock()
	if current := o.runtimes[agentName]; current != nil {
		current.state = reset
	}
	o.mu.Unlock()
	return reset, nil
}
```

Add helpers:

```go
func (o *Orchestrator) markAgentResetFailure(agentName string, err error) {
	message := err.Error()
	o.mu.Lock()
	defer o.mu.Unlock()
	if rt := o.runtimes[agentName]; rt != nil {
		rt.state.Status = domain.AgentFailed
		rt.state.LastError = &message
		_ = o.store.SaveAgentState(rt.state)
	}
}

func resetMetadata(force bool, previousRunID string) map[string]string {
	metadata := map[string]string{"force": strconv.FormatBool(force)}
	if previousRunID != "" {
		metadata["previous_run_id"] = previousRunID
	}
	return metadata
}

func resetEventStatus(force bool, previousRunID string) domain.RunStatus {
	if force && previousRunID != "" {
		return domain.RunInterrupted
	}
	return ""
}
```

- [ ] **Step 12: Run orchestrator tests and verify they pass**

Run:

```bash
go test ./internal/orchestrator
```

Expected: PASS, including the existing root context cancellation test. The root context cancellation test must still produce `failed`, not `interrupted`, because only `ResetAgent(force=true)` sets `interruptingRunID`.

- [ ] **Step 13: Commit orchestrator reset lifecycle**

Run:

```bash
git add internal/orchestrator
git commit -m "feat: reset agent runtime sessions"
```

## Task 3: Reset REST API

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1: Write API idle reset test**

Append to `internal/api/server_test.go`:

```go
func TestResetAgentEndpointReturnsUpdatedState(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	runRR := httptest.NewRecorder()
	srv.ServeHTTP(runRR, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs?wait=true&timeout_seconds=1", runPayload{Message: "first"}))
	if runRR.Code != http.StatusAccepted {
		t.Fatalf("run status = %d, want %d; body = %s", runRR.Code, http.StatusAccepted, runRR.Body.String())
	}

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/agents/Reviewer/reset", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	assertJSONContentType(t, rr)

	var agent domain.AgentState
	decodeResponse(t, rr, &agent)
	if agent.Name != "Reviewer" {
		t.Fatalf("Name = %q, want Reviewer", agent.Name)
	}
	if agent.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", agent.LastRunID)
	}
	if agent.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", agent.Status, domain.AgentIdle)
	}
}
```

- [ ] **Step 2: Write API busy conflict and force tests**

Append:

```go
func TestResetAgentEndpointReturnsConflictWhenBusyWithoutForce(t *testing.T) {
	srv := newTestServerWithConfig(t, func(cfg *domain.SessionConfig) {
		cfg.Agents[0].StringOptions = map[string]string{"delay_ms": "5000"}
	}, "Reviewer")

	first := httptest.NewRecorder()
	srv.ServeHTTP(first, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{Message: "long"}))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d; body = %s", first.Code, http.StatusAccepted, first.Body.String())
	}
	waitForAgentStatus(t, srv, "Reviewer", domain.AgentRunning)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/agents/Reviewer/reset", nil))

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusConflict, rr.Body.String())
	}
	assertJSONContentType(t, rr)
}

func TestResetAgentEndpointForceInterruptsBusyAgent(t *testing.T) {
	srv := newTestServerWithConfig(t, func(cfg *domain.SessionConfig) {
		cfg.Agents[0].StringOptions = map[string]string{"delay_ms": "5000"}
	}, "Reviewer")

	first := httptest.NewRecorder()
	srv.ServeHTTP(first, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{Message: "long"}))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d; body = %s", first.Code, http.StatusAccepted, first.Body.String())
	}
	var run domain.RunRecord
	decodeResponse(t, first, &run)
	waitForAgentStatus(t, srv, "Reviewer", domain.AgentRunning)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/agents/Reviewer/reset?force=true", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var agent domain.AgentState
	decodeResponse(t, rr, &agent)
	if agent.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", agent.LastRunID)
	}

	interrupted, err := srv.orchestrator.Run(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if interrupted.Status != domain.RunInterrupted {
		t.Fatalf("Status = %q, want %q", interrupted.Status, domain.RunInterrupted)
	}
}
```

- [ ] **Step 3: Write API not found test**

Append:

```go
func TestResetAgentEndpointReturnsNotFoundForUnknownAgent(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/agents/Missing/reset", nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
	assertJSONContentType(t, rr)
}
```

- [ ] **Step 4: Add configurable test server helper**

Change `newTestServer` to call a helper:

```go
func newTestServer(t *testing.T, agentNames ...string) *Server {
	t.Helper()
	return newTestServerWithConfig(t, nil, agentNames...)
}

func newTestServerWithConfig(t *testing.T, mutate func(*domain.SessionConfig), agentNames ...string) *Server {
	t.Helper()

	cfg := testConfig(t, agentNames...)
	if mutate != nil {
		mutate(&cfg)
	}
	o, err := orchestrator.New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("orchestrator.New() error = %v", err)
	}
	t.Cleanup(func() {
		runs, err := o.Runs(context.Background())
		if err != nil {
			t.Errorf("Runs() cleanup error = %v", err)
			return
		}
		for _, run := range runs {
			if _, err := o.Wait(context.Background(), run.RunID, time.Second); err != nil {
				t.Errorf("Wait(%q) cleanup error = %v", run.RunID, err)
			}
		}
	})
	return New(o, cfg)
}
```

- [ ] **Step 5: Run API tests and verify they fail**

Run:

```bash
go test ./internal/api
```

Expected: FAIL because the route and handler are missing.

- [ ] **Step 6: Add route and handler**

In `routes`, add:

```go
s.mux.HandleFunc("POST /agents/{name}/reset", s.handleResetAgent)
```

Add handler:

```go
func (s *Server) handleResetAgent(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"

	agent, err := s.orchestrator.ResetAgent(r.Context(), r.PathValue("name"), force)
	if errors.Is(err, orchestrator.ErrAgentNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if errors.Is(err, orchestrator.ErrAgentBusy) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if errors.Is(err, orchestrator.ErrResetTimeout) {
		writeError(w, http.StatusGatewayTimeout, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}
```

- [ ] **Step 7: Run API tests and verify they pass**

Run:

```bash
go test ./internal/api
```

Expected: PASS.

- [ ] **Step 8: Commit API reset endpoint**

Run:

```bash
git add internal/api
git commit -m "feat: expose agent reset endpoint"
```

## Task 4: Documentation And End-To-End Verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update README API examples**

In `README.md`, after the wait example, add:

````markdown
Reset an idle or failed agent so its next run starts in a fresh backend session:

```sh
curl -X POST http://127.0.0.1:8080/agents/Reviewer/reset
```

If an agent is stuck in a run, interrupt that run and reset the agent explicitly:

```sh
curl -X POST 'http://127.0.0.1:8080/agents/Reviewer/reset?force=true'
```

Reset keeps existing run artifacts and appends an `agent_reset` event to `transcript.jsonl`. The next run after reset includes the agent startup prompt again.
````

- [ ] **Step 2: Run all tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Inspect git status**

Run:

```bash
git status --short
```

Expected: only intentional README changes are unstaged after previous commits.

- [ ] **Step 4: Commit docs**

Run:

```bash
git add README.md
git commit -m "docs: document agent reset endpoint"
```

## Task 5: Final Regression Checks

**Files:**
- No code changes expected.

- [ ] **Step 1: Run full test suite again**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Inspect final history**

Run:

```bash
git log --oneline -5
git status --short --branch
```

Expected: recent commits include adapter reset, orchestrator reset, API endpoint, and docs. Working tree is clean except for any unrelated user changes that existed before this work.

- [ ] **Step 3: Manual fake-server smoke test**

Run:

```bash
go run ./cmd/agent-debug-squad serve --config examples/squad.yaml
```

In another shell:

```bash
curl -sS -X POST 'http://127.0.0.1:8080/agents/Reviewer/runs?wait=true&timeout_seconds=2' \
  -H 'Content-Type: application/json' \
  -d '{"message":"first run"}'

curl -sS -X POST http://127.0.0.1:8080/agents/Reviewer/reset

curl -sS http://127.0.0.1:8080/transcript
```

Expected: first command returns a completed run, second command returns an idle `Reviewer` state with empty `last_run_id`, and transcript contains an `agent_reset` event.

---

## Self-Review

Spec coverage:

- Manual-only reset: covered by API route and README tasks.
- Busy default conflict: covered by orchestrator and API tests.
- Force cancellation: covered by per-run cancel, interruption marker, and tests.
- Fresh next backend session: covered by adapter reset tests and `LastRunID` clearing.
- Transcript preservation and reset event: covered by orchestrator idle reset test.

Placeholder scan:

- No placeholder markers or unspecified test requests are present.

Type consistency:

- `ResetAgent`, `ErrResetTimeout`, `Reset`, `agent_reset`, `force`, and `previous_run_id` are used consistently across tasks.
