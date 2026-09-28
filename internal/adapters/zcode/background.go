package zcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Background work tracking. Background bash/work tasks are independent of
// child sessions: parent completion does not imply background completion, and
// a stop acknowledgement alone never establishes cleanup completion. Task
// identities come from the verified session snapshot fields only; shapes the
// source does not define are never guessed.
type backgroundTasks struct {
	ids      map[string]bool
	statuses map[string]string
	order    []string
}

func newBackgroundTasks() *backgroundTasks {
	return &backgroundTasks{ids: map[string]bool{}, statuses: map[string]string{}}
}

func (b *backgroundTasks) count() int { return len(b.order) }

// observe records a task identity with its status evidence.
func (b *backgroundTasks) observe(taskID, status string) {
	if taskID == "" {
		return
	}
	if !b.ids[taskID] {
		b.ids[taskID] = true
		b.order = append(b.order, taskID)
	}
	b.statuses[taskID] = status
}

// backgroundTaskRunning reports whether a status proves the task is still
// executing. The upstream status set is cancelled, completed, failed, lost,
// running, spawn_error, and timed_out, and terminal means anything but
// running; an absent status proves nothing and must not count as terminal.
func backgroundTaskRunning(status string) bool {
	if status == "" {
		// Unknown status: assume running for cleanup decisions, never
		// silently confirm completion.
		return true
	}
	return status == "running"
}

// observeSessionSnapshot tracks background job identity from the verified
// session.backgroundJobs surface of a session snapshot. Anything else is
// ignored; status evidence is kept so confirmation can tell running work from
// finished work.
func (b *backgroundTasks) observeSessionSnapshot(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var session struct {
		BackgroundJobs []struct {
			ID     string `json:"id"`
			TaskID string `json:"taskId"`
			JobID  string `json:"jobId"`
			Status string `json:"status"`
		} `json:"backgroundJobs"`
	}
	if json.Unmarshal(raw, &session) != nil {
		return
	}
	for _, job := range session.BackgroundJobs {
		switch {
		case job.ID != "":
			b.observe(job.ID, job.Status)
		case job.TaskID != "":
			b.observe(job.TaskID, job.Status)
		case job.JobID != "":
			b.observe(job.JobID, job.Status)
		}
	}
}

// readSessionBackgroundJobs reads one session snapshot strictly: the session
// identity must match the requested session and the backgroundJobs surface
// must be present, even when empty. A snapshot without the surface, with a
// foreign session identity, or with unreadable content is an error — missing
// evidence never confirms that background work stopped.
func readSessionBackgroundJobs(ctx context.Context, call func(ctx context.Context, method string, params any, result any) error, session string) (map[string]string, error) {
	var raw json.RawMessage
	if err := call(ctx, "session/read", map[string]any{"sessionId": session}, &raw); err != nil {
		return nil, err
	}
	var envelope struct {
		Session *struct {
			SessionID      string           `json:"sessionId"`
			BackgroundJobs *json.RawMessage `json:"backgroundJobs"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Session == nil {
		return nil, errors.New("zcode session snapshot carried no session object")
	}
	if envelope.Session.SessionID != session {
		return nil, fmt.Errorf("zcode session snapshot names session %q, not %q", envelope.Session.SessionID, session)
	}
	if envelope.Session.BackgroundJobs == nil {
		return nil, errors.New("zcode session snapshot carried no backgroundJobs surface")
	}
	var jobs []struct {
		ID     string `json:"id"`
		TaskID string `json:"taskId"`
		JobID  string `json:"jobId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(*envelope.Session.BackgroundJobs, &jobs); err != nil {
		return nil, errors.New("zcode backgroundJobs surface could not be decoded")
	}
	statuses := map[string]string{}
	for _, job := range jobs {
		switch {
		case job.TaskID != "":
			statuses[job.TaskID] = job.Status
		case job.ID != "":
			statuses[job.ID] = job.Status
		case job.JobID != "":
			statuses[job.JobID] = job.Status
		}
	}
	return statuses, nil
}

// refreshBackgroundTasks re-reads the session snapshot strictly and folds the
// current backgroundJobs surface into the owned set: tasks the attempt started
// during the turn join before the drain, so cleanup cannot pass with an empty
// list while old work still runs.
func refreshBackgroundTasks(ctx context.Context, call func(ctx context.Context, method string, params any, result any) error, session string, tasks *backgroundTasks) error {
	jobs, err := readSessionBackgroundJobs(ctx, call, session)
	if err != nil {
		return err
	}
	for id, status := range jobs {
		tasks.observe(id, status)
	}
	return nil
}

// confirmBackgroundTasksStopped verifies with one fresh session read that
// every owned task has left the runtime or reached a terminal status. ZCode
// keeps finished tasks listed with an updated status, so absence is not the
// only completion evidence — but a task still running, or with unknown status,
// keeps the drain uncertain.
func confirmBackgroundTasksStopped(ctx context.Context, call func(ctx context.Context, method string, params any, result any) error, session string, tasks *backgroundTasks) error {
	if tasks == nil || tasks.count() == 0 {
		return nil
	}
	jobs, err := readSessionBackgroundJobs(ctx, call, session)
	if err != nil {
		return err
	}
	for _, id := range tasks.order {
		if status, ok := jobs[id]; ok && backgroundTaskRunning(status) {
			return fmt.Errorf("background task %s is still running after cancellation", id)
		}
	}
	return nil
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
