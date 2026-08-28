package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestBuildEnvOnlyIncludesWhitelistedVariables(t *testing.T) {
	spec := domain.AgentSpec{
		ListOptions: map[string][]string{
			"inherit_env": {"OPENAI_API_KEY", "CODEX_HOME", "MISSING"},
		},
	}
	environ := []string{
		"OPENAI_API_KEY=secret",
		"CODEX_HOME=/tmp/codex",
		"SHOULD_NOT_PASS=leak",
	}

	got := BuildEnv(spec, environ)

	want := []string{"OPENAI_API_KEY=secret", "CODEX_HOME=/tmp/codex"}
	if len(got) != len(want) {
		t.Fatalf("BuildEnv() len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BuildEnv()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, item := range got {
		if item == "SHOULD_NOT_PASS=leak" {
			t.Fatalf("BuildEnv() included non-whitelisted variable: %#v", got)
		}
	}
}

func TestBuildEnvIncludesExplicitEnvOptions(t *testing.T) {
	spec := domain.AgentSpec{
		ListOptions: map[string][]string{
			"inherit_env": {"PATH"},
			"env": {
				"HTTP_PROXY=http://proxy.example:3128",
				"NO_PROXY=127.0.0.1,localhost",
			},
		},
	}
	environ := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HTTP_PROXY=http://ambient-proxy.example:3128",
	}

	got := BuildEnv(spec, environ)

	want := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.example:3128",
		"NO_PROXY=127.0.0.1,localhost",
	}
	if len(got) != len(want) {
		t.Fatalf("BuildEnv() len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BuildEnv()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResetClearsSessionContinuity(t *testing.T) {
	spec := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "codex",
		StartupPrompt: "Review carefully.",
		StringOptions: map[string]string{"model": "openai/gpt-5"},
	}
	errText := "old error"
	state := domain.AgentState{
		Name:             "Reviewer",
		Backend:          "codex",
		Model:            "old-model",
		StartupPrompt:    "Review carefully.",
		WorkspaceDir:     t.TempDir(),
		BackendSessionID: "thread_old",
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
	if reset.Model != "openai/gpt-5" {
		t.Fatalf("Model = %q, want openai/gpt-5", reset.Model)
	}
}

func TestParseJSONLFindsCompletionAndFinalMessage(t *testing.T) {
	data := []byte(`{"type":"item.completed","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first"}]}}
{"type":"item.completed","item":{"type":"message","role":"assistant","text":"last"}}
{"type":"turn.completed"}`)

	got, err := ParseJSONL(data)
	if err != nil {
		t.Fatalf("ParseJSONL() error = %v", err)
	}
	if !got.Completed {
		t.Fatal("ParseJSONL().Completed = false, want true")
	}
	if got.FinalMessage != "last" {
		t.Fatalf("ParseJSONL().FinalMessage = %q, want %q", got.FinalMessage, "last")
	}
}

func TestParseJSONLDetectsFailure(t *testing.T) {
	data := []byte(`{"type":"turn.failed","error":{"message":"boom"}}`)

	got, err := ParseJSONL(data)
	if err != nil {
		t.Fatalf("ParseJSONL() error = %v", err)
	}
	if !got.Failed {
		t.Fatal("ParseJSONL().Failed = false, want true")
	}
	if got.ErrorMessage != "boom" {
		t.Fatalf("ParseJSONL().ErrorMessage = %q, want %q", got.ErrorMessage, "boom")
	}
}

func TestParseJSONLCapturesBackendSessionID(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "thread_id",
			data: `{"type":"thread.started","thread_id":"thread_123"}`,
			want: "thread_123",
		},
		{
			name: "nested_thread_id",
			data: `{"type":"thread.started","thread":{"id":"thread_456"}}`,
			want: "thread_456",
		},
		{
			name: "session_id",
			data: `{"type":"session.created","session_id":"session_789"}`,
			want: "session_789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseJSONL([]byte(tt.data))
			if err != nil {
				t.Fatalf("ParseJSONL() error = %v", err)
			}
			if got.BackendSessionID != tt.want {
				t.Fatalf("BackendSessionID = %q, want %q", got.BackendSessionID, tt.want)
			}
		})
	}
}

func TestParseJSONLHandlesLargeAssistantEvent(t *testing.T) {
	message := strings.Repeat("x", 70*1024)
	data := []byte(`{"type":"item.completed","item":{"type":"message","role":"assistant","text":"` + message + `"}}
{"type":"turn.completed"}`)

	got, err := ParseJSONL(data)
	if err != nil {
		t.Fatalf("ParseJSONL() error = %v", err)
	}
	if !got.Completed {
		t.Fatal("ParseJSONL().Completed = false, want true")
	}
	if got.FinalMessage != message {
		t.Fatalf("ParseJSONL().FinalMessage len = %d, want %d", len(got.FinalMessage), len(message))
	}
}

func TestBuildArgsAddsYoloFlagByDefault(t *testing.T) {
	spec := domain.AgentSpec{Name: "Reviewer", Backend: "codex"}
	state := domain.AgentState{}
	args := buildArgs(spec, state, "hello", true)
	if !containsString(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("args = %#v, want yolo flag", args)
	}
}

func TestBuildArgsOmitsYoloFlagWhenDisabled(t *testing.T) {
	spec := domain.AgentSpec{Name: "Reviewer", Backend: "codex"}
	disabled := false
	spec.Yolo = &disabled
	state := domain.AgentState{}
	args := buildArgs(spec, state, "hello", false)
	if containsString(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("args = %#v, want no yolo flag", args)
	}
}

func TestBuildArgsAddsModelWhenConfigured(t *testing.T) {
	spec := domain.AgentSpec{
		Name:    "Reviewer",
		Backend: "codex",
		StringOptions: map[string]string{
			"model": "gpt-5.5",
		},
	}

	args := buildArgs(spec, domain.AgentState{}, "hello", false)

	if !containsString(args, "--model") || !containsString(args, "gpt-5.5") {
		t.Fatalf("args = %#v, want --model gpt-5.5", args)
	}
}

func TestBuildArgsAddsReasoningWhenConfigured(t *testing.T) {
	spec := domain.AgentSpec{
		Name:    "Reviewer",
		Backend: "codex",
		StringOptions: map[string]string{
			"reasoning": "medium",
		},
	}

	args := buildArgs(spec, domain.AgentState{}, "hello", false)

	if !containsString(args, "-c") || !containsString(args, `model_reasoning_effort=medium`) {
		t.Fatalf("args = %#v, want reasoning config override", args)
	}
}

func TestBuildArgsPlacesModelAndReasoningBeforeResumeAndPrompt(t *testing.T) {
	spec := domain.AgentSpec{
		Name:    "Reviewer",
		Backend: "codex",
		StringOptions: map[string]string{
			"model":     "gpt-5.3-codex",
			"reasoning": "high",
		},
	}
	state := domain.AgentState{BackendSessionID: "thread_123"}

	args := buildArgs(spec, state, "hello", false)

	want := []string{
		"exec",
		"--json",
		"--model",
		"gpt-5.3-codex",
		"-c",
		`model_reasoning_effort=high`,
		"resume",
		"thread_123",
		"hello",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSendIncludesStartupPromptOnFirstRun(t *testing.T) {
	script, promptPath := codexCommandScript(t, `{"type":"turn.completed"}`)
	spec := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "codex",
		StartupPrompt: "Review carefully.",
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{
		Name:         "Reviewer",
		WorkspaceDir: t.TempDir(),
	}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Reviewer",
		Message: "Please inspect this diff.",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}

	gotPrompt := readTextFile(t, promptPath)
	wantPrompt := "Startup prompt:\nReview carefully.\n\nFacilitator message:\nPlease inspect this diff."
	if gotPrompt != wantPrompt {
		t.Fatalf("prompt = %q, want %q", gotPrompt, wantPrompt)
	}
}

func TestSendDoesNotRepeatStartupPromptAfterFirstRun(t *testing.T) {
	script, promptPath := codexCommandScript(t, `{"type":"turn.completed"}`)
	spec := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "codex",
		StartupPrompt: "Review carefully.",
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{
		Name:             "Reviewer",
		WorkspaceDir:     t.TempDir(),
		LastRunID:        "run_1",
		BackendSessionID: "thread_123",
	}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_2",
		Agent:   "Reviewer",
		Message: "Second turn.",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}

	gotPrompt := readTextFile(t, promptPath)
	if gotPrompt != "Second turn." {
		t.Fatalf("prompt = %q, want second turn only", gotPrompt)
	}
}

func TestSendStoresBackendSessionIDFromThreadStarted(t *testing.T) {
	script, _ := codexCommandScript(t, `{"type":"thread.started","thread_id":"thread_abc"}
{"type":"turn.completed"}`)
	spec := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "codex",
		StartupPrompt: "Review carefully.",
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{
		Name:         "Reviewer",
		WorkspaceDir: t.TempDir(),
	}

	_, nextState, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Reviewer",
		Message: "Start.",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
	if nextState.BackendSessionID != "thread_abc" {
		t.Fatalf("BackendSessionID = %q, want thread_abc", nextState.BackendSessionID)
	}
}

func TestSendAddsYoloFlagByDefault(t *testing.T) {
	script, _, argvPath := codexCommandScriptWithArgv(t, `{"type":"turn.completed"}`)
	spec := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "codex",
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{Name: "Reviewer", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Reviewer",
		Message: "hello",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
	if args := readTextLines(t, argvPath); !containsString(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("argv = %#v, want yolo flag", args)
	}
}

func TestSendOmitsYoloFlagWhenDisabled(t *testing.T) {
	script, _, argvPath := codexCommandScriptWithArgv(t, `{"type":"turn.completed"}`)
	disabled := false
	spec := domain.AgentSpec{
		Name:          "Reviewer",
		Backend:       "codex",
		Yolo:          &disabled,
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{Name: "Reviewer", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Reviewer",
		Message: "hello",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
	if args := readTextLines(t, argvPath); containsString(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("argv = %#v, want no yolo flag", args)
	}
}

func TestSendIncludesModelAndReasoningArgs(t *testing.T) {
	script, _, argvPath := codexCommandScriptWithArgv(t, `{"type":"turn.completed"}`)
	spec := domain.AgentSpec{
		Name:    "Reviewer",
		Backend: "codex",
		StringOptions: map[string]string{
			"command":   script,
			"model":     "gpt-5.3-codex",
			"reasoning": "medium",
		},
	}
	state := domain.AgentState{Name: "Reviewer", WorkspaceDir: t.TempDir()}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID:   "run_1",
		Agent:   "Reviewer",
		Message: "hello",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"exec",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"--model",
		"gpt-5.3-codex",
		"-c",
		`model_reasoning_effort=medium`,
	}
	if args := readTextLines(t, argvPath); !slices.Equal(args, want) {
		t.Fatalf("argv = %#v, want %#v", args, want)
	}
}

func TestSendStreamsStdoutAndStderr(t *testing.T) {
	stdoutLines := []string{
		`{"type":"item.completed","item":{"type":"message","role":"assistant","text":"done"}}`,
		`{"type":"turn.completed"}`,
	}
	script, promptPath := codexCommandScript(t, strings.Join(stdoutLines, "\n"))
	sink := &recordingSink{}
	spec := domain.AgentSpec{Name: "Reviewer", Backend: "codex", StartupPrompt: "Review.", StringOptions: map[string]string{"command": script}}
	state := domain.AgentState{Name: "Reviewer", WorkspaceDir: t.TempDir()}

	result, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Reviewer", Message: "hello"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "done" {
		t.Fatalf("FinalMessage = %q, want done", result.FinalMessage)
	}
	if !slices.Equal(sink.stdout, stdoutLines) {
		t.Fatalf("stdout sink = %#v, want %#v", sink.stdout, stdoutLines)
	}
	if !slices.Equal(sink.stderr, []string{"codex stderr diagnostic"}) {
		t.Fatalf("stderr sink = %#v, want diagnostic line", sink.stderr)
	}
	if gotPrompt := readTextFile(t, promptPath); !strings.Contains(gotPrompt, "hello") {
		t.Fatalf("prompt = %q, want hello", gotPrompt)
	}
}

func TestRunCommandStreamingKillsProcessOnScannerError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCodexLargeLineHelperProcess", "--")
	cmd.Env = append(os.Environ(), "CODEX_TEST_LARGE_LINE=1")

	started := time.Now()
	_, _, err := runCommandStreaming(ctx, cmd, domain.DiscardRunSink())
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("runCommandStreaming() error = nil, want scanner error")
	}
	if !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("runCommandStreaming() error = %v, want token too long", err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("runCommandStreaming() elapsed = %s, want process killed promptly after scanner error", elapsed)
	}
}

func TestCodexLargeLineHelperProcess(t *testing.T) {
	if os.Getenv("CODEX_TEST_LARGE_LINE") != "1" {
		return
	}
	chunk := strings.Repeat("x", 1024)
	for i := 0; i < maxJSONLEventSize/len(chunk)+1024; i++ {
		if _, err := os.Stdout.WriteString(chunk); err != nil {
			os.Exit(0)
		}
	}
	time.Sleep(10 * time.Second)
	os.Exit(0)
}

func TestBuildRunResultParsesStdoutOnlyAndPreservesStderr(t *testing.T) {
	stdout := []byte(`{"type":"item.completed","item":{"type":"message","role":"assistant","text":"done"}}
{"type":"turn.completed"}`)
	stderr := []byte("warning: noisy stderr\n")

	got, err := buildRunResult(stdout, stderr, nil)
	if err != nil {
		t.Fatalf("buildRunResult() error = %v", err)
	}
	if got.FinalMessage != "done" {
		t.Fatalf("buildRunResult().FinalMessage = %q, want %q", got.FinalMessage, "done")
	}
	if got.Stderr != string(stderr) {
		t.Fatalf("buildRunResult().Stderr = %q, want %q", got.Stderr, string(stderr))
	}
}

func TestBuildRunResultNonZeroExitPreservesStderrAndExecError(t *testing.T) {
	execErr := errors.New("exit status 1")
	stderr := []byte("boom on stderr\n")

	got, err := buildRunResult(nil, stderr, execErr)
	if !errors.Is(err, execErr) {
		t.Fatalf("buildRunResult() error = %v, want %v", err, execErr)
	}
	if got.Stderr != string(stderr) {
		t.Fatalf("buildRunResult().Stderr = %q, want %q", got.Stderr, string(stderr))
	}
	if got.ErrorMessage != execErr.Error() {
		t.Fatalf("buildRunResult().ErrorMessage = %q, want %q", got.ErrorMessage, execErr.Error())
	}
}

func TestBuildRunResultTurnFailedPreservesStderrAndParsedError(t *testing.T) {
	stdout := []byte(`{"type":"turn.failed","error":{"message":"boom"}}`)
	stderr := []byte("diagnostic stderr\n")

	got, err := buildRunResult(stdout, stderr, nil)
	if err == nil {
		t.Fatal("buildRunResult() error = nil, want turn failed error")
	}
	if got.Stderr != string(stderr) {
		t.Fatalf("buildRunResult().Stderr = %q, want %q", got.Stderr, string(stderr))
	}
	if got.ErrorMessage != "boom" {
		t.Fatalf("buildRunResult().ErrorMessage = %q, want %q", got.ErrorMessage, "boom")
	}
}

func TestBuildRunResultTurnFailedDefaultsMissingMessage(t *testing.T) {
	stdout := []byte(`{"type":"turn.failed"}`)

	got, err := buildRunResult(stdout, nil, nil)
	if err == nil {
		t.Fatal("buildRunResult() error = nil, want turn failed error")
	}
	if got.ErrorMessage != "codex turn failed" {
		t.Fatalf("buildRunResult().ErrorMessage = %q, want %q", got.ErrorMessage, "codex turn failed")
	}
}

func TestBuildRunResultErrorsWhenTurnDoesNotComplete(t *testing.T) {
	stdout := []byte(`{"type":"item.completed","item":{"type":"message","role":"assistant","text":"almost"}}`)

	got, err := buildRunResult(stdout, nil, nil)
	if err == nil {
		t.Fatal("buildRunResult() error = nil, want incomplete turn error")
	}
	if got.ErrorMessage != "codex turn did not complete" {
		t.Fatalf("buildRunResult().ErrorMessage = %q, want %q", got.ErrorMessage, "codex turn did not complete")
	}
}

func TestBuildRunResultNonZeroExitUsesParsedTurnFailedError(t *testing.T) {
	execErr := errors.New("exit status 1")
	stdout := []byte(`{"type":"turn.failed","error":{"message":"parsed boom"}}`)
	stderr := []byte("diagnostic stderr\n")

	got, err := buildRunResult(stdout, stderr, execErr)
	if err == nil {
		t.Fatal("buildRunResult() error = nil, want turn failed error")
	}
	if got.ErrorMessage != "parsed boom" {
		t.Fatalf("buildRunResult().ErrorMessage = %q, want %q", got.ErrorMessage, "parsed boom")
	}
	if got.Stderr != string(stderr) {
		t.Fatalf("buildRunResult().Stderr = %q, want %q", got.Stderr, string(stderr))
	}
	if len(got.RawEvents) != 1 {
		t.Fatalf("buildRunResult().RawEvents len = %d, want 1", len(got.RawEvents))
	}
}

func codexCommandScript(t *testing.T, stdout string) (string, string) {
	t.Helper()

	scriptPath, promptPath, _ := codexCommandScriptWithArgv(t, stdout)
	return scriptPath, promptPath
}

func codexCommandScriptWithArgv(t *testing.T, stdout string) (string, string, string) {
	t.Helper()

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	argvPath := filepath.Join(dir, "argv.txt")
	scriptPath := filepath.Join(dir, "codex-command.sh")
	script := fmt.Sprintf(`#!/bin/sh
while [ "$#" -gt 1 ]; do
  printf '%%s\n' "$1" >> %s
  shift
done
printf '%%s' "$1" > %s
printf 'codex stderr diagnostic\n' >&2
cat <<'EOF'
%s
EOF
`, shellQuote(argvPath), shellQuote(promptPath), stdout)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return scriptPath, promptPath, argvPath
}

type recordingSink struct {
	stdout []string
	stderr []string
}

func (s *recordingSink) StdoutLine(line string) { s.stdout = append(s.stdout, line) }
func (s *recordingSink) StderrLine(line string) { s.stderr = append(s.stderr, line) }
func (s *recordingSink) Err() error             { return nil }

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
