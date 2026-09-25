package api

import (
	"fmt"
	"net/http"
)

// OneShotAdmission restricts a run-mode server to its selected execution.
// New submissions, manual run/reset mutations, controls of foreign
// executions, and permission replies of foreign runs are rejected with 409;
// read-only history and the selected execution's controls remain available.
type OneShotAdmission struct {
	ExecutionID string
	// OwnsRun reports whether a permission reply may target the run.
	OwnsRun func(runID string) bool
}

// SetOneShotAdmission installs run-mode admission. Calling it with the zero
// admission keeps normal serve behavior.
func (s *Server) SetOneShotAdmission(a OneShotAdmission) {
	s.oneShot = &a
}

// rejectOneShotMutation writes 409 when run mode forbids a mutation that
// always stays outside the selected execution (new workflows, manual
// run/reset).
func (s *Server) rejectOneShotMutation(w http.ResponseWriter, what string) bool {
	if s.oneShot == nil {
		return false
	}
	writeError(w, http.StatusConflict, fmt.Errorf("one-shot run mode rejects %s; only execution %s can be controlled", what, s.oneShot.ExecutionID))
	return true
}

// rejectOneShotForeign writes 409 when run mode receives a control targeting
// an execution other than the selected one.
func (s *Server) rejectOneShotForeign(w http.ResponseWriter, executionID string) bool {
	if s.oneShot == nil || executionID == s.oneShot.ExecutionID {
		return false
	}
	writeError(w, http.StatusConflict, fmt.Errorf("one-shot run mode rejects controls of execution %s; the selected execution is %s", executionID, s.oneShot.ExecutionID))
	return true
}

// rejectOneShotForeignRun writes 409 when run mode receives a permission
// reply for a run outside the selected execution.
func (s *Server) rejectOneShotForeignRun(w http.ResponseWriter, runID string) bool {
	if s.oneShot == nil || (s.oneShot.OwnsRun != nil && s.oneShot.OwnsRun(runID)) {
		return false
	}
	writeError(w, http.StatusConflict, fmt.Errorf("one-shot run mode rejects permission replies for run %s outside the selected execution %s", runID, s.oneShot.ExecutionID))
	return true
}
