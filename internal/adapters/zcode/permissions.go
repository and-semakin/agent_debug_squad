package zcode

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func (a *Adapter) ReplyPermission(ctx context.Context, runID, requestID string, reply domain.PermissionReply) error {
	if err := reply.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	r := a.active
	a.mu.Unlock()
	if r == nil || r.id != runID {
		return domain.ErrPermissionInactive
	}
	job := replyJob{ctx: ctx, id: requestID, reply: reply, result: make(chan error, 1)}
	select {
	case r.replies <- job:
	case <-r.done:
		return domain.ErrPermissionInactive
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-job.result:
		return err
	case <-r.done:
		return domain.ErrPermissionInactive
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *activeRun) callback(ctx context.Context, msg wireMessage) error {
	if msg.Method != "interaction/requestPermission" {
		_ = r.c.respond(ctx, map[string]any{"id": msg.ID, "error": wireError{Code: -32601, Message: "Unsupported Squad host interaction: " + msg.Method}})
		return fmt.Errorf("unsupported zcode host interaction: %s", msg.Method)
	}
	var p struct {
		RequestID  string             `json:"requestId"`
		SessionID  string             `json:"sessionId"`
		TurnID     string             `json:"turnId"`
		ToolName   string             `json:"toolName"`
		ToolCallID string             `json:"toolCallId"`
		Input      json.RawMessage    `json:"input"`
		Options    []permissionOption `json:"options"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return fmt.Errorf("invalid zcode permission: %w", err)
	}
	if p.SessionID != r.session {
		if err := r.pollChildren(ctx, false); err != nil {
			return err
		}
	}
	childProgress, child := r.children[p.SessionID]
	child = child && (childProgress.Status == "running" || childProgress.Status == "waiting" || childProgress.Status == "blocked")
	owned := p.SessionID == r.session && (r.turn != "" && p.TurnID == r.turn) || child
	if !owned || p.RequestID == "" || p.ToolName == "" {
		_ = r.c.respond(ctx, map[string]any{"id": msg.ID, "result": map[string]string{"decision": "deny", "reason": "Request does not belong to the active Squad turn"}})
		return fmt.Errorf("zcode permission does not belong to active turn")
	}
	if r.seen[p.RequestID] {
		// The same wire request is a retransmission; never submit a second decision.
		// A different wire ID reusing a logical request ID is invalid.
		if pending, ok := r.pending[p.RequestID]; ok && string(pending.wireID) == string(msg.ID) {
			return nil
		}
		return fmt.Errorf("zcode reused permission request ID")
	}
	request := domain.PermissionRequest{ID: p.RequestID, SessionID: p.SessionID, Permission: p.ToolName, Metadata: msg.Params, AskedAt: time.Now().UTC()}
	request.Tool, _ = json.Marshal(map[string]string{"toolCallId": p.ToolCallID, "turnId": p.TurnID})
	for _, option := range p.Options {
		if option.Kind == "allow_always" {
			var response struct {
				Updates []struct {
					Rules []struct {
						ToolName    string `json:"toolName"`
						RuleContent string `json:"ruleContent"`
					} `json:"rules"`
				} `json:"permissionUpdates"`
			}
			if json.Unmarshal(option.Response, &response) == nil {
				for _, update := range response.Updates {
					for _, rule := range update.Rules {
						pattern := rule.ToolName
						if rule.RuleContent != "" {
							pattern += ": " + rule.RuleContent
						}
						request.Always = append(request.Always, pattern)
					}
				}
			}
		}
	}
	r.pending[p.RequestID] = approval{request: request, wireID: msg.ID, options: p.Options}
	r.seen[p.RequestID] = true
	r.publish()
	return nil
}
func (r *activeRun) reply(job replyJob) error {
	if err := job.ctx.Err(); err != nil {
		return err
	}
	pending, ok := r.pending[job.id]
	if !ok {
		return domain.ErrPermissionNotFound
	}
	decision := map[string]any{"decision": "deny", "reason": job.reply.Message}
	if job.reply.Reply != "reject" {
		kind := "allow_once"
		if job.reply.Reply == "always" {
			kind = "allow_always"
		}
		found := false
		for _, option := range pending.options {
			if option.Kind == kind && len(option.Response) > 0 {
				if err := json.Unmarshal(option.Response, &decision); err != nil {
					return fmt.Errorf("invalid zcode permission option")
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: ZCode did not offer %s", domain.ErrPermissionReplyInvalid, job.reply.Reply)
		}
	}
	if job.reply.Message != "" {
		decision["reason"] = job.reply.Message
	}
	if err := r.c.respond(job.ctx, map[string]any{"id": pending.wireID, "result": decision}); err != nil {
		return err
	}
	delete(r.pending, job.id)
	record, _ := json.Marshal(map[string]any{"type": "squad.permission.reply", "run_id": r.id, "request_id": job.id, "session_id": pending.request.SessionID, "reply": job.reply.Reply, "source": "coordinator"})
	r.sink.StdoutLine(string(record))
	r.publish()
	return nil
}
