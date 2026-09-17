package zcode

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/cursor"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/promptfmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

//go:embed host.cjs
var hostSource string

const defaultRuntime = "/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs"
const defaultProvider = "account:zai-individual-coding-plan"

type Adapter struct {
	spec   domain.AgentSpec
	mu     sync.Mutex
	active *activeRun
	// Overridden only by protocol tests; production always uses the guarded host.
	command func() *exec.Cmd
}
type approval struct {
	request domain.PermissionRequest
	wireID  json.RawMessage
	options []permissionOption
}
type permissionOption struct {
	Kind     string          `json:"kind"`
	Response json.RawMessage `json:"response"`
}
type replyJob struct {
	ctx    context.Context
	id     string
	reply  domain.PermissionReply
	result chan error
}
type activeRun struct {
	id           string
	session      string
	turn         string
	input        string
	c            *client
	sink         domain.RunSink
	done         chan struct{}
	replies      chan replyJob
	pending      map[string]approval
	seen         map[string]bool
	children     map[string]domain.SubagentProgress
	baseline     map[string]bool
	lastActivity time.Time
}
type snapshot struct {
	Session struct {
		SessionID string `json:"sessionId"`
	} `json:"session"`
	Settings struct {
		Model json.RawMessage `json:"model"`
	} `json:"settings"`
}
type event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
	Payload   struct {
		InputID    string          `json:"inputId"`
		RequestID  string          `json:"requestId"`
		Response   string          `json:"response"`
		ResultType string          `json:"resultType"`
		Error      json.RawMessage `json:"error"`
	} `json:"payload"`
}

func New(spec domain.AgentSpec) *Adapter { return &Adapter{spec: spec} }
func (a *Adapter) option(name, fallback string) string {
	if value := a.spec.StringOptions[name]; value != "" {
		return value
	}
	return fallback
}
func (a *Adapter) selection() map[string]any {
	return map[string]any{"providerId": a.option("provider", defaultProvider), "modelId": a.option("model", "GLM-5.3-Flash"), "options": map[string]string{"reasoningLevel": a.option("reasoning", "low")}}
}
func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if a.option("provider", defaultProvider) != defaultProvider {
		return state, errors.New("zcode host currently supports only account:zai-individual-coding-plan")
	}
	switch a.option("reasoning", "low") {
	case "low", "high", "max":
	default:
		return state, errors.New("zcode reasoning must be low, high, or max")
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = a.option("model", "GLM-5.3-Flash")
	state.StartupPrompt = spec.StartupPrompt
	state.Status = domain.AgentIdle
	return state, nil
}
func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	state.Status = domain.AgentIdle
	return state, ctx.Err()
}
func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.BackendSessionID = ""
	state.LastRunID = ""
	state.LastError = nil
	return a.Init(ctx, spec, state)
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (result domain.RunResult, next domain.AgentState, retErr error) {
	next = state
	var stderrMu sync.Mutex
	var stderr string
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	r := &activeRun{id: run.RunID, sink: sink, done: make(chan struct{}), replies: make(chan replyJob), pending: map[string]approval{}, seen: map[string]bool{}, children: map[string]domain.SubagentProgress{}, baseline: map[string]bool{}, lastActivity: time.Now().UTC()}
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return result, next, errors.New("zcode agent already has an active run")
	}
	a.active = r
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.active = nil
		close(r.done)
		a.mu.Unlock()
		r.pending = map[string]approval{}
		for id, child := range r.children {
			if child.Status == "running" || child.Status == "waiting" || child.Status == "blocked" {
				child.Status = "cancelled"
				child.LastActivityAt = time.Now().UTC()
				r.children[id] = child
			}
		}
		r.publish()
		next.Status = domain.AgentIdle
		result.BackendSessionID = next.BackendSessionID
		if ctx.Err() != nil {
			retErr = ctx.Err()
		}
		stderrMu.Lock()
		stderrTail := stderr
		stderrMu.Unlock()
		if retErr != nil {
			if stderrTail != "" && ctx.Err() == nil {
				retErr = fmt.Errorf("%w; host stderr: %s", retErr, stderrTail)
			}
			result.ErrorMessage = retErr.Error()
		}
	}()
	if err := ctx.Err(); err != nil {
		return result, next, err
	}
	cmd := exec.Command(a.option("command", "node"), "-e", hostSource, a.option("runtime_path", defaultRuntime))
	if a.command != nil {
		cmd = a.command()
	}
	cmd.Dir = state.WorkspaceDir
	cmd.Env = cursor.BuildEnv(a.spec, os.Environ())
	c, err := startClient(cmd, func(line string) {
		sink.StderrLine(line)
		stderrMu.Lock()
		stderr += line + "\n"
		if len(stderr) > 4096 {
			stderr = stderr[len(stderr)-4096:]
		}
		stderrMu.Unlock()
	})
	if err != nil {
		return result, next, fmt.Errorf("start zcode host: %w", err)
	}
	r.c = c
	defer c.close()
	// A watchdog also bounds writes/shutdown when the child stops reading stdin.
	watchdog := context.AfterFunc(ctx, func() {
		select {
		case <-c.exited:
		case <-time.After(3 * time.Second):
			killProcess(cmd)
		}
	})
	defer watchdog()
	defer func() {
		if r.session != "" {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = c.call(cleanup, "session/stop", map[string]any{"sessionId": r.session}, nil)
			_ = c.call(cleanup, "session/close", map[string]any{"sessionId": r.session}, nil)
		}
	}()
	if err = c.call(ctx, "squad/bootstrap", map[string]any{}, nil); err != nil {
		return result, next, err
	}
	mode := "yolo"
	if a.spec.Yolo != nil && !*a.spec.Yolo {
		mode = "build"
	}
	var snap snapshot
	fresh := state.BackendSessionID == ""
	if fresh {
		err = c.call(ctx, "session/create", map[string]any{"workspace": map[string]string{"workspaceKey": state.WorkspaceDir, "workspacePath": state.WorkspaceDir}, "mode": mode, "model": a.selection(), "titleGenerationEnabled": false}, &snap)
	} else {
		err = c.call(ctx, "session/resume", map[string]any{"sessionId": state.BackendSessionID}, &snap)
	}
	if err != nil {
		return result, next, err
	}
	r.session = snap.Session.SessionID
	if r.session == "" {
		return result, next, errors.New("zcode returned an empty session ID")
	}
	if !fresh && r.session != state.BackendSessionID {
		return result, next, errors.New("zcode resumed a different session")
	}
	next.BackendSessionID = r.session
	if len(snap.Settings.Model) > 0 {
		record, _ := json.Marshal(map[string]any{"type": "zcode.models", "model": snap.Settings.Model})
		domain.ReportRunDiagnostic(sink, string(record))
	}
	if err = c.call(ctx, "session/setMode", map[string]any{"sessionId": r.session, "mode": mode}, nil); err != nil {
		return result, next, err
	}
	if !fresh {
		if err = r.pollChildren(ctx, true); err != nil {
			return result, next, err
		}
	}
	if err = c.call(ctx, "session/subscribe", map[string]any{"sessionId": r.session, "deliveryKind": "desktop-continuous"}, nil); err != nil {
		return result, next, err
	}
	message := run.Message
	if fresh {
		startup := state.StartupPrompt
		if startup == "" {
			startup = a.spec.StartupPrompt
		}
		message = promptfmt.WithStartupPrompt(startup, message)
	}
	r.input = run.RunID
	if r.input == "" {
		r.input = fmt.Sprintf("squad-%d", time.Now().UnixNano())
	}
	var accepted struct {
		Accepted bool `json:"accepted"`
	}
	if err = c.call(ctx, "session/send", map[string]any{"sessionId": r.session, "content": message, "inputId": r.input, "modelSelection": a.selection()}, &accepted); err != nil {
		return result, next, err
	}
	if !accepted.Accepted {
		return result, next, errors.New("zcode did not accept the input")
	}
	r.publish()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return result, next, ctx.Err()
		case <-c.done:
			return result, next, c.failure()
		case job := <-r.replies:
			job.result <- r.reply(job)
		case <-ticker.C:
			if r.turn == "" {
				continue
			}
			if err = r.pollChildren(ctx, false); err != nil {
				return result, next, err
			}
		case msg := <-c.inbox:
			if len(msg.ID) > 0 {
				if err = r.callback(ctx, msg); err != nil {
					return result, next, err
				}
				continue
			}
			if msg.Method != "session/event" {
				continue
			}
			var e event
			if err = json.Unmarshal(msg.Params, &e); err != nil {
				return result, next, errors.New("invalid zcode event")
			}
			if e.SessionID != r.session {
				continue
			}
			sink.StdoutLine(string(msg.Params))
			if err = sink.Err(); err != nil {
				return result, next, err
			}
			r.lastActivity = time.Now().UTC()
			if e.Type == "permission.resolved" {
				if _, ok := r.pending[e.Payload.RequestID]; ok {
					delete(r.pending, e.Payload.RequestID)
					r.publish()
				}
			}
			if e.Type == "turn.failed" && e.Payload.InputID == r.input {
				return result, next, fmt.Errorf("zcode turn failed: %s", e.Payload.Error)
			}
			if e.Type == "turn.started" && e.Payload.InputID == r.input {
				r.turn = e.TurnID
				r.publish()
			}
			if r.turn != "" && e.TurnID == r.turn {
				switch e.Type {
				case "turn.failed":
					return result, next, fmt.Errorf("zcode turn failed: %s", e.Payload.Error)
				case "turn.completed":
					if e.Payload.ResultType != "success" {
						return result, next, fmt.Errorf("zcode turn ended with result %q", e.Payload.ResultType)
					}
					if strings.TrimSpace(e.Payload.Response) == "" {
						return result, next, errors.New("zcode completed without new assistant response text")
					}
					if err = r.pollChildren(ctx, false); err != nil {
						return result, next, err
					}
					result.FinalMessage = e.Payload.Response
					return result, next, nil
				}
			}
		}
	}
}

func (r *activeRun) publish() {
	progress := domain.RunProgress{Phase: domain.RunPhaseRunning, LastActivityAt: r.lastActivity}
	for _, child := range r.children {
		progress.Subagents = append(progress.Subagents, child)
		if child.Status == "running" || child.Status == "waiting" || child.Status == "blocked" {
			progress.Phase = domain.RunPhaseWaitingForSubagent
		}
		if progress.ChildLastActivityAt == nil || child.LastActivityAt.After(*progress.ChildLastActivityAt) {
			t := child.LastActivityAt
			progress.ChildLastActivityAt = &t
		}
	}
	for _, p := range r.pending {
		progress.PendingPermissions = append(progress.PendingPermissions, p.request)
	}
	if len(progress.PendingPermissions) > 0 {
		progress.Phase = domain.RunPhaseWaitingForPermission
	}
	sort.Slice(progress.Subagents, func(i, j int) bool { return progress.Subagents[i].ID < progress.Subagents[j].ID })
	sort.Slice(progress.PendingPermissions, func(i, j int) bool { return progress.PendingPermissions[i].ID < progress.PendingPermissions[j].ID })
	domain.ReportRunProgress(r.sink, progress)
}

type childSnapshot struct {
	ChildSessionID string `json:"childSessionId"`
	Status         string `json:"status"`
}

func (r *activeRun) pollChildren(ctx context.Context, baseline bool) error {
	// The root index lists direct children. Query newly owned children as well so
	// nested agents cannot become invisible or receive unowned permission replies.
	queue := []string{r.session}
	visited := map[string]bool{}
	changed := false
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		if visited[parent] {
			continue
		}
		visited[parent] = true
		var snapshot struct {
			ChildSessionIDs []string        `json:"childSessionIds"`
			Running         []childSnapshot `json:"running"`
			Ended           struct {
				Items      []childSnapshot `json:"items"`
				NextCursor string          `json:"nextCursor"`
			} `json:"ended"`
		}
		cursor := ""
		for {
			params := map[string]any{"sessionId": parent, "endedLimit": 100}
			if cursor != "" {
				params["endedCursor"] = cursor
			}
			snapshot.Ended.NextCursor = ""
			if err := r.c.call(ctx, "session/subagents", params, &snapshot); err != nil {
				return err
			}
			if baseline {
				for _, id := range snapshot.ChildSessionIDs {
					r.baseline[id] = true
				}
			}
			for _, child := range append(snapshot.Running, snapshot.Ended.Items...) {
				if child.ChildSessionID == "" {
					continue
				}
				if baseline {
					r.baseline[child.ChildSessionID] = true
					continue
				}
				if r.baseline[child.ChildSessionID] {
					continue
				}
				previous, ok := r.children[child.ChildSessionID]
				if !ok || previous.Status != child.Status {
					changed = true
					r.children[child.ChildSessionID] = domain.SubagentProgress{ID: child.ChildSessionID, ParentID: parent, Status: child.Status, LastActivityAt: time.Now().UTC()}
				}
				if !visited[child.ChildSessionID] {
					queue = append(queue, child.ChildSessionID)
				}
			}
			if baseline || snapshot.Ended.NextCursor == "" {
				break
			}
			if snapshot.Ended.NextCursor == cursor {
				return errors.New("zcode subagent pagination did not advance")
			}
			cursor = snapshot.Ended.NextCursor
		}
	}
	if changed {
		r.publish()
	}
	return nil
}
