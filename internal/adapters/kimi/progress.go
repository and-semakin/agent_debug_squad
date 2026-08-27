package kimi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

const (
	sessionDiscoveryTimeout = 5 * time.Second
	sessionDiscoveryPoll    = 100 * time.Millisecond
	subagentActivityPoll    = 250 * time.Millisecond
)

var kimiSessionStartMu sync.Mutex

type progressTrackingSink struct {
	domain.RunSink
	tracker *progressTracker
}

func (s *progressTrackingSink) StdoutLine(line string) {
	s.RunSink.StdoutLine(line)
	s.tracker.handleRootEvent(line, time.Now().UTC())
}

type progressTracker struct {
	mu             sync.Mutex
	sink           domain.RunSink
	progress       domain.RunProgress
	agentToolCalls map[string]struct{}
}

func newProgressTracker(sink domain.RunSink) *progressTracker {
	return &progressTracker{
		sink:           sink,
		progress:       domain.RunProgress{Phase: domain.RunPhaseRunning, LastActivityAt: time.Now().UTC()},
		agentToolCalls: map[string]struct{}{},
	}
}

func (t *progressTracker) handleRootEvent(line string, at time.Time) {
	var event struct {
		Role       string `json:"role"`
		Type       string `json:"type"`
		ToolCallID string `json:"tool_call_id"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal([]byte(line), &event) != nil {
		t.touch(at)
		return
	}

	t.mu.Lock()
	t.progress.LastActivityAt = at
	for _, call := range event.ToolCalls {
		if call.Function.Name == "Agent" {
			t.agentToolCalls[call.ID] = struct{}{}
			t.progress.Phase = domain.RunPhaseWaitingForSubagent
		}
	}
	if event.Role == "tool" || event.Type == "tool" {
		if _, ok := t.agentToolCalls[event.ToolCallID]; ok {
			delete(t.agentToolCalls, event.ToolCallID)
			if len(t.agentToolCalls) == 0 {
				t.progress.Phase = domain.RunPhaseRunning
				for i := range t.progress.Subagents {
					t.progress.Subagents[i].Status = "completed"
				}
			}
		}
	}
	progress := cloneProgress(t.progress)
	t.mu.Unlock()
	domain.ReportRunProgress(t.sink, progress)
}

func (t *progressTracker) touch(at time.Time) {
	t.mu.Lock()
	t.progress.LastActivityAt = at
	progress := cloneProgress(t.progress)
	t.mu.Unlock()
	domain.ReportRunProgress(t.sink, progress)
}

func (t *progressTracker) updateSubagents(subagents []domain.SubagentProgress) {
	t.mu.Lock()
	t.progress.Subagents = append([]domain.SubagentProgress(nil), subagents...)
	var latest time.Time
	for _, child := range subagents {
		if child.LastActivityAt.After(latest) {
			latest = child.LastActivityAt
		}
	}
	if !latest.IsZero() {
		latest = latest.UTC()
		t.progress.ChildLastActivityAt = &latest
		if latest.After(t.progress.LastActivityAt) {
			t.progress.LastActivityAt = latest
		}
	}
	progress := cloneProgress(t.progress)
	t.mu.Unlock()
	domain.ReportRunProgress(t.sink, progress)
}

func cloneProgress(progress domain.RunProgress) domain.RunProgress {
	cloned := progress
	cloned.Subagents = append([]domain.SubagentProgress(nil), progress.Subagents...)
	if progress.ChildLastActivityAt != nil {
		at := *progress.ChildLastActivityAt
		cloned.ChildLastActivityAt = &at
	}
	return cloned
}

type kimiSessionState struct {
	ID     string `json:"id"`
	CWD    string `json:"cwd"`
	Agents map[string]struct {
		Type          string `json:"type"`
		ParentAgentID string `json:"parentAgentId"`
	} `json:"agents"`
}

type sessionObserver struct {
	root      string
	workspace string
	known     map[string]struct{}
	tracker   *progressTracker
	unlocked  bool
}

func prepareSessionObserver(spec domain.AgentSpec, command, workspace string, tracker *progressTracker) *sessionObserver {
	root := strings.TrimSpace(spec.StringOptions["session_root"])
	if root == "" {
		if filepath.Base(command) != "kimi" {
			return nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		root = filepath.Join(home, ".kimi-code", "sessions")
	}

	kimiSessionStartMu.Lock()
	return &sessionObserver{
		root:      root,
		workspace: filepath.Clean(workspace),
		known:     sessionStatePaths(root),
		tracker:   tracker,
	}
}

func (o *sessionObserver) abort() {
	if o == nil || o.unlocked {
		return
	}
	o.unlocked = true
	kimiSessionStartMu.Unlock()
}

func (o *sessionObserver) discoverAndWatch(ctx context.Context) func() {
	deadline := time.NewTimer(sessionDiscoveryTimeout)
	ticker := time.NewTicker(sessionDiscoveryPoll)
	defer deadline.Stop()
	defer ticker.Stop()

	var sessionDir string
	for sessionDir == "" {
		sessionDir = o.findNewSession()
		if sessionDir != "" {
			break
		}
		select {
		case <-ctx.Done():
			o.abort()
			return nil
		case <-deadline.C:
			o.abort()
			return nil
		case <-ticker.C:
		}
	}
	o.abort()

	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.watch(watchCtx, sessionDir)
	}()
	return func() {
		cancel()
		<-done
	}
}

func (o *sessionObserver) findNewSession() string {
	paths := sessionStatePaths(o.root)
	for path := range paths {
		if _, existed := o.known[path]; existed {
			continue
		}
		state, err := readKimiSessionState(path)
		if err == nil && filepath.Clean(state.CWD) == o.workspace {
			return filepath.Dir(path)
		}
	}
	return ""
}

func (o *sessionObserver) watch(ctx context.Context, sessionDir string) {
	ticker := time.NewTicker(subagentActivityPoll)
	defer ticker.Stop()
	var previous string
	for {
		subagents, fingerprint := readSubagentProgress(sessionDir)
		if fingerprint != previous {
			previous = fingerprint
			o.tracker.updateSubagents(subagents)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func sessionStatePaths(root string) map[string]struct{} {
	paths := map[string]struct{}{}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Name() == "state.json" {
			paths[path] = struct{}{}
		}
		return nil
	})
	return paths
}

func readKimiSessionState(path string) (kimiSessionState, error) {
	var state kimiSessionState
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.ID == "" || state.CWD == "" {
		return state, errors.New("incomplete kimi session state")
	}
	return state, nil
}

func readSubagentProgress(sessionDir string) ([]domain.SubagentProgress, string) {
	state, err := readKimiSessionState(filepath.Join(sessionDir, "state.json"))
	if err != nil {
		return nil, ""
	}
	children := make([]domain.SubagentProgress, 0)
	for id, agent := range state.Agents {
		if agent.Type != "sub" {
			continue
		}
		info, err := os.Stat(filepath.Join(sessionDir, "agents", id, "wire.jsonl"))
		if err != nil {
			continue
		}
		children = append(children, domain.SubagentProgress{
			ID:             id,
			ParentID:       agent.ParentAgentID,
			Status:         "running",
			LastActivityAt: info.ModTime().UTC(),
		})
	}
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })
	fingerprint, _ := json.Marshal(children)
	return children, string(fingerprint)
}
