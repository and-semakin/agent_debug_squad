package zcode

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"
)

// hostBridge performs the narrow, source-defined account reads through the
// guarded JS host. The host owns native credentials and the HTTP calls; Go
// owns policy, budgets, and normalization. Secrets never cross this bridge.
type hostBridge struct {
	call func(ctx context.Context, method string, params any, result any) error
}

// readEnvelope is the bounded result contract of every host read: either a
// normalized payload or a typed failure kind with a safe message. HTTP status
// codes ride along only to steer the retry policy.
type readEnvelope struct {
	OK       bool            `json:"ok"`
	Kind     string          `json:"kind,omitempty"`
	Message  string          `json:"message,omitempty"`
	HTTPCode int             `json:"httpCode,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

func failureKind(value string) routingFailureKind {
	switch value {
	case "auth":
		return readFailureAuth
	case "schema":
		return readFailureSchema
	default:
		return readFailureNetwork
	}
}

func (h *hostBridge) read(ctx context.Context, method string) readOutcome {
	var envelope readEnvelope
	if err := h.call(ctx, method, map[string]any{}, &envelope); err != nil {
		kind := readFailureNetwork
		if strings.Contains(err.Error(), "auth") {
			kind = readFailureAuth
		}
		return readOutcome{kind: kind, failure: "the guarded host read failed"}
	}
	if !envelope.OK {
		return readOutcome{kind: failureKind(envelope.Kind), failure: safeReadFailure(envelope.Message), httpCode: envelope.HTTPCode}
	}
	if len(envelope.Payload) == 0 {
		return readOutcome{kind: readFailureSchema, failure: "the guarded host read returned no evidence"}
	}
	return readOutcome{ok: true, payload: envelope.Payload}
}

// safeReadFailure keeps the host's fixed messages without any raw body.
func safeReadFailure(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "the guarded host read failed"
	}
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}

func (h *hostBridge) readStartBalance(ctx context.Context) readOutcome {
	return h.read(ctx, "squad/readStartBalance")
}

func (h *hostBridge) readIndividualSubscription(ctx context.Context) readOutcome {
	return h.read(ctx, "squad/readIndividualSubscription")
}

func (h *hostBridge) readRegistryView(ctx context.Context) readOutcome {
	return h.read(ctx, "squad/readRegistryView")
}

// overlayRequest carries the evidence-derived account overlay to the host.
// The host attaches the actual native builtin revision it read during
// bootstrap; entitled/current come from the routing evidence, never from
// unconditional hardcoded claims.
type overlayRequest struct {
	Provider string `json:"provider"`
	Entitled bool   `json:"entitled"`
	Current  bool   `json:"current"`
}

// pushOverlay forwards the evidence-based overlay through the host.
func (h *hostBridge) pushOverlay(ctx context.Context, request overlayRequest) error {
	return h.call(ctx, "squad/applyAccountOverlay", request, nil)
}

// authorizeHeaders validates a reverse auth request from the runtime. Go
// never sees the credential material; it only decides whether the request is
// bound to the current generation, workspace, owned session, and the exact
// selected provider/model.
type authorizeHeadersRequest struct {
	RequestID  string `json:"requestId"`
	SessionID  string `json:"sessionId"`
	TurnID     string `json:"turnId"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
	Workspace  string `json:"workspace"`
	Generation uint64 `json:"generation"`
}

type authorizeHeadersResult struct {
	Allow bool `json:"allow"`
}

// headerAuthorizer validates reverse auth bindings against the active run.
type headerAuthorizer interface {
	authorizeHeaders(request authorizeHeadersRequest) authorizeHeadersResult
}

// processGeneration increments for every spawned host process so stale
// requests from a previous generation can never authorize. The counter is
// atomic because parallel agent runs spawn hosts concurrently.
var generationCounter atomic.Uint64

func nextGeneration() uint64 { return generationCounter.Add(1) }

// authRequestDeadline bounds how long the runtime may wait for the host to
// supply provider runtime headers; the source uses 180 seconds, but Squad
// shares its run deadline and a tighter explicit failure is safer.
const authRequestDeadline = 60 * time.Second
