package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/store"
)

func TestSubmitRunCompletesAndWritesOutput(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "please review", map[string]string{"ticket": "123"})
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	if run.RunID != "run_000001" {
		t.Fatalf("RunID = %q, want run_000001", run.RunID)
	}
	if run.Status != domain.RunQueued {
		t.Fatalf("initial Status = %q, want %q", run.Status, domain.RunQueued)
	}

	completed, err := o.Wait(ctx, run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("completed Status = %q, want %q; error = %v", completed.Status, domain.RunCompleted, completed.Error)
	}
	if completed.OutputPath == nil || *completed.OutputPath == "" {
		t.Fatalf("OutputPath = %v, want non-empty", completed.OutputPath)
	}

	loaded, err := o.Run(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if loaded.Status != domain.RunCompleted {
		t.Fatalf("loaded Status = %q, want %q", loaded.Status, domain.RunCompleted)
	}

	events, err := o.Transcript(ctx)
	if err != nil {
		t.Fatalf("Transcript() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Type != "facilitator_message" {
		t.Fatalf("events[0].Type = %q, want facilitator_message", events[0].Type)
	}
	if events[1].Type != "agent_result" {
		t.Fatalf("events[1].Type = %q, want agent_result", events[1].Type)
	}
}

func TestRunWorkerWritesStreamingEvents(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	s := store.New(cfg)
	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "stream please", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	if _, err := o.Wait(ctx, run.RunID, time.Second); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	path := filepath.Join(cfg.WorkspaceDir, cfg.StateDirName, "sessions", cfg.SessionID, "runs", run.RunID, run.Agent+".events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(events) error = %v", err)
	}
	if !strings.Contains(string(data), "Reviewer received run run_000001") {
		t.Fatalf("events = %q, want fake stream line", string(data))
	}
}

func TestRunWorkerWritesOpenCodeStreamingEvents(t *testing.T) {
	ctx := context.Background()
	const agentName = "Reviewer"
	const messageID = "msg_ads_run_000001"
	const currentRunMarkerEvent = `{"type":"message.updated","properties":{"sessionID":"session_123","info":{"id":"msg_ads_run_000001","role":"user"}}}`
	const toolEvent = `{"type":"session.next.tool.called","properties":{"sessionID":"session_123","info":{"parentID":"msg_ads_run_000001"},"tool":"read"}}`
	const idleEvent = `{"type":"session.idle","properties":{"sessionID":"session_123","info":{"parentID":"msg_ads_run_000001"}}}`

	eventOpened := make(chan struct{})
	connectedSent := make(chan struct{})
	promptSeen := make(chan struct{})
	idleSent := make(chan struct{})
	var gotPrompt map[string]any

	writeEvent := func(w http.ResponseWriter, raw string) {
		fmt.Fprintln(w, "data: "+raw)
		fmt.Fprintln(w)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "session_123"})
		case "/event":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			close(eventOpened)
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer does not flush")
			}
			writeEvent(w, `{"type":"server.connected"}`)
			flusher.Flush()
			close(connectedSent)
			select {
			case <-promptSeen:
			case <-time.After(time.Second):
				t.Fatal("prompt_async did not arrive")
			}
			writeEvent(w, currentRunMarkerEvent)
			writeEvent(w, toolEvent)
			writeEvent(w, idleEvent)
			flusher.Flush()
			close(idleSent)
		case "/session/session_123/prompt_async":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			select {
			case <-eventOpened:
			default:
				t.Fatal("prompt_async arrived before /event was opened")
			}
			select {
			case <-connectedSent:
			default:
				t.Fatal("prompt_async arrived before server.connected")
			}
			if err := json.NewDecoder(r.Body).Decode(&gotPrompt); err != nil {
				t.Fatal(err)
			}
			close(promptSeen)
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case "/session/session_123/message":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			select {
			case <-idleSent:
			default:
				t.Fatal("message history fetched before session.idle")
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"info":  map[string]string{"id": "msg_old", "role": "assistant", "parentID": "msg_other"},
					"parts": []map[string]string{{"type": "text", "text": "wrong answer"}},
				},
				{
					"info":  map[string]string{"id": "msg_assistant", "role": "assistant", "parentID": messageID},
					"parts": []map[string]string{{"type": "text", "text": "Final OpenCode answer"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := testConfig(t, agentName)
	cfg.Agents[0].Backend = "opencode"
	cfg.Agents[0].StringOptions = map[string]string{
		"base_url":        server.URL,
		"timeout_seconds": "3",
	}
	s := store.New(cfg)
	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, agentName, "stream via opencode", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	completed, err := o.Wait(ctx, run.RunID, 2*time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("completed Status = %q, want %q; error = %v", completed.Status, domain.RunCompleted, completed.Error)
	}
	if gotPrompt["messageID"] != messageID {
		t.Fatalf("prompt messageID = %v, want %q; prompt = %#v", gotPrompt["messageID"], messageID, gotPrompt)
	}
	if completed.OutputPath == nil {
		t.Fatal("OutputPath = nil, want final answer artifact")
	}
	output, err := os.ReadFile(*completed.OutputPath)
	if err != nil {
		t.Fatalf("ReadFile(output) error = %v", err)
	}
	if !strings.Contains(string(output), "Final OpenCode answer") {
		t.Fatalf("output = %q, want final answer", string(output))
	}

	path := filepath.Join(cfg.WorkspaceDir, cfg.StateDirName, "sessions", cfg.SessionID, "runs", run.RunID, agentName+".events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(events) error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{currentRunMarkerEvent, toolEvent, idleEvent}
	if len(lines) != len(want) {
		t.Fatalf("events lines = %#v, want %#v", lines, want)
	}
	for i, line := range lines {
		if line != want[i] {
			t.Fatalf("events[%d] = %q, want %q; all events = %q", i, line, want[i], string(data))
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("events[%d] is not JSON: %v", i, err)
		}
	}
}

func TestSubmitRunRejectsConcurrentRunForSameAgent(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := o.SubmitRun(ctx, "Reviewer", "first", nil)
	if err != nil {
		t.Fatalf("SubmitRun(first) error = %v", err)
	}

	_, err = o.SubmitRun(ctx, "Reviewer", "second", nil)
	if !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("SubmitRun(second) error = %v, want %v", err, ErrAgentBusy)
	}

	if _, err := o.Wait(ctx, first.RunID, time.Second); err != nil {
		t.Fatalf("Wait(first) error = %v", err)
	}
}

func TestSubmitRunContinuesAfterCallerContextCancelled(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	submitCtx, cancel := context.WithCancel(ctx)
	run, err := o.SubmitRun(submitCtx, "Reviewer", "continue after submit", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	cancel()

	completed, err := o.Wait(ctx, run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("Status = %q, want %q; error = %v", completed.Status, domain.RunCompleted, completed.Error)
	}
}

func TestRootContextCancellationFailsRunningRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(context.Background(), "Reviewer", "cancel with process", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	cancel()

	completed, err := o.Wait(context.Background(), run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if completed.Status != domain.RunFailed {
		t.Fatalf("Status = %q, want %q", completed.Status, domain.RunFailed)
	}
	if completed.Error == nil || !strings.Contains(*completed.Error, context.Canceled.Error()) {
		t.Fatalf("Error = %v, want context canceled", completed.Error)
	}
}

func TestWaitForWorkersWaitsForActiveRun(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "wait for worker", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := o.WaitForWorkers(waitCtx); err != nil {
		t.Fatalf("WaitForWorkers() error = %v", err)
	}

	completed, err := o.Run(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("Status = %q, want %q", completed.Status, domain.RunCompleted)
	}
}

func TestWaitReturnsImmediatelyForCompletedRun(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "finish first", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	completed, err := o.Wait(ctx, run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait(first) error = %v", err)
	}
	if completed.Status != domain.RunCompleted {
		t.Fatalf("first Status = %q, want %q", completed.Status, domain.RunCompleted)
	}

	o.mu.Lock()
	o.waiters[run.RunID] = make(chan struct{})
	o.mu.Unlock()

	withTinyTimeout, err := o.Wait(ctx, run.RunID, time.Nanosecond)
	if err != nil {
		t.Fatalf("Wait(tiny timeout) error = %v", err)
	}
	if withTinyTimeout.Status != domain.RunCompleted {
		t.Fatalf("tiny timeout Status = %q, want %q", withTinyTimeout.Status, domain.RunCompleted)
	}

	zeroCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	withZeroTimeout, err := o.Wait(zeroCtx, run.RunID, 0)
	if err != nil {
		t.Fatalf("Wait(zero timeout) error = %v", err)
	}
	if withZeroTimeout.Status != domain.RunCompleted {
		t.Fatalf("zero timeout Status = %q, want %q", withZeroTimeout.Status, domain.RunCompleted)
	}
}

func TestSubmitRunAllowsDifferentAgentsConcurrently(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer", "Implementer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := o.SubmitRun(ctx, "Reviewer", "first", nil)
	if err != nil {
		t.Fatalf("SubmitRun(Reviewer) error = %v", err)
	}
	second, err := o.SubmitRun(ctx, "Implementer", "second", nil)
	if err != nil {
		t.Fatalf("SubmitRun(Implementer) error = %v", err)
	}

	firstDone, err := o.Wait(ctx, first.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait(first) error = %v", err)
	}
	secondDone, err := o.Wait(ctx, second.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait(second) error = %v", err)
	}
	if firstDone.Status != domain.RunCompleted || secondDone.Status != domain.RunCompleted {
		t.Fatalf("statuses = %q/%q, want both completed", firstDone.Status, secondDone.Status)
	}
}

func TestNewMarksExistingRunningRunInterrupted(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	s := store.New(cfg)
	existing := domain.RunRecord{
		RunID:     "run_000001",
		Agent:     "Reviewer",
		Status:    domain.RunRunning,
		Message:   "in flight",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.SaveRun(existing); err != nil {
		t.Fatalf("SaveRun() error = %v", err)
	}

	if _, err := New(ctx, cfg, s); err != nil {
		t.Fatalf("New() error = %v", err)
	}

	loaded, err := s.LoadRun(existing.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if loaded.Status != domain.RunInterrupted {
		t.Fatalf("Status = %q, want %q", loaded.Status, domain.RunInterrupted)
	}
	if loaded.Error == nil || *loaded.Error == "" {
		t.Fatalf("Error = %v, want startup interruption message", loaded.Error)
	}
}

func TestNewPersistsAgentModelFromSpec(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Critic")
	cfg.Agents[0].StringOptions = map[string]string{"model": "zai-coding-plan/glm-5.1"}
	s := store.New(cfg)

	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	state, ok := o.Agent("Critic")
	if !ok {
		t.Fatal("Agent(Critic) not found")
	}
	if state.Model != "zai-coding-plan/glm-5.1" {
		t.Fatalf("state.Model = %q, want zai-coding-plan/glm-5.1", state.Model)
	}

	loaded, err := s.LoadAgentState("Critic")
	if err != nil {
		t.Fatalf("LoadAgentState() error = %v", err)
	}
	if loaded.Model != "zai-coding-plan/glm-5.1" {
		t.Fatalf("persisted Model = %q, want zai-coding-plan/glm-5.1", loaded.Model)
	}
}

func TestNewAppliesDefaultYoloToRuntimeSpec(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	cfg.Defaults.Yolo = false
	s := store.New(cfg)

	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rt := o.runtimes["Reviewer"]
	if rt == nil {
		t.Fatal("runtime for Reviewer not found")
	}
	if rt.spec.Yolo == nil || *rt.spec.Yolo {
		t.Fatalf("runtime spec Yolo = %v, want false from defaults", rt.spec.Yolo)
	}
}

func TestNewKeepsAgentYoloOverride(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	cfg.Defaults.Yolo = false
	enabled := true
	cfg.Agents[0].Yolo = &enabled
	s := store.New(cfg)

	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rt := o.runtimes["Reviewer"]
	if rt == nil {
		t.Fatal("runtime for Reviewer not found")
	}
	if rt.spec.Yolo == nil || !*rt.spec.Yolo {
		t.Fatalf("runtime spec Yolo = %v, want true agent override", rt.spec.Yolo)
	}
}

func TestRunFailsWhenFinalAgentStatePersistenceFails(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	s := store.New(cfg)
	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	agentDir := filepath.Join(s.SessionDir(), "agents", "Reviewer")
	if err := os.Chmod(agentDir, 0o500); err != nil {
		t.Fatalf("Chmod(agentDir) error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(agentDir, 0o700); err != nil {
			t.Errorf("cleanup Chmod(agentDir) error = %v", err)
		}
	})

	run, err := o.SubmitRun(ctx, "Reviewer", "please review", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	completed, err := o.Wait(ctx, run.RunID, time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if completed.Status != domain.RunFailed {
		t.Fatalf("Status = %q, want %q", completed.Status, domain.RunFailed)
	}
	if completed.Error == nil || !strings.Contains(*completed.Error, "save agent state") {
		t.Fatalf("Error = %v, want save agent state error", completed.Error)
	}
}

func TestResetAgentClearsIdleAgentContinuity(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "first", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	if _, err := o.Wait(ctx, run.RunID, time.Second); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	result, err := o.ResetAgent(ctx, "Reviewer", false)
	if err != nil {
		t.Fatalf("ResetAgent() error = %v", err)
	}
	reset := result.State
	if result.Agent != "Reviewer" {
		t.Fatalf("Agent = %q, want Reviewer", result.Agent)
	}
	if result.PreviousBackendSessionID != "" {
		t.Fatalf("PreviousBackendSessionID = %q, want empty initial fake session id", result.PreviousBackendSessionID)
	}
	if result.BackendSessionID != "fake_Reviewer_reset" {
		t.Fatalf("BackendSessionID = %q, want fake_Reviewer_reset", result.BackendSessionID)
	}
	if result.ActiveRun {
		t.Fatal("ActiveRun = true, want false")
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}
	if reset.LastError != nil {
		t.Fatalf("LastError = %v, want nil", reset.LastError)
	}
	if reset.BackendSessionID != "fake_Reviewer_reset" {
		t.Fatalf("BackendSessionID = %q, want fake_Reviewer_reset", reset.BackendSessionID)
	}

	events, err := o.Transcript(ctx)
	if err != nil {
		t.Fatalf("Transcript() error = %v", err)
	}
	last := events[len(events)-1]
	if last.Type != "agent_reset" || last.Agent != "Reviewer" {
		t.Fatalf("last transcript event = %#v, want agent_reset for Reviewer", last)
	}
	if last.Metadata["force"] != "false" {
		t.Fatalf("force metadata = %q, want false", last.Metadata["force"])
	}
}

func TestResetAgentTranscriptFailurePersistsResetDerivedFailedState(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	s := store.New(cfg)
	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "first", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	if _, err := o.Wait(ctx, run.RunID, time.Second); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	transcriptPath := filepath.Join(s.SessionDir(), "transcript.jsonl")
	if err := os.Remove(transcriptPath); err != nil {
		t.Fatalf("Remove(transcript) error = %v", err)
	}
	if err := os.Mkdir(transcriptPath, 0o755); err != nil {
		t.Fatalf("Mkdir(transcript path) error = %v", err)
	}

	_, err = o.ResetAgent(ctx, "Reviewer", false)
	if err == nil {
		t.Fatal("ResetAgent() error = nil, want transcript append error")
	}

	loaded, err := s.LoadAgentState("Reviewer")
	if err != nil {
		t.Fatalf("LoadAgentState() error = %v", err)
	}
	if loaded.Status != domain.AgentFailed {
		t.Fatalf("Status = %q, want %q", loaded.Status, domain.AgentFailed)
	}
	if loaded.LastError == nil || *loaded.LastError == "" {
		t.Fatalf("LastError = %v, want transcript append error", loaded.LastError)
	}
	if loaded.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty reset-derived state", loaded.LastRunID)
	}
	if loaded.BackendSessionID != "fake_Reviewer_reset" {
		t.Fatalf("BackendSessionID = %q, want fake_Reviewer_reset", loaded.BackendSessionID)
	}

	runtime, ok := o.Agent("Reviewer")
	if !ok {
		t.Fatal("Agent(Reviewer) not found")
	}
	if runtime.Status != domain.AgentFailed || runtime.LastRunID != "" || runtime.BackendSessionID != "fake_Reviewer_reset" {
		t.Fatalf("runtime state = %#v, want failed reset-derived state", runtime)
	}
}

func TestResetAgentBusyWithoutForceReturnsConflict(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	cfg.Agents[0].StringOptions = map[string]string{"delay_ms": "5000"}
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "long run", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	waitForAgentStatus(t, o, "Reviewer", domain.AgentRunning)

	_, err = o.ResetAgent(ctx, "Reviewer", false)
	if !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("ResetAgent() error = %v, want %v", err, ErrAgentBusy)
	}

	if _, err := o.ResetAgent(ctx, "Reviewer", true); err != nil {
		t.Fatalf("ResetAgent(force) cleanup error = %v", err)
	}
	completed, err := o.Run(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if completed.Status != domain.RunInterrupted {
		t.Fatalf("Status = %q, want %q", completed.Status, domain.RunInterrupted)
	}
}

func TestResetAgentForceInterruptsActiveRun(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	cfg.Agents[0].StringOptions = map[string]string{"delay_ms": "5000"}
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "long run", map[string]string{"kind": "test"})
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	waitForAgentStatus(t, o, "Reviewer", domain.AgentRunning)

	result, err := o.ResetAgent(ctx, "Reviewer", true)
	if err != nil {
		t.Fatalf("ResetAgent(force) error = %v", err)
	}
	reset := result.State
	if !result.ActiveRun {
		t.Fatal("ActiveRun = false, want true")
	}
	if result.PreviousRunID != run.RunID {
		t.Fatalf("PreviousRunID = %q, want %q", result.PreviousRunID, run.RunID)
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}

	interrupted, err := o.Run(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if interrupted.Status != domain.RunInterrupted {
		t.Fatalf("Status = %q, want %q; error = %v", interrupted.Status, domain.RunInterrupted, interrupted.Error)
	}
	if interrupted.Error == nil || *interrupted.Error != "interrupted by force reset" {
		t.Fatalf("Error = %v, want force reset message", interrupted.Error)
	}
}

func TestResetAgentForceWaitsForReleaseAfterInterruptWindowClosed(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	o.mu.Lock()
	rt := o.runtimes["Reviewer"]
	rt.busy = true
	rt.activeRunID = "run_000999"
	rt.activeRunDone = make(chan struct{})
	rt.runInterruptible = false
	o.mu.Unlock()

	type resetResult struct {
		result domain.AgentResetResult
		err    error
	}
	resultCh := make(chan resetResult, 1)
	go func() {
		result, err := o.ResetAgent(ctx, "Reviewer", true)
		resultCh <- resetResult{result: result, err: err}
	}()

	waitForRuntimeResetting(t, o, "Reviewer")
	select {
	case result := <-resultCh:
		t.Fatalf("ResetAgent(force) returned before worker release: result = %#v, err = %v", result.result, result.err)
	case <-time.After(20 * time.Millisecond):
	}

	o.releaseAgent("Reviewer")

	var result resetResult
	select {
	case result = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("ResetAgent(force) did not return after worker release")
	}
	if result.err != nil {
		t.Fatalf("ResetAgent(force) error = %v", result.err)
	}
	if result.result.State.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", result.result.State.Status, domain.AgentIdle)
	}
	if !result.result.ActiveRun {
		t.Fatal("ActiveRun = false, want true because runtime was busy")
	}

	events, err := o.Transcript(ctx)
	if err != nil {
		t.Fatalf("Transcript() error = %v", err)
	}
	last := events[len(events)-1]
	if last.Type != "agent_reset" || last.Agent != "Reviewer" {
		t.Fatalf("last transcript event = %#v, want agent_reset for Reviewer", last)
	}
	if last.Status != "" {
		t.Fatalf("Status = %q, want empty", last.Status)
	}
	if last.Metadata["force"] != "true" {
		t.Fatalf("force metadata = %q, want true", last.Metadata["force"])
	}
	if last.Metadata["previous_run_id"] != "" {
		t.Fatalf("previous_run_id metadata = %q, want empty", last.Metadata["previous_run_id"])
	}
}

func waitForAgentStatus(t *testing.T, o *Orchestrator, agentName string, want domain.AgentStatus) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, agent := range o.Agents() {
			if agent.Name == agentName && agent.Status == want {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("agent %q did not reach status %q", agentName, want)
		case <-ticker.C:
		}
	}
}

func waitForRuntimeResetting(t *testing.T, o *Orchestrator, agentName string) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		o.mu.Lock()
		rt := o.runtimes[agentName]
		resetting := rt != nil && rt.resetting
		o.mu.Unlock()
		if resetting {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("agent %q did not start resetting", agentName)
		case <-ticker.C:
		}
	}
}

func testConfig(t *testing.T, agentNames ...string) domain.SessionConfig {
	t.Helper()

	agents := make([]domain.AgentSpec, 0, len(agentNames))
	for _, name := range agentNames {
		agents = append(agents, domain.AgentSpec{
			Name:          name,
			Backend:       "fake",
			StartupPrompt: "You are " + name,
		})
	}

	return domain.SessionConfig{
		SessionName:  "test",
		SessionID:    "session_test",
		WorkspaceDir: t.TempDir(),
		StateDirName: ".agent-debug-squad",
		Host:         "127.0.0.1",
		Port:         8080,
		Defaults:     domain.SessionDefaults{Yolo: true},
		Agents:       agents,
	}
}
