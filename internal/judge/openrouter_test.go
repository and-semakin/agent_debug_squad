package judge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func testOpenRouter(t *testing.T, endpoint string) *OpenRouter {
	t.Helper()
	j, err := NewOpenRouter(OpenRouterConfig{
		APIKey:      "test-key",
		Model:       "~typesafe/jev-latest",
		Endpoint:    endpoint,
		Timeout:     2 * time.Second,
		MaxAttempts: 2,
		Backoff:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new openrouter: %v", err)
	}
	return j
}

type capturedRequest struct {
	Authorization string
	Model         string
	State         map[string]string
	Questions     map[string]Question
}

func decisionsHandler(t *testing.T, captured *capturedRequest, answer string, status func(attempt int) int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	attempt := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempt++
		current := attempt
		mu.Unlock()
		if status != nil {
			if code := status(current); code != http.StatusOK {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"error":"boom"}`))
				return
			}
		}
		var payload openRouterPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured.Authorization = r.Header.Get("Authorization")
		captured.Model = payload.Model
		captured.State = payload.State
		captured.Questions = payload.Questions
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
}

const choiceAnswer = `{
  "model": "typesafe/jev-1.13-20260917",
  "answers": {
    "verdict": {
      "type": "choice",
      "choice": "issues_found",
      "probabilities": {"review_passed": 0.05, "issues_found": 0.9, "error": 0.05},
      "confidence": 0.9
    }
  },
  "usage": {"input_tokens": 10, "output_tokens": 2},
  "id": "gen-dec-test"
}`

func TestOpenRouterChoiceDecision(t *testing.T) {
	captured := &capturedRequest{}
	server := decisionsHandler(t, captured, choiceAnswer, nil)
	defer server.Close()
	j := testOpenRouter(t, server.URL)

	decision, err := j.Decide(context.Background(), Request{
		State:        map[string]string{"agent_response": "needs fixes"},
		QuestionName: "verdict",
		Question: Question{
			Type:         string(QuestionChoice),
			Instructions: "Classify the outcome.",
			Criteria:     map[string]string{"review_passed": "", "issues_found": "problems remain", "error": ""},
		},
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if captured.Authorization != "Bearer test-key" {
		t.Fatalf("auth header: %q", captured.Authorization)
	}
	if captured.Model != "~typesafe/jev-latest" {
		t.Fatalf("model: %q", captured.Model)
	}
	if captured.State["agent_response"] != "needs fixes" {
		t.Fatalf("state: %v", captured.State)
	}
	question := captured.Questions["verdict"]
	if question.Type != "choice" || question.Instructions != "Classify the outcome." || question.Criteria["issues_found"] != "problems remain" {
		t.Fatalf("question: %+v", question)
	}
	if decision.Choice != "issues_found" || decision.Confidence != 0.9 {
		t.Fatalf("decision: %+v", decision)
	}
	if decision.Probabilities["issues_found"] != 0.9 || len(decision.Probabilities) != 3 {
		t.Fatalf("probabilities: %v", decision.Probabilities)
	}
	if decision.Model != "typesafe/jev-1.13-20260917" {
		t.Fatalf("resolved model: %q", decision.Model)
	}
	if len(decision.Raw) == 0 || !strings.Contains(string(decision.Raw), "gen-dec-test") {
		t.Fatalf("raw response not preserved: %s", decision.Raw)
	}
}

func TestOpenRouterNoulAndScoreDecisions(t *testing.T) {
	noulAnswer := `{"model":"m","answers":{"verdict":{"type":"noul","noul":0.7,"probabilities":{"true":0.7,"false":0.3}}}}`
	scoreAnswer := `{"model":"m","answers":{"verdict":{"type":"score","score":"high","probabilities":{"low":0.1,"high":0.9},"confidence":0.9}}}`
	for _, tc := range []struct {
		name     string
		answer   string
		question Question
		check    func(Decision) bool
	}{
		{
			name:     "noul",
			answer:   noulAnswer,
			question: Question{Type: string(QuestionNoul), Instructions: "Is it done?", Criteria: map[string]string{"true": "", "false": ""}},
			check:    func(d Decision) bool { return d.Noul == 0.7 && d.Choice == "" && d.Confidence == 0 },
		},
		{
			name:     "score",
			answer:   scoreAnswer,
			question: Question{Type: string(QuestionScore), Instructions: "How risky?", Criteria: map[string]string{"low": "", "high": ""}},
			check:    func(d Decision) bool { return d.Choice == "high" && d.Confidence == 0.9 },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := &capturedRequest{}
			server := decisionsHandler(t, captured, tc.answer, nil)
			defer server.Close()
			j := testOpenRouter(t, server.URL)
			decision, err := j.Decide(context.Background(), Request{QuestionName: "verdict", Question: tc.question})
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			if !tc.check(decision) {
				t.Fatalf("decision: %+v", decision)
			}
		})
	}
}

func TestOpenRouterModelOverridePerRequest(t *testing.T) {
	captured := &capturedRequest{}
	server := decisionsHandler(t, captured, choiceAnswer, nil)
	defer server.Close()
	j := testOpenRouter(t, server.URL)
	if _, err := j.Decide(context.Background(), Request{Model: "typesafe/jev-1.13", QuestionName: "verdict", Question: Question{Type: string(QuestionChoice), Criteria: map[string]string{"a": "", "b": ""}}}); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if captured.Model != "typesafe/jev-1.13" {
		t.Fatalf("model override ignored: %q", captured.Model)
	}
}

func TestOpenRouterRetriesTransientStatus(t *testing.T) {
	captured := &capturedRequest{}
	server := decisionsHandler(t, captured, choiceAnswer, func(attempt int) int {
		if attempt == 1 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	})
	defer server.Close()
	j := testOpenRouter(t, server.URL)
	decision, err := j.Decide(context.Background(), Request{QuestionName: "verdict", Question: Question{Type: string(QuestionChoice), Criteria: map[string]string{"a": "", "b": ""}}})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Choice != "issues_found" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestOpenRouterRetryExhaustionFails(t *testing.T) {
	var requests int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer server.Close()
	j := testOpenRouter(t, server.URL)
	if _, err := j.Decide(context.Background(), Request{QuestionName: "verdict", Question: Question{Type: string(QuestionChoice), Criteria: map[string]string{"a": "", "b": ""}}}); err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("requests: %d", requests)
	}
}

func TestOpenRouterNonRetriableStatus(t *testing.T) {
	var requests int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()
	j := testOpenRouter(t, server.URL)
	if _, err := j.Decide(context.Background(), Request{QuestionName: "verdict", Question: Question{Type: string(QuestionChoice), Criteria: map[string]string{"a": "", "b": ""}}}); err == nil {
		t.Fatal("expected auth error")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("requests: %d", requests)
	}
}

func TestOpenRouterUsesConfiguredProxy(t *testing.T) {
	var proxied int
	var mu sync.Mutex
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxied++
		mu.Unlock()
		// The proxy answers directly; reaching the real endpoint would
		// prove the client bypassed it.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(choiceAnswer))
	}))
	defer proxy.Close()

	j, err := NewOpenRouter(OpenRouterConfig{
		APIKey:      "test-key",
		Model:       "~typesafe/jev-latest",
		ProxyURL:    proxy.URL,
		Endpoint:    "http://decisions.invalid/api/alpha/decisions",
		Timeout:     2 * time.Second,
		MaxAttempts: 1,
	})
	if err != nil {
		t.Fatalf("new openrouter: %v", err)
	}
	decision, err := j.Decide(context.Background(), Request{QuestionName: "verdict", Question: Question{Type: string(QuestionChoice), Criteria: map[string]string{"a": "", "b": ""}}})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Choice != "issues_found" {
		t.Fatalf("decision: %+v", decision)
	}
	mu.Lock()
	defer mu.Unlock()
	if proxied != 1 {
		t.Fatalf("proxy was not used: %d", proxied)
	}
}

func TestOpenRouterValidatesConfig(t *testing.T) {
	if _, err := NewOpenRouter(OpenRouterConfig{Model: "m"}); err == nil {
		t.Fatal("expected missing key error")
	}
	if _, err := NewOpenRouter(OpenRouterConfig{APIKey: "k"}); err == nil {
		t.Fatal("expected missing model error")
	}
}

func TestLoadAPIKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openrouter-api-key")
	if err := os.WriteFile(path, []byte("  sk-or-token \n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	key, err := LoadAPIKey(path)
	if err != nil || key != "sk-or-token" {
		t.Fatalf("load: %q %v", key, err)
	}
	if _, err := LoadAPIKey(filepath.Join(dir, "missing")); err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("missing file error should name the path: %v", err)
	}
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatalf("write empty key: %v", err)
	}
	if _, err := LoadAPIKey(path); err == nil {
		t.Fatal("expected empty key error")
	}
	if err := os.WriteFile(path, []byte("line1\nline2"), 0o600); err != nil {
		t.Fatalf("write multiline key: %v", err)
	}
	if _, err := LoadAPIKey(path); err == nil {
		t.Fatal("expected multiline key error")
	}
}

func TestSetupGating(t *testing.T) {
	home := t.TempDir()
	workflow := &domain.WorkflowDefinition{Tasks: map[string]domain.WorkflowTaskDefinition{
		"review": {Verdicts: map[string]string{"ok": "", "retry": ""}},
	}}

	// No judge section, no verdict tasks: no judge, no key required.
	none, err := Setup(nil, &domain.WorkflowDefinition{Tasks: map[string]domain.WorkflowTaskDefinition{"a": {}}}, home, "")
	if err != nil || none != nil {
		t.Fatalf("expected nil judge, got %v %v", none, err)
	}

	// Verdict tasks without a key file: actionable error naming the path.
	if _, err := Setup(nil, workflow, home, ""); err == nil || !strings.Contains(err.Error(), DefaultKeyPath(home)) {
		t.Fatalf("expected error naming key path: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(home, ".agent-debug-squad"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(DefaultKeyPath(home), []byte("sk-or-token\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	// Verdict tasks with a key: judge constructed with defaults.
	j, err := Setup(nil, workflow, home, "")
	if err != nil || j == nil {
		t.Fatalf("expected judge, got %v %v", j, err)
	}

	// Empty workflow pointer behaves like no workflow.
	none, err = Setup(nil, nil, home, "")
	if err != nil || none != nil {
		t.Fatalf("expected nil judge for nil workflow, got %v %v", none, err)
	}

	// Unsupported provider is rejected.
	if _, err := Setup(&domain.JudgeConfig{Provider: "other"}, nil, home, ""); err == nil {
		t.Fatal("expected unsupported provider error")
	}

	// Explicit key file override is honored.
	custom := filepath.Join(home, "custom-key")
	if err := os.WriteFile(custom, []byte("sk-or-custom"), 0o600); err != nil {
		t.Fatalf("write custom key: %v", err)
	}
	j, err = Setup(&domain.JudgeConfig{APIKeyFile: custom}, nil, home, "")
	if err != nil || j == nil {
		t.Fatalf("expected judge from custom key file, got %v %v", j, err)
	}
}

func TestSetupMachineProxyDefaultApplies(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agent-debug-squad"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(DefaultKeyPath(home), []byte("sk-or-token\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	workflow := &domain.WorkflowDefinition{Tasks: map[string]domain.WorkflowTaskDefinition{
		"review": {Verdicts: map[string]string{"ok": ""}},
	}}

	// The machine default must reach the OpenRouter transport: an unparseable
	// URL makes construction fail, proving the fallback wired the proxy in.
	if _, err := Setup(nil, workflow, home, "://bad-proxy"); err == nil || !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("expected machine proxy to reach transport construction, got %v", err)
	}

	// A valid machine default constructs fine.
	if _, err := Setup(nil, workflow, home, "http://proxy.example:8080"); err != nil {
		t.Fatalf("expected judge with machine proxy default, got %v", err)
	}

	// The session judge proxy wins over the machine default: the session
	// value is valid while the machine value is not, so success proves the
	// machine value was not applied.
	session := &domain.JudgeConfig{ProxyURL: "http://session.example:3128"}
	if _, err := Setup(session, nil, home, "://bad-proxy"); err != nil {
		t.Fatalf("session proxy must win over machine default, got %v", err)
	}
}
