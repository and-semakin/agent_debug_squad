package kimi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestParseStreamJSONUsesLastAssistantMessage(t *testing.T) {
	input := []byte(`{"type":"assistant","message":{"content":"First"}}
{"type":"tool","name":"read_file"}
{"type":"assistant","message":{"content":"Final"}}
`)

	result, err := ParseStreamJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "Final" {
		t.Fatalf("FinalMessage = %q, want %q", result.FinalMessage, "Final")
	}
}

func TestParseStreamJSONUsesRoleContentAssistantMessage(t *testing.T) {
	input := []byte(`{"role":"assistant","content":"First"}
{"role":"tool","content":"ignore me"}
{"role":"assistant","content":"Final answer"}
`)

	result, err := ParseStreamJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "Final answer" {
		t.Fatalf("FinalMessage = %q, want %q", result.FinalMessage, "Final answer")
	}
}

func TestParseStreamJSONReturnsErrorForMalformedJSON(t *testing.T) {
	_, err := ParseStreamJSON([]byte(`{"type":"assistant","message":{"content":"unterminated"}`))
	if err == nil {
		t.Fatal("ParseStreamJSON() error = nil, want malformed JSON error")
	}
}

func TestParseStreamJSONHandlesLargeAssistantEvent(t *testing.T) {
	message := strings.Repeat("x", 70*1024)
	input := []byte(`{"type":"assistant","message":{"content":"` + message + `"}}
`)

	result, err := ParseStreamJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != message {
		t.Fatalf("FinalMessage len = %d, want %d", len(result.FinalMessage), len(message))
	}
}

func TestBuildRunResultErrorsWhenNoAssistantMessage(t *testing.T) {
	result, err := buildRunResult([]byte(`{"type":"tool","content":"not assistant"}
`), nil)
	if err == nil {
		t.Fatal("buildRunResult() error = nil, want missing assistant error")
	}
	if result.ErrorMessage != missingAssistantMessageError {
		t.Fatalf("ErrorMessage = %q, want %q", result.ErrorMessage, missingAssistantMessageError)
	}
}

func TestResetClearsSessionContinuity(t *testing.T) {
	spec := domain.AgentSpec{
		Name:          "Implementer",
		Backend:       "kimi",
		StartupPrompt: "Implement carefully.",
	}
	errText := "old error"
	state := domain.AgentState{
		Name:             "Implementer",
		Backend:          "kimi",
		StartupPrompt:    "Implement carefully.",
		WorkspaceDir:     t.TempDir(),
		BackendSessionID: "kimi_old",
		Status:           domain.AgentFailed,
		LastRunID:        "run_000123",
		LastError:        &errText,
	}

	reset, err := New(spec).Reset(context.Background(), spec, state)
	if err != nil {
		t.Fatal(err)
	}
	if reset.BackendSessionID != "" {
		t.Fatalf("BackendSessionID = %q, want empty", reset.BackendSessionID)
	}
	if reset.LastRunID != "" {
		t.Fatalf("LastRunID = %q, want empty", reset.LastRunID)
	}
	if reset.LastError != nil {
		t.Fatalf("LastError = %v, want nil", reset.LastError)
	}
	if reset.Status != domain.AgentIdle {
		t.Fatalf("Status = %q, want %q", reset.Status, domain.AgentIdle)
	}
}

func TestBuildArgsOmitsYoloInPromptMode(t *testing.T) {
	args := buildArgs("hello")
	if containsString(args, "--yolo") {
		t.Fatalf("args = %#v, want no --yolo because Kimi prompt mode rejects it", args)
	}
}

func TestBuildArgsOmitsYoloWhenDisabled(t *testing.T) {
	args := buildArgs("hello")
	if containsString(args, "--yolo") {
		t.Fatalf("args = %#v, want no --yolo", args)
	}
}

func TestSendIncludesStartupPromptEveryRun(t *testing.T) {
	script, promptPath := kimiCommandScript(t, `{"type":"assistant","message":{"content":"done"}}`)
	spec := domain.AgentSpec{
		Name:          "Implementer",
		Backend:       "kimi",
		StartupPrompt: "Implement directly.",
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{
		Name:         "Implementer",
		WorkspaceDir: t.TempDir(),
		LastRunID:    "run_previous",
	}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_2",
		Agent:   "Implementer",
		Message: "Fix the failing test.",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}

	gotPrompt := readTextFile(t, promptPath)
	wantPrompt := "Startup prompt:\nImplement directly.\n\nFacilitator message:\nFix the failing test."
	if gotPrompt != wantPrompt {
		t.Fatalf("prompt = %q, want %q", gotPrompt, wantPrompt)
	}
}

func TestSendOmitsYoloInPromptMode(t *testing.T) {
	script, _, argvPath := kimiCommandScriptWithArgv(t, `{"type":"assistant","message":{"content":"done"}}`)
	spec := domain.AgentSpec{
		Name:          "Advocat",
		Backend:       "kimi",
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{Name: "Advocat", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Advocat",
		Message: "hello",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
	if args := readTextLines(t, argvPath); containsString(args, "--yolo") {
		t.Fatalf("argv = %#v, want no --yolo because Kimi prompt mode rejects it", args)
	}
}

func TestSendOmitsYoloWhenDisabled(t *testing.T) {
	script, _, argvPath := kimiCommandScriptWithArgv(t, `{"type":"assistant","message":{"content":"done"}}`)
	disabled := false
	spec := domain.AgentSpec{
		Name:          "Advocat",
		Backend:       "kimi",
		Yolo:          &disabled,
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{Name: "Advocat", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Advocat",
		Message: "hello",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
	if args := readTextLines(t, argvPath); containsString(args, "--yolo") {
		t.Fatalf("argv = %#v, want no --yolo", args)
	}
}

func TestSendStreamsStdoutAndStderr(t *testing.T) {
	stdoutLines := []string{`{"type":"assistant","message":{"content":"Final answer"}}`}
	script, promptPath := kimiCommandScript(t, strings.Join(stdoutLines, "\n"))
	sink := &recordingSink{}
	spec := domain.AgentSpec{Name: "Advocat", Backend: "kimi", StartupPrompt: "Defend.", StringOptions: map[string]string{"command": script}}
	state := domain.AgentState{Name: "Advocat", WorkspaceDir: t.TempDir()}

	result, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Advocat", Message: "hello"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "Final answer" {
		t.Fatalf("FinalMessage = %q, want Final answer", result.FinalMessage)
	}
	if !slices.Equal(sink.stdout, stdoutLines) {
		t.Fatalf("stdout sink = %#v, want %#v", sink.stdout, stdoutLines)
	}
	if !slices.Equal(sink.stderr, []string{"kimi stderr diagnostic"}) {
		t.Fatalf("stderr sink = %#v, want diagnostic line", sink.stderr)
	}
	if gotPrompt := readTextFile(t, promptPath); !strings.Contains(gotPrompt, "hello") {
		t.Fatalf("prompt = %q, want hello", gotPrompt)
	}
}

func TestProgressTrackerWaitsForAgentToolAndReturnsToRunning(t *testing.T) {
	sink := &recordingSink{}
	tracker := newProgressTracker(sink)

	tracker.handleRootEvent(`{"role":"assistant","tool_calls":[{"id":"tool_agent_1","function":{"name":"Agent"}}]}`, time.Now().UTC())
	progress := sink.lastProgress(t)
	if progress.Phase != domain.RunPhaseWaitingForSubagent {
		t.Fatalf("phase = %q, want %q", progress.Phase, domain.RunPhaseWaitingForSubagent)
	}

	childActivity := time.Now().UTC().Add(time.Second)
	tracker.updateSubagents([]domain.SubagentProgress{{
		ID:             "agent-0",
		ParentID:       "main",
		Status:         "running",
		LastActivityAt: childActivity,
	}})
	progress = sink.lastProgress(t)
	if progress.ChildLastActivityAt == nil || !progress.ChildLastActivityAt.Equal(childActivity) {
		t.Fatalf("child_last_activity_at = %v, want %v", progress.ChildLastActivityAt, childActivity)
	}

	tracker.handleRootEvent(`{"role":"tool","tool_call_id":"tool_agent_1","content":"done"}`, time.Now().UTC().Add(2*time.Second))
	progress = sink.lastProgress(t)
	if progress.Phase != domain.RunPhaseRunning {
		t.Fatalf("phase = %q, want %q", progress.Phase, domain.RunPhaseRunning)
	}
	if progress.Subagents[0].Status != "completed" {
		t.Fatalf("subagent status = %q, want completed", progress.Subagents[0].Status)
	}
}

func kimiCommandScript(t *testing.T, stdout string) (string, string) {
	t.Helper()

	scriptPath, promptPath, _ := kimiCommandScriptWithArgv(t, stdout)
	return scriptPath, promptPath
}

func kimiCommandScriptWithArgv(t *testing.T, stdout string) (string, string, string) {
	t.Helper()

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	argvPath := filepath.Join(dir, "argv.txt")
	scriptPath := filepath.Join(dir, "kimi-command.sh")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s' "$2" > %s
while [ "$#" -gt 0 ]; do
  printf '%%s\n' "$1" >> %s
  shift
done
printf 'kimi stderr diagnostic\n' >&2
cat <<'EOF'
%s
EOF
`, shellQuote(promptPath), shellQuote(argvPath), stdout)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return scriptPath, promptPath, argvPath
}

type recordingSink struct {
	mu       sync.Mutex
	stdout   []string
	stderr   []string
	progress []domain.RunProgress
}

func (s *recordingSink) StdoutLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdout = append(s.stdout, line)
}
func (s *recordingSink) StderrLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stderr = append(s.stderr, line)
}
func (s *recordingSink) Err() error { return nil }
func (s *recordingSink) Progress(progress domain.RunProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = append(s.progress, cloneProgress(progress))
}

func (s *recordingSink) lastProgress(t *testing.T) domain.RunProgress {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.progress) == 0 {
		t.Fatal("no progress was reported")
	}
	return cloneProgress(s.progress[len(s.progress)-1])
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readTextLines(t *testing.T, path string) []string {
	t.Helper()

	text := readTextFile(t, path)
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
