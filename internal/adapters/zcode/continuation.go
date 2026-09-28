package zcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// Continuation after a confirmed Start quota-exhaustion failure. This is one
// new native turn in the same backend session and logical Squad run: never a
// live model-step switch, never a resend of the original task or startup
// instructions, and at most one transition per Squad run.

// continuationMessage is the fixed short instruction sent to the existing
// conversation after the provider switch. It is never replaced by the
// original prompt.
const continuationMessage = "Please continue from where you stopped. Use the existing conversation and completed work; do not restart the task."

// continuationMinDeadline is the minimum remaining budget required on a
// finite original run deadline immediately before the continuation send.
const continuationMinDeadline = 5 * time.Second

// quotaExhaustionMarkers are the structured error markers this build accepts
// as verified quota-exhaustion evidence. The set is intentionally tiny and
// fixture-pinned: generic rate limits, admission busy, auth errors, network
// errors, unknown transport outcomes, and arbitrary error text never trigger
// the transition. The exact upstream exhaustion marker still requires the
// separately authorized read-only account verification (task 4.4); unverified
// shapes fail closed, which loses at most an automatic continuation.
var quotaExhaustionMarkers = map[string]bool{
	"start_plan_quota_exhausted": true,
}

// quotaExhaustionNegativeMarkers are structured signals that disqualify an
// otherwise matching payload: admission busy, rate limiting, and auth
// failures are never exhaustion.
var quotaExhaustionNegativeMarkers = map[string]bool{
	"model_rate_limited":                   true,
	"start_plan_busy":                      true,
	"start_plan_busy_auto_retry_exhausted": true,
	"authentication_required":              true,
}

// quotaExhaustionEvidence is the safe transition record: from/to provider,
// reason, and attempt/input IDs only, never auth or billing bodies.
type quotaExhaustionEvidence struct {
	FromProvider string
	ToProvider   string
	Reason       string
	AttemptID    string
	InputID      string
}

func emitContinuationDiagnostic(sink domain.RunSink, evidence quotaExhaustionEvidence, code string) {
	record, err := json.Marshal(map[string]any{
		"type":          "zcode.routing",
		"reason_code":   code,
		"from_provider": evidence.FromProvider,
		"to_provider":   evidence.ToProvider,
		"attempt_id":    evidence.AttemptID,
		"input_id":      evidence.InputID,
	})
	if err != nil {
		return
	}
	domain.ReportRunDiagnostic(sink, string(record))
}

// classifyQuotaExhaustion inspects a structured turn.failure error payload.
// Only objects carrying a fixture-verified exhaustion marker without any
// negative marker qualify; scalar payloads and unrecognized text never do.
func classifyQuotaExhaustion(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var structured struct {
		Code   string          `json:"code"`
		Type   string          `json:"type"`
		Name   string          `json:"name"`
		Nested json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &structured); err != nil {
		return false
	}
	marker := ""
	for _, candidate := range []string{structured.Code, structured.Type, structured.Name} {
		trimmed := strings.ToLower(strings.TrimSpace(candidate))
		if trimmed == "" {
			continue
		}
		if quotaExhaustionNegativeMarkers[trimmed] {
			return false
		}
		if quotaExhaustionMarkers[trimmed] {
			marker = trimmed
		}
	}
	if marker != "" {
		return true
	}
	// Provider-prefixed projections nest the failure under error; inspect one
	// level so a structured wrapper does not defeat the typed check.
	if len(structured.Nested) > 0 {
		return classifyQuotaExhaustion(structured.Nested)
	}
	return false
}

// continuationGate captures everything that must hold before the single
// automatic Start-to-Individual continuation is dispatched.
type continuationGate struct {
	deadline      time.Time
	hasDeadline   bool
	individualOK  bool
	cleanupOK     bool
	cancelled     bool
	alreadyFailed bool
}

// admit applies the transition rules: verified classification and known
// terminal outcome are checked by the caller; this gate enforces eligibility,
// cleanup certainty, cancellation, and the five-second minimum remaining
// budget checked immediately before send.
func (g continuationGate) admit() error {
	if g.alreadyFailed {
		return fmt.Errorf("the automatic Individual continuation already failed; no further automatic input is sent")
	}
	if g.cancelled {
		return fmt.Errorf("cancellation suppresses the automatic continuation")
	}
	if !g.individualOK {
		return fmt.Errorf("Individual eligibility for the same selection is uncertain; the run fails without dispatching overlapping work")
	}
	if !g.cleanupOK {
		return fmt.Errorf("old-attempt cleanup could not be established; the run fails rather than overlapping attempts")
	}
	if g.hasDeadline {
		remaining := time.Until(g.deadline)
		if remaining < continuationMinDeadline {
			return fmt.Errorf("%w: only %s remains of the original run deadline", errContinuationBudgetShort, remaining.Truncate(time.Millisecond))
		}
	}
	return nil
}

// errContinuationBudgetShort marks the too-little-time outcome, which is
// reported with the continuation_budget_insufficient diagnostic.
var errContinuationBudgetShort = errors.New("continuation budget insufficient")

// runContinuation dispatches the single continuation native turn. It shares
// the original caller deadline and cancellation; no fresh run timeout is
// granted.
func runContinuation(ctx context.Context, call func(ctx context.Context, method string, params any, result any) error, session, inputID string, selection map[string]any) error {
	var accepted struct {
		Accepted bool `json:"accepted"`
	}
	if err := call(ctx, "session/send", map[string]any{"sessionId": session, "content": continuationMessage, "inputId": inputID, "modelSelection": selection}, &accepted); err != nil {
		return err
	}
	if !accepted.Accepted {
		return fmt.Errorf("zcode did not accept the continuation input")
	}
	return nil
}
