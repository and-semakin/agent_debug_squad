# Run Status Long Poll Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional long polling to `GET /runs/{run_id}` through `wait=true&timeout_seconds=N`.

**Architecture:** Keep wait behavior centralized in `orchestrator.Wait`. The API layer will parse GET-specific wait options, call `Run` for the default non-blocking path, and call `Wait` only when `wait=true`. Documentation will describe that the timeout limits the HTTP wait and does not stop the run.

**Tech Stack:** Go 1.22, `net/http`, existing `httptest` API tests, existing fake adapter.

---

## File Structure

- Modify `internal/api/server.go`: add a GET status wait default, add a reusable wait-timeout parser, and extend `handleRun`.
- Modify `internal/api/server_test.go`: add API tests for GET long polling, timeout behavior, invalid timeout values, and missing run ids with `wait=true`.
- Modify `README.md`: document the status long-poll endpoint and timeout semantics.

## Task 1: API Tests For GET Long Polling

**Files:**
- Modify: `internal/api/server_test.go`

- [ ] **Step 1: Add failing tests for successful GET wait and timeout behavior**

Add these tests after `TestWaitTrueCompletesWithOutputPath`:

```go
func TestGetRunWaitTrueCompletesWithOutputPath(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	create := httptest.NewRecorder()
	srv.ServeHTTP(create, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{
		Message: "finish",
	}))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want %d; body = %s", create.Code, http.StatusAccepted, create.Body.String())
	}
	var created domain.RunRecord
	decodeResponse(t, create, &created)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/runs/"+created.RunID+"?wait=true&timeout_seconds=1", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	assertJSONContentType(t, rr)

	var run domain.RunRecord
	decodeResponse(t, rr, &run)
	if run.RunID != created.RunID {
		t.Fatalf("RunID = %q, want %q", run.RunID, created.RunID)
	}
	if run.Status != domain.RunCompleted {
		t.Fatalf("Status = %q, want %q", run.Status, domain.RunCompleted)
	}
	if run.OutputPath == nil || *run.OutputPath == "" {
		t.Fatalf("OutputPath = %v, want non-empty", run.OutputPath)
	}
}

func TestGetRunWaitTimeoutReturnsCurrentRunAndDoesNotStopAgent(t *testing.T) {
	srv := newTestServerWithConfig(t, func(cfg *domain.SessionConfig) {
		cfg.Agents[0].StringOptions = map[string]string{"delay_ms": "1500"}
	}, "Reviewer")

	create := httptest.NewRecorder()
	srv.ServeHTTP(create, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{
		Message: "slow",
	}))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want %d; body = %s", create.Code, http.StatusAccepted, create.Body.String())
	}
	var created domain.RunRecord
	decodeResponse(t, create, &created)
	waitForAgentStatus(t, srv, "Reviewer", domain.AgentRunning)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/runs/"+created.RunID+"?wait=true&timeout_seconds=1", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	assertJSONContentType(t, rr)

	var run domain.RunRecord
	decodeResponse(t, rr, &run)
	if run.RunID != created.RunID {
		t.Fatalf("RunID = %q, want %q", run.RunID, created.RunID)
	}
	if run.Status != domain.RunQueued && run.Status != domain.RunRunning {
		t.Fatalf("Status = %q, want queued or running", run.Status)
	}

	completed, err := srv.orchestrator.Wait(context.Background(), created.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait(%q) error = %v", created.RunID, err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("Status after timeout = %q, want %q", completed.Status, domain.RunCompleted)
	}
}
```

- [ ] **Step 2: Add failing tests for invalid timeout and missing run**

Add these tests near the existing unknown-run and invalid-timeout tests:

```go
func TestTimeoutFromQueryUsesProvidedDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/runs/run_000001?wait=true", nil)

	timeout, err := timeoutFromQuery(req, defaultStatusWaitTimeout)
	if err != nil {
		t.Fatalf("timeoutFromQuery() error = %v", err)
	}
	if timeout != 30*time.Second {
		t.Fatalf("timeout = %s, want 30s", timeout)
	}
}

func TestGetRunWaitUnknownRunReturnsNotFound(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/runs/run_missing?wait=true&timeout_seconds=1", nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
	assertJSONContentType(t, rr)
}

func TestGetRunWaitInvalidTimeoutSecondsReturnBadRequest(t *testing.T) {
	for _, timeoutSeconds := range []string{"abc", "0"} {
		t.Run(timeoutSeconds, func(t *testing.T) {
			srv := newTestServer(t, "Reviewer")

			create := httptest.NewRecorder()
			srv.ServeHTTP(create, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{
				Message: "finish",
			}))
			if create.Code != http.StatusAccepted {
				t.Fatalf("create status = %d, want %d; body = %s", create.Code, http.StatusAccepted, create.Body.String())
			}
			var created domain.RunRecord
			decodeResponse(t, create, &created)

			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/runs/"+created.RunID+"?wait=true&timeout_seconds="+timeoutSeconds, nil))

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			assertJSONContentType(t, rr)
		})
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
go test ./internal/api -run 'TestGetRunWait'
```

Expected: FAIL. The successful-wait test should return a non-completed immediate response, and the invalid-timeout test should not return `400` because `GET /runs/{run_id}` does not parse wait options yet.

## Task 2: Implement GET Wait Parsing And Handler Behavior

**Files:**
- Modify: `internal/api/server.go`

- [ ] **Step 1: Add a GET-specific default wait timeout**

Change the existing timeout constant block near the top of `internal/api/server.go` to:

```go
const (
	defaultWaitTimeout       = 60 * time.Second
	defaultStatusWaitTimeout = 30 * time.Second
)
```

- [ ] **Step 2: Replace `timeoutFromQuery` with a default-aware helper**

Replace the existing helper:

```go
func timeoutFromQuery(r *http.Request) (time.Duration, error) {
	value := r.URL.Query().Get("timeout_seconds")
	if value == "" {
		return defaultWaitTimeout, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0, errors.New("timeout_seconds must be a positive integer")
	}
	return time.Duration(seconds) * time.Second, nil
}
```

with:

```go
func timeoutFromQuery(r *http.Request, defaultTimeout time.Duration) (time.Duration, error) {
	value := r.URL.Query().Get("timeout_seconds")
	if value == "" {
		return defaultTimeout, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0, errors.New("timeout_seconds must be a positive integer")
	}
	return time.Duration(seconds) * time.Second, nil
}
```

- [ ] **Step 3: Update create-run wait parsing to preserve its current default**

In `handleCreateRun`, replace:

```go
timeout, err = timeoutFromQuery(r)
```

with:

```go
timeout, err = timeoutFromQuery(r, defaultWaitTimeout)
```

- [ ] **Step 4: Extend `handleRun` with optional wait support**

Replace `handleRun` with:

```go
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	wait := r.URL.Query().Get("wait") == "true"
	timeout := defaultStatusWaitTimeout
	if wait {
		var err error
		timeout, err = timeoutFromQuery(r, defaultStatusWaitTimeout)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	var run domain.RunRecord
	var err error
	if wait {
		run, err = s.orchestrator.Wait(r.Context(), r.PathValue("run_id"), timeout)
	} else {
		run, err = s.orchestrator.Run(r.Context(), r.PathValue("run_id"))
	}
	if errors.Is(err, orchestrator.ErrWaitTimeout) {
		writeJSON(w, http.StatusOK, run)
		return
	}
	if errors.Is(err, orchestrator.ErrRunNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if isUnsafePathError(err) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
```

- [ ] **Step 5: Run API tests**

Run:

```bash
go test ./internal/api
```

Expected: PASS.

- [ ] **Step 6: Commit API implementation**

Run:

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "feat: add run status long polling"
```

## Task 3: README Documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update README with status long-poll example**

After the existing create-run wait example, add:

````markdown
Wait for an existing run without starting another one:

```sh
curl -X GET 'http://127.0.0.1:8080/runs/run_000001?wait=true&timeout_seconds=600'
```

If the wait timeout expires before the run finishes, the response is still `200 OK` with the latest `RunRecord`, and the agent continues running in the background.
````

- [ ] **Step 2: Run README diff check**

Run:

```bash
git diff -- README.md
```

Expected: diff shows only the new status long-poll example and timeout note.

- [ ] **Step 3: Commit documentation**

Run:

```bash
git add README.md
git commit -m "docs: document run status long polling"
```

## Task 4: Final Verification

**Files:**
- Verify: `internal/api/server.go`
- Verify: `internal/api/server_test.go`
- Verify: `README.md`

- [ ] **Step 1: Run full Go test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Inspect git status**

Run:

```bash
git status --short --branch
```

Expected: clean working tree on the current branch or detached HEAD.

- [ ] **Step 3: Inspect recent commits**

Run:

```bash
git log --oneline -5
```

Expected: recent history includes:

```text
docs: document run status long polling
feat: add run status long polling
docs: design run status long polling
```
