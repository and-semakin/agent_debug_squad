package zcode

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/cursor"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/promptfmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/procgroup"
)

//go:embed host.cjs
var hostSource string

const defaultRuntime = "/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs"

// Descendant ownership traversal bounds. Exceeding either cap is an explicit
// incomplete-ownership failure, never silent success.
const (
	maxTraversalPages       = 100
	maxTraversalDescendants = 10000
)

type Adapter struct {
	spec   domain.AgentSpec
	mu     sync.Mutex
	active *activeRun
	// Overridden only by protocol tests; production always uses the guarded host.
	command func() *exec.Cmd
}
type approval struct {
	request domain.PermissionRequest
	wireID  json.RawMessage
	options []permissionOption
}
type permissionOption struct {
	Kind     string          `json:"kind"`
	Response json.RawMessage `json:"response"`
}
type replyJob struct {
	ctx    context.Context
	id     string
	reply  domain.PermissionReply
	result chan error
}
type activeRun struct {
	id      string
	session string
	turn    string
	input   string
	c       *client
	sink    domain.RunSink
	done    chan struct{}
	replies chan replyJob
	// hostErr carries explicit turn failures raised by the dispatcher, such as
	// unsupported interactions.
	hostErr    chan error
	pending    map[string]approval
	seen       map[string]bool
	children   map[string]domain.SubagentProgress
	baseline   map[string]bool
	background *backgroundTasks
	// stateMu guards the mutable run maps and identity fields, which the
	// dispatcher goroutine and the Send loop now touch concurrently.
	stateMu    sync.Mutex
	generation uint64
	workspace  string
	provider   string
	model      string
	// preferencesSessionID records the one bootstrap association granted while
	// the create/resume result is outstanding; the returned snapshot's session
	// ID must agree with it. preferencesGranted counts granted associations.
	preferencesSessionID string
	preferencesGranted   int
	continued            bool
	decision             routingDecision
	lastActivity         time.Time
}
type snapshot struct {
	// SessionRaw keeps the verified session surface (including the
	// backgroundJobs list) for ownership tracking; SessionID is the decoded
	// identity used for correlation.
	SessionRaw json.RawMessage `json:"-"`
	Session    struct {
		SessionID string `json:"sessionId"`
	} `json:"session"`
	Settings struct {
		Model json.RawMessage `json:"model"`
	} `json:"settings"`
}

// decodeSnapshot decodes a create/resume snapshot and preserves the raw
// session object for background job tracking.
func decodeSnapshot(raw []byte, snap *snapshot) error {
	var envelope struct {
		Session struct {
			SessionID string `json:"sessionId"`
		} `json:"session"`
		Settings struct {
			Model json.RawMessage `json:"model"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	snap.Session = envelope.Session
	snap.Settings = envelope.Settings
	var full struct {
		Session json.RawMessage `json:"session"`
	}
	if err := json.Unmarshal(raw, &full); err == nil {
		snap.SessionRaw = full.Session
	}
	return nil
}

type event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
	Payload   struct {
		InputID    string          `json:"inputId"`
		RequestID  string          `json:"requestId"`
		TaskID     string          `json:"taskId"`
		Response   string          `json:"response"`
		ResultType string          `json:"resultType"`
		Error      json.RawMessage `json:"error"`
	} `json:"payload"`
}

func New(spec domain.AgentSpec) *Adapter { return &Adapter{spec: spec} }
func (a *Adapter) option(name, fallback string) string {
	if value := a.spec.StringOptions[name]; value != "" {
		return value
	}
	return fallback
}
func (a *Adapter) planPolicy() string         { return a.option("plan_policy", PlanPolicyFixed) }
func (a *Adapter) requestedModel() string     { return a.option("model", "GLM-5.3-Flash") }
func (a *Adapter) requestedReasoning() string { return a.option("reasoning", "low") }

// selection builds the complete model selection for the effective provider.
// Every send passes the full selection; switching providers never changes the
// requested model or reasoning.
func (a *Adapter) selection(provider string) map[string]any {
	return map[string]any{"providerId": provider, "modelId": a.requestedModel(), "options": map[string]string{"reasoningLevel": a.requestedReasoning()}}
}

func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if err := validateAgentOptions(a.planPolicy(), a.option("provider", ""), a.requestedReasoning()); err != nil {
		return state, err
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = a.requestedModel()
	state.StartupPrompt = spec.StartupPrompt
	state.Status = domain.AgentIdle
	return state, nil
}
func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	state.Status = domain.AgentIdle
	return state, ctx.Err()
}
func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.BackendSessionID = ""
	state.LastRunID = ""
	state.LastError = nil
	return a.Init(ctx, spec, state)
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (result domain.RunResult, next domain.AgentState, retErr error) {
	next = state
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	r := &activeRun{
		id: run.RunID, sink: sink, done: make(chan struct{}), replies: make(chan replyJob), hostErr: make(chan error, 8),
		pending: map[string]approval{}, seen: map[string]bool{}, children: map[string]domain.SubagentProgress{},
		baseline: map[string]bool{}, background: newBackgroundTasks(),
		generation: nextGeneration(), workspace: state.WorkspaceDir,
		model: a.requestedModel(), lastActivity: time.Now().UTC(),
	}
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return result, next, errors.New("zcode agent already has an active run")
	}
	a.active = r
	a.mu.Unlock()
	var c *client
	stderrLines := 0
	defer func() {
		a.mu.Lock()
		a.active = nil
		a.mu.Unlock()
		close(r.done)
		r.stateMu.Lock()
		pending := r.pending
		r.pending = map[string]approval{}
		children := r.children
		r.stateMu.Unlock()
		for id := range pending {
			delete(pending, id)
		}
		for id, child := range children {
			if child.Status == "running" || child.Status == "waiting" || child.Status == "blocked" {
				child.Status = "cancelled"
				child.LastActivityAt = time.Now().UTC()
				children[id] = child
			}
		}
		// Every final terminal path shares the five-second cleanup deadline
		// before the owned process group is released; a blocked writer or an
		// already-exited leader never extends it.
		if c != nil {
			r.stateMu.Lock()
			descendants := make([]string, 0, len(children))
			for id := range children {
				descendants = append(descendants, id)
			}
			session := r.session
			r.stateMu.Unlock()
			sort.Strings(descendants)
			if err := runTerminalCleanup(c.call, cleanupSpec{session: session, descendants: descendants, background: r.background, stopRoot: true}); err != nil && ctx.Err() == nil {
				// A cleanup failure prevents clean success; the final text is
				// retained as evidence with an explicit cleanup failure.
				if retErr == nil {
					retErr = fmt.Errorf("zcode terminal cleanup failed: %w", err)
				}
			}
			c.close()
		}
		r.publish()
		next.Status = domain.AgentIdle
		result.BackendSessionID = next.BackendSessionID
		if ctx.Err() != nil {
			retErr = ctx.Err()
		}
		if retErr != nil {
			if stderrLines > 0 && ctx.Err() == nil {
				// Bounded safe operational provenance: a count only, never the
				// raw stderr content.
				retErr = fmt.Errorf("%w (host reported %d stderr lines; content suppressed)", retErr, stderrLines)
			}
			result.ErrorMessage = retErr.Error()
		}
	}()
	if err := ctx.Err(); err != nil {
		return result, next, err
	}
	// Raw runtime stderr never reaches errors or artifacts. Lines are counted
	// for a bounded safe provenance note; the host emits fixed typed errors
	// through the protocol for anything actionable.
	cmd := exec.Command(a.option("command", "node"), "-e", hostSource, a.option("runtime_path", defaultRuntime))
	if a.command != nil {
		cmd = a.command()
	}
	cmd.Dir = state.WorkspaceDir
	cmd.Env = cursor.BuildEnv(a.spec, os.Environ())
	started, err := startClient(cmd, func(line string) {
		stderrLines++
	})
	if err != nil {
		return result, next, fmt.Errorf("start zcode host: %w", err)
	}
	c = started
	r.c = c
	// A watchdog also bounds writes/shutdown when the child stops reading stdin.
	watchdog := context.AfterFunc(ctx, func() {
		select {
		case <-c.exited:
		case <-time.After(3 * time.Second):
			procgroup.Kill(cmd)
		}
	})
	defer watchdog()

	c.setHandler(r.hostRequest)

	// The bootstrap resolves credentials under the policy's identity rule and
	// starts the app-server; its typed failure maps to auth-required.
	var boot struct {
		OK            bool   `json:"ok"`
		Kind          string `json:"kind,omitempty"`
		Message       string `json:"message,omitempty"`
		Revision      string `json:"revision,omitempty"`
		HasStart      bool   `json:"hasStart"`
		HasIndividual bool   `json:"hasIndividual"`
		IdentityMatch bool   `json:"identityMatch"`
	}
	bootstrapCtx, bootstrapCancel := context.WithTimeout(ctx, rpcTimeout)
	err = c.call(bootstrapCtx, "squad/bootstrap", map[string]any{"generation": r.generation, "workspace": state.WorkspaceDir, "planPolicy": a.planPolicy()}, &boot)
	bootstrapCancel()
	if err != nil {
		return result, next, err
	}
	if !boot.OK {
		failure := errors.New(boot.Message)
		if boot.Kind == "auth" {
			failure = routingError{code: CodeRoutingAuthRequired, safe: boot.Message}
		}
		return result, next, failure
	}

	// Wire compatibility is established before any conversation exists.
	if err = probeWireCompatibility(ctx, c.call); err != nil {
		return result, next, err
	}

	// Account-bound eligibility precedes every new Squad turn.
	engine := newRoutingEngine(&hostBridge{call: c.call})
	eligibilityIn := eligibility{policy: a.planPolicy(), requestedModel: a.requestedModel(), requestedLevel: a.requestedReasoning(), provider: a.option("provider", "")}
	var decision routingDecision
	if a.planPolicy() == PlanPolicyStartFirst {
		decision, err = engine.decideStartFirst(ctx, eligibilityIn)
	} else {
		decision, err = engine.decideFixed(ctx, eligibilityIn)
	}
	if err != nil {
		if routingErr, ok := err.(routingError); ok {
			emitRoutingDiagnostic(sink, a.requestedModel(), a.planPolicy(), routingDecision{ReasonCode: routingErr.code}, r.id)
		}
		return result, next, err
	}
	r.stateMu.Lock()
	r.provider = decision.Provider
	r.decision = decision
	r.stateMu.Unlock()
	if decision.ReasonCode != "" {
		emitRoutingDiagnostic(sink, a.requestedModel(), a.planPolicy(), decision, r.id)
	}
	// The overlay carries the evidence verdict; the host attaches the actual
	// native builtin revision it read during bootstrap.
	overlayCtx, overlayCancel := context.WithTimeout(ctx, rpcTimeout)
	err = c.call(overlayCtx, "squad/applyAccountOverlay", overlayRequest{Provider: decision.Provider, Entitled: true, Current: true}, nil)
	overlayCancel()
	if err != nil {
		return result, next, err
	}

	mode := "yolo"
	if a.spec.Yolo != nil && !*a.spec.Yolo {
		mode = "build"
	}
	var snapRaw json.RawMessage
	fresh := state.BackendSessionID == ""
	if fresh {
		err = c.call(ctx, "session/create", map[string]any{"workspace": map[string]string{"workspaceKey": state.WorkspaceDir, "workspacePath": state.WorkspaceDir}, "mode": mode, "model": a.selection(decision.Provider), "titleGenerationEnabled": false}, &snapRaw)
	} else {
		err = c.call(ctx, "session/resume", map[string]any{"sessionId": state.BackendSessionID}, &snapRaw)
	}
	if err != nil {
		return result, next, err
	}
	var snap snapshot
	if err = decodeSnapshot(snapRaw, &snap); err != nil {
		return result, next, fmt.Errorf("zcode returned an unreadable session snapshot: %w", err)
	}
	r.stateMu.Lock()
	r.session = snap.Session.SessionID
	session := r.session
	r.stateMu.Unlock()
	if session == "" {
		return result, next, errors.New("zcode returned an empty session ID")
	}
	if !fresh && session != state.BackendSessionID {
		return result, next, errors.New("zcode resumed a different session")
	}
	if err = r.reconcilePreferences(session); err != nil {
		return result, next, err
	}
	next.BackendSessionID = session
	r.background.observeSessionSnapshot(snap.SessionRaw)
	if len(snap.Settings.Model) > 0 {
		record, _ := json.Marshal(map[string]any{"type": "zcode.models", "model": snap.Settings.Model})
		domain.ReportRunDiagnostic(sink, string(record))
	}
	if err = c.call(ctx, "session/setMode", map[string]any{"sessionId": session, "mode": mode}, nil); err != nil {
		return result, next, err
	}
	if !fresh {
		if err = r.pollChildren(ctx, true); err != nil {
			return result, next, err
		}
	}
	if err = c.call(ctx, "session/subscribe", map[string]any{"sessionId": session, "deliveryKind": "desktop-continuous"}, nil); err != nil {
		return result, next, err
	}
	message := run.Message
	if fresh {
		startup := state.StartupPrompt
		if startup == "" {
			startup = a.spec.StartupPrompt
		}
		message = promptfmt.WithStartupPrompt(startup, message)
	}
	r.stateMu.Lock()
	r.input = run.RunID
	if r.input == "" {
		r.input = fmt.Sprintf("squad-%d", time.Now().UnixNano())
	}
	input := r.input
	r.stateMu.Unlock()
	var accepted struct {
		Accepted bool `json:"accepted"`
	}
	if err = c.call(ctx, "session/send", map[string]any{"sessionId": session, "content": message, "inputId": input, "modelSelection": a.selection(decision.Provider)}, &accepted); err != nil {
		return result, next, err
	}
	if !accepted.Accepted {
		return result, next, errors.New("zcode did not accept the input")
	}
	r.publish()
	result, retErr = r.awaitTurn(ctx, a)
	return result, next, retErr
}

// awaitTurn consumes events until the owned turn completes, fails, or the
// caller cancels. A confirmed quota-exhaustion failure under start-first
// triggers the single automatic continuation defined by the change.
func (r *activeRun) awaitTurn(ctx context.Context, a *Adapter) (domain.RunResult, error) {
	var result domain.RunResult
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-r.c.done:
			return result, r.c.failure()
		case err := <-r.hostErr:
			return result, err
		case job := <-r.replies:
			job.result <- r.reply(job)
		case <-ticker.C:
			r.stateMu.Lock()
			turn := r.turn
			r.stateMu.Unlock()
			if turn == "" {
				continue
			}
			if err := r.pollChildren(ctx, false); err != nil {
				return result, err
			}
		case msg := <-r.c.events:
			if msg.Method == "interaction/requestPermission" {
				if err := r.callback(msg); err != nil {
					return result, err
				}
				continue
			}
			var e event
			if err := json.Unmarshal(msg.Params, &e); err != nil {
				return result, errors.New("invalid zcode event")
			}
			r.stateMu.Lock()
			session := r.session
			r.stateMu.Unlock()
			if e.SessionID != session {
				continue
			}
			r.sink.StdoutLine(string(msg.Params))
			if err := r.sink.Err(); err != nil {
				return result, err
			}
			r.stateMu.Lock()
			r.lastActivity = time.Now().UTC()
			if e.Type == "permission.resolved" {
				if _, ok := r.pending[e.Payload.RequestID]; ok {
					delete(r.pending, e.Payload.RequestID)
				}
			}
			input := r.input
			turn := r.turn
			continued := r.continued
			r.stateMu.Unlock()
			if e.Type == "turn.failed" && e.Payload.InputID == input {
				if continued {
					// The one Individual continuation failed: the run ends
					// without a loop or switch back.
					emitContinuationDiagnostic(r.sink, quotaExhaustionEvidence{
						FromProvider: ProviderStart, ToProvider: ProviderIndividual,
						Reason: "continuation_failed", AttemptID: r.id, InputID: input,
					}, CodeRoutingContinuationFailed)
					return result, fmt.Errorf("zcode continuation turn failed: %s", e.Payload.Error)
				}
				if a.planPolicy() != PlanPolicyStartFirst || !classifyQuotaExhaustion(e.Payload.Error) {
					return result, fmt.Errorf("zcode turn failed: %s", e.Payload.Error)
				}
				if err := r.transitionToIndividual(ctx, a, e.Payload.Error); err != nil {
					return result, err
				}
				// The continuation owns the run now; keep waiting.
				continue
			}
			if e.Type == "turn.started" && e.Payload.InputID == input {
				r.stateMu.Lock()
				r.turn = e.TurnID
				r.stateMu.Unlock()
				r.publish()
				continue
			}
			r.stateMu.Lock()
			turn = r.turn
			r.stateMu.Unlock()
			if turn != "" && e.TurnID == turn {
				switch e.Type {
				case "turn.failed":
					return result, fmt.Errorf("zcode turn failed: %s", e.Payload.Error)
				case "turn.completed":
					if e.Payload.ResultType != "success" {
						return result, fmt.Errorf("zcode turn ended with result %q", e.Payload.ResultType)
					}
					if strings.TrimSpace(e.Payload.Response) == "" {
						return result, errors.New("zcode completed without new assistant response text")
					}
					if err := r.pollChildren(ctx, false); err != nil {
						return result, err
					}
					result.FinalMessage = e.Payload.Response
					return result, nil
				}
			}
		}
	}
}

// transitionToIndividual performs the single Start-to-Individual continuation:
// retire old permissions, drain owned old-attempt work, refresh Individual
// eligibility with fresh evidence, and send the short continuation input
// under the original deadline.
func (r *activeRun) transitionToIndividual(ctx context.Context, a *Adapter, failurePayload json.RawMessage) error {
	r.stateMu.Lock()
	if r.continued {
		r.stateMu.Unlock()
		return errors.New("zcode quota continuation already used for this run")
	}
	r.continued = true
	session := r.session
	provider := r.provider
	r.stateMu.Unlock()
	_ = provider
	_ = failurePayload

	// Old pending permissions and auth requests become inactive.
	r.stateMu.Lock()
	r.pending = map[string]approval{}
	r.stateMu.Unlock()
	r.publish()

	// Drain positively owned descendant/background work from the failed
	// attempt; uncertainty fails the run rather than overlapping attempts.
	r.stateMu.Lock()
	descendants := make([]string, 0, len(r.children))
	for id := range r.children {
		descendants = append(descendants, id)
	}
	r.stateMu.Unlock()
	sort.Strings(descendants)
	if err := runTerminalCleanup(r.c.call, cleanupSpec{session: session, descendants: descendants, background: r.background, stopRoot: false}); err != nil {
		return fmt.Errorf("old-attempt cleanup could not be established: %w", err)
	}

	// Fresh eligibility for the same account, model, and reasoning; cached
	// pre-failure evidence is bypassed.
	engine := newRoutingEngine(&hostBridge{call: r.c.call})
	engine.invalidate("individual-subscription")
	engine.invalidate("registry-view")
	engine.invalidate("start-balance")
	decision, err := engine.decideIndividual(ctx, eligibility{
		policy: a.planPolicy(), requestedModel: a.requestedModel(), requestedLevel: a.requestedReasoning(), provider: ProviderIndividual,
	}, CodeRoutingStartExhausted)
	if err != nil {
		emitRoutingDiagnostic(r.sink, a.requestedModel(), a.planPolicy(), routingDecision{ReasonCode: routingErrorCode(err)}, r.id)
		return fmt.Errorf("Individual eligibility is uncertain after quota exhaustion: %w", err)
	}
	if decision.Provider != ProviderIndividual {
		return errors.New("Individual is not available for the same selection; no continuation was dispatched")
	}
	emitRoutingDiagnostic(r.sink, a.requestedModel(), a.planPolicy(), routingDecision{ReasonCode: CodeRoutingStartExhausted, Provider: ProviderIndividual, ObservedAt: decision.ObservedAt, hasObservedAt: decision.hasObservedAt}, r.id)

	overlayCtx, overlayCancel := context.WithTimeout(ctx, rpcTimeout)
	overlayErr := r.c.call(overlayCtx, "squad/applyAccountOverlay", overlayRequest{Provider: ProviderIndividual, Entitled: true, Current: true}, nil)
	overlayCancel()
	if overlayErr != nil {
		return overlayErr
	}

	r.stateMu.Lock()
	gate := continuationGate{
		hasDeadline:  false,
		individualOK: true,
		cleanupOK:    true,
	}
	if ctxDeadline, ok := ctx.Deadline(); ok {
		gate.deadline = ctxDeadline
		gate.hasDeadline = true
	}
	if ctx.Err() != nil {
		gate.cancelled = true
	}
	input := r.input
	r.stateMu.Unlock()
	if err := gate.admit(); err != nil {
		if errors.Is(err, errContinuationBudgetShort) {
			emitRoutingDiagnostic(r.sink, a.requestedModel(), a.planPolicy(), routingDecision{ReasonCode: CodeContinuationBudgetShort}, r.id)
		}
		return err
	}
	continuationInput := fmt.Sprintf("%s-continuation", input)
	sendCtx, sendCancel := context.WithTimeout(ctx, rpcTimeout)
	err = runContinuation(sendCtx, r.c.call, session, continuationInput, a.selection(ProviderIndividual))
	sendCancel()
	if err != nil {
		emitContinuationDiagnostic(r.sink, quotaExhaustionEvidence{
			FromProvider: ProviderStart, ToProvider: ProviderIndividual,
			Reason: "continuation_send_failed", AttemptID: r.id, InputID: continuationInput,
		}, CodeRoutingContinuationFailed)
		return err
	}
	r.stateMu.Lock()
	r.input = continuationInput
	r.turn = ""
	r.provider = ProviderIndividual
	r.stateMu.Unlock()
	emitContinuationDiagnostic(r.sink, quotaExhaustionEvidence{
		FromProvider: ProviderStart, ToProvider: ProviderIndividual,
		Reason: CodeRoutingStartExhausted, AttemptID: r.id, InputID: continuationInput,
	}, CodeRoutingContinuationStarted)
	return nil
}

func routingErrorCode(err error) string {
	if routingErr, ok := err.(routingError); ok {
		return routingErr.code
	}
	return CodeRoutingIndividualUnknown
}

// hostRequest is the dispatcher's inbound table. It runs on the dedicated
// dispatcher goroutine while ordinary RPCs are pending, and never blocks the
// reader: every response is bounded, and unknown operations get an explicit
// method-not-supported failure.
func (r *activeRun) hostRequest(msg wireMessage) {
	switch msg.Method {
	case "session/requestRuntimePreferences":
		r.preferences(msg)
	case "squad/authorizeProviderHeaders":
		r.authorizeHeaders(msg)
	case "interaction/providerRuntimeHeadersCancelled":
		// Notification: the runtime cancelled a pending auth request. Squad
		// holds no pending material; late results are discarded by binding.
		return
	case "interaction/requestUserInput":
		_ = r.c.respond(context.Background(), map[string]any{"id": msg.ID, "error": wireError{Code: -32601, Message: "Unsupported Squad host interaction: interactive questions are not supported"}})
		select {
		case r.hostErr <- errors.New("zcode requested an interactive questionnaire Squad cannot represent"):
		default:
		}
	default:
		_ = r.c.respond(context.Background(), map[string]any{"id": msg.ID, "error": wireError{Code: -32601, Message: "Unsupported Squad host interaction: " + msg.Method}})
		select {
		case r.hostErr <- fmt.Errorf("unsupported zcode host interaction: %s", msg.Method):
		default:
		}
	}
}

// preferences answers the runtime preferences before the create result is
// known. One schema-valid bootstrap association is permitted per outstanding
// create/resume; a session-scoped request must match the active session, and
// the association never grants tool or credential access.
func (r *activeRun) preferences(msg wireMessage) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(msg.Params, &params)
	respond := func() {
		// Current Squad policy: no native search enhancements, no memory, and
		// no automatic question resolution. Upstream fallback defaults that
		// enable question auto-resolution are never used.
		_ = r.c.respond(context.Background(), map[string]any{"id": msg.ID, "result": map[string]any{"nativeSearchEnhancementsEnabled": false, "memoryEnabled": false, "askUserQuestionAutoResolutionEnabled": false}})
	}
	reject := func() {
		_ = r.c.respond(context.Background(), map[string]any{"id": msg.ID, "error": wireError{Code: -32602, Message: "Runtime preferences do not belong to the active Squad session"}})
	}
	r.stateMu.Lock()
	session := r.session
	if session != "" {
		// Session known: the request must carry the active session identity.
		// A foreign identity is a hard protocol violation that fails the run,
		// not merely a rejected request.
		matches := params.SessionID == "" || params.SessionID == session
		r.stateMu.Unlock()
		if !matches {
			reject()
			return
		}
		respond()
		return
	}
	// Bootstrap window: the runtime names the new sessionId while the
	// create/resume result is outstanding. Exactly one association is
	// permitted per outstanding operation; a repeated request with the same
	// identity is answered identically, a different identity is rejected.
	if r.preferencesGranted == 0 || (r.preferencesSessionID != "" && r.preferencesSessionID == params.SessionID) {
		r.preferencesSessionID = params.SessionID
		r.preferencesGranted++
		r.stateMu.Unlock()
		respond()
		return
	}
	r.stateMu.Unlock()
	reject()
}

// reconcilePreferences verifies the bootstrap association against the
// returned snapshot identity. A mismatch fails the run before any turn work.
func (r *activeRun) reconcilePreferences(returnedSession string) error {
	r.stateMu.Lock()
	associated := r.preferencesSessionID
	granted := r.preferencesGranted
	r.preferencesSessionID = ""
	r.preferencesGranted = 0
	r.stateMu.Unlock()
	if granted > 0 && associated != "" && associated != returnedSession {
		return errors.New("zcode preference association does not match the returned session identity")
	}
	return nil
}

// authorizeHeaders validates a reverse auth request against the current
// generation, workspace, session, and the exact selected provider/model.
// Approval grants nothing else; the credential material itself never crosses
// into Go.
func (r *activeRun) authorizeHeaders(msg wireMessage) {
	var request authorizeHeadersRequest
	if err := json.Unmarshal(msg.Params, &request); err != nil {
		_ = r.c.respond(context.Background(), map[string]any{"id": msg.ID, "result": authorizeHeadersResult{Allow: false}})
		return
	}
	r.stateMu.Lock()
	allow := request.Generation == r.generation &&
		request.Workspace == r.workspace &&
		request.SessionID == r.session &&
		request.ProviderID == r.provider &&
		strings.TrimSpace(request.ModelID) == r.model &&
		r.session != ""
	r.stateMu.Unlock()
	_ = r.c.respond(context.Background(), map[string]any{"id": msg.ID, "result": authorizeHeadersResult{Allow: allow}})
}

func (r *activeRun) publish() {
	r.stateMu.Lock()
	progress := domain.RunProgress{Phase: domain.RunPhaseRunning, LastActivityAt: r.lastActivity}
	for _, child := range r.children {
		progress.Subagents = append(progress.Subagents, child)
		if child.Status == "running" || child.Status == "waiting" || child.Status == "blocked" {
			progress.Phase = domain.RunPhaseWaitingForSubagent
		}
		if progress.ChildLastActivityAt == nil || child.LastActivityAt.After(*progress.ChildLastActivityAt) {
			t := child.LastActivityAt
			progress.ChildLastActivityAt = &t
		}
	}
	for _, p := range r.pending {
		progress.PendingPermissions = append(progress.PendingPermissions, p.request)
	}
	if len(progress.PendingPermissions) > 0 {
		progress.Phase = domain.RunPhaseWaitingForPermission
	}
	r.stateMu.Unlock()
	sort.Slice(progress.Subagents, func(i, j int) bool { return progress.Subagents[i].ID < progress.Subagents[j].ID })
	sort.Slice(progress.PendingPermissions, func(i, j int) bool { return progress.PendingPermissions[i].ID < progress.PendingPermissions[j].ID })
	domain.ReportRunProgress(r.sink, progress)
}

type childSnapshot struct {
	ChildSessionID string `json:"childSessionId"`
	Status         string `json:"status"`
}

// pollChildren traverses the complete historical descendant baseline and all
// observed pages, bounded by explicit caps with cycle detection. Exceeding a
// cap is an incomplete-ownership failure, never silent success, and historical
// children can never become owned permission targets.
func (r *activeRun) pollChildren(ctx context.Context, baseline bool) error {
	queue := []string{r.session}
	visited := map[string]bool{}
	changed := false
	observed := 0
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		if visited[parent] {
			continue
		}
		visited[parent] = true
		cursorValue := ""
		for page := 0; ; page++ {
			if page >= maxTraversalPages {
				return errors.New("zcode descendant traversal exceeded its page budget; ownership is incomplete")
			}
			var snapshot struct {
				ChildSessionIDs []string        `json:"childSessionIds"`
				Running         []childSnapshot `json:"running"`
				Ended           struct {
					Items      []childSnapshot `json:"items"`
					NextCursor string          `json:"nextCursor"`
				} `json:"ended"`
			}
			params := map[string]any{"sessionId": parent, "endedLimit": 100}
			if cursorValue != "" {
				params["endedCursor"] = cursorValue
			}
			snapshot.Ended.NextCursor = ""
			if err := r.c.call(ctx, "session/subagents", params, &snapshot); err != nil {
				return err
			}
			observed += len(snapshot.ChildSessionIDs) + len(snapshot.Running) + len(snapshot.Ended.Items)
			if observed > maxTraversalDescendants {
				return errors.New("zcode descendant traversal exceeded its descendant budget; ownership is incomplete")
			}
			for _, id := range snapshot.ChildSessionIDs {
				if baseline {
					r.rememberBaseline(id)
					continue
				}
				_ = id
			}
			for _, child := range append(snapshot.Running, snapshot.Ended.Items...) {
				if child.ChildSessionID == "" {
					continue
				}
				if baseline {
					r.rememberBaseline(child.ChildSessionID)
					// Historical nested descendants join the baseline tree so
					// a later page can never claim them as newly owned.
					if !visited[child.ChildSessionID] {
						queue = append(queue, child.ChildSessionID)
					}
					continue
				}
				if r.knownBaseline(child.ChildSessionID) {
					continue
				}
				previous, ok := r.childStatus(child.ChildSessionID)
				if !ok || previous != child.Status {
					changed = true
					r.setChild(child.ChildSessionID, parent, child.Status)
				}
				if !visited[child.ChildSessionID] {
					queue = append(queue, child.ChildSessionID)
				}
			}
			if snapshot.Ended.NextCursor == "" {
				break
			}
			if snapshot.Ended.NextCursor == cursorValue {
				return errors.New("zcode subagent pagination repeated a cursor; ownership is incomplete")
			}
			cursorValue = snapshot.Ended.NextCursor
		}
	}
	if changed {
		r.publish()
	}
	return nil
}

func (r *activeRun) rememberBaseline(id string) {
	r.stateMu.Lock()
	r.baseline[id] = true
	r.stateMu.Unlock()
}

func (r *activeRun) knownBaseline(id string) bool {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.baseline[id]
}

func (r *activeRun) childStatus(id string) (string, bool) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	child, ok := r.children[id]
	return child.Status, ok
}

func (r *activeRun) setChild(id, parent, status string) {
	r.stateMu.Lock()
	r.children[id] = domain.SubagentProgress{ID: id, ParentID: parent, Status: status, LastActivityAt: time.Now().UTC()}
	r.stateMu.Unlock()
}
