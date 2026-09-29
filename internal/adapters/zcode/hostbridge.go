package zcode

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"
)

// hostBridge performs the narrow, source-defined registry-view read through
// the guarded JS host. The host owns native registry structures; Go owns the
// selection policy. Secrets never cross this bridge.
type hostBridge struct {
	call func(ctx context.Context, method string, params any, result any) error
}

// routingFailureKind is the typed failure the host reports for a guarded
// read.
type routingFailureKind string

const (
	readFailureAuth    routingFailureKind = "auth"
	readFailureNetwork routingFailureKind = "network"
	readFailureSchema  routingFailureKind = "schema"
)

// readOutcome is the bounded result of one host read: either a normalized
// payload or a typed failure kind with a safe message.
type readOutcome struct {
	ok       bool
	kind     routingFailureKind
	payload  json.RawMessage
	failure  string
	httpCode int
}

// readEnvelope is the bounded result contract of the host read: either a
// normalized payload or a typed failure. No credential or raw body ever
// reaches Go; messages are fixed templates.
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

func (h *hostBridge) read(ctx context.Context, method string, params any) readOutcome {
	var envelope readEnvelope
	if err := h.call(ctx, method, params, &envelope); err != nil {
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

// readRegistryView carries the account evidence Go resolved: the shim applies
// exactly this entitlement to its guarded registry instance before projecting
// the selectable view. Without it the fail-closed account source enumerates no
// entitled models.
func (h *hostBridge) readRegistryView(ctx context.Context, evidence overlayRequest) readOutcome {
	return h.read(ctx, "squad/readRegistryView", evidence)
}

// overlayRequest carries the account overlay to the host. The host attaches
// the actual native builtin revision it read during bootstrap; the configured
// Individual provider is marked entitled and current.
type overlayRequest struct {
	Provider string `json:"provider"`
	Entitled bool   `json:"entitled"`
	Current  bool   `json:"current"`
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
