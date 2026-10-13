package devin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func (t *turn) permission(m message) error {
	var p struct {
		Session string `json:"sessionId"`
		Tool    struct {
			ID    string `json:"toolCallId"`
			Title string `json:"title"`
			Meta  struct {
				Command string `json:"cognition.ai/editableCommand"`
			} `json:"_meta"`
		} `json:"toolCall"`
		Options []permissionOption `json:"options"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.Session != t.sessionID || !t.live || len(m.ID) == 0 || string(m.ID) == "null" || p.Tool.ID == "" {
		return errors.New("devin: permission does not belong to active turn")
	}
	key := string(m.ID)
	t.mu.Lock()
	if t.inactive || t.ctx.Err() != nil {
		t.mu.Unlock()
		return domain.ErrPermissionInactive
	}
	if t.seen[key] {
		t.mu.Unlock()
		return errors.New("devin: duplicate permission request")
	}
	if len(t.seen) >= 10000 {
		t.mu.Unlock()
		return errors.New("devin: permission count limit")
	}
	once, reject := false, false
	ids := map[string]bool{}
	for _, o := range p.Options {
		if o.ID == "" || ids[o.ID] {
			t.mu.Unlock()
			return errors.New("devin: invalid permission options")
		}
		ids[o.ID] = true
		once = once || o.Kind == "allow_once"
		reject = reject || o.Kind == "reject_once"
	}
	if !once || !reject {
		t.mu.Unlock()
		return errors.New("devin: required permission choices unavailable")
	}
	// Use an opaque run-local ID, never native IDs or unfiltered RPC metadata.
	id := fmt.Sprintf("devin-%d", len(t.seen)+1)
	title := p.Tool.Title
	if title == "" {
		title = "native tool"
	}
	request := domain.PermissionRequest{ID: id, SessionID: t.sessionID, Permission: t.clean(title), AskedAt: time.Now().UTC()}
	if p.Tool.Meta.Command != "" {
		request.Patterns = []string{t.clean(p.Tool.Meta.Command)}
	}
	t.seen[key] = true
	t.pending[id] = approval{wire: append(json.RawMessage(nil), m.ID...), request: request, options: p.Options}
	t.mu.Unlock()
	t.publish()
	return nil
}

func (a *Adapter) ReplyPermission(ctx context.Context, runID, requestID string, reply domain.PermissionReply) error {
	if e := reply.Validate(); e != nil {
		return e
	}
	if reply.Reply == "always" {
		return fmt.Errorf("%w: devin only supports once and reject", domain.ErrPermissionReplyInvalid)
	}
	a.mu.Lock()
	t := a.active
	a.mu.Unlock()
	if t == nil || t.runID != runID {
		return domain.ErrPermissionInactive
	}
	t.mu.Lock()
	if t.inactive || t.ctx.Err() != nil {
		t.mu.Unlock()
		return domain.ErrPermissionInactive
	}
	p, ok := t.pending[requestID]
	if !ok {
		t.mu.Unlock()
		return domain.ErrPermissionNotFound
	}
	kind := "allow_once"
	if reply.Reply == "reject" {
		kind = "reject_once"
	}
	option := ""
	for _, o := range p.options {
		if o.Kind == kind {
			if option != "" {
				t.mu.Unlock()
				return errors.New("devin: ambiguous permission choice")
			}
			option = o.ID
		}
	}
	if option == "" {
		t.mu.Unlock()
		return domain.ErrPermissionReplyInvalid
	}
	// Hold the lock through the serialized write to prevent duplicate decisions.
	e := t.c.write(ctx, map[string]any{"jsonrpc": "2.0", "id": p.wire, "result": map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option}}})
	if e == nil {
		delete(t.pending, requestID)
	}
	t.mu.Unlock()
	if e == nil {
		t.event(map[string]string{"type": "permission_reply", "request_id": requestID, "reply": reply.Reply})
		t.publish()
	}
	return e
}
