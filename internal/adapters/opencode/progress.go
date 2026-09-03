package opencode

import (
	"sort"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

type progressTracker struct {
	rootSessionID string
	sink          domain.RunSink
	progress      domain.RunProgress
	subagents     map[string]domain.SubagentProgress
	activeTasks   map[string]string
	rootMessages  map[string]struct{}
}

func newProgressTracker(rootSessionID, messageID string, sink domain.RunSink) *progressTracker {
	return &progressTracker{
		rootSessionID: rootSessionID,
		sink:          sink,
		progress: domain.RunProgress{
			Phase:          domain.RunPhaseRunning,
			LastActivityAt: time.Now().UTC(),
		},
		subagents:   map[string]domain.SubagentProgress{},
		activeTasks: map[string]string{},
		rootMessages: map[string]struct{}{
			messageID: {},
		},
	}
}

func (t *progressTracker) handleEvent(event map[string]any, rootRunEvent bool, at time.Time) bool {
	properties, ok := event["properties"].(map[string]any)
	if !ok {
		return false
	}
	sessionID := stringValue(properties["sessionID"])
	if sessionID == "" {
		return false
	}

	rootEvent := sessionID == t.rootSessionID
	if rootEvent {
		if info, ok := properties["info"].(map[string]any); ok {
			if _, tracked := t.rootMessages[stringValue(info["parentID"])]; tracked {
				t.rootMessages[stringValue(info["id"])] = struct{}{}
			}
		}
		if !rootRunEvent {
			part, ok := properties["part"].(map[string]any)
			if !ok {
				return false
			}
			if _, tracked := t.rootMessages[stringValue(part["messageID"])]; !tracked {
				return false
			}
		}
		t.handleTaskPart(properties, at)
		if stringValue(event["type"]) == "session.next.tool.called" && stringValue(properties["tool"]) == "task" {
			if callID := stringValue(properties["callID"]); callID != "" {
				t.activeTasks[callID] = ""
				t.report(at)
			}
		}
	}

	if stringValue(event["type"]) == "session.created" {
		if info, ok := properties["info"].(map[string]any); ok {
			parentID := stringValue(info["parentID"])
			if parentID == t.rootSessionID || t.isSubagent(parentID) {
				t.touchSubagent(sessionID, parentID, "running", at)
				return true
			}
		}
	}

	if !rootEvent && !t.isSubagent(sessionID) {
		return false
	}
	if rootEvent {
		return true
	}

	child := t.subagents[sessionID]
	status := child.Status
	if status == "" {
		status = "running"
	}
	switch stringValue(event["type"]) {
	case "session.idle":
		status = "completed"
	case "session.error":
		status = "failed"
	case "session.status":
		if value, ok := properties["status"].(map[string]any); ok {
			switch stringValue(value["type"]) {
			case "idle":
				status = "completed"
			case "retry":
				status = "retrying"
			}
		}
	}
	t.touchSubagent(sessionID, child.ParentID, status, at)
	return true
}

func (t *progressTracker) handleTaskPart(properties map[string]any, at time.Time) {
	part, ok := properties["part"].(map[string]any)
	if !ok || stringValue(part["type"]) != "tool" || stringValue(part["tool"]) != "task" {
		return
	}
	callID := stringValue(part["callID"])
	state, ok := part["state"].(map[string]any)
	if !ok || callID == "" {
		return
	}
	status := stringValue(state["status"])
	childID := taskSessionID(part, state)
	switch status {
	case "pending", "running":
		t.activeTasks[callID] = childID
		if childID != "" {
			t.touchSubagent(childID, t.rootSessionID, "running", at)
		}
	case "completed", "error":
		if childID == "" {
			childID = t.activeTasks[callID]
		}
		delete(t.activeTasks, callID)
		if childID != "" {
			childStatus := "completed"
			if status == "error" {
				childStatus = "failed"
			}
			t.setSubagentStatus(childID, t.rootSessionID, childStatus, at)
		}
	}
	t.report(at)
}

func taskSessionID(part map[string]any, state map[string]any) string {
	for _, value := range []any{state["metadata"], part["metadata"]} {
		metadata, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if sessionID := stringValue(metadata["sessionId"]); sessionID != "" {
			return sessionID
		}
		if sessionID := stringValue(metadata["sessionID"]); sessionID != "" {
			return sessionID
		}
	}
	return ""
}

func (t *progressTracker) isSubagent(sessionID string) bool {
	_, ok := t.subagents[sessionID]
	return ok
}

func (t *progressTracker) touchSubagent(sessionID, parentID, status string, at time.Time) {
	if sessionID == "" || sessionID == t.rootSessionID {
		return
	}
	at = at.UTC()
	child := t.subagents[sessionID]
	child.ID = sessionID
	if parentID != "" {
		child.ParentID = parentID
	}
	child.Status = status
	child.LastActivityAt = at
	t.subagents[sessionID] = child
	t.report(at)
}

func (t *progressTracker) setSubagentStatus(sessionID, parentID, status string, at time.Time) {
	child := t.subagents[sessionID]
	if child.ID == "" {
		child.ID = sessionID
		child.LastActivityAt = at.UTC()
	}
	if parentID != "" {
		child.ParentID = parentID
	}
	child.Status = status
	t.subagents[sessionID] = child
}

func (t *progressTracker) report(at time.Time) {
	at = at.UTC()
	t.progress.LastActivityAt = at
	waiting := len(t.activeTasks) > 0
	for _, child := range t.subagents {
		if child.Status == "running" || child.Status == "retrying" {
			waiting = true
			break
		}
	}
	if waiting {
		t.progress.Phase = domain.RunPhaseWaitingForSubagent
	} else {
		t.progress.Phase = domain.RunPhaseRunning
	}
	t.progress.Subagents = t.progress.Subagents[:0]
	for _, child := range t.subagents {
		t.progress.Subagents = append(t.progress.Subagents, child)
	}
	sort.Slice(t.progress.Subagents, func(i, j int) bool {
		return t.progress.Subagents[i].ID < t.progress.Subagents[j].ID
	})
	if len(t.progress.Subagents) > 0 {
		latest := t.progress.Subagents[0].LastActivityAt
		for _, child := range t.progress.Subagents[1:] {
			if child.LastActivityAt.After(latest) {
				latest = child.LastActivityAt
			}
		}
		t.progress.ChildLastActivityAt = &latest
	}
	progress := t.progress
	progress.Subagents = append([]domain.SubagentProgress(nil), t.progress.Subagents...)
	if t.progress.ChildLastActivityAt != nil {
		latest := *t.progress.ChildLastActivityAt
		progress.ChildLastActivityAt = &latest
	}
	domain.ReportRunProgress(t.sink, progress)
}
