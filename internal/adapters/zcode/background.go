package zcode

import (
	"context"
	"encoding/json"
	"time"
)

// Background work tracking. Background bash/work tasks are independent of
// child sessions: parent completion does not imply background completion, and
// a stop acknowledgement alone never establishes cleanup completion. Task
// identities come from allowlisted snapshot/event fields only.
type backgroundTasks struct {
	ids   map[string]bool
	order []string
}

func newBackgroundTasks() *backgroundTasks {
	return &backgroundTasks{ids: map[string]bool{}}
}

// observe records a task identity from an allowlisted wire field. Unknown or
// malformed shapes are ignored rather than guessed.
func (b *backgroundTasks) observe(taskID string) {
	if taskID == "" || b.ids[taskID] {
		return
	}
	b.ids[taskID] = true
	b.order = append(b.order, taskID)
}

func (b *backgroundTasks) count() int { return len(b.order) }

// backgroundTaskEventTypes lists the event kinds that carry background task
// identity at this baseline. The payload field is allowlisted; anything else
// is ignored.
var backgroundTaskEventTypes = map[string]bool{
	"background_task.started":   true,
	"background_task.completed": true,
	"background_task.failed":    true,
}

// observeEvent tracks background task identity from a session event.
func (b *backgroundTasks) observeEvent(e *event) {
	if !backgroundTaskEventTypes[e.Type] {
		return
	}
	b.observe(e.Payload.TaskID)
}

// observeSnapshot tracks background task identity from a session snapshot.
func (b *backgroundTasks) observeSnapshot(raw json.RawMessage) {
	var snap struct {
		BackgroundTasks []struct {
			TaskID string `json:"taskId"`
			ID     string `json:"id"`
		} `json:"backgroundTasks"`
	}
	if json.Unmarshal(raw, &snap) != nil {
		return
	}
	for _, task := range snap.BackgroundTasks {
		if task.TaskID != "" {
			b.observe(task.TaskID)
		} else {
			b.observe(task.ID)
		}
	}
}

// cleanupDeadline is the shared terminal cleanup budget for every final
// Squad-run terminal path; graceful calls share the shorter prefix.
const (
	cleanupDeadline    = 5 * time.Second
	cleanupGracePeriod = 2 * time.Second
)

// cleanupSpec describes what one terminal cleanup must stop, in dependency
// order: stop the root turn, cancel known owned background tasks through the
// owning runtime, stop/close owned descendants, close the root, then close
// stdin and terminate the owned process group with the remaining budget. The
// continuation drain reuses the same order with stopRoot false: the root
// conversation stays open across the provider switch.
type cleanupSpec struct {
	session     string
	descendants []string
	background  *backgroundTasks
	stopRoot    bool
}

// runTerminalCleanup executes the shared cleanup under one deadline. A stop
// acknowledgement alone is not completion; every step gets whatever budget
// remains, and failure is reported rather than swallowed. Only positively
// owned work is targeted; processes are never selected by executable name.
func runTerminalCleanup(call func(ctx context.Context, method string, params any, result any) error, spec cleanupSpec) error {
	deadline := time.Now().Add(cleanupDeadline)
	step := func(graceful time.Duration, method string, params map[string]any) error {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errCleanupBudgetExhausted
		}
		budget := remaining
		if graceful > 0 && graceful < budget {
			budget = graceful
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		return call(ctx, method, params, nil)
	}
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if spec.stopRoot && spec.session != "" {
		record(step(cleanupGracePeriod, "session/stop", map[string]any{"sessionId": spec.session}))
	}
	if spec.background != nil {
		for _, taskID := range spec.background.order {
			record(step(0, "session/cancelBackgroundTask", map[string]any{"sessionId": spec.session, "taskId": taskID}))
		}
	}
	for _, child := range spec.descendants {
		record(step(0, "session/stop", map[string]any{"sessionId": child}))
		record(step(0, "session/close", map[string]any{"sessionId": child}))
	}
	if spec.stopRoot && spec.session != "" {
		record(step(0, "session/close", map[string]any{"sessionId": spec.session}))
	}
	return firstErr
}

type cleanupBudgetError struct{}

func (cleanupBudgetError) Error() string {
	return "zcode terminal cleanup exceeded its shared five-second deadline"
}

var errCleanupBudgetExhausted error = cleanupBudgetError{}
