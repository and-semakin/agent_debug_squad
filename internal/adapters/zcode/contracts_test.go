package zcode

import (
	"context"
	"encoding/json"
	"os"
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
	if len(matches) < 2 {
		t.Fatalf("expected the full fixture set, found %d", len(matches))
	}
	for _, path := range matches {
		relative, _ := filepath.Rel("testdata", path)
		fixturePayload(t, relative)
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
	// A model whose option spec carries no reasoning values cannot support the
	// requested level: unknown reasoning is never treated as supported.
	bare := registryModel{ProviderID: ProviderIndividual, ModelID: "GLM-5.3"}
	if bare.supportsReasoning("low") {
		t.Fatal("absent reasoning values treated as supported")
	}
	// The disabledReason decode path stays additive for future sources.
	synthetic, err := decodeRegistryView(json.RawMessage(`{"revision":"r","providers":[{"providerId":"p","models":[{"modelId":"m","reasoningLevels":["low"],"disabledReason":"x"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !synthetic.Models[registrySelectionKey("p", "m")].Disabled {
		t.Fatal("explicit disabledReason not honored")
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
	if failure.Params.Payload.Error.Code == "" {
		t.Fatal("typed failure code lost")
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

func TestInitRejectsRemovedOptionForeignProviderAndReasoning(t *testing.T) {
	spec := domain.AgentSpec{Name: "a", Backend: "zcode", StartupPrompt: "s", StringOptions: map[string]string{"plan_policy": "fixed"}}
	if _, err := New(spec).Init(context.Background(), spec, domain.AgentState{}); err == nil || !contains(err.Error(), "plan_policy") {
		t.Fatalf("Init accepted the removed plan_policy option: %v", err)
	}
	spec.StringOptions = map[string]string{"provider": "account:zai-start-plan"}
	if _, err := New(spec).Init(context.Background(), spec, domain.AgentState{}); err == nil {
		t.Fatal("Init accepted the Start provider")
	}
	spec.StringOptions = map[string]string{"reasoning": "ultra"}
	if _, err := New(spec).Init(context.Background(), spec, domain.AgentState{}); err == nil {
		t.Fatal("Init accepted an unsupported reasoning level")
	}
	spec.StringOptions = map[string]string{}
	if _, err := New(spec).Init(context.Background(), spec, domain.AgentState{}); err != nil {
		t.Fatalf("Init rejected a clean default configuration: %v", err)
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

func fixtureRegistry(t *testing.T) json.RawMessage {
	t.Helper()
	return fixturePayload(t, filepath.Join("registry", "registry-view.json"))
}

func TestVerifySelectionAcceptsConfiguredSelection(t *testing.T) {
	if err := verifySelection(context.Background(), stubHost(map[string]json.RawMessage{
		"squad/readRegistryView": fixtureRegistry(t),
	}), ProviderIndividual, "GLM-5.3-Flash", "low"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySelectionFailsClosed(t *testing.T) {
	host := stubHost(map[string]json.RawMessage{"squad/readRegistryView": fixtureRegistry(t)})
	if err := verifySelection(context.Background(), host, ProviderIndividual, "GLM-9.9", "low"); err == nil || !contains(err.Error(), "does not supply the requested model") {
		t.Fatalf("absent model accepted: %v", err)
	}
	if err := verifySelection(context.Background(), host, ProviderIndividual, "GLM-5.3-Flash", "max"); err == nil || !contains(err.Error(), "not selectable") {
		t.Fatalf("unsupported reasoning accepted: %v", err)
	}
	unreadable := stubHost(nil)
	if err := verifySelection(context.Background(), unreadable, ProviderIndividual, "GLM-5.3-Flash", "low"); err == nil || !contains(err.Error(), "registry view") {
		t.Fatalf("unreadable view dispatched: %v", err)
	}
	// The live registry legitimately lists other account providers; a model
	// they offer must never satisfy the configured Individual selection.
	foreign := stubHost(map[string]json.RawMessage{"squad/readRegistryView": fixtureRegistry(t)})
	if err := verifySelection(context.Background(), foreign, ProviderIndividual, "GLM-5-Turbo", "low"); err == nil {
		t.Fatal("a foreign provider's model satisfied the Individual selection")
	}
}

func TestSelectionGateFailsBeforeDispatch(t *testing.T) {
	for _, tc := range []struct{ scenario, want string }{
		{"model-unavailable", "does not supply the requested model"},
		{"reasoning-unsupported", "not selectable"},
		{"registry-unreadable", "registry view"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			a, state := fakeAdapter(t, tc.scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			_, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
			if err == nil || !contains(err.Error(), tc.want) {
				t.Fatalf("expected a selection failure, got %v", err)
			}
			if next.BackendSessionID != "" {
				t.Fatal("session created despite the selection failure")
			}
		})
	}
}

func TestBootstrapAuthRequired(t *testing.T) {
	a, state := fakeAdapter(t, "auth-fail")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	if err == nil || !contains(err.Error(), "sign in") {
		t.Fatalf("expected an actionable auth failure, got %v", err)
	}
	if next.BackendSessionID != "" {
		t.Fatal("session created despite auth failure")
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
	if err := runTerminalCleanup(call, cleanupSpec{session: "root", descendants: []string{"child"}, background: background}); err != nil {
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

func TestTerminalCleanupFailureReported(t *testing.T) {
	call := func(ctx context.Context, method string, params any, result any) error {
		if method == "session/stop" {
			return context.DeadlineExceeded
		}
		return nil
	}
	if err := runTerminalCleanup(call, cleanupSpec{session: "root"}); err == nil {
		t.Fatal("cleanup failure swallowed")
	}
}
