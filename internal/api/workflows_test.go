package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
	"github.com/and-semakin/agent_debug_squad/internal/store"
	"github.com/and-semakin/agent_debug_squad/internal/workflow"
)

type scriptedWorkflows struct {
	createErr       error
	created         bool
	createView      domain.WorkflowExecutionView
	createRequestID string

	listSummaries []domain.WorkflowExecutionSummary
	listErr       error

	view    map[string]domain.WorkflowExecutionView
	viewErr error

	waitView     domain.WorkflowExecutionView
	waitErr      error
	waitCalled   bool
	waitTimeouts []time.Duration

	pauseErr   error
	resumeErr  error
	cancelErr  error
	cancelOpts []workflow.CancelOptions

	retryView    domain.WorkflowExecutionView
	retryCreated bool
	retryErr     error
	retryCalls   []workflow.RetryRequest

	overrideView    domain.WorkflowExecutionView
	overrideCreated bool
	overrideErr     error
}

func (s *scriptedWorkflows) Create(requestID string) (domain.WorkflowExecutionView, bool, error) {
	s.createRequestID = requestID
	if s.createErr != nil {
		return domain.WorkflowExecutionView{}, false, s.createErr
	}
	return s.createView, s.created, nil
}

func (s *scriptedWorkflows) List() ([]domain.WorkflowExecutionSummary, error) {
	return s.listSummaries, s.listErr
}

func (s *scriptedWorkflows) View(executionID string) (domain.WorkflowExecutionView, error) {
	if s.viewErr != nil {
		return domain.WorkflowExecutionView{}, s.viewErr
	}
	view, ok := s.view[executionID]
	if !ok {
		return domain.WorkflowExecutionView{}, workflow.ErrExecutionNotFound
	}
	return view, nil
}

func (s *scriptedWorkflows) Wait(ctx context.Context, executionID string, timeout time.Duration) (domain.WorkflowExecutionView, error) {
	s.waitCalled = true
	s.waitTimeouts = append(s.waitTimeouts, timeout)
	return s.waitView, s.waitErr
}

func (s *scriptedWorkflows) Pause(executionID string) (domain.WorkflowExecutionView, error) {
	if s.pauseErr != nil {
		return domain.WorkflowExecutionView{}, s.pauseErr
	}
	return s.view[executionID], nil
}

func (s *scriptedWorkflows) Resume(executionID string) (domain.WorkflowExecutionView, error) {
	if s.resumeErr != nil {
		return domain.WorkflowExecutionView{}, s.resumeErr
	}
	return s.view[executionID], nil
}

func (s *scriptedWorkflows) Cancel(executionID string, opts workflow.CancelOptions) (domain.WorkflowExecutionView, error) {
	s.cancelOpts = append(s.cancelOpts, opts)
	if s.cancelErr != nil {
		return domain.WorkflowExecutionView{}, s.cancelErr
	}
	return s.view[executionID], nil
}

func (s *scriptedWorkflows) RetryTask(executionID, taskID string, req workflow.RetryRequest) (domain.WorkflowExecutionView, bool, error) {
	s.retryCalls = append(s.retryCalls, req)
	if s.retryErr != nil {
		return domain.WorkflowExecutionView{}, false, s.retryErr
	}
	return s.retryView, s.retryCreated, nil
}

func (s *scriptedWorkflows) OverrideVerdict(executionID, taskID string, attempt int, req workflow.OverrideVerdictRequest) (domain.WorkflowExecutionView, bool, error) {
	if s.overrideErr != nil {
		return domain.WorkflowExecutionView{}, false, s.overrideErr
	}
	return s.overrideView, s.overrideCreated, nil
}

func newWorkflowTestServer(t *testing.T, wf WorkflowManager) *Server {
	t.Helper()
	cfg := testConfig(t, "Reviewer")
	o, err := orchestrator.New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("orchestrator.New() error = %v", err)
	}
	return New(o, wf, cfg)
}

func TestWorkflowCreateReturns202AndReplay200(t *testing.T) {
	wf := &scriptedWorkflows{created: true, createView: domain.WorkflowExecutionView{ExecutionID: "wf_000001", State: domain.WorkflowRunning}}
	srv := newWorkflowTestServer(t, wf)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"request_id":"req-1"}`)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, body %s", rr.Code, rr.Body.String())
	}
	if wf.createRequestID != "req-1" {
		t.Fatalf("request id: %q", wf.createRequestID)
	}

	wf.created = false
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"request_id":"req-1"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("replay status = %d", rr.Code)
	}
}

func TestWorkflowCreateValidation(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		err    error
		status int
	}{
		{"missing request id", `{}`, nil, http.StatusBadRequest},
		{"empty request id", `{"request_id":"  "}`, nil, http.StatusBadRequest},
		{"malformed json", `{`, nil, http.StatusBadRequest},
		{"unknown field", `{"request_id":"r","extra":1}`, nil, http.StatusBadRequest},
		{"no definition", `{"request_id":"r"}`, workflow.ErrNoDefinition, http.StatusBadRequest},
		{"changed definition", `{"request_id":"r"}`, workflow.ErrDefinitionChanged, http.StatusConflict},
		{"active execution", `{"request_id":"r"}`, workflow.ErrExecutionActive, http.StatusConflict},
		{"invalid definition", `{"request_id":"r"}`, fmtInvalidRequest("workflow name is required"), http.StatusBadRequest},
		{"storage failure", `{"request_id":"r"}`, errors.New("disk exploded"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := &scriptedWorkflows{createErr: tc.err}
			srv := newWorkflowTestServer(t, wf)
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(tc.body)))
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d, body %s", rr.Code, tc.status, rr.Body.String())
			}
		})
	}
}

func TestWorkflowCreateWithoutManagerReturns400(t *testing.T) {
	srv := newWorkflowTestServer(t, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"request_id":"r"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestWorkflowListReturnsSummaries(t *testing.T) {
	wf := &scriptedWorkflows{listSummaries: []domain.WorkflowExecutionSummary{
		{ExecutionID: "wf_000001", State: domain.WorkflowSucceeded},
		{ExecutionID: "wf_000002", State: domain.WorkflowRunning},
	}}
	srv := newWorkflowTestServer(t, wf)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workflows", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var summaries []domain.WorkflowExecutionSummary
	if err := json.Unmarshal(rr.Body.Bytes(), &summaries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(summaries) != 2 || summaries[1].ExecutionID != "wf_000002" {
		t.Fatalf("summaries: %+v", summaries)
	}
}

func TestWorkflowGetWithoutWaitReturnsView(t *testing.T) {
	wf := &scriptedWorkflows{view: map[string]domain.WorkflowExecutionView{
		"wf_000001": {ExecutionID: "wf_000001", State: domain.WorkflowRunning, Revision: 3},
	}}
	srv := newWorkflowTestServer(t, wf)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workflows/wf_000001", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var view domain.WorkflowExecutionView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Revision != 3 || !wf.waitCalled == false {
		t.Fatalf("view: %+v", view)
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workflows/wf_999999", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown execution status = %d", rr.Code)
	}
}

func TestWorkflowGetWaitValidatesBounds(t *testing.T) {
	wf := &scriptedWorkflows{waitView: domain.WorkflowExecutionView{ExecutionID: "wf_000001", State: domain.WorkflowRunning}}
	srv := newWorkflowTestServer(t, wf)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workflows/wf_000001?wait=true", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("default wait status = %d", rr.Code)
	}
	if len(wf.waitTimeouts) != 1 || wf.waitTimeouts[0] != 30*time.Second {
		t.Fatalf("default timeout: %v", wf.waitTimeouts)
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workflows/wf_000001?wait=true&timeout_seconds=600", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("max timeout status = %d", rr.Code)
	}

	for _, invalid := range []string{"0", "-1", "601", "abc", "1.5"} {
		rr = httptest.NewRecorder()
		srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workflows/wf_000001?wait=true&timeout_seconds="+invalid, nil))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("timeout_seconds=%s status = %d", invalid, rr.Code)
		}
	}
}

func TestWorkflowControlRoutes(t *testing.T) {
	view := domain.WorkflowExecutionView{ExecutionID: "wf_000001", State: domain.WorkflowPaused}
	wf := &scriptedWorkflows{view: map[string]domain.WorkflowExecutionView{"wf_000001": view}}
	srv := newWorkflowTestServer(t, wf)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/pause", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("pause status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/resume", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/cancel", strings.NewReader(`{"confirm_previous_stopped":true}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body %s", rr.Code, rr.Body.String())
	}
	if len(wf.cancelOpts) != 1 || !wf.cancelOpts[0].ConfirmPreviousStopped {
		t.Fatalf("cancel options: %+v", wf.cancelOpts)
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/cancel", strings.NewReader(`{"unexpected":true}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown cancel field status = %d", rr.Code)
	}
}

func TestWorkflowControlErrors(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		err    error
		status int
	}{
		{"pause terminal", http.MethodPost, "/workflows/wf_000001/pause", workflow.ErrInvalidTransition, http.StatusConflict},
		{"pause unknown", http.MethodPost, "/workflows/wf_999999/pause", workflow.ErrExecutionNotFound, http.StatusNotFound},
		{"resume uncertainty", http.MethodPost, "/workflows/wf_000001/resume", workflow.ErrUncertaintyUnresolved, http.StatusConflict},
		{"resume worker live", http.MethodPost, "/workflows/wf_000001/resume", workflow.ErrWorkerActive, http.StatusConflict},
		{"cancel terminal", http.MethodPost, "/workflows/wf_000001/cancel", workflow.ErrInvalidTransition, http.StatusConflict},
		{"cancel storage", http.MethodPost, "/workflows/wf_000001/cancel", errors.New("io error"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := &scriptedWorkflows{pauseErr: tc.err, resumeErr: tc.err, cancelErr: tc.err}
			srv := newWorkflowTestServer(t, wf)
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d, body %s", rr.Code, tc.status, rr.Body.String())
			}
		})
	}
}

func TestWorkflowRetryRoute(t *testing.T) {
	wf := &scriptedWorkflows{
		retryView:    domain.WorkflowExecutionView{ExecutionID: "wf_000001"},
		retryCreated: true,
	}
	srv := newWorkflowTestServer(t, wf)

	body := `{"request_id":"retry-1","expected_attempt":1,"confirm_previous_stopped":true}`
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/retry", strings.NewReader(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d, body %s", rr.Code, rr.Body.String())
	}
	if len(wf.retryCalls) != 1 {
		t.Fatalf("retry calls: %+v", wf.retryCalls)
	}
	call := wf.retryCalls[0]
	if call.RequestID != "retry-1" || call.ExpectedAttempt != 1 || !call.ConfirmPreviousStopped {
		t.Fatalf("retry payload: %+v", call)
	}

	wf.retryCreated = false
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/retry", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("replay status = %d", rr.Code)
	}

	wf.retryErr = workflow.ErrRetryConflict
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/retry", strings.NewReader(`{"request_id":"r2","expected_attempt":2}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d", rr.Code)
	}

	wf.retryErr = workflow.ErrTaskNotFound
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/ghost/retry", strings.NewReader(`{"request_id":"r3","expected_attempt":1}`)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task status = %d", rr.Code)
	}

	wf.retryErr = nil
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/a/retry", strings.NewReader(`{"expected_attempt":1}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing request id status = %d", rr.Code)
	}
}

func TestWorkflowVerdictOverrideRoute(t *testing.T) {
	wf := &scriptedWorkflows{
		overrideView:    domain.WorkflowExecutionView{ExecutionID: "wf_000001", State: domain.WorkflowRunning},
		overrideCreated: true,
	}
	srv := newWorkflowTestServer(t, wf)

	body := `{"request_id":"ovr-1","verdict":"review_passed"}`
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/attempts/1/verdict", strings.NewReader(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("override status = %d, body %s", rr.Code, rr.Body.String())
	}

	wf.overrideCreated = false
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/attempts/1/verdict", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("replay status = %d", rr.Code)
	}

	wf.overrideErr = workflow.ErrVerdictConflict
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/attempts/1/verdict", strings.NewReader(`{"request_id":"ovr-2","verdict":"issues_found"}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d", rr.Code)
	}

	wf.overrideErr = fmtInvalidRequest("verdict %q is not declared by task %q")
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/attempts/1/verdict", strings.NewReader(`{"request_id":"ovr-3","verdict":"nope"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("undeclared verdict status = %d", rr.Code)
	}

	wf.overrideErr = workflow.ErrAttemptNotFound
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/attempts/9/verdict", strings.NewReader(`{"request_id":"ovr-4","verdict":"review_passed"}`)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown attempt status = %d", rr.Code)
	}

	wf.overrideErr = nil
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/attempts/1/verdict", strings.NewReader(`{"verdict":"review_passed"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing request id status = %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workflows/wf_000001/tasks/review_a/attempts/0/verdict", strings.NewReader(`{"request_id":"ovr-5","verdict":"review_passed"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid attempt status = %d", rr.Code)
	}
}

func fmtInvalidRequest(message string) error {
	return fmt.Errorf("%w: %s", workflow.ErrInvalidRequest, message)
}
