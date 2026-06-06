# OpenCode SSE Run Streaming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Change the OpenCode adapter from synchronous `/message` waiting to `prompt_async` plus `/event` SSE streaming, completing runs on `session.idle` and fetching the final assistant message by `parentID`.

**Architecture:** Keep the orchestrator and `RunSink` unchanged. Localize the implementation to `internal/adapters/opencode/opencode.go`, with focused map-based SSE/event helpers and tests in `internal/adapters/opencode/opencode_test.go`. OpenCode raw JSON events flow through `sink.StdoutLine`, while final text still returns through `RunResult.FinalMessage`.

**Tech Stack:** Go, `net/http`, `httptest`, SSE line parsing with `bufio.Scanner`, JSON maps, existing Agent Debug Squad domain interfaces.

---

## File Structure

- Modify `internal/adapters/opencode/opencode.go`: add async send lifecycle, SSE parsing, event filtering, final message history fetch, best-effort abort, and shared request helpers.
- Modify `internal/adapters/opencode/opencode_test.go`: replace synchronous `/message` send expectations with async `/event` + `/prompt_async` + `/message` tests, add event filtering/final answer/error/cancel/auth coverage.
- Modify `README.md`: document that OpenCode also writes `.events.jsonl` during active runs.
- Modify `docs/code-review-squad.md`: clarify that the OpenCode Critic emits raw OpenCode events when using the HTTP adapter.

---

### Task 1: Add Test Helpers And Message ID Unit Tests

**Files:**
- Modify: `internal/adapters/opencode/opencode_test.go`
- Modify: `internal/adapters/opencode/opencode.go`

- [ ] **Step 1: Add a test sink helper**

Append this helper near the top of `internal/adapters/opencode/opencode_test.go`, after imports:

```go
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
```

- [ ] **Step 2: Add failing tests for deterministic OpenCode message IDs**

Add these tests in `internal/adapters/opencode/opencode_test.go`:

```go
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
```

- [ ] **Step 3: Run the focused tests and verify failure**

Run:

```bash
go test ./internal/adapters/opencode -run 'TestGeneratedMessageID' -count=1
```

Expected: FAIL because `generatedMessageID` is undefined.

- [ ] **Step 4: Add message ID generation**

Add this helper to `internal/adapters/opencode/opencode.go`:

```go
func generatedMessageID(runID string) string {
	var b strings.Builder
	b.WriteString("msg_ads_")
	for _, r := range runID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
```

- [ ] **Step 5: Run tests and commit**

Run:

```bash
go test ./internal/adapters/opencode -run 'TestGeneratedMessageID' -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/adapters/opencode/opencode.go internal/adapters/opencode/opencode_test.go
git commit -m "test: cover opencode message ids"
```

---

### Task 2: Add SSE Parsing And Event Filtering

**Files:**
- Modify: `internal/adapters/opencode/opencode.go`
- Modify: `internal/adapters/opencode/opencode_test.go`

- [ ] **Step 1: Add failing tests for SSE parsing**

Add these tests to `internal/adapters/opencode/opencode_test.go`:

```go
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
```

- [ ] **Step 2: Add failing tests for event filtering and text accumulation**

Add these tests:

```go
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
	if got := fallbackTextFromEvent(delta, "msg_ads_run_1"); got != "hello" {
		t.Fatalf("fallbackTextFromEvent(delta) = %q, want hello", got)
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
	if got := fallbackTextFromEvent(part, "msg_ads_run_1"); got != "final" {
		t.Fatalf("fallbackTextFromEvent(part) = %q, want final", got)
	}
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/adapters/opencode -run 'TestDecodeSSEEvents|TestIsRunEvent|TestFallbackTextFromEvent' -count=1
```

Expected: FAIL because helper functions are undefined.

- [ ] **Step 4: Implement SSE and event helpers**

Add imports to `internal/adapters/opencode/opencode.go`:

```go
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)
```

Add these helpers:

```go
func decodeSSEEvents(r io.Reader) ([]string, error) {
	var events []string
	var data []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				events = append(events, strings.Join(data, "\n"))
				data = nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			data = append(data, value)
		}
	}
	if len(data) > 0 {
		events = append(events, strings.Join(data, "\n"))
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

func isRunEvent(event map[string]any, sessionID string, messageID string) bool {
	properties, _ := event["properties"].(map[string]any)
	if stringValue(properties["sessionID"]) != sessionID {
		return false
	}

	part, _ := properties["part"].(map[string]any)
	if partMessageID := stringValue(part["messageID"]); partMessageID != "" {
		return partMessageID == messageID
	}

	info, _ := properties["info"].(map[string]any)
	if infoParentID := stringValue(info["parentID"]); infoParentID != "" {
		return infoParentID == messageID
	}
	if infoID := stringValue(info["id"]); infoID != "" && stringValue(info["role"]) == "user" {
		return infoID == messageID
	}

	return true
}

func fallbackTextFromEvent(event map[string]any, messageID string) string {
	eventType := stringValue(event["type"])
	properties, _ := event["properties"].(map[string]any)
	switch eventType {
	case "session.next.text.delta":
		return stringValue(properties["delta"])
	case "session.next.text.ended":
		return stringValue(properties["text"])
	case "message.part.updated":
		part, _ := properties["part"].(map[string]any)
		if stringValue(part["messageID"]) != messageID || stringValue(part["type"]) != "text" {
			return ""
		}
		return stringValue(part["text"])
	default:
		return ""
	}
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
```

- [ ] **Step 5: Run tests and commit**

Run:

```bash
go test ./internal/adapters/opencode -run 'TestDecodeSSEEvents|TestIsRunEvent|TestFallbackTextFromEvent|TestGeneratedMessageID' -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/adapters/opencode/opencode.go internal/adapters/opencode/opencode_test.go
git commit -m "feat: add opencode sse event helpers"
```

---

### Task 3: Add Final Message Fetching

**Files:**
- Modify: `internal/adapters/opencode/opencode.go`
- Modify: `internal/adapters/opencode/opencode_test.go`

- [ ] **Step 1: Add failing tests for final message extraction**

Add these tests:

```go
func TestMessageHistoryFinalTextSelectsAssistantByParentID(t *testing.T) {
	messages := []sessionMessage{
		{
			Info: messageInfo{ID: "msg_old", Role: "assistant", ParentID: "msg_old_parent"},
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
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/adapters/opencode -run 'TestMessageHistoryFinalText' -count=1
```

Expected: FAIL because message history types and `finalTextFromMessages` are undefined.

- [ ] **Step 3: Add message history types and final text helper**

Replace the existing `messageResponse` type block in `internal/adapters/opencode/opencode.go` with:

```go
type messageResponse struct {
	Info  messageInfo `json:"info"`
	Parts []part      `json:"parts"`
}

type sessionMessage struct {
	Info  messageInfo `json:"info"`
	Parts []part      `json:"parts"`
}

type messageInfo struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	ParentID string `json:"parentID"`
}

type part struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
```

Replace `messageResponse.finalText()` with:

```go
func (r messageResponse) finalText() string {
	return joinTextParts(r.Parts)
}

func finalTextFromMessages(messages []sessionMessage, messageID string, fallback string) (string, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Info.Role != "assistant" || msg.Info.ParentID != messageID {
			continue
		}
		if text := joinTextParts(msg.Parts); text != "" {
			return text, nil
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("opencode run completed without assistant message for messageID %s", messageID)
}

func joinTextParts(parts []part) string {
	var out []string
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}
```

- [ ] **Step 4: Run tests and commit**

Run:

```bash
go test ./internal/adapters/opencode -run 'TestMessageHistoryFinalText|TestSendPostsMessageToSessionAndExtractsFinalText' -count=1
```

Expected: final text helper tests pass. If the old synchronous send test fails at this point, leave that failure for Task 4 where the send path is intentionally replaced.

Commit:

```bash
git add internal/adapters/opencode/opencode.go internal/adapters/opencode/opencode_test.go
git commit -m "feat: extract opencode final message text"
```

---

### Task 4: Implement Async Happy Path Send

**Files:**
- Modify: `internal/adapters/opencode/opencode.go`
- Modify: `internal/adapters/opencode/opencode_test.go`

- [ ] **Step 1: Replace the old happy-path send test with async behavior**

Replace `TestSendPostsMessageToSessionAndExtractsFinalText` in `internal/adapters/opencode/opencode_test.go` with:

```go
func TestSendStreamsEventsUntilIdleAndFetchesFinalText(t *testing.T) {
	var requestOrder []string
	var gotPrompt map[string]any
	eventConnected := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestOrder = append(requestOrder, r.URL.Path)
		switch r.URL.Path {
		case "/event":
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response does not implement http.Flusher")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_ready","type":"server.connected","properties":{}}`)
			_, _ = fmt.Fprintln(w)
			flusher.Flush()
			close(eventConnected)
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_tool","type":"session.next.tool.called","properties":{"sessionID":"session_123","callID":"call_1","tool":"read","input":{"file":"README.md"},"provider":{"executed":true}}}`)
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_idle","type":"session.idle","properties":{"sessionID":"session_123"}}`)
			_, _ = fmt.Fprintln(w)
			flusher.Flush()
		case "/session/session_123/prompt_async":
			select {
			case <-eventConnected:
			default:
				t.Fatal("prompt_async arrived before SSE stream was ready")
			}
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotPrompt); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/session/session_123/message":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]any{"id": "msg_assistant", "role": "assistant", "parentID": "msg_ads_run_1"},
					"parts": []map[string]any{{"type": "text", "text": "Final OpenCode answer"}},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
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
	sink := &captureSink{}

	result, nextState, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Skeptic",
		Message: "hello",
	}, sink)
	if err != nil {
		t.Fatal(err)
	}

	if result.FinalMessage != "Final OpenCode answer" {
		t.Fatalf("FinalMessage = %q, want Final OpenCode answer", result.FinalMessage)
	}
	if nextState.LastRunID != "run_1" {
		t.Fatalf("LastRunID = %q, want run_1", nextState.LastRunID)
	}
	if gotPrompt["messageID"] != "msg_ads_run_1" {
		t.Fatalf("messageID = %#v, want msg_ads_run_1", gotPrompt["messageID"])
	}
	if gotPrompt["agent"] != "build" {
		t.Fatalf("agent = %#v, want build", gotPrompt["agent"])
	}
	model := gotPrompt["model"].(map[string]any)
	if model["providerID"] != "anthropic" || model["modelID"] != "claude-sonnet-4.5" {
		t.Fatalf("model = %#v", model)
	}
	if len(sink.stdout) != 2 {
		t.Fatalf("sink stdout = %#v, want tool and idle events", sink.stdout)
	}
	if !strings.Contains(sink.stdout[0], `"session.next.tool.called"`) {
		t.Fatalf("first stdout event = %q", sink.stdout[0])
	}
	if !strings.Contains(sink.stdout[1], `"session.idle"`) {
		t.Fatalf("second stdout event = %q", sink.stdout[1])
	}
	if requestOrder[0] != "/event" {
		t.Fatalf("first request = %q, want /event; order=%#v", requestOrder[0], requestOrder)
	}
}
```

- [ ] **Step 2: Update startup prompt test expectations for async prompt body**

In `TestSendIncludesStartupPromptOnFirstRun`, change the fake server to handle `/event`, `/session/session_123/prompt_async`, and `/session/session_123/message` using the same pattern as the previous step. Keep this assertion:

```go
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
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/adapters/opencode -run 'TestSendStreamsEventsUntilIdleAndFetchesFinalText|TestSendIncludesStartupPromptOnFirstRun' -count=1
```

Expected: FAIL because `Send` still posts to `/message`.

- [ ] **Step 4: Add async send support**

Add these helper types to `internal/adapters/opencode/opencode.go`:

```go
type streamResult struct {
	FallbackText string
	ErrorMessage string
	Err          error
}
```

Add this streaming SSE helper:

```go
func scanSSEEvents(r io.Reader, handle func(string) bool) error {
	var data []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				if stop := handle(strings.Join(data, "\n")); stop {
					return nil
				}
				data = nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			data = append(data, value)
		}
	}
	if len(data) > 0 {
		_ = handle(strings.Join(data, "\n"))
	}
	return scanner.Err()
}
```

Add request helpers:

```go
func (a *Adapter) newRequest(ctx context.Context, method string, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL()+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if password := a.spec.StringOptions["password"]; password != "" {
		username := a.spec.StringOptions["username"]
		if username == "" {
			username = "opencode"
		}
		req.SetBasicAuth(username, password)
	}
	return req, nil
}

func (a *Adapter) doJSON(ctx context.Context, method string, path string, body any, out any) error {
	req, err := a.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpStatusError(path, resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

Replace `postJSON` body with:

```go
func (a *Adapter) postJSON(ctx context.Context, path string, body any, out any) error {
	return a.doJSON(ctx, http.MethodPost, path, body, out)
}
```

Add async helpers:

```go
func (a *Adapter) promptBody(message string, messageID string) map[string]any {
	body := map[string]any{
		"messageID": messageID,
		"parts": []map[string]any{
			{"type": "text", "text": message},
		},
	}
	if model := a.spec.StringOptions["model"]; model != "" {
		body["model"] = modelPayload(model)
	}
	if agent := a.spec.StringOptions["agent"]; agent != "" {
		body["agent"] = agent
	}
	return body
}

func (a *Adapter) streamEvents(ctx context.Context, sessionID string, messageID string, sink domain.RunSink, ready chan<- error) streamResult {
	req, err := a.newRequest(ctx, http.MethodGet, "/event", nil)
	if err != nil {
		ready <- err
		return streamResult{Err: err}
	}
	resp, err := a.httpClient().Do(req)
	if err != nil {
		ready <- err
		return streamResult{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := httpStatusError("/event", resp)
		ready <- err
		return streamResult{Err: err}
	}

	var fallback strings.Builder
	readySent := false
	terminal := false
	var result streamResult

	err = scanSSEEvents(resp.Body, func(line string) bool {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			result.Err = err
			return true
		}
		if stringValue(event["type"]) == "server.connected" {
			if !readySent {
				ready <- nil
				readySent = true
			}
			return false
		}
		if !isRunEvent(event, sessionID, messageID) {
			return false
		}
		sink.StdoutLine(line)
		if text := fallbackTextFromEvent(event, messageID); text != "" {
			fallback.WriteString(text)
		}
		switch stringValue(event["type"]) {
		case "session.idle":
			terminal = true
			result.FallbackText = fallback.String()
			return true
		case "session.error":
			terminal = true
			result.FallbackText = fallback.String()
			result.ErrorMessage = line
			return true
		}
		return false
	})
	if !readySent {
		if err != nil {
			ready <- err
		} else {
			ready <- errors.New("opencode event stream ended before server.connected")
		}
		readySent = true
	}
	if result.Err != nil {
		return result
	}
	if terminal {
		return result
	}
	if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
		return streamResult{FallbackText: fallback.String(), Err: err}
	}
	if err := ctx.Err(); err != nil {
		return streamResult{FallbackText: fallback.String(), Err: err}
	}
	return streamResult{FallbackText: fallback.String(), Err: errors.New("opencode event stream ended before session.idle")}
}

func (a *Adapter) fetchFinalMessage(ctx context.Context, sessionID string, messageID string, fallback string) (string, error) {
	var messages []sessionMessage
	if err := a.doJSON(ctx, http.MethodGet, "/session/"+sessionID+"/message", nil, &messages); err != nil {
		return "", err
	}
	return finalTextFromMessages(messages, messageID, fallback)
}
```

Replace the send request part in `Send` with:

```go
	messageID := generatedMessageID(run.RunID)
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	ready := make(chan error, 1)
	streamDone := make(chan streamResult, 1)
	go func() {
		streamDone <- a.streamEvents(streamCtx, state.BackendSessionID, messageID, sink, ready)
	}()

	if err := <-ready; err != nil {
		cancelStream()
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}

	body := a.promptBody(message, messageID)
	if err := a.postJSON(ctx, "/session/"+state.BackendSessionID+"/prompt_async", body, nil); err != nil {
		cancelStream()
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}

	streamResult := <-streamDone
	if streamResult.Err != nil {
		return domain.RunResult{ErrorMessage: streamResult.Err.Error()}, state, streamResult.Err
	}
	if streamResult.ErrorMessage != "" {
		err := errors.New(streamResult.ErrorMessage)
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}

	finalMessage, err := a.fetchFinalMessage(ctx, state.BackendSessionID, messageID, streamResult.FallbackText)
	if err != nil {
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}

	state.Status = domain.AgentIdle
	state.LastRunID = run.RunID
	return domain.RunResult{FinalMessage: finalMessage}, state, nil
```

- [ ] **Step 5: Run focused tests and commit**

Run:

```bash
go test ./internal/adapters/opencode -run 'TestSendStreamsEventsUntilIdleAndFetchesFinalText|TestSendIncludesStartupPromptOnFirstRun|TestSendOmitsEmptyModelAndAgent' -count=1
```

Expected: PASS after updating the affected old tests to serve `/event`, `/prompt_async`, and final `/message`.

Commit:

```bash
git add internal/adapters/opencode/opencode.go internal/adapters/opencode/opencode_test.go
git commit -m "feat: stream opencode runs over sse"
```

---

### Task 5: Add Error, Cancellation, Auth, And Fallback Coverage

**Files:**
- Modify: `internal/adapters/opencode/opencode.go`
- Modify: `internal/adapters/opencode/opencode_test.go`

- [ ] **Step 1: Add tests for fallback text and irrelevant event filtering**

Add:

```go
func TestSendUsesFallbackTextWhenHistoryHasNoAssistant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_ready","type":"server.connected","properties":{}}`)
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_delta","type":"session.next.text.delta","properties":{"sessionID":"session_123","delta":"fallback"}}`)
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_idle","type":"session.idle","properties":{"sessionID":"session_123"}}`)
			_, _ = fmt.Fprintln(w)
		case "/session/session_123/prompt_async":
			w.WriteHeader(http.StatusNoContent)
		case "/session/session_123/message":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()

	spec := domain.AgentSpec{Name: "Skeptic", Backend: "opencode", StringOptions: map[string]string{"base_url": server.URL}}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", LastRunID: "run_previous"}

	result, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Skeptic", Message: "hello"}, &captureSink{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "fallback" {
		t.Fatalf("FinalMessage = %q, want fallback", result.FinalMessage)
	}
}
```

- [ ] **Step 2: Add tests for session.error and SSE disconnect**

Add:

```go
func TestSendFailsOnSessionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_ready","type":"server.connected","properties":{}}`)
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_error","type":"session.error","properties":{"sessionID":"session_123","error":{"name":"ProviderAuthError","message":"auth failed"}}}`)
			_, _ = fmt.Fprintln(w)
		case "/session/session_123/prompt_async":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()

	spec := domain.AgentSpec{Name: "Skeptic", Backend: "opencode", StringOptions: map[string]string{"base_url": server.URL}}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", LastRunID: "run_previous"}
	result, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Skeptic", Message: "hello"}, &captureSink{})
	if err == nil {
		t.Fatal("Send() error = nil, want session error")
	}
	if !strings.Contains(result.ErrorMessage, "session.error") {
		t.Fatalf("ErrorMessage = %q, want session.error payload", result.ErrorMessage)
	}
}

func TestSendFailsWhenSSEEndsBeforeIdle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_ready","type":"server.connected","properties":{}}`)
			_, _ = fmt.Fprintln(w)
		case "/session/session_123/prompt_async":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()

	spec := domain.AgentSpec{Name: "Skeptic", Backend: "opencode", StringOptions: map[string]string{"base_url": server.URL}}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", LastRunID: "run_previous"}
	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Skeptic", Message: "hello"}, &captureSink{})
	if err == nil {
		t.Fatal("Send() error = nil, want stream ended error")
	}
	if !strings.Contains(err.Error(), "event stream ended before session.idle") {
		t.Fatalf("error = %q", err.Error())
	}
}
```

- [ ] **Step 3: Add cancellation and abort test**

Add:

```go
func TestSendAbortsSessionOnContextCancellation(t *testing.T) {
	abortCalled := make(chan struct{}, 1)
	promptSeen := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_ready","type":"server.connected","properties":{}}`)
			_, _ = fmt.Fprintln(w)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		case "/session/session_123/prompt_async":
			close(promptSeen)
			w.WriteHeader(http.StatusNoContent)
		case "/session/session_123/abort":
			abortCalled <- struct{}{}
			_ = json.NewEncoder(w).Encode(true)
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()

	spec := domain.AgentSpec{Name: "Skeptic", Backend: "opencode", StringOptions: map[string]string{"base_url": server.URL}}
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", LastRunID: "run_previous"}
	ctx, cancel := context.WithCancel(context.Background())

	errC := make(chan error, 1)
	go func() {
		_, _, err := New(spec).Send(ctx, state, domain.RunRequest{RunID: "run_1", Agent: "Skeptic", Message: "hello"}, &captureSink{})
		errC <- err
	}()

	<-promptSeen
	cancel()

	select {
	case err := <-errC:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return after cancellation")
	}
	select {
	case <-abortCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("abort was not called")
	}
}
```

- [ ] **Step 4: Implement best-effort abort on cancellation**

Add:

```go
func (a *Adapter) abortSession(ctx context.Context, sessionID string) {
	abortCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if ctx.Err() == nil {
		return
	}
	_ = a.postJSON(abortCtx, "/session/"+sessionID+"/abort", nil, nil)
}
```

Update the stream error handling in `Send`:

```go
	if streamResult.Err != nil {
		if errors.Is(streamResult.Err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			a.abortSession(ctx, state.BackendSessionID)
		}
		return domain.RunResult{ErrorMessage: streamResult.Err.Error()}, state, streamResult.Err
	}
```

- [ ] **Step 5: Update Basic Auth tests for all async endpoints**

Replace `TestSendUsesDefaultBasicAuthUsername` and `TestSendUsesConfiguredBasicAuthUsername` server handlers with a handler that accepts `/event`, `/session/session_123/prompt_async`, and `/session/session_123/message`, and asserts Basic Auth on every request:

```go
func assertBasicAuth(t *testing.T, r *http.Request, wantUser string, wantPassword string) {
	t.Helper()
	username, password, ok := r.BasicAuth()
	if !ok {
		t.Fatalf("BasicAuth missing for %s", r.URL.Path)
	}
	if username != wantUser || password != wantPassword {
		t.Fatalf("BasicAuth = %q/%q, want %q/%q", username, password, wantUser, wantPassword)
	}
}
```

Use `assertBasicAuth(t, r, "opencode", "secret")` for default username and `assertBasicAuth(t, r, "custom", "secret")` for configured username.

- [ ] **Step 6: Run full OpenCode adapter tests and commit**

Run:

```bash
go test ./internal/adapters/opencode -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/adapters/opencode/opencode.go internal/adapters/opencode/opencode_test.go
git commit -m "test: cover opencode async run errors"
```

---

### Task 6: Verify Orchestrator Artifact Integration

**Files:**
- Modify: `internal/orchestrator/orchestrator_test.go`

- [ ] **Step 1: Add integration test with fake OpenCode HTTP server**

Add this test to `internal/orchestrator/orchestrator_test.go`:

```go
func TestOpenCodeRunWritesSSEEventsArtifact(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "session_123"})
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_ready","type":"server.connected","properties":{}}`)
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_tool","type":"session.next.tool.called","properties":{"sessionID":"session_123","callID":"call_1","tool":"read","input":{},"provider":{"executed":true}}}`)
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, `data: {"id":"evt_idle","type":"session.idle","properties":{"sessionID":"session_123"}}`)
			_, _ = fmt.Fprintln(w)
		case "/session/session_123/prompt_async":
			w.WriteHeader(http.StatusNoContent)
		case "/session/session_123/message":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]any{"id": "msg_assistant", "role": "assistant", "parentID": "msg_ads_run_000001"},
					"parts": []map[string]any{{"type": "text", "text": "done"}},
				},
			})
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := domain.SessionConfig{
		SessionName:  "test",
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
		Agents: []domain.AgentSpec{{
			Name:          "Critic",
			Backend:       "opencode",
			StartupPrompt: "Review.",
			StringOptions: map[string]string{"base_url": server.URL},
		}},
	}
	st := store.New(cfg)
	o, err := New(context.Background(), cfg, st)
	if err != nil {
		t.Fatal(err)
	}

	run, err := o.SubmitRun(context.Background(), "Critic", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := o.Wait(context.Background(), run.RunID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("Status = %q, want completed", completed.Status)
	}

	eventsPath := filepath.Join(root, ".agent-debug-squad", "sessions", "session_test", "runs", run.RunID, "Critic.events.jsonl")
	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), "session.next.tool.called") || !strings.Contains(string(events), "session.idle") {
		t.Fatalf("events artifact = %q", string(events))
	}
}
```

Ensure imports include `encoding/json`, `fmt`, `net/http`, `net/http/httptest`, `os`, and `path/filepath` if not already present.

- [ ] **Step 2: Run test and verify failure or pass**

Run:

```bash
go test ./internal/orchestrator -run TestOpenCodeRunWritesSSEEventsArtifact -count=1
```

Expected: PASS if Task 4 implementation already integrates cleanly; otherwise fix compile/import issues only.

- [ ] **Step 3: Run orchestrator tests and commit**

Run:

```bash
go test ./internal/orchestrator -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/orchestrator/orchestrator_test.go
git commit -m "test: verify opencode event artifacts"
```

---

### Task 7: Update Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/code-review-squad.md`

- [ ] **Step 1: Update README output layout note**

In `README.md`, replace the sentence that starts with `CLI-backed agents stream intermediate stdout` with:

```markdown
CLI-backed agents stream intermediate stdout into `.events.jsonl` and stderr into `.stderr.log` while the run is still active. OpenCode-backed agents stream raw OpenCode `/event` JSON into `.events.jsonl` while the run is active, then write the final assistant response to `<agent>.txt` after `session.idle`. The same lines are emitted to the server log with `run`, `agent`, and `stream` fields. YOLO mode defaults to `defaults.yolo: true`; Codex uses `--dangerously-bypass-approvals-and-sandbox`, while Kimi prompt mode ignores YOLO because Kimi 0.10.1 rejects `--prompt` combined with permission flags and OpenCode HTTP mode does not expose an equivalent permission bypass. An agent can opt out with `options.yolo: false`.
```

- [ ] **Step 2: Update code review squad docs**

In `docs/code-review-squad.md`, add this paragraph after the artifact path list:

```markdown
For OpenCode agents, `<agent>.events.jsonl` contains raw OpenCode `/event` payloads for the active session, including `session.next.*`, `message.*`, `session.error`, and `session.idle` events. The final `<agent>.txt` output is still assembled from OpenCode message history after the session returns to idle.
```

- [ ] **Step 3: Run markdown/content checks**

Run:

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 4: Commit docs**

Commit:

```bash
git add README.md docs/code-review-squad.md
git commit -m "docs: describe opencode event streaming"
```

---

### Task 8: Full Verification

**Files:**
- No source edits expected.

- [ ] **Step 1: Run all Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run formatting**

Run:

```bash
gofmt -w internal/adapters/opencode/opencode.go internal/adapters/opencode/opencode_test.go internal/orchestrator/orchestrator_test.go
```

Expected: command exits 0.

- [ ] **Step 3: Re-run all Go tests after formatting**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Check repository status**

Run:

```bash
git status --short --branch
```

Expected: clean worktree on current detached HEAD or branch.

- [ ] **Step 5: Optional live smoke test with local OpenCode**

Only run this if OpenCode credentials and a lightweight model are available:

```bash
opencode serve --port 45123 --hostname 127.0.0.1
```

In another terminal, configure an OpenCode agent with `base_url: http://127.0.0.1:45123`, submit a short run, and inspect:

```bash
tail -f .agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.events.jsonl
cat .agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.txt
```

Expected: `.events.jsonl` receives OpenCode events before the final run completes, and `<agent>.txt` contains the final assistant response.

---

## Self-Review

- Spec coverage: the plan covers async `/prompt_async`, `/event` SSE, raw event sink writes, `session.idle` completion, final message lookup by `parentID`, fallback text, errors, cancellation abort, Basic Auth, docs, and full verification.
- Scope: the changes are limited to OpenCode adapter behavior, focused tests, one orchestrator integration test, and docs.
- Type consistency: helper names used across tasks are `generatedMessageID`, `decodeSSEEvents`, `isRunEvent`, `fallbackTextFromEvent`, `finalTextFromMessages`, `promptBody`, `streamEvents`, `fetchFinalMessage`, and `abortSession`.
