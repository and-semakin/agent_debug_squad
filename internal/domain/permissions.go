package domain

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrPermissionNotFound     = errors.New("pending permission not found")
	ErrPermissionInactive     = errors.New("run is not active")
	ErrPermissionUnsupported  = errors.New("backend does not support permission replies")
	ErrPermissionReplyInvalid = errors.New("reply must be once, always, or reject")
)

type PermissionRequest struct {
	ID               string          `json:"id"`
	SessionID        string          `json:"session_id"`
	Permission       string          `json:"permission"`
	Patterns         []string        `json:"patterns"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	Always           []string        `json:"always,omitempty"`
	Tool             json.RawMessage `json:"tool,omitempty"`
	AskedAt          time.Time       `json:"asked_at"`
	AutoApproving    bool            `json:"auto_approving,omitempty"`
	AutoApproveError string          `json:"auto_approve_error,omitempty"`
}

type PermissionReply struct {
	Reply   string `json:"reply"`
	Message string `json:"message,omitempty"`
}

func (r PermissionReply) Validate() error {
	switch r.Reply {
	case "once", "always", "reject":
		return nil
	default:
		return ErrPermissionReplyInvalid
	}
}

func ClonePermissions(requests []PermissionRequest) []PermissionRequest {
	if requests == nil {
		return nil
	}
	out := make([]PermissionRequest, len(requests))
	for i, p := range requests {
		out[i] = p
		out[i].Patterns = append([]string(nil), p.Patterns...)
		out[i].Always = append([]string(nil), p.Always...)
		out[i].Metadata = append(json.RawMessage(nil), p.Metadata...)
		out[i].Tool = append(json.RawMessage(nil), p.Tool...)
	}
	return out
}

func (p RunProgress) NeedsPermissionReply() bool {
	for _, request := range p.PendingPermissions {
		if !request.AutoApproving {
			return true
		}
	}
	return false
}
