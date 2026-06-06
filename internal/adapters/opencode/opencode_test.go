package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

type captureSink struct {
	stdout []string
	stderr []string
}

func (s *captureSink) StdoutLine(line string) {
	s.stdout = append(s.stdout, line)
}

func (s *captureSink) StderrLine(line string) {
	s.stderr = append(s.stderr, line)
}

func (s *captureSink) Err() error {
	return nil
}

func TestGeneratedMessageIDUsesRunID(t *testing.T) {
	if got := generatedMessageID("run_000123"); got != "msg_ads_run_000123" {
		t.Fatalf("generatedMessageID() = %q, want %q", got, "msg_ads_run_000123")
	}
}

func TestGeneratedMessageIDSanitizesUnsafeCharacters(t *testing.T) {
	if got := generatedMessageID("run/../bad id"); got != "msg_ads_run____bad_id" {
		t.Fatalf("generatedMessageID() = %q, want sanitized id", got)
	}
}

func TestDecodeSSEEventsParsesDataFrames(t *testing.T) {
	input := strings.NewReader("data: {\"type\":\"server.connected\"}\n\n" +
		"data: {\"type\":\"session.idle\",\"properties\":{\"sessionID\":\"session_123\"}}\n\n")

	events, err := decodeSSEEvents(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0] != "{\"type\":\"server.connected\"}" {
		t.Fatalf("events[0] = %q", events[0])
	}
	if events[1] != "{\"type\":\"session.idle\",\"properties\":{\"sessionID\":\"session_123\"}}" {
		t.Fatalf("events[1] = %q", events[1])
	}
}

func TestDecodeSSEEventsIgnoresCommentsAndNonDataLines(t *testing.T) {
	input := strings.NewReader(": keepalive\nid: evt_1\nevent: message\ndata: {\"type\":\"server.connected\"}\n\n")

	events, err := decodeSSEEvents(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "{\"type\":\"server.connected\"}" {
		t.Fatalf("events = %#v, want one server.connected payload", events)
	}
}

func TestDecodeSSEEventsHandlesLargeDataLines(t *testing.T) {
	payload := strings.Repeat("x", 1024*1024+1)
	input := strings.NewReader("data: " + payload + "\n\n")

	events, err := decodeSSEEvents(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != payload {
		t.Fatalf("decoded large events len=%d", len(events))
	}
}

func TestIsRunEventFiltersBySessionID(t *testing.T) {
	event := map[string]any{
		"type": "session.next.tool.called",
		"properties": map[string]any{
			"sessionID": "session_123",
			"tool":      "read",
		},
	}
	if !isRunEvent(event, "session_123", "msg_ads_run_1") {
		t.Fatal("isRunEvent() = false, want true")
	}
	if isRunEvent(event, "session_other", "msg_ads_run_1") {
		t.Fatal("isRunEvent() = true for other session, want false")
	}
}

func TestIsRunEventFiltersMessagePartByMessageID(t *testing.T) {
	event := map[string]any{
		"type": "message.part.updated",
		"properties": map[string]any{
			"sessionID": "session_123",
			"part": map[string]any{
				"messageID": "msg_ads_run_1",
				"type":      "text",
			},
		},
	}
	if !isRunEvent(event, "session_123", "msg_ads_run_1") {
		t.Fatal("isRunEvent() = false, want true")
	}
	if isRunEvent(event, "session_123", "msg_ads_other") {
		t.Fatal("isRunEvent() = true for other message, want false")
	}
}

func TestFallbackTextFromEvent(t *testing.T) {
	delta := map[string]any{
		"type": "session.next.text.delta",
		"properties": map[string]any{
			"sessionID": "session_123",
			"delta":     "hello",
		},
	}
	if got := fallbackTextFromEvent(delta, "msg_ads_run_1"); got.Text != "hello" || got.Replace {
		t.Fatalf("fallbackTextFromEvent(delta) = %#v, want append hello", got)
	}

	part := map[string]any{
		"type": "message.part.updated",
		"properties": map[string]any{
			"sessionID": "session_123",
			"part": map[string]any{
				"messageID": "msg_ads_run_1",
				"type":      "text",
				"text":      "final",
			},
		},
	}
	if got := fallbackTextFromEvent(part, "msg_ads_run_1"); got.Text != "final" || !got.Replace {
		t.Fatalf("fallbackTextFromEvent(part) = %#v, want replacement final", got)
	}
}

func TestFallbackTextUpdatesDistinguishAppendAndReplace(t *testing.T) {
	delta := map[string]any{
		"type": "session.next.text.delta",
		"properties": map[string]any{
			"sessionID": "session_123",
			"delta":     "hello",
		},
	}
	if got := fallbackTextFromEvent(delta, "msg_ads_run_1"); got.Text != "hello" || got.Replace {
		t.Fatalf("fallbackTextFromEvent(delta) = %#v, want append hello", got)
	}

	ended := map[string]any{
		"type": "session.next.text.ended",
		"properties": map[string]any{
			"sessionID": "session_123",
			"text":      "hello world",
		},
	}
	if got := fallbackTextFromEvent(ended, "msg_ads_run_1"); got.Text != "hello world" || !got.Replace {
		t.Fatalf("fallbackTextFromEvent(ended) = %#v, want replacement hello world", got)
	}
}

func TestFallbackTextUpdateMessagePartIsReplacement(t *testing.T) {
	first := map[string]any{
		"type": "message.part.updated",
		"properties": map[string]any{
			"sessionID": "session_123",
			"part": map[string]any{
				"messageID": "msg_ads_run_1",
				"type":      "text",
				"text":      "hello",
			},
		},
	}
	if got := fallbackTextFromEvent(first, "msg_ads_run_1"); got.Text != "hello" || !got.Replace {
		t.Fatalf("fallbackTextFromEvent(first) = %#v, want replacement hello", got)
	}

	second := map[string]any{
		"type": "message.part.updated",
		"properties": map[string]any{
			"sessionID": "session_123",
			"part": map[string]any{
				"messageID": "msg_ads_run_1",
				"type":      "text",
				"text":      "hello world",
			},
		},
	}
	if got := fallbackTextFromEvent(second, "msg_ads_run_1"); got.Text != "hello world" || !got.Replace {
		t.Fatalf("fallbackTextFromEvent(second) = %#v, want replacement hello world", got)
	}
}

func TestMessageHistoryFinalTextSelectsAssistantByParentID(t *testing.T) {
	messages := []sessionMessage{
		{
			Info:  messageInfo{ID: "msg_old", Role: "assistant", ParentID: "msg_old_parent"},
			Parts: []part{{Type: "text", Text: "old"}},
		},
		{
			Info: messageInfo{ID: "msg_assistant", Role: "assistant", ParentID: "msg_ads_run_1"},
			Parts: []part{
				{Type: "text", Text: "line one"},
				{Type: "text", Text: "line two"},
			},
		},
	}

	got, err := finalTextFromMessages(messages, "msg_ads_run_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "line one\nline two" {
		t.Fatalf("finalTextFromMessages() = %q", got)
	}
}

func TestMessageHistoryFinalTextUsesFallbackWhenAssistantMissing(t *testing.T) {
	got, err := finalTextFromMessages(nil, "msg_ads_run_1", "fallback text")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback text" {
		t.Fatalf("finalTextFromMessages() = %q, want fallback text", got)
	}
}

func TestMessageHistoryFinalTextErrorsWhenNoText(t *testing.T) {
	_, err := finalTextFromMessages(nil, "msg_ads_run_1", "")
	if err == nil {
		t.Fatal("finalTextFromMessages() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "without assistant message") {
		t.Fatalf("error = %q, want assistant message error", err.Error())
	}
}

func TestHTTPClientUsesDefaultTimeout(t *testing.T) {
	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{},
	}

	if got := New(spec).httpClient().Timeout; got != defaultHTTPTimeout {
		t.Fatalf("Timeout = %v, want %v", got, defaultHTTPTimeout)
	}
}

func TestHTTPClientUsesConfiguredTimeoutSeconds(t *testing.T) {
	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"timeout_seconds": "3"},
	}

	if got := New(spec).httpClient().Timeout; got != 3*time.Second {
		t.Fatalf("Timeout = %v, want %v", got, 3*time.Second)
	}
}

func TestResetCreatesNewSessionAndClearsContinuity(t *testing.T) {
	var sessionCreates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session" {
			t.Fatalf("request = %s %s, want POST /session", r.Method, r.URL.Path)
		}
		sessionCreates++
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "session_new"})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"base_url": server.URL},
	}
	errText := "old error"
	state := domain.AgentState{
		Name:             "Skeptic",
		Backend:          "opencode",
		StartupPrompt:    "Challenge assumptions.",
		WorkspaceDir:     t.TempDir(),
		BackendSessionID: "session_old",
		Status:           domain.AgentFailed,
		LastRunID:        "run_000123",
		LastError:        &errText,
	}

	reset, err := New(spec).Reset(context.Background(), spec, state)
	if err != nil {
		t.Fatal(err)
	}
	if sessionCreates != 1 {
		t.Fatalf("sessionCreates = %d, want 1", sessionCreates)
	}
	if reset.BackendSessionID != "session_new" {
		t.Fatalf("BackendSessionID = %q, want session_new", reset.BackendSessionID)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}
	if reset.LastError != nil {
		t.Fatalf("LastError = %v, want nil", reset.LastError)
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
}

func TestSendStreamsEventsUntilIdleAndFetchesFinalText(t *testing.T) {
	var gotBody map[string]any
	eventOpened := make(chan struct{})
	connectedSent := make(chan struct{})
	promptSeen := make(chan struct{})
	releaseEvent := make(chan struct{})
	toolEvent := `{"type":"session.next.tool.called","properties":{"sessionID":"session_123","info":{"parentID":"msg_ads_run_1"},"tool":"read"}}`
	idleEvent := `{"type":"session.idle","properties":{"sessionID":"session_123"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			close(eventOpened)
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer does not flush")
			}
			fmt.Fprintln(w, `data: {"type":"server.connected"}`)
			fmt.Fprintln(w)
			flusher.Flush()
			close(connectedSent)
			select {
			case <-promptSeen:
			case <-time.After(time.Second):
				t.Fatal("prompt_async did not arrive after server.connected")
			}
			fmt.Fprintln(w, "data: "+toolEvent)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "data: "+idleEvent)
			fmt.Fprintln(w)
			flusher.Flush()
			select {
			case <-releaseEvent:
			case <-r.Context().Done():
			case <-time.After(time.Second):
				t.Fatal("stream was not closed after session.idle")
			}
		case "/session/session_123/prompt_async":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			select {
			case <-eventOpened:
			default:
				t.Fatal("prompt_async arrived before /event was opened")
			}
			select {
			case <-connectedSent:
			default:
				t.Fatal("prompt_async arrived before server.connected")
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatal(err)
			}
			close(promptSeen)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/session/session_123/message":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]any{"id": "msg_old", "role": "assistant", "parentID": "msg_other"},
					"parts": []map[string]any{{"type": "text", "text": "wrong answer"}},
				},
				{
					"info":  map[string]any{"id": "msg_assistant", "role": "assistant", "parentID": "msg_ads_run_1"},
					"parts": []map[string]any{{"type": "text", "text": "Final OpenCode answer"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer close(releaseEvent)
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
	sink := &captureSink{}

	result, nextState, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Skeptic",
		Message: "hello",
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["messageID"] != "msg_ads_run_1" {
		t.Fatalf("messageID = %v, want msg_ads_run_1; body = %#v", gotBody["messageID"], gotBody)
	}
	model, ok := gotBody["model"].(map[string]any)
	if !ok {
		t.Fatalf("model = %#v, want object", gotBody["model"])
	}
	if model["providerID"] != "anthropic" || model["modelID"] != "claude-sonnet-4.5" {
		t.Fatalf("model = %#v, want providerID/modelID", model)
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
	if len(sink.stdout) != 2 || sink.stdout[0] != toolEvent || sink.stdout[1] != idleEvent {
		t.Fatalf("stdout = %#v, want tool and idle events", sink.stdout)
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
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprintln(w, `data: {"type":"server.connected"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"type":"session.idle","properties":{"sessionID":"session_123"}}`)
			fmt.Fprintln(w)
			flusher.Flush()
		case "/session/session_123/prompt_async":
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/session/session_123/message":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]any{"id": "msg_assistant", "role": "assistant", "parentID": "msg_ads_run_1"},
					"parts": []map[string]any{{"type": "text", "text": "Final OpenCode answer"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
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
	}, domain.DiscardRunSink())
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
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprintln(w, `data: {"type":"server.connected"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"type":"session.idle","properties":{"sessionID":"session_123"}}`)
			fmt.Fprintln(w)
			flusher.Flush()
		case "/session/session_123/prompt_async":
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/session/session_123/message":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]any{"id": "msg_assistant", "role": "assistant", "parentID": "msg_ads_run_1"},
					"parts": []map[string]any{{"type": "text", "text": "Final OpenCode answer"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
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
	}, domain.DiscardRunSink())
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
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprintln(w, `data: {"type":"server.connected"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"type":"session.idle","properties":{"sessionID":"session_123"}}`)
			fmt.Fprintln(w)
			flusher.Flush()
		case "/session/session_123/prompt_async":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/session/session_123/message":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]any{"id": "msg_assistant", "role": "assistant", "parentID": "msg_ads_run_1"},
					"parts": []map[string]any{{"type": "text", "text": "Final OpenCode answer"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
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
	}, domain.DiscardRunSink())
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
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprintln(w, `data: {"type":"server.connected"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"type":"session.idle","properties":{"sessionID":"session_123"}}`)
			fmt.Fprintln(w)
			flusher.Flush()
		case "/session/session_123/prompt_async":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/session/session_123/message":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]any{"id": "msg_assistant", "role": "assistant", "parentID": "msg_ads_run_1"},
					"parts": []map[string]any{{"type": "text", "text": "Final OpenCode answer"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
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
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
}

func TestSendReturnsHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprintln(w, `data: {"type":"server.connected"}`)
			fmt.Fprintln(w)
			flusher.Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		case "/session/session_123/prompt_async":
			http.Error(w, "backend failed", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
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
	}, domain.DiscardRunSink())
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

func TestSendLogsUnsupportedYoloWarning(t *testing.T) {
	var logs bytes.Buffer
	previous := logger
	logger = log.New(&logs, "", 0)
	t.Cleanup(func() { logger = previous })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprintln(w, `data: {"type":"server.connected"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"type":"session.idle","properties":{"sessionID":"session_123"}}`)
			fmt.Fprintln(w)
			flusher.Flush()
		case "/session/session_123/prompt_async":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/session/session_123/message":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]any{"id": "msg_assistant", "role": "assistant", "parentID": "msg_ads_run_1"},
					"parts": []map[string]any{{"type": "text", "text": "ok"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	enabled := true
	spec := domain.AgentSpec{
		Name:          "Critic",
		Backend:       "opencode",
		StartupPrompt: "Review.",
		Yolo:          &enabled,
		StringOptions: map[string]string{"base_url": server.URL},
	}
	state := domain.AgentState{Name: "Critic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir(), LastRunID: "run_previous"}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Critic", Message: "hello"}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "backend=opencode yolo=true unsupported") {
		t.Fatalf("logs = %q, want unsupported yolo warning", logs.String())
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
