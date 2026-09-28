package zcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// fixturePayload loads a testdata fixture and verifies its provenance header:
// every checked-in fixture must name the upstream commit, source path, and
// dialect it was derived from.
func fixturePayload(t *testing.T, relative string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", relative))
	if err != nil {
		t.Fatal(err)
	}
	var wrapper struct {
		Provenance struct {
			SourceCommit string `json:"source_commit"`
			SourcePath   string `json:"source_path"`
			Dialect      string `json:"dialect"`
			Captured     string `json:"captured"`
		} `json:"provenance"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("%s: %v", relative, err)
	}
	if wrapper.Provenance.SourceCommit == "" || wrapper.Provenance.SourcePath == "" || wrapper.Provenance.Dialect == "" || wrapper.Provenance.Captured != "source-derived" {
		t.Fatalf("%s: incomplete provenance", relative)
	}
	return wrapper.Payload
}

func TestFixtureProvenance(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) < 7 {
		t.Fatalf("expected the full fixture set, found %d", len(matches))
	}
	for _, path := range matches {
		relative, _ := filepath.Rel("testdata", path)
		fixturePayload(t, relative)
	}
}

func TestStartBalanceFixtureDecoding(t *testing.T) {
	balance, err := decodeStartBalance(fixturePayload(t, filepath.Join("billing", "start-balance.json")))
	if err != nil {
		t.Fatal(err)
	}
	if balance.ServerTime.IsZero() {
		t.Fatal("server time lost")
	}
	now := balance.ServerTime
	if len(balance.Plans) != 1 || !balance.Plans[0].active(now) {
		t.Fatal("active plan lost")
	}
	var flash, glm bool
	for _, bucket := range balance.Buckets {
		switch bucket.Model {
		case "GLM-5.3-Flash":
			flash = true
			if ok, known := bucket.spendable(now); !known || !ok {
				t.Fatal("spendable Flash bucket lost")
			}
			if ok, known := bucket.busy(); known && ok {
				t.Fatal("spendable bucket misread as busy")
			}
		case "GLM-5.2":
			glm = true
			if ok, known := bucket.exhausted(); !known || !ok {
				t.Fatal("exhausted bucket lost")
			}
		}
	}
	if !flash || !glm {
		t.Fatal("bucket models lost")
	}
}

func TestReservedBalanceIsNotExhaustion(t *testing.T) {
	balance, err := decodeStartBalance(fixturePayload(t, filepath.Join("billing", "start-balance-reserved.json")))
	if err != nil {
		t.Fatal(err)
	}
	bucket := balance.Buckets[0]
	if ok, known := bucket.busy(); !known || !ok {
		t.Fatal("reservation pressure not detected")
	}
	if ok, known := bucket.exhausted(); known && ok {
		t.Fatal("reservation misread as exhaustion")
	}
	if ok, known := bucket.spendable(balance.ServerTime); known && ok {
		t.Fatal("zero available read as spendable")
	}
}

func TestExpiredPlanDemotesBuckets(t *testing.T) {
	balance, err := decodeStartBalance(fixturePayload(t, filepath.Join("billing", "start-balance-expired.json")))
	if err != nil {
		t.Fatal(err)
	}
	// The plan's end passed before the paired server time: the bucket may not
	// authorize spending even though the amounts look positive.
	if balance.Plans[0].active(balance.ServerTime) {
		t.Fatal("expired plan stayed active")
	}
	if _, known := balance.Buckets[0].spendable(balance.ServerTime); !known {
		t.Fatal("amount evidence lost")
	}
	if bucketOwnedByActivePlan(balance, balance.Buckets[0], balance.ServerTime) {
		t.Fatal("bucket owned by expired plan")
	}
}

func TestSubscriptionFixtureStates(t *testing.T) {
	active, err := decodeIndividualSubscription(fixturePayload(t, filepath.Join("billing", "subscription-list.json")))
	if err != nil || active != subscriptionActive {
		t.Fatalf("active subscription lost: %v %v", active, err)
	}
	none, err := decodeIndividualSubscription(fixturePayload(t, filepath.Join("billing", "subscription-list-none.json")))
	if err != nil || none != subscriptionUnavailable {
		t.Fatalf("missing subscription misclassified: %v %v", none, err)
	}
	// A Coding record without the period flag is malformed relevant evidence:
	// unknown, never silently unavailable.
	unknown, err := decodeIndividualSubscription(json.RawMessage(`{"data":[{"productId":"zcode-coding-plan-month","status":"VALID"}]}`))
	if err != nil || unknown != subscriptionUnknown {
		t.Fatalf("malformed record misclassified: %v %v", unknown, err)
	}
}

func TestRegistryViewFixture(t *testing.T) {
	view, err := decodeRegistryView(fixturePayload(t, filepath.Join("registry", "registry-view.json")))
	if err != nil {
		t.Fatal(err)
	}
	if view.Revision == "" {
		t.Fatal("registry revision lost")
	}
	flash := view.Models[registrySelectionKey(ProviderIndividual, "GLM-5.3-Flash")]
	if flash.ModelID == "" || flash.Disabled || !flash.supportsReasoning("low") {
		t.Fatal("Individual Flash selectability lost")
	}
	if flash.supportsReasoning("max") {
		t.Fatal("unsupported reasoning accepted")
	}
	turbo := view.Models[registrySelectionKey(ProviderStart, "GLM-5-Turbo")]
	if !turbo.Disabled {
		t.Fatal("disabled model read as selectable")
	}
	if _, ok := view.Models[registrySelectionKey(ProviderStart, "GLM-5.3")]; ok {
		t.Fatal("Start plan falsely offers GLM-5.3")
	}
}

func TestLegacyWireFixtureShapes(t *testing.T) {
	var wire struct {
		Frames []struct {
			Note  string          `json:"note"`
			Frame json.RawMessage `json:"frame"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(fixturePayload(t, filepath.Join("protocol", "legacy-wire.json")), &wire); err != nil {
		t.Fatal(err)
	}
	byNote := map[string]json.RawMessage{}
	for _, frame := range wire.Frames {
		byNote[frame.Note] = frame.Frame
	}
	// Both JSON request-ID types must decode into the wire envelope.
	for _, note := range []string{"string request id", "numeric request id"} {
		var msg wireMessage
		if err := json.Unmarshal(byNote[note], &msg); err != nil || msg.Method == "" {
			t.Fatalf("%s: %v", note, err)
		}
	}
	var cancelled wireMessage
	if err := json.Unmarshal(byNote["cancellation notification"], &cancelled); err != nil || cancelled.Method != "interaction/providerRuntimeHeadersCancelled" {
		t.Fatal("cancellation notification lost")
	}
	var rejection wireMessage
	if err := json.Unmarshal(byNote["invalid params rejection"], &rejection); err != nil || rejection.Error.Code != jsonRPCInvalidParams {
		t.Fatal("invalid-params rejection lost")
	}
	if err := json.Unmarshal(byNote["method not found rejection"], &rejection); err != nil || rejection.Error.Code != jsonRPCMethodNotFound {
		t.Fatal("method-not-found rejection lost")
	}
	var failure struct {
		Params struct {
			Payload struct {
				Error struct {
					Code string `json:"code"`
					Type string `json:"type"`
				} `json:"error"`
			} `json:"payload"`
		} `json:"params"`
	}
	if err := json.Unmarshal(byNote["terminal failure"], &failure); err != nil {
		t.Fatal(err)
	}
	if !classifyQuotaExhaustion(mustRaw(t, failure.Params.Payload.Error)) {
		t.Fatal("typed exhaustion marker not classified")
	}
	var busy struct {
		Params struct {
			Payload struct {
				Error struct {
					Code string `json:"code"`
					Type string `json:"type"`
				} `json:"error"`
			} `json:"payload"`
		} `json:"params"`
	}
	if err := json.Unmarshal(byNote["admission busy failure is not exhaustion"], &busy); err != nil {
		t.Fatal(err)
	}
	if classifyQuotaExhaustion(mustRaw(t, busy.Params.Payload.Error)) {
		t.Fatal("admission busy classified as exhaustion")
	}
	var snapshotResponse struct {
		Result struct {
			Session json.RawMessage `json:"session"`
		} `json:"result"`
	}
	if err := json.Unmarshal(byNote["session snapshot background jobs"], &snapshotResponse); err != nil {
		t.Fatal(err)
	}
	tasks := newBackgroundTasks()
	tasks.observeSessionSnapshot(snapshotResponse.Result.Session)
	if tasks.count() != 1 {
		t.Fatal("background job identity lost")
	}
}

func mustRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestQuotaClassificationBoundaries(t *testing.T) {
	positive := []json.RawMessage{
		json.RawMessage(`{"code":"start_plan_quota_exhausted"}`),
		json.RawMessage(`{"type":"quota_exhaustion","code":"start_plan_quota_exhausted"}`),
		json.RawMessage(`{"error":{"code":"start_plan_quota_exhausted"}}`),
	}
	for _, raw := range positive {
		if !classifyQuotaExhaustion(raw) {
			t.Fatalf("expected classification for %s", raw)
		}
	}
	negative := []json.RawMessage{
		json.RawMessage(`"quota over"`),
		json.RawMessage(`{"message":"insufficient quota"}`),
		json.RawMessage(`{"code":"3010","type":"admission_busy"}`),
		json.RawMessage(`{"code":"model_rate_limited"}`),
		json.RawMessage(`{"code":"authentication_required"}`),
		json.RawMessage(`{"code":"start_plan_busy_auto_retry_exhausted"}`),
		json.RawMessage(`{}`),
		json.RawMessage(`null`),
	}
	for _, raw := range negative {
		if classifyQuotaExhaustion(raw) {
			t.Fatalf("unexpected classification for %s", raw)
		}
	}
}

func TestPlanPolicyValidation(t *testing.T) {
	valid := []struct {
		policy   string
		provider string
	}{
		{"", ""}, {"", ProviderIndividual}, {"fixed", ProviderIndividual}, {"fixed", ""},
		{"start-first", ""}, {"start-first", ProviderStart}, {"start-first", ProviderIndividual},
	}
	for _, item := range valid {
		if err := ValidatePlanPolicy(item.policy, item.provider); err != nil {
			t.Fatalf("rejected valid %q/%q: %v", item.policy, item.provider, err)
		}
	}
	invalid := []struct {
		policy   string
		provider string
	}{
		{"auto", ""}, {"fixed", ProviderStart}, {"start-first", "openai"}, {"start-first", "account:bigmodel-start-plan"},
	}
	for _, item := range invalid {
		if err := ValidatePlanPolicy(item.policy, item.provider); err == nil {
			t.Fatalf("accepted invalid %q/%q", item.policy, item.provider)
		}
	}
}

func TestInitRejectsInvalidPolicyAndReasoning(t *testing.T) {
	spec := domain.AgentSpec{Name: "a", Backend: "zcode", StartupPrompt: "s", StringOptions: map[string]string{"plan_policy": "turbo"}}
	if _, err := New(spec).Init(context.Background(), spec, domain.AgentState{}); err == nil {
		t.Fatal("Init accepted an unknown policy")
	}
	spec.StringOptions = map[string]string{"plan_policy": "fixed", "provider": ProviderStart}
	if _, err := New(spec).Init(context.Background(), spec, domain.AgentState{}); err == nil {
		t.Fatal("Init accepted Start provider under fixed")
	}
	spec.StringOptions = map[string]string{"plan_policy": "start-first", "reasoning": "ultra"}
	if _, err := New(spec).Init(context.Background(), spec, domain.AgentState{}); err == nil {
		t.Fatal("Init accepted an unsupported reasoning level")
	}
}

func startFirstAdapter(t *testing.T, scenario string) (*Adapter, domain.AgentState) {
	t.Helper()
	spec := domain.AgentSpec{Name: "test", Backend: "zcode", StartupPrompt: "startup marker", StringOptions: map[string]string{"plan_policy": PlanPolicyStartFirst}, ListOptions: map[string][]string{"env": {"GO_WANT_ZCODE_HELPER=1"}}}
	a := New(spec)
	a.command = func() *exec.Cmd {
		return exec.Command(os.Args[0], "-test.run=^TestProtocolHelper$", "--", scenario)
	}
	state, err := a.Init(context.Background(), spec, domain.AgentState{WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return a, state
}

func TestQuotaContinuationFlow(t *testing.T) {
	a, state := startFirstAdapter(t, "quota-continuation")
	sink := &recordingSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalMessage != "continued-pong" {
		t.Fatalf("continuation result: %q", res.FinalMessage)
	}
	if next.BackendSessionID != "session" {
		t.Fatal("session lost across the transition")
	}
	codes := sinkDiagnosticCodes(t, sink)
	for _, expected := range []string{CodeRoutingStartAvailable, CodeRoutingContinuationStarted} {
		if !codes[expected] {
			t.Fatalf("missing diagnostic %s in %v", expected, codes)
		}
	}
}

func sinkDiagnosticCodes(t *testing.T, sink *recordingSink) map[string]bool {
	t.Helper()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	codes := map[string]bool{}
	for _, line := range sink.diagnostics {
		var record struct {
			Type       string `json:"type"`
			ReasonCode string `json:"reason_code"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Type == "zcode.routing" {
			codes[record.ReasonCode] = true
		}
	}
	return codes
}

func TestBalanceUnknownFailsClosed(t *testing.T) {
	a, state := startFirstAdapter(t, "balance-unknown")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	if err == nil {
		t.Fatal("start-first dispatched on unknown balance")
	}
	if !contains(err.Error(), "Start balance") {
		t.Fatalf("unexpected error: %v", err)
	}
	if next.BackendSessionID != "" {
		t.Fatal("session created despite unknown balance")
	}
}

func TestFixedIndividualUnknownFailsClosed(t *testing.T) {
	a, state := fakeAdapter(t, "individual-unknown")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	if err == nil {
		t.Fatal("fixed dispatched without Individual evidence")
	}
	if !contains(err.Error(), "Individual entitlement") {
		t.Fatalf("unexpected error: %v", err)
	}
	if next.BackendSessionID != "" {
		t.Fatal("session created despite unknown Individual evidence")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestBootstrapAuthRequired(t *testing.T) {
	a, state := startFirstAdapter(t, "auth-fail")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	var routingErr routingError
	if !errors.As(err, &routingErr) || routingErr.code != CodeRoutingAuthRequired {
		t.Fatalf("expected auth-required, got %v", err)
	}
	if next.BackendSessionID != "" {
		t.Fatal("session created despite auth failure")
	}
}

func TestQuotaBusyAndGenericFailuresDoNotContinue(t *testing.T) {
	for _, scenario := range []string{"quota-busy", "quota-generic"} {
		t.Run(scenario, func(t *testing.T) {
			a, state := startFirstAdapter(t, scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			sink := &recordingSink{}
			_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, sink)
			if err == nil || !contains(err.Error(), "turn failed") {
				t.Fatalf("expected a plain turn failure: %v", err)
			}
			codes := sinkDiagnosticCodes(t, sink)
			if codes[CodeRoutingContinuationStarted] || codes[CodeRoutingContinuationFailed] {
				t.Fatalf("continuation diagnosed for %s: %v", scenario, codes)
			}
		})
	}
}

func TestPreferencesDuringCreate(t *testing.T) {
	a, state := fakeAdapter(t, "preferences-during-create")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	res, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalMessage != "pong" || next.BackendSessionID != "session" {
		t.Fatal("create with reverse preferences failed")
	}
}

func TestContinuationGateDeadlines(t *testing.T) {
	// Four seconds remaining is too little; five or more admits.
	short := continuationGate{hasDeadline: true, deadline: time.Now().Add(4 * time.Second), individualOK: true, cleanupOK: true}
	if err := short.admit(); err == nil || !errors.Is(err, errContinuationBudgetShort) {
		t.Fatalf("short deadline admitted: %v", err)
	}
	accepted := continuationGate{hasDeadline: true, deadline: time.Now().Add(continuationMinDeadline + time.Second), individualOK: true, cleanupOK: true}
	if err := accepted.admit(); err != nil {
		t.Fatalf("sufficient budget rejected: %v", err)
	}
	noDeadline := continuationGate{individualOK: true, cleanupOK: true}
	if err := noDeadline.admit(); err != nil {
		t.Fatalf("no-deadline context rejected: %v", err)
	}
	cancelled := continuationGate{individualOK: true, cleanupOK: true, cancelled: true}
	if err := cancelled.admit(); err == nil {
		t.Fatal("cancelled gate admitted")
	}
	uncertain := continuationGate{individualOK: false, cleanupOK: true}
	if err := uncertain.admit(); err == nil {
		t.Fatal("uncertain eligibility admitted")
	}
	dirty := continuationGate{individualOK: true, cleanupOK: false}
	if err := dirty.admit(); err == nil {
		t.Fatal("uncertain cleanup admitted")
	}
	done := continuationGate{individualOK: true, cleanupOK: true, alreadyFailed: true}
	if err := done.admit(); err == nil {
		t.Fatal("second continuation admitted")
	}
}

func TestTerminalCleanupOrderAndBudget(t *testing.T) {
	var calls []string
	call := func(ctx context.Context, method string, params any, result any) error {
		var p struct {
			SessionID string `json:"sessionId"`
			TaskID    string `json:"taskId"`
		}
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, method+":"+p.SessionID+":"+p.TaskID)
		return nil
	}
	background := newBackgroundTasks()
	background.observe("bg-1")
	background.observe("bg-2")
	if err := runTerminalCleanup(call, cleanupSpec{session: "root", descendants: []string{"child"}, background: background, stopRoot: true}); err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"session/stop:root:",
		"session/cancelBackgroundTask:root:bg-1",
		"session/cancelBackgroundTask:root:bg-2",
		"session/stop:child:",
		"session/close:child:",
		"session/close:root:",
	}
	if len(calls) != len(expected) {
		t.Fatalf("cleanup calls: %v", calls)
	}
	for i, want := range expected {
		if calls[i] != want {
			t.Fatalf("call %d: got %q want %q", i, calls[i], want)
		}
	}
}

func TestContinuationDrainKeepsRootOpen(t *testing.T) {
	var calls []string
	call := func(ctx context.Context, method string, params any, result any) error {
		var p struct {
			SessionID string `json:"sessionId"`
		}
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, method+":"+p.SessionID)
		return nil
	}
	background := newBackgroundTasks()
	background.observe("bg-1")
	if err := runTerminalCleanup(call, cleanupSpec{session: "root", descendants: []string{"child"}, background: background, stopRoot: false}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range calls {
		if entry == "session/stop:root" || entry == "session/close:root" {
			t.Fatalf("root conversation was stopped during the drain: %v", calls)
		}
	}
	found := false
	for _, entry := range calls {
		if entry == "session/cancelBackgroundTask:root" {
			found = true
		}
	}
	if !found {
		t.Fatalf("background task was not cancelled: %v", calls)
	}
}

func TestTerminalCleanupFailureReported(t *testing.T) {
	call := func(ctx context.Context, method string, params any, result any) error {
		if method == "session/stop" {
			return context.DeadlineExceeded
		}
		return nil
	}
	if err := runTerminalCleanup(call, cleanupSpec{session: "root", stopRoot: true}); err == nil {
		t.Fatal("cleanup failure swallowed")
	}
}

func TestReadAttemptCapAndRetry(t *testing.T) {
	// Each block uses a fresh engine: sharing joins a matching in-flight read
	// within the one-second window, which is exercised separately below.
	attempts := 0
	stub := func(callCtx context.Context) readOutcome {
		attempts++
		return readOutcome{kind: readFailureNetwork, failure: "upstream unavailable", httpCode: 503}
	}
	engine := newRoutingEngine(&hostBridge{})
	outcome := engine.readEndpoint(context.Background(), "start-balance", &endpointAttempts{}, stub)
	if outcome.ok || attempts != routingMaxAttempts {
		t.Fatalf("attempts=%d outcome=%+v", attempts, outcome)
	}
	// One retry then success stays inside the same cap.
	attempts = 0
	flaky := func(callCtx context.Context) readOutcome {
		attempts++
		if attempts == 1 {
			return readOutcome{kind: readFailureNetwork, failure: "bad gateway", httpCode: 502}
		}
		return readOutcome{ok: true, payload: json.RawMessage(`{}`)}
	}
	engine = newRoutingEngine(&hostBridge{})
	outcome = engine.readEndpoint(context.Background(), "start-balance", &endpointAttempts{}, flaky)
	if !outcome.ok || attempts != 2 {
		t.Fatalf("attempts=%d outcome=%+v", attempts, outcome)
	}
	// A rate limit is never retried.
	attempts = 0
	rateLimited := func(callCtx context.Context) readOutcome {
		attempts++
		return readOutcome{kind: readFailureNetwork, failure: "too many requests", httpCode: 429}
	}
	engine = newRoutingEngine(&hostBridge{})
	engine.readEndpoint(context.Background(), "start-balance", &endpointAttempts{}, rateLimited)
	if attempts != 1 {
		t.Fatalf("429 retried: attempts=%d", attempts)
	}
}

func TestSharedEvidenceAge(t *testing.T) {
	now := time.Unix(1759000000, 0)
	engine := newRoutingEngine(&hostBridge{})
	engine.setClock(func() time.Time { return now })
	reads := 0
	stub := func(callCtx context.Context) readOutcome {
		reads++
		return readOutcome{ok: true, payload: json.RawMessage(`{}`)}
	}
	counter := &endpointAttempts{}
	engine.readEndpoint(context.Background(), "start-balance", counter, stub)
	if reads != 1 {
		t.Fatalf("reads=%d", reads)
	}
	// A join inside the age window reuses the in-flight entry without a read.
	now = now.Add(500 * time.Millisecond)
	engine.readEndpoint(context.Background(), "start-balance", counter, stub)
	if reads != 1 {
		t.Fatalf("join triggered a second read: reads=%d", reads)
	}
	// Evidence older than one second is never reused: a fresh request starts.
	now = now.Add(2 * time.Second)
	engine.readEndpoint(context.Background(), "start-balance", counter, stub)
	if reads != 2 {
		t.Fatalf("stale evidence reused: reads=%d", reads)
	}
}

func TestCursorRepeatFailsOwnership(t *testing.T) {
	a, state := fakeAdapter(t, "cursor-loop")
	state.BackendSessionID = "session"
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	if err == nil || !contains(err.Error(), "repeated a cursor") {
		t.Fatalf("expected an incomplete-ownership failure, got %v", err)
	}
}

func TestBackgroundJobsFromSessionSnapshot(t *testing.T) {
	tasks := newBackgroundTasks()
	tasks.observeSessionSnapshot(json.RawMessage(`{"sessionId":"s1","backgroundJobs":[{"id":"job-1","kind":"bash"},{"taskId":"task-2"}]}`))
	if tasks.count() != 2 {
		t.Fatalf("background jobs lost: %v", tasks.order)
	}
	// A snapshot without the surface, or with an unreadable one, stays silent.
	before := tasks.count()
	tasks.observeSessionSnapshot(json.RawMessage(`{"sessionId":"s1"}`))
	tasks.observeSessionSnapshot(json.RawMessage(`not json`))
	tasks.observeSessionSnapshot(nil)
	if tasks.count() != before {
		t.Fatal("unverified shapes contributed identities")
	}
}

// stubHost answers the guarded read methods with fixed payloads; every
// unmatched method fails as schema so missing evidence cannot pass silently.
func stubHost(payloads map[string]json.RawMessage) *hostBridge {
	return &hostBridge{call: func(ctx context.Context, method string, params any, result any) error {
		var env readEnvelope
		if raw, ok := payloads[method]; ok {
			env = readEnvelope{OK: true, Payload: raw}
		} else {
			env = readEnvelope{OK: false, Kind: "schema", Message: "no fixture for " + method}
		}
		raw, err := json.Marshal(env)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, result)
	}}
}

func fixtureBalance(t *testing.T, name string) json.RawMessage {
	t.Helper()
	return fixturePayload(t, filepath.Join("billing", name))
}

func fixtureRegistry(t *testing.T) json.RawMessage {
	t.Helper()
	return fixturePayload(t, filepath.Join("registry", "registry-view.json"))
}

func TestStartFirstExplicitIndividualPrefersStart(t *testing.T) {
	// An explicit Individual provider under start-first designates the
	// routing family, not a pinned plan: a spendable Start bucket wins.
	spec := domain.AgentSpec{Name: "test", Backend: "zcode", StartupPrompt: "startup marker", StringOptions: map[string]string{"plan_policy": PlanPolicyStartFirst, "provider": ProviderIndividual}, ListOptions: map[string][]string{"env": {"GO_WANT_ZCODE_HELPER=1"}}}
	a := New(spec)
	a.command = func() *exec.Cmd {
		return exec.Command(os.Args[0], "-test.run=^TestProtocolHelper$", "--", "quota-continuation")
	}
	state, err := a.Init(context.Background(), spec, domain.AgentState{WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalMessage != "continued-pong" {
		t.Fatalf("unexpected result %q", res.FinalMessage)
	}
}

func TestIncompleteBalanceStaysUnknown(t *testing.T) {
	// A response with no plans/balances arrays is truncated evidence, never
	// authoritative absence.
	balance, err := decodeStartBalance(json.RawMessage(`{"data":{"server_time":1770000000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if completeEligibility(balance) {
		t.Fatal("missing arrays read as complete")
	}
	// Present-but-empty arrays with paired server time are authoritative.
	empty, err := decodeStartBalance(json.RawMessage(`{"data":{"server_time":1770000000,"plans":[],"balances":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !completeEligibility(empty) {
		t.Fatal("authoritative empty list read as unknown")
	}
}

func TestExpiredPromotionFallsBackToIndividual(t *testing.T) {
	engine := newRoutingEngine(stubHost(map[string]json.RawMessage{
		"squad/readStartBalance":           fixtureBalance(t, "start-balance-expired.json"),
		"squad/readIndividualSubscription": fixtureBalance(t, "subscription-list.json"),
		"squad/readRegistryView":           fixtureRegistry(t),
	}))
	decision, err := engine.decideStartFirst(context.Background(), eligibility{requestedModel: "GLM-5.3-Flash", requestedLevel: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Provider != ProviderIndividual || decision.ReasonCode != CodeRoutingStartExpired {
		t.Fatalf("expired promotion routed %+v", decision)
	}
}

func TestDecisionBudgetSharedAcrossReads(t *testing.T) {
	// Each guarded read takes 4.5 seconds. With one shared decision budget a
	// parent deadline of twelve seconds must fail the third read; with per-end
	// budgets all three would succeed at 13.5 seconds.
	slow := map[string]json.RawMessage{
		"squad/readStartBalance":           json.RawMessage(`{"data":{"server_time":1770000000,"plans":[],"balances":[]}}`),
		"squad/readIndividualSubscription": fixtureBalance(t, "subscription-list.json"),
		"squad/readRegistryView":           fixtureRegistry(t),
	}
	engine := newRoutingEngine(&hostBridge{call: func(ctx context.Context, method string, params any, result any) error {
		env := readEnvelope{OK: false, Kind: "schema", Message: "no fixture"}
		if raw, ok := slow[method]; ok {
			env = readEnvelope{OK: true, Payload: raw}
		}
		select {
		case <-time.After(4500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
		raw, err := json.Marshal(env)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, result)
	}})
	parent, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	start := time.Now()
	_, err := engine.decideStartFirst(parent, eligibility{requestedModel: "GLM-5.3-Flash", requestedLevel: "low"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("the decision exceeded its shared budget")
	}
	if elapsed >= 14*time.Second {
		t.Fatalf("decision ran %s without a shared budget", elapsed)
	}
	if !contains(err.Error(), "registry view") {
		t.Fatalf("unexpected failure: %v", err)
	}
}

func TestPreferenceForeignSessionRejected(t *testing.T) {
	// A preference request naming a foreign session after the session is
	// known is rejected explicitly, and the run continues normally.
	a, state := fakeAdapter(t, "preferences-foreign")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	res, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalMessage != "pong" {
		t.Fatalf("unexpected result %q", res.FinalMessage)
	}
}

func TestPreferenceAssociationMismatchFails(t *testing.T) {
	// The one bootstrap association binds to the returned snapshot identity;
	// a mismatch fails the run before any turn work.
	a, state := fakeAdapter(t, "preferences-mismatch")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	// Both fail-closed outcomes are legitimate depending on dispatch timing:
	// the recorded association mismatches the returned session, or the late
	// foreign identity is rejected outright and fails the run.
	if err == nil || (!contains(err.Error(), "does not match the returned session") && !contains(err.Error(), "foreign session")) {
		t.Fatalf("expected an association mismatch failure, got %v", err)
	}
}
