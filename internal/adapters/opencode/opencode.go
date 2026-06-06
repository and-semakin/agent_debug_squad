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

type messageResponse struct {
	Parts []part `json:"parts"`
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

	body := map[string]any{
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
	var response messageResponse
	if err := a.postJSON(ctx, "/session/"+state.BackendSessionID+"/message", body, &response); err != nil {
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}

	state.Status = domain.AgentIdle
	state.LastRunID = run.RunID
	return domain.RunResult{FinalMessage: response.finalText()}, state, nil
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
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL()+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if password := a.spec.StringOptions["password"]; password != "" {
		username := a.spec.StringOptions["username"]
		if username == "" {
			username = "opencode"
		}
		req.SetBasicAuth(username, password)
	}

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpStatusError(path, resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var events []string
	var frame []string
	flush := func() {
		if len(frame) == 0 {
			return
		}
		events = append(events, strings.Join(frame, "\n"))
		frame = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = data[1:]
			}
			frame = append(frame, data)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return events, nil
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

func fallbackTextFromEvent(event map[string]any, messageID string) string {
	properties, ok := event["properties"].(map[string]any)
	if !ok {
		return ""
	}

	switch stringValue(event["type"]) {
	case "session.next.text.delta":
		return stringValue(properties["delta"])
	case "session.next.text.ended":
		return stringValue(properties["text"])
	case "message.part.updated":
		part, ok := properties["part"].(map[string]any)
		if !ok {
			return ""
		}
		if stringValue(part["messageID"]) != messageID || stringValue(part["type"]) != "text" {
			return ""
		}
		return stringValue(part["text"])
	default:
		return ""
	}
}

func stringValue(value any) string {
	if value, ok := value.(string); ok {
		return value
	}
	return ""
}

func (r messageResponse) finalText() string {
	var out []string
	for _, part := range r.Parts {
		if part.Text != "" {
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
