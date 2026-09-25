package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/workflow"
)

func newOneShotTestServer(t *testing.T) (*Server, *scriptedWorkflows) {
	t.Helper()
	wf := &scriptedWorkflows{}
	srv := newWorkflowTestServer(t, wf)
	srv.SetOneShotAdmission(OneShotAdmission{
		ExecutionID: "wf_000001",
		OwnsRun:     func(runID string) bool { return strings.HasPrefix(runID, "wrun_000001_") },
	})
	return srv, wf
}

func TestOneShotRejectsNewWorkflowSubmission(t *testing.T) {
	srv, wf := newOneShotTestServer(t)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"request_id":"req-2"}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("create status = %d, body %s", rr.Code, rr.Body.String())
	}
	if wf.createRequestID != "" {
		t.Fatalf("create reached the manager: %q", wf.createRequestID)
	}
	if !strings.Contains(rr.Body.String(), "one-shot run mode") {
		t.Fatalf("mode error missing: %s", rr.Body.String())
	}
}

func TestOneShotRejectsManualRunAndReset(t *testing.T) {
	srv, _ := newOneShotTestServer(t)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/agents/Reviewer/runs", strings.NewReader(`{"message":"hi"}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("manual run status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/agents/Reviewer/reset", nil))
	if rr.Code != http.StatusConflict {
		t.Fatalf("manual reset status = %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestOneShotRejectsForeignExecutionControls(t *testing.T) {
	srv, wf := newOneShotTestServer(t)

	for _, path := range []string{
		"/workflows/wf_000002/pause",
		"/workflows/wf_000002/resume",
		"/workflows/wf_000002/cancel",
		"/workflows/wf_000002/tasks/alpha/retry",
		"/workflows/wf_000002/loops/outer/extend",
		"/workflows/wf_000002/loops/outer/stop",
	} {
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"request_id":"req-3"}`)))
		if rr.Code != http.StatusConflict {
			t.Fatalf("%s status = %d, body %s", path, rr.Code, rr.Body.String())
		}
	}
	if len(wf.cancelOpts) != 0 {
		t.Fatalf("foreign cancel reached the manager")
	}
}

func TestOneShotSelectedControlsRemainAvailable(t *testing.T) {
	srv, wf := newOneShotTestServer(t)
	wf.view = map[string]domain.WorkflowExecutionView{
		"wf_000001": {ExecutionID: "wf_000001", State: domain.WorkflowPaused},
	}

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/pause", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("selected pause status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/alpha/retry", strings.NewReader(`{"request_id":"req-4","expected_attempt":1}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("selected retry status = %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestOneShotRejectsForeignPermissionReply(t *testing.T) {
	srv, _ := newOneShotTestServer(t)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/runs/wrun_000002_000001/permissions/perm-1/reply", strings.NewReader(`{"reply":"once"}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("foreign permission status = %d, body %s", rr.Code, rr.Body.String())
	}

	// A reply for a run of the selected execution passes admission and fails
	// downstream with the normal not-found semantics of the test orchestrator.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/runs/wrun_000001_000001/permissions/perm-1/reply", strings.NewReader(`{"reply":"once"}`)))
	if rr.Code == http.StatusConflict {
		t.Fatalf("selected permission reply was rejected by admission: %s", rr.Body.String())
	}
}

func TestOneShotReadOnlyHistoryRemainsAvailable(t *testing.T) {
	srv, _ := newOneShotTestServer(t)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workflows/wf_000002", nil))
	if rr.Code == http.StatusConflict {
		t.Fatalf("read-only view was rejected by admission")
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workflows", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestOneShotTerminalFenceReturns409(t *testing.T) {
	wf := &scriptedWorkflows{retryErr: workflow.ErrExecutionSealed}
	srv := newWorkflowTestServer(t, wf)
	srv.SetOneShotAdmission(OneShotAdmission{ExecutionID: "wf_000001"})

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/alpha/retry", strings.NewReader(`{"request_id":"req-5","expected_attempt":1}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("sealed retry status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "sealed") {
		t.Fatalf("fence error missing: %s", rr.Body.String())
	}
}
