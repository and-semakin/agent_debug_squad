package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/orchestrator"
	"github.com/andrey/agent-debug-squad/internal/store"
)

func TestRunEndpointReturnsRunIDAndStatus(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{
		Message:  "please review",
		Metadata: map[string]string{"reason": "test"},
	}))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
	assertJSONContentType(t, rr)

	var run domain.RunRecord
	decodeResponse(t, rr, &run)
	if run.RunID == "" {
		t.Fatalf("RunID is empty")
	}
	if run.Agent != "Reviewer" {
		t.Fatalf("Agent = %q, want Reviewer", run.Agent)
	}
	if run.Status != domain.RunQueued && run.Status != domain.RunRunning && run.Status != domain.RunCompleted {
		t.Fatalf("Status = %q, want an accepted run status", run.Status)
	}
}

func TestBusyAgentReturnsConflict(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	first := httptest.NewRecorder()
	srv.ServeHTTP(first, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{Message: "first"}))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d; body = %s", first.Code, http.StatusAccepted, first.Body.String())
	}
	waitForAgentStatus(t, srv, "Reviewer", domain.AgentRunning)

	second := httptest.NewRecorder()
	srv.ServeHTTP(second, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{Message: "second"}))
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, want %d; body = %s", second.Code, http.StatusConflict, second.Body.String())
	}
	assertJSONContentType(t, second)
}

func TestHealthEndpointReturnsOK(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	assertJSONContentType(t, rr)

	var body map[string]string
	decodeResponse(t, rr, &body)
	if body["status"] != "ok" {
		t.Fatalf("status body = %q, want ok", body["status"])
	}
}

func TestAgentEndpointReturnsOneAgent(t *testing.T) {
	srv := newTestServer(t, "Reviewer", "Implementer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/agents/Reviewer", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	assertJSONContentType(t, rr)

	var agent domain.AgentState
	decodeResponse(t, rr, &agent)
	if agent.Name != "Reviewer" {
		t.Fatalf("Name = %q, want Reviewer", agent.Name)
	}
	if agent.StartupPrompt != "You are Reviewer" {
		t.Fatalf("StartupPrompt = %q, want %q", agent.StartupPrompt, "You are Reviewer")
	}
}

func TestAgentEndpointReturnsNotFoundForUnknownAgent(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/agents/Missing", nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
	assertJSONContentType(t, rr)
}

func TestBlankMessageReturnsBadRequest(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs", runPayload{Message: " \t\n"}))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	assertJSONContentType(t, rr)
}

func TestUnknownRunReturnsNotFound(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/runs/run_missing", nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
	assertJSONContentType(t, rr)
}

func TestWaitTrueCompletesWithOutputPath(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs?wait=true&timeout_seconds=1", runPayload{
		Message:  "finish",
		Metadata: map[string]string{"reason": "test"},
	}))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
	assertJSONContentType(t, rr)

	var run domain.RunRecord
	decodeResponse(t, rr, &run)
	if run.Status != domain.RunCompleted {
		t.Fatalf("Status = %q, want %q", run.Status, domain.RunCompleted)
	}
	if run.OutputPath == nil || *run.OutputPath == "" {
		t.Fatalf("OutputPath = %v, want non-empty", run.OutputPath)
	}
}

func TestInvalidTimeoutSecondsReturnBadRequest(t *testing.T) {
	for _, timeoutSeconds := range []string{"abc", "0"} {
		t.Run(timeoutSeconds, func(t *testing.T) {
			srv := newTestServer(t, "Reviewer")

			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs?wait=true&timeout_seconds="+timeoutSeconds, runPayload{
				Message: "finish",
			}))

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			assertJSONContentType(t, rr)

			runs, err := srv.orchestrator.Runs(context.Background())
			if err != nil {
				t.Fatalf("Runs() error = %v", err)
			}
			if len(runs) != 0 {
				t.Fatalf("len(runs) = %d, want 0", len(runs))
			}
		})
	}
}

func TestWaitTrueUsesRequestContextCancellation(t *testing.T) {
	srv := newTestServer(t, "Reviewer")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rr := httptest.NewRecorder()
	req := newJSONRequest(t, http.MethodPost, "/agents/Reviewer/runs?wait=true&timeout_seconds=60", runPayload{
		Message: "finish after request cancel",
	}).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeHTTP(rr, req)
	}()

	run := waitForRunCreated(t, srv)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after request context cancellation")
	}
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	assertJSONContentType(t, rr)

	completed, err := srv.orchestrator.Wait(context.Background(), run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait(%q) error = %v", run.RunID, err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("Status = %q, want %q", completed.Status, domain.RunCompleted)
	}
}

func TestUnsafeRunPathReturnsClientError(t *testing.T) {
	srv := newTestServer(t, "Reviewer")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/runs/bad%2Fid", nil))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	assertJSONContentType(t, rr)
}

func TestWriteJSONLogsEncodeErrors(t *testing.T) {
	var logs bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	writeJSON(httptest.NewRecorder(), http.StatusOK, make(chan int))

	if got := logs.String(); !strings.Contains(got, "json encode response: json: unsupported type: chan int") {
		t.Fatalf("log output = %q, want json encode response error", got)
	}
}

type runPayload struct {
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func newTestServer(t *testing.T, agentNames ...string) *Server {
	t.Helper()

	cfg := testConfig(t, agentNames...)
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

func waitForAgentStatus(t *testing.T, srv *Server, agentName string, want domain.AgentStatus) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, agent := range srv.orchestrator.Agents() {
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

func waitForRunCreated(t *testing.T, srv *Server) domain.RunRecord {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runs, err := srv.orchestrator.Runs(context.Background())
		if err != nil {
			t.Fatalf("Runs() error = %v", err)
		}
		if len(runs) > 0 {
			return runs[0]
		}
		select {
		case <-deadline:
			t.Fatal("run was not created")
		case <-ticker.C:
		}
	}
}

func testConfig(t *testing.T, agentNames ...string) domain.SessionConfig {
	t.Helper()

	agents := make([]domain.AgentSpec, 0, len(agentNames))
	for _, name := range agentNames {
		agents = append(agents, domain.AgentSpec{
			Name:          name,
			Backend:       "fake",
			StartupPrompt: "You are " + name,
		})
	}

	return domain.SessionConfig{
		SessionName:  "test",
		SessionID:    "session_test",
		WorkspaceDir: t.TempDir(),
		StateDirName: ".agent-debug-squad",
		Host:         "127.0.0.1",
		Port:         8080,
		Agents:       agents,
	}
}

func newJSONRequest(t *testing.T, method, target string, payload runPayload) *http.Request {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()

	if err := json.Unmarshal(rr.Body.Bytes(), dst); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", rr.Body.String(), err)
	}
}

func assertJSONContentType(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}
