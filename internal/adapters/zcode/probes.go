package zcode

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Startup wire-compatibility probes. Before creating or resuming a
// conversation, every method Squad requires must reject schema-invalid empty
// parameters with the expected invalid-params code, proving the method exists
// and parses before it works. An unexpected success or a method-not-found
// error fails as incompatible. The whole set completes within one
// fifteen-second startup budget, never fifteen seconds per method.

const probeBudget = 15 * time.Second

// jsonRPCMethodNotFound is the code a missing method returns; parse failures
// return invalid params instead.
const jsonRPCMethodNotFound = -32601

// jsonRPCInvalidParams is the expected rejection code for schema-invalid
// parameters at the inspected baseline, where parseParams precedes work.
const jsonRPCInvalidParams = -32602

// requiredWireMethods are the legacy session operations Squad depends on.
// runtime/capabilities is optional corroboration and is never probed as a
// requirement.
var requiredWireMethods = []string{
	"session/create",
	"session/resume",
	"session/send",
	"session/subscribe",
	"session/setMode",
	"session/stop",
	"session/close",
	"session/subagents",
	"session/read",
	"session/cancelBackgroundTask",
}

// probeWireCompatibility runs the bounded parse-first probe set. No session
// is created or resumed, no prompt is sent, and no connectivity or model tool
// runs; a probe error never authorizes an auth or account callback.
func probeWireCompatibility(ctx context.Context, call func(ctx context.Context, method string, params any, result any) error) error {
	deadline := time.Now().Add(probeBudget)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for _, method := range requiredWireMethods {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("zcode runtime wire check exceeded its fifteen-second startup budget at %s", method)
		}
		probeCtx, cancel := context.WithTimeout(ctx, remaining)
		err := call(probeCtx, method, map[string]any{}, nil)
		cancel()
		var wireErr *wireProbeError
		switch {
		case err == nil:
			// An unexpected success on invalid parameters is evidence of a
			// different dialect, never of support.
			return fmt.Errorf("zcode runtime accepted empty parameters for %s; the wire dialect is incompatible", method)
		case errors.As(err, &wireErr):
			if wireErr.Code == jsonRPCMethodNotFound {
				return fmt.Errorf("zcode runtime is missing the required method %s; the installation is not wire-compatible", method)
			}
			if wireErr.Code != jsonRPCInvalidParams {
				return fmt.Errorf("zcode runtime rejected the %s probe with an unexpected error; the wire dialect is incompatible", method)
			}
		default:
			// Transport-level failures keep their own diagnostics; a probe
			// that never reached the runtime cannot certify compatibility.
			return fmt.Errorf("zcode runtime wire check for %s could not complete: %v", method, err)
		}
	}
	return nil
}
