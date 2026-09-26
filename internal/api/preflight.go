package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// writePreflightOrError renders a failed backend-installation admission as a
// structured 503 with the sanitized aggregate report; every other error keeps
// its existing JSON error shape. Caller cancellation produces no synthetic
// response: the request is already gone, and no 408 or custom 499 contract
// exists.
func writePreflightOrError(w http.ResponseWriter, err error) {
	var preflight *domain.PreflightError
	if errors.As(err, &preflight) && preflight.Report != nil {
		writeJSON(w, http.StatusServiceUnavailable, preflightResponse{
			Error:  preflight.Report.AggregateText(),
			Code:   domain.PreflightErrorCode,
			Issues: preflight.Report.Issues,
		})
		return
	}
	if errors.Is(err, context.Canceled) {
		// The submitting request went away; stop processing without writing
		// a fabricated response body.
		return
	}
	writeError(w, http.StatusInternalServerError, err)
}

type preflightResponse struct {
	Error  string                  `json:"error"`
	Code   string                  `json:"code"`
	Issues []domain.PreflightIssue `json:"issues"`
}
