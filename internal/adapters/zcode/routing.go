package zcode

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// Routing decision codes. They are allowlisted, safe, and distinct from wire,
// transport, and cleanup diagnostics. Raw balances, account identity, or
// environment values never appear in a routing diagnostic.
const (
	CodeRoutingStartAvailable      = "routing_start_available"
	CodeRoutingStartModelAbsent    = "routing_start_model_absent"
	CodeRoutingStartExpired        = "routing_start_expired"
	CodeRoutingStartExhausted      = "routing_start_exhausted"
	CodeRoutingTemporaryBusy       = "routing_temporary_busy"
	CodeRoutingBalanceUnknown      = "routing_balance_unknown"
	CodeRoutingIndividualUnknown   = "routing_individual_unknown"
	CodeRoutingIndividualUnavail   = "routing_individual_unavailable"
	CodeRoutingModelUnavailable    = "routing_model_unavailable"
	CodeRoutingAuthRequired        = "routing_auth_required"
	CodeRoutingContinuationStarted = "routing_continuation_started"
	CodeContinuationBudgetShort    = "continuation_budget_insufficient"
	CodeRoutingContinuationFailed  = "routing_continuation_failed"
)

// Numeric eligibility budgets. The decision budget bounds queueing, reads,
// registry projection, and waits; the caller's earlier deadline always wins.
const (
	routingDecisionBudget = 15 * time.Second
	routingAttemptBudget  = 5 * time.Second
	routingRetryDelay     = 250 * time.Millisecond
	routingBusyRecheck    = 1 * time.Second
	routingMaxAttempts    = 2
	routingEvidenceMaxAge = 1 * time.Second
)

// routingFailureKind is the typed failure the host reports for an account
// read; the retry policy keys off it.
type routingFailureKind string

const (
	readFailureAuth    routingFailureKind = "auth"
	readFailureNetwork routingFailureKind = "network"
	readFailureSchema  routingFailureKind = "schema"
)

type readOutcome struct {
	ok        bool
	kind      routingFailureKind
	payload   json.RawMessage
	failure   string
	httpCode  int
	identity  string
	startedAt time.Time
}

type routingDecision struct {
	Provider      string
	ReasonCode    string
	ObservedAt    time.Time
	hasObservedAt bool
}

// emitRoutingDiagnostic publishes the allowlisted decision evidence. Unknown
// timestamps stay absent rather than invented.
func emitRoutingDiagnostic(sink domain.RunSink, requestedModel, policy string, decision routingDecision, attemptID string) {
	record := map[string]any{
		"type":            "zcode.routing",
		"requested_model": requestedModel,
		"plan_policy":     policy,
		"reason_code":     decision.ReasonCode,
	}
	if decision.Provider != "" {
		record["effective_provider"] = decision.Provider
	}
	if decision.hasObservedAt {
		record["observed_at"] = decision.ObservedAt.UTC().Format(time.RFC3339)
	}
	if attemptID != "" {
		record["attempt_id"] = attemptID
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	domain.ReportRunDiagnostic(sink, string(encoded))
}

// sharedEvidence is one in-flight or completed account read joinable by
// decisions with a matching scope. A response is reusable only within one
// second of its request start and never across identity changes; a slow
// response remains usable by its initiator alone.
type sharedEvidence struct {
	ready     chan struct{}
	started   time.Time
	outcome   readOutcome
	initiator bool
}

// routingEngine executes the eligibility rules with fixed numeric budgets.
// Sharing is process-local; no persistent or cross-squad cache exists.
type routingEngine struct {
	host *hostBridge

	mu     sync.Mutex
	now    func() time.Time
	shared map[string]*sharedEvidence
}

func newRoutingEngine(host *hostBridge) *routingEngine {
	return &routingEngine{host: host, now: time.Now, shared: map[string]*sharedEvidence{}}
}

func (e *routingEngine) setClock(now func() time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = now
}

// invalidate drops any shareable evidence for a scope, used before the fresh
// quota-failure evaluation and the fresh busy recheck.
func (e *routingEngine) invalidate(scope string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.shared, scope)
}

// joinOrStart attaches to an in-flight read of the same scope or registers a
// new one. Joining is allowed only within the evidence age window; each
// waiter keeps its own deadline.
func (e *routingEngine) joinOrStart(scope string) (*sharedEvidence, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if existing, ok := e.shared[scope]; ok && now.Sub(existing.started) <= routingEvidenceMaxAge {
		return existing, false
	}
	entry := &sharedEvidence{ready: make(chan struct{}), started: now, initiator: true}
	e.shared[scope] = entry
	return entry, true
}

func (e *routingEngine) complete(scope string, entry *sharedEvidence, outcome readOutcome) {
	e.mu.Lock()
	defer e.mu.Unlock()
	entry.outcome = outcome
	close(entry.ready)
	if current, ok := e.shared[scope]; ok && current == entry {
		go func() {
			time.Sleep(routingEvidenceMaxAge)
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.shared[scope] == entry {
				delete(e.shared, scope)
			}
		}()
	}
}

// waitShared consumes joined evidence, honoring the caller deadline. An
// over-age result at receipt is unusable by a joiner.
func (e *routingEngine) waitShared(ctx context.Context, entry *sharedEvidence) (readOutcome, bool) {
	select {
	case <-entry.ready:
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.now().Sub(entry.started) > routingEvidenceMaxAge {
			return readOutcome{}, false
		}
		return entry.outcome, true
	case <-ctx.Done():
		return readOutcome{kind: readFailureNetwork, failure: ctx.Err().Error()}, false
	}
}

// endpointAttempts bounds automatic read attempts per endpoint per decision.
type endpointAttempts struct {
	used int
}

// readEndpoint performs one endpoint's reads under the shared limits: at most
// routingMaxAttempts attempts per decision, one automatic retry after 250 ms
// limited to transport failures and HTTP 502/503/504, each attempt bounded to
// five seconds and the remaining decision budget. The first read may join an
// in-flight request of the same scope; a joiner whose evidence aged out
// before receipt starts its own bounded request.
func (e *routingEngine) readEndpoint(ctx context.Context, scope string, counter *endpointAttempts, attempt func(context.Context) readOutcome) readOutcome {
	decisionDeadline := time.Now().Add(routingDecisionBudget)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(decisionDeadline) {
		decisionDeadline = ctxDeadline
	}
	if remaining := time.Until(decisionDeadline); remaining <= 0 {
		return readOutcome{kind: readFailureNetwork, failure: "decision budget exhausted"}
	}
	for pass := 0; pass < 2; pass++ {
		entry, initiator := e.joinOrStart(scope)
		if !initiator {
			outcome, usable := e.waitShared(ctx, entry)
			if usable {
				return outcome
			}
			if ctx.Err() != nil {
				return readOutcome{kind: readFailureNetwork, failure: ctx.Err().Error()}
			}
			// Over-age or failed share: obtain own evidence, once.
			continue
		}
		return e.performAttempts(ctx, decisionDeadline, counter, entry, scope, attempt)
	}
	return readOutcome{kind: readFailureNetwork, failure: "decision budget exhausted"}
}

func (e *routingEngine) performAttempts(ctx context.Context, decisionDeadline time.Time, counter *endpointAttempts, entry *sharedEvidence, scope string, attempt func(context.Context) readOutcome) readOutcome {
	var outcome readOutcome
	for {
		if used := e.now(); used.Sub(entry.started) > routingDecisionBudget || time.Until(decisionDeadline) <= 0 {
			outcome = readOutcome{kind: readFailureNetwork, failure: "decision budget exhausted"}
			break
		}
		if counter.used >= routingMaxAttempts {
			outcome = readOutcome{kind: readFailureNetwork, failure: "endpoint attempt cap reached"}
			break
		}
		counter.used++
		budget := time.Until(decisionDeadline)
		if budget > routingAttemptBudget {
			budget = routingAttemptBudget
		}
		callCtx, cancel := context.WithTimeout(ctx, budget)
		outcome = attempt(callCtx)
		cancel()
		if outcome.ok || !retryableRead(outcome) || counter.used >= routingMaxAttempts {
			break
		}
		if wait := routingRetryDelay; wait < time.Until(decisionDeadline) {
			select {
			case <-ctx.Done():
				outcome = readOutcome{kind: readFailureNetwork, failure: ctx.Err().Error()}
			case <-time.After(wait):
				continue
			}
		}
		break
	}
	e.complete(scope, entry, outcome)
	return outcome
}

func retryableRead(outcome readOutcome) bool {
	if outcome.kind != readFailureNetwork {
		return false
	}
	return outcome.httpCode == 0 || outcome.httpCode == 502 || outcome.httpCode == 503 || outcome.httpCode == 504
}

// eligibility is the account-bound selection input for one turn decision.
type eligibility struct {
	policy         string
	requestedModel string
	requestedLevel string
	provider       string
}

// decideStartFirst resolves the Start preference for the requested model. A
// confirmed absence, expiry, or exhaustion may fall back to Individual; every
// unknown or ambiguous state fails closed without spending Individual quota.
func (e *routingEngine) decideStartFirst(ctx context.Context, in eligibility) (routingDecision, error) {
	// One budget bounds the whole decision: Start read, busy recheck,
	// Individual evidence, and registry projection all draw from the same
	// fifteen seconds (or the caller's earlier deadline).
	ctx, cancel := context.WithTimeout(ctx, routingDecisionBudget)
	defer cancel()
	var counter endpointAttempts
	balanceOutcome := e.readEndpoint(ctx, "start-balance", &counter, func(callCtx context.Context) readOutcome {
		return e.host.readStartBalance(callCtx)
	})
	if !balanceOutcome.ok {
		if balanceOutcome.kind == readFailureAuth {
			return routingDecision{}, routingError{code: CodeRoutingAuthRequired, safe: "account authentication could not be established; sign in or refresh the plan in ZCode"}
		}
		return routingDecision{}, routingError{code: CodeRoutingBalanceUnknown, safe: "Start balance could not be established; start-first refuses to spend Individual quota on unknown evidence"}
	}
	balance, err := decodeStartBalance(balanceOutcome.payload)
	if err != nil {
		return routingDecision{}, routingError{code: CodeRoutingBalanceUnknown, safe: "Start balance evidence could not be decoded"}
	}
	now := balance.ServerTime
	if now.IsZero() {
		now = e.now()
	}
	modelKey := strings.ToLower(strings.TrimSpace(in.requestedModel))
	var candidates []startBucket
	for _, bucket := range balance.Buckets {
		if strings.ToLower(strings.TrimSpace(bucket.Model)) != modelKey {
			continue
		}
		candidates = append(candidates, bucket)
	}
	spendable, busy, exhausted, expired := false, false, false, false
	sawModel := len(candidates) > 0
	for _, bucket := range candidates {
		// Expiry evidence counts regardless of plan ownership: an ended
		// promotion must fall back to Individual, not read as incomplete data.
		if bucket.hasExpiry && !bucket.ExpiresAt.IsZero() && !bucket.ExpiresAt.After(now) {
			expired = true
		}
		if owningPlan(balance, bucket) != nil && !owningPlan(balance, bucket).active(now) {
			expired = true
		}
		if !bucketOwnedByActivePlan(balance, bucket, now) {
			continue
		}
		if ok, known := bucket.spendable(now); known && ok {
			spendable = true
		}
		if ok, known := bucket.busy(); known && ok {
			busy = true
		}
		if ok, known := bucket.exhausted(); known && ok {
			exhausted = true
		}
	}
	if spendable {
		return routingDecision{Provider: e.startProvider(in), ReasonCode: CodeRoutingStartAvailable, ObservedAt: now, hasObservedAt: true}, nil
	}
	if busy {
		return e.recheckBusy(ctx, in, modelKey, counter)
	}
	if expired && !exhausted {
		return e.decideIndividual(ctx, in, CodeRoutingStartExpired)
	}
	if sawModel && exhausted {
		return e.decideIndividual(ctx, in, CodeRoutingStartExhausted)
	}
	if sawModel {
		// The model appeared but its bucket evidence was incomplete or
		// unowned: unknown, never a confirmed absence.
		return routingDecision{}, routingError{code: CodeRoutingBalanceUnknown, safe: "Start balance evidence for the requested model is incomplete"}
	}
	if completeEligibility(balance) {
		return e.decideIndividual(ctx, in, CodeRoutingStartModelAbsent)
	}
	return routingDecision{}, routingError{code: CodeRoutingBalanceUnknown, safe: "Start balance evidence is incomplete"}
}

// recheckBusy waits the fixed one-second pause, then performs exactly one
// fresh balance read within the same endpoint attempt cap and decision
// budget. Unresolved pressure returns temporary busy.
func (e *routingEngine) recheckBusy(ctx context.Context, in eligibility, modelKey string, counter endpointAttempts) (routingDecision, error) {
	deadline := time.Now().Add(routingDecisionBudget)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if pause := routingBusyRecheck; time.Until(deadline) > pause {
		select {
		case <-ctx.Done():
			return routingDecision{}, routingError{code: CodeRoutingBalanceUnknown, safe: "Start balance recheck was cancelled"}
		case <-time.After(pause):
		}
	}
	e.invalidate("start-balance")
	if counter.used >= routingMaxAttempts {
		return routingDecision{}, routingError{code: CodeRoutingTemporaryBusy, safe: "Start allowance is temporarily reserved; retry later"}
	}
	outcome := e.readEndpoint(ctx, "start-balance", &counter, func(callCtx context.Context) readOutcome {
		return e.host.readStartBalance(callCtx)
	})
	if !outcome.ok {
		return routingDecision{}, routingError{code: CodeRoutingBalanceUnknown, safe: "Start balance recheck could not be established"}
	}
	refreshed, err := decodeStartBalance(outcome.payload)
	if err != nil {
		return routingDecision{}, routingError{code: CodeRoutingBalanceUnknown, safe: "Start balance recheck could not be decoded"}
	}
	now := refreshed.ServerTime
	if now.IsZero() {
		now = e.now()
	}
	for _, bucket := range refreshed.Buckets {
		if strings.ToLower(strings.TrimSpace(bucket.Model)) != modelKey {
			continue
		}
		if ok, known := bucket.spendable(now); known && ok && bucketOwnedByActivePlan(refreshed, bucket, now) {
			return routingDecision{Provider: e.startProvider(in), ReasonCode: CodeRoutingStartAvailable, ObservedAt: now, hasObservedAt: true}, nil
		}
	}
	return routingDecision{}, routingError{code: CodeRoutingTemporaryBusy, safe: "Start allowance is temporarily reserved; retry later"}
}

// completeEligibility reports whether the balance response is complete enough
// to confirm that a model is authoritatively absent from Start. A truncated
// or ambiguous payload must stay unknown.
func completeEligibility(balance startBalance) bool {
	if balance.ServerTime.IsZero() || !balance.hasPlans || !balance.hasBalances {
		return false
	}
	for _, bucket := range balance.Buckets {
		if bucket.Model == "" {
			return false
		}
	}
	return true
}

// startProvider resolves the Start-side provider of the routing family. An
// explicit start-first provider ID designates the family, never a pinned
// plan: an explicit Individual provider still routes through Start whenever
// Start has the spendable allowance.
func (e *routingEngine) startProvider(in eligibility) string {
	_ = in
	return ProviderStart
}

// owningPlan returns the plan instance that owns a bucket, matched the way
// the source pairs them: user_plan_id first, then an unowned bucket falls to
// any listed plan. Nil means no owning plan record.
func owningPlan(balance startBalance, bucket startBucket) *startPlan {
	for i, plan := range balance.Plans {
		if bucket.UserPlanID != "" && plan.UserPlanID != "" && bucket.UserPlanID == plan.UserPlanID {
			return &balance.Plans[i]
		}
	}
	if bucket.UserPlanID == "" && bucket.EntitlementID == "" && len(balance.Plans) > 0 {
		return &balance.Plans[0]
	}
	return nil
}

// bucketOwnedByActivePlan associates a bucket with an active plan instance.
// Buckets without ownership evidence stay unknown and never authorize
// spending.
func bucketOwnedByActivePlan(balance startBalance, bucket startBucket, now time.Time) bool {
	if balance.ServerTime.IsZero() {
		return false
	}
	plan := owningPlan(balance, bucket)
	return plan != nil && plan.active(now)
}

// decideFixed enforces the tightened fixed-mode gates: fresh Individual
// entitlement and exact selectable model/reasoning evidence before dispatch.
// Fixed never reads Start billing and never requires cross-plan identity.
func (e *routingEngine) decideFixed(ctx context.Context, in eligibility) (routingDecision, error) {
	return e.decideIndividual(ctx, in, "")
}

// decideIndividual evaluates the three-part Individual evidence rule lazily:
// credential resolution, fresh active Coding subscription, and the exact
// provider/model/reasoning in the live selectable registry view. Unknown
// evidence fails closed without dispatching.
func (e *routingEngine) decideIndividual(ctx context.Context, in eligibility, startReason string) (routingDecision, error) {
	// The subscription read, the registry projection, and any upstream
	// caller's remaining decision budget share one bounded context.
	ctx, cancel := context.WithTimeout(ctx, routingDecisionBudget)
	defer cancel()
	var counter endpointAttempts
	subOutcome := e.readEndpoint(ctx, "individual-subscription", &counter, func(callCtx context.Context) readOutcome {
		return e.host.readIndividualSubscription(callCtx)
	})
	if !subOutcome.ok {
		if subOutcome.kind == readFailureAuth {
			return routingDecision{}, routingError{code: CodeRoutingAuthRequired, safe: "account authentication could not be established; sign in or refresh the plan in ZCode"}
		}
		return routingDecision{}, routingError{code: CodeRoutingIndividualUnknown, safe: "Individual entitlement could not be established"}
	}
	switch state, err := decodeIndividualSubscription(subOutcome.payload); {
	case err != nil:
		return routingDecision{}, routingError{code: CodeRoutingIndividualUnknown, safe: "Individual subscription evidence could not be decoded"}
	case state == subscriptionUnknown:
		return routingDecision{}, routingError{code: CodeRoutingIndividualUnknown, safe: "Individual subscription evidence is ambiguous"}
	case state == subscriptionUnavailable:
		return routingDecision{}, routingError{code: CodeRoutingIndividualUnavail, safe: "no active Individual Coding subscription is available"}
	}
	var viewCounter endpointAttempts
	viewOutcome := e.readEndpoint(ctx, "registry-view", &viewCounter, func(callCtx context.Context) readOutcome {
		return e.host.readRegistryView(callCtx)
	})
	if !viewOutcome.ok {
		return routingDecision{}, routingError{code: CodeRoutingIndividualUnknown, safe: "the live selectable registry view could not be read"}
	}
	view, err := decodeRegistryView(viewOutcome.payload)
	if err != nil {
		return routingDecision{}, routingError{code: CodeRoutingIndividualUnknown, safe: "the live selectable registry view could not be decoded"}
	}
	model, ok := view.Models[registrySelectionKey(ProviderIndividual, in.requestedModel)]
	if !ok || model.Disabled {
		return routingDecision{}, routingError{code: CodeRoutingModelUnavailable, safe: "neither plan supplies the requested model"}
	}
	if !model.supportsReasoning(in.requestedLevel) {
		return routingDecision{}, routingError{code: CodeRoutingModelUnavailable, safe: "the requested reasoning level is not selectable for the model"}
	}
	// Fixed-mode success carries no routing reason code: only start-first
	// transitions publish routing diagnostics; fixed failures surface through
	// the typed routing errors above.
	return routingDecision{Provider: ProviderIndividual, ReasonCode: startReason, ObservedAt: e.now(), hasObservedAt: true}, nil
}

// routingError is a safe, typed routing failure.
type routingError struct {
	code string
	safe string
}

func (e routingError) Error() string { return e.safe }
