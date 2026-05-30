package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

func TestSendPostsMessageToSessionAndExtractsFinalText(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want %s", r.Method, http.MethodPost)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"info": map[string]any{"id": "msg_1"},
			"parts": []map[string]any{
				{"type": "text", "text": "Final OpenCode answer"},
			},
		})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{
			"base_url": server.URL,
			"model":    "anthropic/claude-sonnet-4.5",
			"agent":    "build",
		},
	}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir(), LastRunID: "run_previous"}

	result, nextState, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Skeptic",
		Message: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/session/session_123/message" {
		t.Fatalf("path = %s, want %s", gotPath, "/session/session_123/message")
	}
	if gotBody["model"] != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("model = %v, body = %#v", gotBody["model"], gotBody)
	}
	if gotBody["agent"] != "build" {
		t.Fatalf("agent = %v, body = %#v", gotBody["agent"], gotBody)
	}
	parts, ok := gotBody["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("parts = %#v, want one text part", gotBody["parts"])
	}
	part, ok := parts[0].(map[string]any)
	if !ok || part["text"] != "hello" {
		t.Fatalf("part = %#v, want text hello", parts[0])
	}
	if result.FinalMessage != "Final OpenCode answer" {
		t.Fatalf("FinalMessage = %q, want %q", result.FinalMessage, "Final OpenCode answer")
	}
	if nextState.LastRunID != "run_1" {
		t.Fatalf("LastRunID = %q, want %q", nextState.LastRunID, "run_1")
	}
}

func TestSendIncludesStartupPromptOnFirstRun(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"parts": []map[string]any{
				{"type": "text", "text": "Final OpenCode answer"},
			},
		})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"base_url": server.URL},
	}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Skeptic",
		Message: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}

	parts, ok := gotBody["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("parts = %#v, want one text part", gotBody["parts"])
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("part = %#v, want object", parts[0])
	}
	want := "Startup prompt:\nChallenge assumptions.\n\nFacilitator message:\nhello"
	if part["text"] != want {
		t.Fatalf("part text = %q, want %q", part["text"], want)
	}
}

func TestSendOmitsEmptyModelAndAgent(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"parts": []map[string]any{
				{"type": "text", "text": "Final OpenCode answer"},
			},
		})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"base_url": server.URL},
	}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Skeptic",
		Message: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["model"]; ok {
		t.Fatalf("body unexpectedly included empty model: %#v", gotBody)
	}
	if _, ok := gotBody["agent"]; ok {
		t.Fatalf("body unexpectedly included empty agent: %#v", gotBody)
	}
}

func TestSendUsesDefaultBasicAuthUsername(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			t.Fatal("BasicAuth missing")
		}
		if username != "opencode" || password != "secret" {
			t.Fatalf("BasicAuth = %q/%q, want %q/%q", username, password, "opencode", "secret")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"parts": []map[string]any{
				{"type": "text", "text": "Final OpenCode answer"},
			},
		})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{
			"base_url": server.URL,
			"password": "secret",
		},
	}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Skeptic",
		Message: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSendUsesConfiguredBasicAuthUsername(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			t.Fatal("BasicAuth missing")
		}
		if username != "custom" || password != "secret" {
			t.Fatalf("BasicAuth = %q/%q, want %q/%q", username, password, "custom", "secret")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"parts": []map[string]any{
				{"type": "text", "text": "Final OpenCode answer"},
			},
		})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{
			"base_url": server.URL,
			"username": "custom",
			"password": "secret",
		},
	}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Skeptic",
		Message: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSendReturnsHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"base_url": server.URL},
	}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir()}

	result, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Skeptic",
		Message: "hello",
	})
	if err == nil {
		t.Fatal("Send() error = nil, want HTTP status error")
	}
	if !strings.Contains(err.Error(), "opencode HTTP 500") {
		t.Fatalf("error = %q, want HTTP status", err.Error())
	}
	if result.ErrorMessage != err.Error() {
		t.Fatalf("ErrorMessage = %q, want %q", result.ErrorMessage, err.Error())
	}
}

func TestInitCreatesSessionWhenMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "created_session"})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"base_url": server.URL},
	}

	state, err := New(spec).Init(context.Background(), spec, domain.AgentState{})
	if err != nil {
		t.Fatal(err)
	}
	if state.BackendSessionID != "created_session" {
		t.Fatalf("BackendSessionID = %q, want %q", state.BackendSessionID, "created_session")
	}
}

func TestInitReturnsHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad session request", http.StatusBadRequest)
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"base_url": server.URL},
	}

	_, err := New(spec).Init(context.Background(), spec, domain.AgentState{})
	if err == nil {
		t.Fatal("Init() error = nil, want HTTP status error")
	}
	if !strings.Contains(err.Error(), "opencode HTTP 400") {
		t.Fatalf("error = %q, want HTTP status", err.Error())
	}
}
