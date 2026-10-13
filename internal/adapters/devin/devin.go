// Package devin integrates the official local Cognition CLI using ACP v1.
package devin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/promptfmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
)

type Adapter struct {
	spec     domain.AgentSpec
	mu       sync.Mutex
	active   *turn
	prepared *preparation
}

func New(spec domain.AgentSpec) *Adapter { return &Adapter{spec: spec} }

type configOption struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Current  string `json:"currentValue"`
	Options  []struct {
		Value string `json:"value"`
	} `json:"options"`
}
type session struct {
	ID      string         `json:"sessionId"`
	Options []configOption `json:"configOptions"`
}
type approval struct {
	wire    json.RawMessage
	request domain.PermissionRequest
	options []permissionOption
}
type permissionOption struct {
	ID   string `json:"optionId"`
	Kind string `json:"kind"`
}
type turn struct {
	mu               sync.Mutex
	ctx              context.Context
	c                *client
	runID, sessionID string
	sink             domain.RunSink
	pending          map[string]approval
	seen             map[string]bool
	tools            map[string]string
	text             strings.Builder
	hadUser          bool
	live             bool
	inactive         bool
	clean            func(string) string
}

func canonical(path string) (string, error) {
	p, e := filepath.Abs(path)
	if e != nil {
		return "", errors.New("devin: invalid workspace")
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return "", errors.New("devin: workspace unavailable")
	}
	return p, nil
}
func (a *Adapter) open(ctx context.Context, workspace string) (*client, func(), error) {
	if e := ValidateSpec(a.spec); e != nil {
		return nil, func() {}, e
	}
	env := BuildEnv(a.spec, os.Environ())
	cmd := a.spec.StringOptions["command"]
	if cmd == "" {
		cmd = "devin"
	}
	resolved, issue := preflight.ResolveCommand(cmd, env, workspace)
	if issue != nil {
		return nil, func() {}, errors.New("devin: executable unavailable; see https://docs.devin.ai/cli")
	}
	path, cleanup, e := profile(env, workspace)
	if e != nil {
		return nil, func() {}, e
	}
	command := exec.Command(resolved, "--config", path, "acp")
	command.Env = env
	command.Dir = workspace
	c, e := startClient(command)
	if e != nil {
		cleanup()
		return nil, func() {}, e
	}
	r, e := boundedCall(ctx, c, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}, "clientInfo": map[string]string{"name": "agent-debug-squad", "version": "dev"}}, nil)
	var init struct {
		Version int `json:"protocolVersion"`
		Caps    struct {
			Load     bool                       `json:"loadSession"`
			Sessions map[string]json.RawMessage `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	if e == nil && (json.Unmarshal(r, &init) != nil || init.Version != 1 || !init.Caps.Load || init.Caps.Sessions["list"] == nil) {
		e = errors.New("devin: incompatible ACP capabilities; official CLI with load/list support required")
	}
	if e != nil {
		c.close()
		cleanup()
		return nil, func() {}, e
	}
	return c, cleanup, nil
}
func boundedCall(ctx context.Context, c *client, method string, p any, h func(message) error) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	return c.call(ctx, method, p, h)
}
func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (out domain.AgentState, err error) {
	if ctx.Err() != nil {
		return state, ctx.Err()
	}
	state.Name = spec.Name
	state.Backend = "devin"
	state.StartupPrompt = spec.StartupPrompt
	state.Model = spec.StringOptions["model"]
	state.Status = domain.AgentIdle
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	if state.BackendSessionID != "" {
		return state, nil
	}
	workspace, e := canonical(state.WorkspaceDir)
	if e != nil {
		return state, e
	}
	state.WorkspaceDir = workspace
	c, cleanup, e := a.open(ctx, workspace)
	if e != nil {
		return state, e
	}
	b, e := boundedCall(ctx, c, "session/new", map[string]any{"cwd": workspace, "mcpServers": []any{}}, nil)
	var s session
	if e == nil && (json.Unmarshal(b, &s) != nil || s.ID == "" || len(s.ID) > 4096) {
		e = errors.New("devin: invalid new session identity")
	}
	if e != nil {
		c.close()
		cleanup()
		return state, e
	}
	p := &preparation{c: c, cleanup: cleanup, session: s, workspace: workspace}
	a.mu.Lock()
	if a.prepared != nil || a.active != nil {
		a.mu.Unlock()
		p.close()
		return state, errors.New("devin: operation already active")
	}
	a.prepared = p
	p.timer = time.AfterFunc(30*time.Second, func() { a.expirePreparation(p) })
	p.stop = context.AfterFunc(ctx, func() { a.expirePreparation(p) })
	a.mu.Unlock()
	state.BackendSessionID = s.ID
	return state, nil
}
func (a *Adapter) Recover(ctx context.Context, s domain.AgentState) (domain.AgentState, error) {
	s.Status = domain.AgentIdle
	return s, ctx.Err()
}
func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, s domain.AgentState) (domain.AgentState, error) {
	if ctx.Err() != nil {
		return s, ctx.Err()
	}
	s.Name = spec.Name
	s.Backend = "devin"
	s.Model = spec.StringOptions["model"]
	s.StartupPrompt = spec.StartupPrompt
	s.BackendSessionID = ""
	s.LastRunID = ""
	s.LastError = nil
	s.Status = domain.AgentIdle
	return s, nil
}
func validateSession(ctx context.Context, c *client, id, workspace string) error {
	cursor := ""
	seen := map[string]bool{}
	total := 0
	for page := 0; page < 100; page++ {
		params := map[string]any{"cwd": workspace}
		if cursor != "" {
			params["cursor"] = cursor
		}
		b, e := boundedCall(ctx, c, "session/list", params, nil)
		if e != nil {
			return e
		}
		total += len(b)
		if total > 32<<20 {
			return errors.New("devin: session listing limit")
		}
		var list struct {
			Sessions []struct {
				ID  string `json:"sessionId"`
				Cwd string `json:"cwd"`
			} `json:"sessions"`
			Next string `json:"nextCursor"`
		}
		if json.Unmarshal(b, &list) != nil || list.Sessions == nil {
			return errors.New("devin: incompatible session list")
		}
		for _, s := range list.Sessions {
			if s.ID == id {
				path, e := canonical(s.Cwd)
				if e != nil || path != workspace {
					return errors.New("devin: saved session workspace mismatch")
				}
				return nil
			}
		}
		if list.Next == "" {
			return errors.New("devin: saved session missing in workspace; explicit reset required")
		}
		if seen[list.Next] {
			return errors.New("devin: repeated session cursor")
		}
		seen[list.Next] = true
		cursor = list.Next
	}
	return errors.New("devin: session listing limit")
}
func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (result domain.RunResult, out domain.AgentState, err error) {
	out = state
	out.Status = domain.AgentIdle
	defer a.ClosePreparation()
	if strings.HasPrefix(strings.TrimSpace(run.Message), "/") {
		return result, out, errors.New("devin: executable slash commands are unsupported")
	}
	if state.BackendSessionID == "" {
		return result, out, errors.New("devin: missing persisted session identity")
	}
	workspace, e := canonical(state.WorkspaceDir)
	if e != nil {
		return result, out, e
	}
	a.mu.Lock()
	p := a.prepared
	if p != nil {
		a.prepared = nil
		p.timer.Stop()
		p.stop()
	}
	a.mu.Unlock()
	var c *client
	var cleanup func()
	if p != nil {
		if p.session.ID != state.BackendSessionID || p.workspace != workspace {
			p.close()
			return result, out, errors.New("devin: prepared session ownership mismatch")
		}
		c, cleanup = p.c, p.cleanup
	} else {
		c, cleanup, e = a.open(ctx, workspace)
		if e != nil {
			return result, out, e
		}
	}
	defer cleanup()
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	t := &turn{ctx: ctx, c: c, runID: run.RunID, sessionID: state.BackendSessionID, sink: sink, pending: map[string]approval{}, seen: map[string]bool{}, tools: map[string]string{}, clean: modelprobe.Cleaner(modelprobe.EnvSecrets(BuildEnv(a.spec, os.Environ())))}
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		c.close()
		return result, out, errors.New("devin: concurrent turn rejected")
	}
	a.active = t
	a.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.inactive = true
		t.pending = map[string]approval{}
		t.mu.Unlock()
		if ctx.Err() != nil {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = c.notify(cancelCtx, "session/cancel", map[string]string{"sessionId": t.sessionID})
			cancel()
		}
		if closeErr := c.close(); err == nil {
			err = closeErr
		}
		a.mu.Lock()
		a.active = nil
		a.mu.Unlock()
		result.FinalMessage = t.clean(t.text.String())
		if result.FinalMessage != "" {
			t.event(map[string]string{"type": "agent_message", "text": result.FinalMessage})
		}
		result.BackendSessionID = t.sessionID
		if err == nil && sink.Err() != nil {
			err = errors.New("devin: output persistence failed")
		}
		if err != nil {
			result.ErrorMessage = err.Error()
		}
	}()
	var s session
	if p != nil {
		s = p.session
	} else {
		if e = validateSession(ctx, c, t.sessionID, workspace); e != nil {
			return result, out, e
		}
		b, loadErr := boundedCall(ctx, c, "session/load", map[string]any{"sessionId": t.sessionID, "cwd": workspace, "mcpServers": []any{}}, t.handle)
		if loadErr != nil {
			return result, out, loadErr
		}
		if json.Unmarshal(b, &s) != nil {
			return result, out, errors.New("devin: invalid loaded session")
		}
	}
	if e = applyControls(ctx, c, t.sessionID, s.Options, a.spec, t.handle); e != nil {
		return result, out, e
	}
	prompt := run.Message
	if !t.hadUser {
		prompt = promptfmt.WithStartupPrompt(state.StartupPrompt, run.Message)
	}
	t.live = true
	t.publish()
	b, e := c.call(ctx, "session/prompt", map[string]any{"sessionId": t.sessionID, "prompt": []any{map[string]string{"type": "text", "text": prompt}}}, t.handle)
	if e != nil {
		return result, out, e
	}
	if ctx.Err() != nil {
		return result, out, ctx.Err()
	}
	var end struct {
		Stop string `json:"stopReason"`
	}
	if json.Unmarshal(b, &end) != nil {
		return result, out, errors.New("devin: malformed prompt completion")
	}
	if end.Stop != "end_turn" {
		switch end.Stop {
		case "cancelled", "refusal", "max_tokens", "max_turn_requests":
			return result, out, fmt.Errorf("devin: prompt stopped (%s)", end.Stop)
		default:
			return result, out, errors.New("devin: unsupported stop reason")
		}
	}
	if strings.TrimSpace(t.text.String()) == "" {
		return result, out, errors.New("devin: completed turn has no new assistant text")
	}
	for _, status := range t.tools {
		if status != "completed" && status != "failed" {
			return result, out, errors.New("devin: unsettled tool at turn completion")
		}
	}
	t.mu.Lock()
	pending := len(t.pending)
	t.mu.Unlock()
	if pending > 0 {
		return result, out, errors.New("devin: unsettled permission at turn completion")
	}
	if e = sink.Err(); e != nil {
		return result, out, errors.New("devin: output persistence failed")
	}
	return result, out, nil
}
func (t *turn) handle(m message) error {
	if m.Method == "session/request_permission" {
		return t.permission(m)
	}
	if len(m.ID) > 0 {
		_ = t.c.write(t.ctx, map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "Unsupported client interaction"}})
		return errors.New("devin: unsupported client interaction")
	}
	if m.Method != "session/update" {
		return nil
	}
	var p struct {
		Session string `json:"sessionId"`
		Update  struct {
			Type    string `json:"sessionUpdate"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			ToolID string `json:"toolCallId"`
			Status string `json:"status"`
			Title  string `json:"title"`
			Kind   string `json:"kind"`
		} `json:"update"`
	}
	// Tool content is an array, while message content is an object. Decode the
	// envelope first and read text only for message updates.
	var envelope struct {
		Session string          `json:"sessionId"`
		Update  json.RawMessage `json:"update"`
	}
	if json.Unmarshal(m.Params, &envelope) != nil || envelope.Session != t.sessionID {
		return errors.New("devin: foreign or malformed session update")
	}
	var u map[string]json.RawMessage
	if json.Unmarshal(envelope.Update, &u) != nil {
		return errors.New("devin: invalid session update")
	}
	_ = json.Unmarshal(u["sessionUpdate"], &p.Update.Type)
	if !t.live {
		if p.Update.Type == "user_message_chunk" {
			t.hadUser = true
		}
		return nil
	}
	if p.Update.Type == "agent_message_chunk" {
		if json.Unmarshal(u["content"], &p.Update.Content) != nil {
			return errors.New("devin: invalid assistant content")
		}
		if p.Update.Content.Type == "text" {
			text := p.Update.Content.Text
			if t.text.Len()+len(text) > 32<<20 {
				return errors.New("devin: response size limit")
			}
			t.text.WriteString(text)
			t.event(map[string]string{"type": "agent_message_chunk"})
		}
	}
	if p.Update.Type == "tool_call" || p.Update.Type == "tool_call_update" {
		_ = json.Unmarshal(u["toolCallId"], &p.Update.ToolID)
		_ = json.Unmarshal(u["status"], &p.Update.Status)
		_ = json.Unmarshal(u["title"], &p.Update.Title)
		_ = json.Unmarshal(u["kind"], &p.Update.Kind)
		if p.Update.ToolID == "" {
			return errors.New("devin: tool identity missing")
		}
		if len(t.tools) >= 10000 {
			return errors.New("devin: tool count limit")
		}
		if p.Update.Status != "" {
			t.tools[p.Update.ToolID] = p.Update.Status
		} else if _, ok := t.tools[p.Update.ToolID]; !ok {
			t.tools[p.Update.ToolID] = "pending"
		}
		t.event(map[string]string{"type": p.Update.Type, "tool_id": t.clean(p.Update.ToolID), "status": t.clean(p.Update.Status), "title": t.clean(p.Update.Title), "kind": t.clean(p.Update.Kind)})
	}
	t.publish()
	if t.sink.Err() != nil {
		return errors.New("devin: stream persistence failed")
	}
	return nil
}
func (t *turn) event(v any) { b, _ := json.Marshal(v); t.sink.StdoutLine(string(b)) }
func (t *turn) publish() {
	t.mu.Lock()
	p := domain.RunProgress{Phase: domain.RunPhaseRunning, LastActivityAt: time.Now().UTC()}
	for _, a := range t.pending {
		p.PendingPermissions = append(p.PendingPermissions, a.request)
	}
	t.mu.Unlock()
	sort.Slice(p.PendingPermissions, func(i, j int) bool { return p.PendingPermissions[i].ID < p.PendingPermissions[j].ID })
	if len(p.PendingPermissions) > 0 {
		p.Phase = domain.RunPhaseWaitingForPermission
	}
	domain.ReportRunProgress(t.sink, p)
}
