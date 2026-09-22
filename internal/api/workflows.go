package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/workflow"
)

// errInvalidBody marks request-shape failures that belong on the 400 path.
var errInvalidBody = errors.New("invalid request body")

const (
	defaultWorkflowWaitTimeout = 30 * time.Second
	maxWorkflowWaitTimeout     = 600 * time.Second
)

// WorkflowManager decouples the HTTP layer from the workflow scheduler.
type WorkflowManager interface {
	Create(requestID string) (domain.WorkflowExecutionView, bool, error)
	List() ([]domain.WorkflowExecutionSummary, error)
	View(executionID string) (domain.WorkflowExecutionView, error)
	Wait(ctx context.Context, executionID string, timeout time.Duration) (domain.WorkflowExecutionView, error)
	Pause(executionID string) (domain.WorkflowExecutionView, error)
	Resume(executionID string) (domain.WorkflowExecutionView, error)
	Cancel(executionID string, opts workflow.CancelOptions) (domain.WorkflowExecutionView, error)
	RetryTask(executionID, taskID string, req workflow.RetryRequest) (domain.WorkflowExecutionView, bool, error)
	OverrideVerdict(executionID, taskID string, attempt int, req workflow.OverrideVerdictRequest) (domain.WorkflowExecutionView, bool, error)
}

func (s *Server) handleWorkflowCreate(w http.ResponseWriter, r *http.Request) {
	if s.workflows == nil {
		writeError(w, http.StatusBadRequest, workflow.ErrNoDefinition)
		return
	}
	var body workflowCreateRequest
	if err := decodeStrictJSON(w, r.Body, &body); err != nil {
		writeJSONWorkflowError(w, err)
		return
	}
	if strings.TrimSpace(body.RequestID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("request_id is required"))
		return
	}

	view, created, err := s.workflows.Create(body.RequestID)
	if err != nil {
		writeJSONWorkflowError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, view)
}

func (s *Server) handleWorkflowList(w http.ResponseWriter, r *http.Request) {
	if s.workflows == nil {
		writeError(w, http.StatusBadRequest, workflow.ErrNoDefinition)
		return
	}
	summaries, err := s.workflows.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (s *Server) handleWorkflowGet(w http.ResponseWriter, r *http.Request) {
	if s.workflows == nil {
		writeError(w, http.StatusBadRequest, workflow.ErrNoDefinition)
		return
	}
	executionID := r.PathValue("execution_id")
	wait := r.URL.Query().Get("wait") == "true"
	if !wait {
		view, err := s.workflows.View(executionID)
		writeJSONWorkflowErrorOrValue(w, err, func() { writeJSON(w, http.StatusOK, view) })
		return
	}

	timeout, err := workflowWaitTimeout(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	view, err := s.workflows.Wait(r.Context(), executionID, timeout)
	if err != nil && !errors.Is(err, context.Canceled) {
		writeJSONWorkflowError(w, err)
		return
	}
	// Wait expiry and client disconnect both return the current state; the
	// execution is never cancelled by waiting.
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleWorkflowPause(w http.ResponseWriter, r *http.Request) {
	s.controlWorkflow(w, func() (domain.WorkflowExecutionView, error) {
		return s.workflows.Pause(r.PathValue("execution_id"))
	})
}

func (s *Server) handleWorkflowResume(w http.ResponseWriter, r *http.Request) {
	s.controlWorkflow(w, func() (domain.WorkflowExecutionView, error) {
		return s.workflows.Resume(r.PathValue("execution_id"))
	})
}

func (s *Server) handleWorkflowCancel(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil && r.ContentLength != 0 {
		var body workflowCancelRequest
		if err := decodeStrictJSON(w, r.Body, &body); err != nil {
			writeJSONWorkflowError(w, err)
			return
		}
		s.controlWorkflow(w, func() (domain.WorkflowExecutionView, error) {
			return s.workflows.Cancel(r.PathValue("execution_id"), workflow.CancelOptions{
				ConfirmPreviousStopped: body.ConfirmPreviousStopped,
			})
		})
		return
	}
	s.controlWorkflow(w, func() (domain.WorkflowExecutionView, error) {
		return s.workflows.Cancel(r.PathValue("execution_id"), workflow.CancelOptions{})
	})
}

func (s *Server) handleWorkflowRetry(w http.ResponseWriter, r *http.Request) {
	if s.workflows == nil {
		writeError(w, http.StatusBadRequest, workflow.ErrNoDefinition)
		return
	}
	var body workflowRetryRequest
	if err := decodeStrictJSON(w, r.Body, &body); err != nil {
		writeJSONWorkflowError(w, err)
		return
	}
	if strings.TrimSpace(body.RequestID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("request_id is required"))
		return
	}
	if body.ExpectedAttempt < 1 {
		writeError(w, http.StatusBadRequest, errors.New("expected_attempt must be a positive integer"))
		return
	}
	view, created, err := s.workflows.RetryTask(r.PathValue("execution_id"), r.PathValue("task_id"), workflow.RetryRequest{
		RequestID:              body.RequestID,
		ExpectedAttempt:        body.ExpectedAttempt,
		ConfirmPreviousStopped: body.ConfirmPreviousStopped,
	})
	if err != nil {
		writeJSONWorkflowError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, view)
}

func (s *Server) handleWorkflowVerdictOverride(w http.ResponseWriter, r *http.Request) {
	if s.workflows == nil {
		writeError(w, http.StatusBadRequest, workflow.ErrNoDefinition)
		return
	}
	attempt, err := strconv.Atoi(r.PathValue("attempt"))
	if err != nil || attempt < 1 {
		writeError(w, http.StatusBadRequest, errors.New("attempt must be a positive integer"))
		return
	}
	var body workflowVerdictOverrideRequest
	if err := decodeStrictJSON(w, r.Body, &body); err != nil {
		writeJSONWorkflowError(w, err)
		return
	}
	if strings.TrimSpace(body.RequestID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("request_id is required"))
		return
	}
	if strings.TrimSpace(body.Verdict) == "" {
		writeError(w, http.StatusBadRequest, errors.New("verdict is required"))
		return
	}
	view, created, err := s.workflows.OverrideVerdict(r.PathValue("execution_id"), r.PathValue("task_id"), attempt, workflow.OverrideVerdictRequest{
		RequestID: body.RequestID,
		Verdict:   body.Verdict,
	})
	if err != nil {
		writeJSONWorkflowError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, view)
}

func (s *Server) controlWorkflow(w http.ResponseWriter, call func() (domain.WorkflowExecutionView, error)) {
	if s.workflows == nil {
		writeError(w, http.StatusBadRequest, workflow.ErrNoDefinition)
		return
	}
	view, err := call()
	if err != nil {
		writeJSONWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type workflowCreateRequest struct {
	RequestID string `json:"request_id"`
}

type workflowCancelRequest struct {
	ConfirmPreviousStopped bool `json:"confirm_previous_stopped"`
}

type workflowRetryRequest struct {
	RequestID              string `json:"request_id"`
	ExpectedAttempt        int    `json:"expected_attempt"`
	ConfirmPreviousStopped bool   `json:"confirm_previous_stopped"`
}

type workflowVerdictOverrideRequest struct {
	RequestID string `json:"request_id"`
	Verdict   string `json:"verdict"`
}

func decodeStrictJSON(w http.ResponseWriter, body io.Reader, target any) error {
	var reader io.Reader = body
	if closer, ok := body.(io.ReadCloser); ok {
		reader = http.MaxBytesReader(w, closer, maxRunRequestBody)
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if err == nil {
		var extra any
		if trailingErr := decoder.Decode(&extra); trailingErr != io.EOF {
			if trailingErr != nil {
				err = trailingErr
			} else {
				err = errors.New("expected one JSON object")
			}
		}
	}
	if err != nil {
		return fmt.Errorf("%w: %v", errInvalidBody, err)
	}
	return nil
}

func workflowWaitTimeout(r *http.Request) (time.Duration, error) {
	value := r.URL.Query().Get("timeout_seconds")
	if value == "" {
		return defaultWorkflowWaitTimeout, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 || seconds > int(maxWorkflowWaitTimeout/time.Second) {
		return 0, errors.New("timeout_seconds must be an integer between 1 and 600")
	}
	return time.Duration(seconds) * time.Second, nil
}

func writeJSONWorkflowErrorOrValue(w http.ResponseWriter, err error, writeValue func()) {
	if err != nil {
		writeJSONWorkflowError(w, err)
		return
	}
	writeValue()
}

func writeJSONWorkflowError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("request body exceeds 1 MiB limit"))
	case errors.Is(err, errInvalidBody), isJSONSyntaxError(err):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, workflow.ErrExecutionNotFound),
		errors.Is(err, workflow.ErrTaskNotFound),
		errors.Is(err, workflow.ErrAttemptNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, workflow.ErrInvalidRequest),
		errors.Is(err, workflow.ErrNoDefinition):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, workflow.ErrDefinitionChanged),
		errors.Is(err, workflow.ErrExecutionActive),
		errors.Is(err, workflow.ErrInvalidTransition),
		errors.Is(err, workflow.ErrRetryConflict),
		errors.Is(err, workflow.ErrVerdictConflict),
		errors.Is(err, workflow.ErrUncertaintyUnresolved),
		errors.Is(err, workflow.ErrWorkerActive):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

func isJSONSyntaxError(err error) bool {
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	return errors.As(err, &syntaxError) || errors.As(err, &typeError)
}
