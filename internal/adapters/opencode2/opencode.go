package opencode2

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/promptfmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "http://127.0.0.1:4096"
const defaultHTTPTimeout = 600 * time.Second

type Adapter struct {
	spec        domain.AgentSpec
	runtime     *Runtime
	settings    domain.MachineBackendSettings
	workspace   string
	mu          sync.Mutex
	permissions *permissionRun
}
type sessionResponse struct {
	ID       string `json:"id"`
	ParentID string `json:"parentID"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

func New(spec domain.AgentSpec) *Adapter { return &Adapter{spec: spec} }
func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if state.Backend != "" && state.Backend != "opencode2" {
		return state, errors.New("cannot recover another backend as OpenCode 2")
	}
	if err := a.checkReady(ctx); err != nil {
		return state, err
	}
	if a.runtime != nil && state.BackendSessionID != "" {
		if err := a.verifySession(ctx, state.BackendSessionID); err != nil {
			return state, err
		}
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if state.Name == "" {
		state.Name = spec.Name
		state.Backend = spec.Backend
		state.StartupPrompt = spec.StartupPrompt
		if state.CreatedAt.IsZero() {
			state.CreatedAt = time.Now().UTC()
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

func (a *Adapter) startupPrompt(state domain.AgentState) string {
	if state.StartupPrompt != "" {
		return state.StartupPrompt
	}
	return a.spec.StartupPrompt
}

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	if state.Backend != "" && state.Backend != "opencode2" {
		return state, errors.New("cannot recover another backend as OpenCode 2")
	}
	if err := a.checkReady(ctx); err != nil {
		return state, err
	}
	if a.runtime != nil && state.BackendSessionID != "" {
		if err := a.verifySession(ctx, state.BackendSessionID); err != nil {
			return state, err
		}
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Status = domain.AgentIdle
	return state, nil
}

// Reset clears the local session identity without contacting the server:
// creation of the replacement external session is deferred to the next
// checked turn, so resets never start a managed server or allocate sessions.
func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = spec.StringOptions["model"]
	state.StartupPrompt = spec.StartupPrompt
	state.BackendSessionID = ""
	state.Status = domain.AgentIdle
	state.LastRunID = ""
	state.LastError = nil
	return state, nil
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
	if a.workspace != "" {
		req.Header.Set("x-opencode-directory", a.workspace)
	}
	if a.runtime != nil && a.settings.Mode == "managed" {
		a.runtime.mu.Lock()
		password := a.runtime.password
		a.runtime.mu.Unlock()
		req.SetBasicAuth("opencode", password)
	} else if password := a.spec.StringOptions["password"]; password != "" {
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
		return a.safeError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.safeError(httpStatusError(path, resp))
	}
	if out == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return a.safeError(err)
	}
	return a.safeError(json.Unmarshal(data, out))
}

func (a *Adapter) baseURL() string {
	if a.runtime != nil {
		if a.settings.Mode == "external" {
			return strings.TrimRight(a.settings.BaseURL, "/")
		}
		a.runtime.mu.Lock()
		defer a.runtime.mu.Unlock()
		return a.runtime.endpoint
	}
	baseURL := strings.TrimRight(a.spec.StringOptions["base_url"], "/")
	if baseURL == "" {
		return defaultBaseURL
	}
	return baseURL
}

func (a *Adapter) httpClient() *http.Client {
	return &http.Client{Timeout: a.httpTimeout(), Transport: directTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
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

func scanSSEEvents(r io.Reader, handle func(string) bool) error {
	reader := bufio.NewScanner(r)
	reader.Buffer(make([]byte, 4096), 8<<20)
	size := 0

	var frame []string
	flush := func() bool {
		if len(frame) == 0 {
			return false
		}
		event := strings.Join(frame, "\n")
		frame = nil
		return !handle(event)
	}

	for reader.Scan() {
		line := strings.TrimSuffix(reader.Text(), "\r")
		if line == "" {
			if flush() {
				return nil
			}
			size = 0
		} else if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			size += len(data)
			if size > 8<<20 {
				return errors.New("OpenCode 2 event exceeded frame limit")
			}
			frame = append(frame, data)
		}
	}
	if err := reader.Err(); err != nil {
		return errors.New("OpenCode 2 event stream read failed")
	}

	if flush() {
		return nil
	}
	return nil
}

func stringValue(v any) string { s, _ := v.(string); return s }
func httpStatusError(path string, r *http.Response) error {
	return fmt.Errorf("OpenCode 2 HTTP %d for %s", r.StatusCode, path)
}
func (a *Adapter) createSession(ctx context.Context) (string, error) {
	body := map[string]any{"title": a.spec.Name, "location": map[string]string{"directory": a.workspace}}
	if agent := a.spec.StringOptions["agent"]; agent != "" {
		body["agent"] = agent
	}
	if model := a.spec.StringOptions["model"]; model != "" {
		m, err := modelPayload(model)
		if err != nil {
			return "", err
		}
		body["model"] = m
	}
	var result struct {
		Data sessionResponse `json:"data"`
	}
	if err := a.postJSON(ctx, "/api/session", body, &result); err != nil {
		return "", err
	}
	if result.Data.ID == "" || !sameDirectory(result.Data.Location.Directory, a.workspace) {
		return "", errors.New("OpenCode 2 created an invalid session or workspace")
	}
	return result.Data.ID, nil
}
func modelPayload(model string) (map[string]string, error) {
	provider, id, ok := strings.Cut(model, "/")
	if !ok || provider == "" || id == "" {
		return nil, errors.New("OpenCode 2 model must be provider/model")
	}
	return map[string]string{"providerID": provider, "id": id}, nil
}
func sessionPath(id string) string { return "/api/session/" + url.PathEscape(id) }
func (a *Adapter) interruptSession(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.postJSON(ctx, sessionPath(id)+"/interrupt?resume=false", nil, nil)
}
func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (result domain.RunResult, next domain.AgentState, sendErr error) {
	next = state
	defer func() {
		result.FinalMessage = a.redact(result.FinalMessage)
		if sendErr != nil {
			sendErr = a.safeError(sendErr)
			result.ErrorMessage = sendErr.Error()
		}
	}()
	if err := a.checkReady(ctx); err != nil {
		return result, state, err
	}
	if state.BackendSessionID == "" {
		return result, state, errors.New("OpenCode 2 session ID is empty")
	}
	if err := a.verifySession(ctx, state.BackendSessionID); err != nil {
		return result, state, err
	}
	ctx, cancel := context.WithTimeout(ctx, a.httpTimeout())
	defer cancel()
	// Never enqueue into an execution left active by a previously interrupted client.
	var active struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := a.doJSON(ctx, "GET", "/api/session/active", nil, &active); err != nil {
		return result, state, err
	}
	if _, busy := active.Data[state.BackendSessionID]; busy {
		return result, state, errors.New("OpenCode 2 session is still running; interrupt it before continuing")
	}
	if model := a.spec.StringOptions["model"]; model != "" {
		m, err := modelPayload(model)
		if err != nil {
			return result, state, err
		}
		if err = a.postJSON(ctx, sessionPath(state.BackendSessionID)+"/model", map[string]any{"model": m}, nil); err != nil {
			return result, state, err
		}
	}
	if agent := a.spec.StringOptions["agent"]; agent != "" {
		if err := a.postJSON(ctx, sessionPath(state.BackendSessionID)+"/agent", map[string]any{"agent": agent}, nil); err != nil {
			return result, state, err
		}
	}
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	sink = redactingSink{RunSink: sink, adapter: a}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return result, state, err
	}
	messageID := "msg_" + hex.EncodeToString(token)
	tracker := newProgressTracker(state.BackendSessionID, messageID, sink)
	permissions := newPermissionRun(ctx, a, run.RunID, tracker, sink)
	a.mu.Lock()
	a.permissions = permissions
	a.mu.Unlock()
	defer func() { permissions.close(); a.mu.Lock(); a.permissions = nil; a.mu.Unlock() }()
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	ready := make(chan error, 1)
	done := make(chan turnResult, 1)
	go func() {
		done <- a.streamTurn(streamCtx, state.BackendSessionID, messageID, sink, tracker, permissions, ready)
	}()
	select {
	case err := <-ready:
		if err != nil {
			stopStream()
			<-done
			return result, state, err
		}
	case <-ctx.Done():
		stopStream()
		<-done
		return result, state, ctx.Err()
	}
	message := run.Message
	if state.LastRunID == "" {
		message = promptfmt.WithStartupPrompt(a.startupPrompt(state), message)
	}
	var admitted struct {
		Data struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		} `json:"data"`
	}
	err := a.postJSON(ctx, sessionPath(state.BackendSessionID)+"/prompt", map[string]any{"id": messageID, "text": message}, &admitted)
	if err != nil || admitted.Data.ID != messageID || admitted.Data.SessionID != state.BackendSessionID {
		stopStream()
		<-done
		permissions.cancel()
		a.interruptOwned(tracker, sink)
		if err == nil {
			err = errors.New("OpenCode 2 returned an uncorrelated prompt receipt; work was not replayed")
		}
		return result, state, err
	}
	stream := <-done
	if stream.Err != nil {
		permissions.cancel()
		a.interruptOwned(tracker, sink)
		return result, state, stream.Err
	}
	if stream.AssistantID == "" {
		return result, state, errors.New("OpenCode 2 completed without current-turn assistant output")
	}
	var final struct {
		Data struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"data"`
	}
	if err := a.doJSON(ctx, "GET", sessionPath(state.BackendSessionID)+"/message/"+url.PathEscape(stream.AssistantID), nil, &final); err != nil {
		return result, state, err
	}
	if final.Data.ID != stream.AssistantID || final.Data.Type != "assistant" {
		return result, state, errors.New("OpenCode 2 final message identity mismatch")
	}
	var texts []string
	for _, c := range final.Data.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	result.FinalMessage = strings.Join(texts, "\n")
	next.Status = domain.AgentIdle
	return result, next, nil
}

type turnResult struct {
	AssistantID string
	Err         error
}

func (a *Adapter) streamTurn(ctx context.Context, sessionID, messageID string, sink domain.RunSink, tracker *progressTracker, p *permissionRun, ready chan<- error) (result turnResult) {
	announced := false
	announce := func(err error) {
		if !announced {
			announced = true
			ready <- err
		}
	}
	defer func() {
		if !announced {
			announce(errors.New("OpenCode 2 stream ended before server.connected"))
		}
	}()
	req, err := a.newRequest(ctx, "GET", "/api/event", nil)
	if err != nil {
		result.Err = err
		return
	}
	resp, err := a.httpClient().Do(req)
	if err != nil {
		result.Err = err
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		result.Err = httpStatusError("/api/event", resp)
		return
	}
	delivered := false
	terminal := false
	err = scanSSEEvents(resp.Body, func(raw string) bool {
		var e map[string]any
		if json.Unmarshal([]byte(raw), &e) != nil {
			result.Err = errors.New("invalid OpenCode 2 event")
			return false
		}
		kind := stringValue(e["type"])
		if kind == "server.connected" {
			announce(nil)
			return true
		}
		data, _ := e["data"].(map[string]any)
		sid := stringValue(data["sessionID"])
		if sid == sessionID && kind == "session.inbox.delivered" && stringValue(data["inboxID"]) == messageID {
			delivered = true
		}
		if !delivered {
			return true
		}
		owned := tracker.handleEvent(e, sid == sessionID, time.Now().UTC())
		if !owned {
			return true
		}
		sink.StdoutLine(raw)
		if err := sink.Err(); err != nil {
			result.Err = err
			return false
		}
		if kind == "session.inbox.delivered" && sid == sessionID || kind == "session.created" && sid != sessionID {
			if err := a.syncPermissions(ctx, sid, p); err != nil {
				result.Err = err
				return false
			}
		}
		p.handleEvent(normalizePermission(e))
		if sid != sessionID {
			return true
		}
		if kind == "session.step.started" {
			result.AssistantID = stringValue(data["assistantMessageID"])
		}
		switch kind {
		case "session.execution.succeeded":
			terminal = true
			return false
		case "session.execution.failed", "session.execution.interrupted":
			result.Err = fmt.Errorf("OpenCode 2 %s", kind)
			return false
		}
		return true
	})
	if result.Err == nil {
		if ctx.Err() != nil {
			result.Err = ctx.Err()
		} else if err != nil {
			result.Err = err
		} else if !terminal {
			result.Err = errors.New("OpenCode 2 event stream ended before confirmed completion; work was not replayed")
		}
	}
	return
}

func (a *Adapter) syncPermissions(ctx context.Context, sid string, p *permissionRun) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result struct {
		Data *[]map[string]any `json:"data"`
	}
	if err := a.doJSON(ctx, "GET", sessionPath(sid)+"/permission", nil, &result); err != nil {
		return errors.New("cannot reconcile OpenCode 2 permissions")
	}
	if result.Data == nil {
		return errors.New("invalid OpenCode 2 permission list")
	}
	for _, d := range *result.Data {
		if stringValue(d["sessionID"]) == sid {
			p.handleEvent(normalizePermission(map[string]any{"type": "permission.asked", "data": d}))
		}
	}
	return nil
}
func (a *Adapter) interruptOwned(t *progressTracker, sink domain.RunSink) {
	t.mu.Lock()
	ids := []string{t.rootSessionID}
	for id := range t.subagents {
		ids = append(ids, id)
	}
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := a.postJSON(ctx, sessionPath(id)+"/interrupt?resume=false", nil, nil); err != nil {
				domain.ReportRunDiagnostic(sink, "OpenCode 2 interruption failed; remote side effects may still be running")
			}
		}(id)
	}
	wg.Wait()
}
