package opencode

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

	"github.com/andrey/agent-debug-squad/internal/adapters/promptfmt"
	"github.com/andrey/agent-debug-squad/internal/domain"
)

const (
	defaultBaseURL     = "http://127.0.0.1:4096"
	defaultHTTPTimeout = 10 * time.Minute
)

var logger = log.Default()

type Adapter struct {
	spec domain.AgentSpec
}

type sessionResponse struct {
	ID string `json:"id"`
}

type sessionMessage struct {
	Info  messageInfo `json:"info"`
	Parts []part      `json:"parts"`
}

type streamResult struct {
	FallbackText string
	ErrorMessage string
	Err          error
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

func New(spec domain.AgentSpec) *Adapter {
	return &Adapter{spec: spec}
}

func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if state.Name == "" {
		state = domain.AgentState{
			Name:          spec.Name,
			Backend:       spec.Backend,
			StartupPrompt: spec.StartupPrompt,
			Status:        domain.AgentIdle,
			CreatedAt:     time.Now().UTC(),
		}
	}
	if state.BackendSessionID == "" {
		id, err := a.createSession(ctx)
		if err != nil {
			return state, err
		}
		state.BackendSessionID = id
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error) {
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	if state.BackendSessionID == "" {
		err := errors.New("opencode backend_session_id is empty")
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}
	if a.spec.Yolo != nil && *a.spec.Yolo {
		logger.Printf("agent=%s backend=opencode yolo=true unsupported by opencode HTTP adapter", state.Name)
	}

	message := run.Message
	if state.LastRunID == "" {
		message = promptfmt.WithStartupPrompt(a.startupPrompt(state), run.Message)
	}

	sendCtx, cancelSend := context.WithTimeout(ctx, a.httpTimeout())
	defer cancelSend()

	messageID := generatedMessageID(run.RunID)
	streamCtx, cancel := context.WithCancel(sendCtx)
	defer cancel()

	ready := make(chan error, 1)
	streamDone := make(chan streamResult, 1)
	go func() {
		streamDone <- a.streamEvents(streamCtx, state.BackendSessionID, messageID, sink, ready)
	}()

	if err := <-ready; err != nil {
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}

	if err := a.doJSON(sendCtx, http.MethodPost, "/session/"+state.BackendSessionID+"/prompt_async", a.promptBody(message, messageID), nil); err != nil {
		cancel()
		<-streamDone
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}

	stream := <-streamDone
	if stream.Err != nil {
		return domain.RunResult{ErrorMessage: stream.Err.Error()}, state, stream.Err
	}
	if stream.ErrorMessage != "" {
		err := errors.New(stream.ErrorMessage)
		return domain.RunResult{ErrorMessage: stream.ErrorMessage}, state, err
	}

	final, err := a.fetchFinalMessage(sendCtx, state.BackendSessionID, messageID, stream.FallbackText)
	if err != nil {
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}
	state.Status = domain.AgentIdle
	state.LastRunID = run.RunID
	return domain.RunResult{FinalMessage: final}, state, nil
}

func (a *Adapter) startupPrompt(state domain.AgentState) string {
	if state.StartupPrompt != "" {
		return state.StartupPrompt
	}
	return a.spec.StartupPrompt
}

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	id, err := a.createSession(ctx)
	if err != nil {
		return state, err
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = spec.StringOptions["model"]
	state.StartupPrompt = spec.StartupPrompt
	state.BackendSessionID = id
	state.Status = domain.AgentIdle
	state.LastRunID = ""
	state.LastError = nil
	return state, nil
}

func (a *Adapter) createSession(ctx context.Context) (string, error) {
	var response sessionResponse
	if err := a.postJSON(ctx, "/session", map[string]any{"title": a.spec.Name}, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", errors.New("opencode create session response did not include id")
	}
	return response.ID, nil
}

func (a *Adapter) postJSON(ctx context.Context, path string, body any, out any) error {
	return a.doJSON(ctx, http.MethodPost, path, body, out)
}

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
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

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

	var result streamResult
	var terminal bool
	var readySent bool
	sendReady := func(err error) {
		if readySent {
			return
		}
		readySent = true
		ready <- err
	}

	var fallback string
	var handleErr error
	err = scanSSEEvents(resp.Body, func(raw string) bool {
		var event map[string]any
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			handleErr = err
			sendReady(err)
			return true
		}

		eventType := stringValue(event["type"])
		if eventType == "server.connected" {
			sendReady(nil)
			return false
		}

		if !isRunEvent(event, sessionID, messageID) {
			return false
		}

		sink.StdoutLine(raw)
		if err := sink.Err(); err != nil {
			handleErr = err
			return true
		}

		update := fallbackTextFromEvent(event, messageID)
		if update.Replace {
			fallback = update.Text
		} else if update.Text != "" {
			fallback += update.Text
		}

		switch eventType {
		case "session.idle":
			terminal = true
			result.FallbackText = fallback
			return true
		case "session.error":
			terminal = true
			result.FallbackText = fallback
			result.ErrorMessage = errorMessageFromEvent(event)
			return true
		default:
			return false
		}
	})
	if handleErr != nil {
		return streamResult{FallbackText: fallback, Err: handleErr}
	}
	if err != nil {
		sendReady(err)
		return streamResult{FallbackText: fallback, Err: err}
	}
	if !readySent {
		err := errors.New("opencode event stream ended before server.connected")
		sendReady(err)
		return streamResult{FallbackText: fallback, Err: err}
	}
	if terminal {
		return result
	}

	err = errors.New("opencode event stream ended before session.idle")
	return streamResult{FallbackText: fallback, Err: err}
}

func (a *Adapter) fetchFinalMessage(ctx context.Context, sessionID string, messageID string, fallback string) (string, error) {
	var messages []sessionMessage
	if err := a.doJSON(ctx, http.MethodGet, "/session/"+sessionID+"/message", nil, &messages); err != nil {
		return "", err
	}
	return finalTextFromMessages(messages, messageID, fallback)
}

func (a *Adapter) baseURL() string {
	baseURL := strings.TrimRight(a.spec.StringOptions["base_url"], "/")
	if baseURL == "" {
		return defaultBaseURL
	}
	return baseURL
}

func (a *Adapter) httpClient() *http.Client {
	return &http.Client{Timeout: a.httpTimeout()}
}

func (a *Adapter) httpTimeout() time.Duration {
	value := strings.TrimSpace(a.spec.StringOptions["timeout_seconds"])
	if value == "" {
		return defaultHTTPTimeout
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return defaultHTTPTimeout
	}
	return time.Duration(seconds) * time.Second
}

func modelPayload(model string) map[string]string {
	providerID, modelID, ok := strings.Cut(model, "/")
	if !ok {
		return map[string]string{
			"providerID": "",
			"modelID":    model,
		}
	}
	return map[string]string{
		"providerID": providerID,
		"modelID":    modelID,
	}
}

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

func decodeSSEEvents(r io.Reader) ([]string, error) {
	reader := bufio.NewReader(r)

	var events []string
	var frame []string
	flush := func() {
		if len(frame) == 0 {
			return
		}
		events = append(events, strings.Join(frame, "\n"))
		frame = nil
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
		}
		if line == "" {
			flush()
		} else if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = data[1:]
			}
			frame = append(frame, data)
		}
		if err == io.EOF {
			break
		}
	}
	flush()
	return events, nil
}

func scanSSEEvents(r io.Reader, handle func(string) bool) error {
	reader := bufio.NewReader(r)

	var frame []string
	flush := func() bool {
		if len(frame) == 0 {
			return false
		}
		event := strings.Join(frame, "\n")
		frame = nil
		return handle(event)
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
		}
		if line == "" {
			if flush() {
				return nil
			}
		} else if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = data[1:]
			}
			frame = append(frame, data)
		}
		if err == io.EOF {
			break
		}
	}
	if flush() {
		return nil
	}
	return nil
}

func isRunEvent(event map[string]any, sessionID string, messageID string) bool {
	properties, ok := event["properties"].(map[string]any)
	if !ok || stringValue(properties["sessionID"]) != sessionID {
		return false
	}

	if part, ok := properties["part"].(map[string]any); ok {
		partMessageID := stringValue(part["messageID"])
		if partMessageID != "" {
			return partMessageID == messageID
		}
	}

	if info, ok := properties["info"].(map[string]any); ok {
		parentID := stringValue(info["parentID"])
		if parentID != "" {
			return parentID == messageID
		}
		if stringValue(info["role"]) == "user" {
			infoID := stringValue(info["id"])
			if infoID != "" {
				return infoID == messageID
			}
		}
	}

	return true
}

type fallbackTextUpdate struct {
	Text    string
	Replace bool
}

func fallbackTextFromEvent(event map[string]any, messageID string) fallbackTextUpdate {
	properties, ok := event["properties"].(map[string]any)
	if !ok {
		return fallbackTextUpdate{}
	}

	switch stringValue(event["type"]) {
	case "session.next.text.delta":
		return fallbackTextUpdate{Text: stringValue(properties["delta"])}
	case "session.next.text.ended":
		return fallbackTextUpdate{Text: stringValue(properties["text"]), Replace: true}
	case "message.part.updated":
		part, ok := properties["part"].(map[string]any)
		if !ok {
			return fallbackTextUpdate{}
		}
		if stringValue(part["messageID"]) != messageID || stringValue(part["type"]) != "text" {
			return fallbackTextUpdate{}
		}
		return fallbackTextUpdate{Text: stringValue(part["text"]), Replace: true}
	default:
		return fallbackTextUpdate{}
	}
}

func errorMessageFromEvent(event map[string]any) string {
	properties, ok := event["properties"].(map[string]any)
	if !ok {
		return "opencode session.error"
	}
	if message := stringValue(properties["message"]); message != "" {
		return message
	}
	if err := stringValue(properties["error"]); err != "" {
		return err
	}
	if err, ok := properties["error"].(map[string]any); ok {
		if message := stringValue(err["message"]); message != "" {
			return message
		}
	}
	return "opencode session.error"
}

func stringValue(value any) string {
	if value, ok := value.(string); ok {
		return value
	}
	return ""
}

func finalTextFromMessages(messages []sessionMessage, messageID string, fallback string) (string, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Info.Role != "assistant" || message.Info.ParentID != messageID {
			continue
		}
		if text := joinTextParts(message.Parts); text != "" {
			return text, nil
		}
	}

	if text := strings.TrimSpace(fallback); text != "" {
		return fallback, nil
	}

	return "", fmt.Errorf("opencode run completed without assistant message for messageID %s", messageID)
}

func joinTextParts(parts []part) string {
	var out []string
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}

func httpStatusError(path string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Errorf("opencode HTTP %d for %s", resp.StatusCode, path)
	}
	return fmt.Errorf("opencode HTTP %d for %s: %s", resp.StatusCode, path, message)
}
