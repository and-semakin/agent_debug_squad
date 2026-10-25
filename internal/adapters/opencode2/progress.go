package opencode2

import (
	"sort"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

type progressTracker struct {
	mu            sync.Mutex
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
	t.mu.Lock()
	defer t.mu.Unlock()
	d, _ := event["data"].(map[string]any)
	sid := stringValue(d["sessionID"])
	kind := stringValue(event["type"])
	if sid == "" {
		return false
	}
	if kind == "session.created" && sid != t.rootSessionID {
		parent := stringValue(d["parentID"])
		if parent == t.rootSessionID || t.isSubagent(parent) {
			t.touchSubagent(sid, parent, "running", at)
			return true
		}
	}
	if sid != t.rootSessionID && !t.isSubagent(sid) {
		return false
	}
	if sid == t.rootSessionID {
		if !rootRunEvent {
			return false
		}
		if id := stringValue(d["assistantMessageID"]); id != "" {
			t.rootMessages[id] = struct{}{}
		}
		if kind == "session.tool.input.started" && stringValue(d["name"]) == "task" {
			t.activeTasks[stringValue(d["id"])] = ""
		}
		if kind == "session.tool.success" || kind == "session.tool.failed" {
			delete(t.activeTasks, stringValue(d["id"]))
		}
		t.report(at)
		return true
	}
	child := t.subagents[sid]
	status := child.Status
	switch kind {
	case "session.execution.started":
		status = "running"
	case "session.execution.succeeded":
		status = "completed"
	case "session.execution.failed", "session.execution.interrupted":
		status = "failed"
	case "session.retry.scheduled":
		status = "retrying"
	}
	t.touchSubagent(sid, child.ParentID, status, at)
	return true
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
	if len(t.progress.PendingPermissions) > 0 {
		t.progress.Phase = domain.RunPhaseWaitingForPermission
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
	progress.PendingPermissions = domain.ClonePermissions(t.progress.PendingPermissions)
	progress.Subagents = append([]domain.SubagentProgress(nil), t.progress.Subagents...)
	if t.progress.ChildLastActivityAt != nil {
		latest := *t.progress.ChildLastActivityAt
		progress.ChildLastActivityAt = &latest
	}
	domain.ReportRunProgress(t.sink, progress)
}

// setPermissions and event updates share a lock so either source retains the
// latest subagent and permission state when publishing a snapshot.
func (t *progressTracker) setPermissions(pending []domain.PermissionRequest) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.progress.PendingPermissions = domain.ClonePermissions(pending)
	t.report(time.Now().UTC())
}

func (t *progressTracker) ownsPermission(sessionID, messageID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sessionID == t.rootSessionID {
		if messageID == "" {
			return true
		}
		_, ok := t.rootMessages[messageID]
		return ok
	}
	return t.isSubagent(sessionID)
}
