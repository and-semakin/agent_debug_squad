package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

const permissionReplyTimeout = 5 * time.Second

// A registry lives for one Send, never for the lifetime of the shared server.
type permissionRun struct {
	ctx       context.Context
	cancel    context.CancelFunc
	adapter   *Adapter
	runID     string
	tracker   *progressTracker
	sink      domain.RunSink
	mu        sync.Mutex
	pending   map[string]domain.PermissionRequest
	seen      map[string]bool
	replied   map[string]string
	replyGate chan struct{}
	workers   sync.WaitGroup
}

func newPermissionRun(ctx context.Context, a *Adapter, runID string, tracker *progressTracker, sink domain.RunSink) *permissionRun {
	ctx, cancel := context.WithCancel(ctx)
	return &permissionRun{ctx: ctx, cancel: cancel, adapter: a, runID: runID, tracker: tracker, sink: sink,
		pending: map[string]domain.PermissionRequest{}, seen: map[string]bool{}, replied: map[string]string{}, replyGate: make(chan struct{}, 1)}
}

func (a *Adapter) ReplyPermission(ctx context.Context, runID, requestID string, reply domain.PermissionReply) error {
	if err := reply.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	p := a.permissions
	a.mu.Unlock()
	if p == nil || p.runID != runID {
		return domain.ErrPermissionInactive
	}
	return p.reply(ctx, requestID, reply, false)
}

func (p *permissionRun) handleEvent(event map[string]any) bool {
	kind := stringValue(event["type"])
	if kind != "permission.asked" && kind != "permission.replied" {
		return false
	}
	props, ok := event["properties"].(map[string]any)
	if !ok {
		return false
	}
	sessionID := stringValue(props["sessionID"])
	tool, _ := props["tool"].(map[string]any)
	if !p.tracker.ownsPermission(sessionID, stringValue(tool["messageID"])) {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		return false
	}
	if kind == "permission.replied" {
		id := stringValue(props["requestID"])
		if id != "" {
			p.seen[id] = true
		}
		if request, ok := p.pending[id]; ok && request.SessionID == sessionID {
			p.replied[id] = stringValue(props["reply"])
			delete(p.pending, id)
			p.publishLocked()
		}
		return true
	}
	id := stringValue(props["id"])
	if id == "" || stringValue(props["permission"]) == "" {
		return false
	}
	if p.seen[id] {
		return true
	}
	p.seen[id] = true
	request := domain.PermissionRequest{ID: id, SessionID: sessionID,
		Permission: stringValue(props["permission"]), Patterns: stringSlice(props["patterns"]),
		Always: stringSlice(props["always"]), AskedAt: time.Now().UTC()}
	if value, ok := props["metadata"]; ok {
		request.Metadata, _ = json.Marshal(value)
	}
	if value, ok := props["tool"]; ok {
		request.Tool, _ = json.Marshal(value)
	}
	request.AutoApproving = p.adapter.spec.Yolo == nil || *p.adapter.spec.Yolo
	p.pending[id] = request
	p.publishLocked()
	if request.AutoApproving {
		p.workers.Add(1)
		go func() {
			defer p.workers.Done()
			_ = p.reply(p.ctx, id, domain.PermissionReply{Reply: "once"}, true)
		}()
	}
	return true
}

func stringSlice(value any) []string {
	result := []string{}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if v, ok := item.(string); ok {
				result = append(result, v)
			}
		}
	}
	return result
}

// All calls, including manual replies, register before close starts waiting.
func (p *permissionRun) reply(caller context.Context, id string, reply domain.PermissionReply, automatic bool) error {
	p.mu.Lock()
	if p.ctx.Err() != nil {
		p.mu.Unlock()
		return domain.ErrPermissionInactive
	}
	p.workers.Add(1)
	p.mu.Unlock()
	defer p.workers.Done()
	ctx, cancel := context.WithCancel(caller)
	defer cancel()
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	select {
	case p.replyGate <- struct{}{}:
		defer func() { <-p.replyGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	p.mu.Lock()
	request, ok := p.pending[id]
	p.mu.Unlock()
	if !ok {
		return domain.ErrPermissionNotFound
	}
	if err := p.ctx.Err(); err != nil {
		return domain.ErrPermissionInactive
	}
	replyCtx, cancelReply := context.WithTimeout(ctx, permissionReplyTimeout)
	defer cancelReply()
	err := p.adapter.doJSON(replyCtx, http.MethodPost, "/permission/"+url.PathEscape(id)+"/reply", reply, nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.replied[id] == reply.Reply {
		err = nil
	}
	source := "coordinator"
	if automatic {
		source = "yolo"
	}
	audit := map[string]any{"type": "squad.permission.reply", "run_id": p.runID, "request_id": id, "session_id": request.SessionID, "reply": reply.Reply, "source": source, "at": time.Now().UTC()}
	if err != nil {
		audit["error"] = err.Error()
	}
	raw, _ := json.Marshal(audit)
	p.sink.StdoutLine(string(raw))
	if current, exists := p.pending[id]; exists {
		if err == nil {
			delete(p.pending, id)
		} else if automatic {
			current.AutoApproving = false
			current.AutoApproveError = err.Error()
			p.pending[id] = current
		}
		p.publishLocked()
	}
	return err
}

func (p *permissionRun) publishLocked() {
	pending := make([]domain.PermissionRequest, 0, len(p.pending))
	for _, request := range p.pending {
		pending = append(pending, request)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].ID < pending[j].ID })
	p.tracker.setPermissions(pending)
}

func (p *permissionRun) close() {
	p.mu.Lock()
	p.cancel()
	p.mu.Unlock()
	p.workers.Wait()
	p.mu.Lock()
	p.pending = map[string]domain.PermissionRequest{}
	p.publishLocked()
	p.mu.Unlock()
}
