package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

const defaultBaseURL = "http://127.0.0.1:4096"

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

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest) (domain.RunResult, domain.AgentState, error) {
	if state.BackendSessionID == "" {
		err := errors.New("opencode backend_session_id is empty")
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}

	body := map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": run.Message},
		},
	}
	if model := a.spec.StringOptions["model"]; model != "" {
		body["model"] = model
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

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Status = domain.AgentIdle
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

	resp, err := http.DefaultClient.Do(req)
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
