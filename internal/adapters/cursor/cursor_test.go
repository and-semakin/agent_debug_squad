package cursor

import (
	"context"
	"encoding/json"
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

func TestParseStreamJSONFindsSuccessSessionAndFinalMessage(t *testing.T) {
	data := []byte(`{"type":"system","subtype":"init","session_id":"cursor_123","model":"Composer 2.5"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial"}]},"session_id":"cursor_123"}
{"type":"result","subtype":"success","is_error":false,"result":"Final answer","session_id":"cursor_123"}`)

	got, err := ParseStreamJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Completed || got.Failed {
		t.Fatalf("result = %#v, want completed success", got)
	}
	if got.BackendSessionID != "cursor_123" {
		t.Fatalf("BackendSessionID = %q, want cursor_123", got.BackendSessionID)
	}
	if got.FinalMessage != "Final answer" {
		t.Fatalf("FinalMessage = %q, want Final answer", got.FinalMessage)
	}
	if len(got.RawEvents) != 3 {
		t.Fatalf("RawEvents len = %d, want 3", len(got.RawEvents))
	}
}

func TestParseStreamJSONUsesAssistantDeltasAsFallback(t *testing.T) {
	data := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"world"}]}}
{"type":"result","subtype":"success","is_error":false,"session_id":"cursor_123"}`)

	got, err := ParseStreamJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalMessage != "Hello world" {
		t.Fatalf("FinalMessage = %q, want Hello world", got.FinalMessage)
	}
}

func TestParseStreamJSONPrefersAggregateAssistantFallbackOverPartialDeltas(t *testing.T) {
	data := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "}]},"timestamp_ms":123}
{"type":"assistant","message":{"content":[{"type":"text","text":"world"}]},"timestamp_ms":124}
{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}
{"type":"result","subtype":"success","is_error":false,"session_id":"cursor_123"}`)

	got, err := ParseStreamJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalMessage != "Hello world" {
		t.Fatalf("FinalMessage = %q, want Hello world", got.FinalMessage)
	}
}

func TestParseStreamJSONDetectsTerminalError(t *testing.T) {
	data := []byte(`{"type":"result","subtype":"error","is_error":true,"result":"model unavailable","session_id":"cursor_123"}`)

	got, err := ParseStreamJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Failed || got.Completed {
		t.Fatalf("result = %#v, want failed terminal result", got)
	}
	if got.ErrorMessage != "model unavailable" {
		t.Fatalf("ErrorMessage = %q, want model unavailable", got.ErrorMessage)
	}
}

func TestParseStreamJSONReturnsMalformedJSONError(t *testing.T) {
	_, err := ParseStreamJSON([]byte(`{"type":"result"`))
	if err == nil {
		t.Fatal("ParseStreamJSON() error = nil, want malformed JSON error")
	}
}

func TestParseStreamJSONHandlesLargeResultEvent(t *testing.T) {
	message := strings.Repeat("x", 70*1024)
	data := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"` + message + `"}`)

	got, err := ParseStreamJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalMessage != message {
		t.Fatalf("FinalMessage len = %d, want %d", len(got.FinalMessage), len(message))
	}
}

func TestBuildRunResultErrorsWithoutTerminalResult(t *testing.T) {
	stdout := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"almost"}]}}`)

	got, err := buildRunResult(stdout, nil, nil)
	if err == nil {
		t.Fatal("buildRunResult() error = nil, want incomplete result error")
	}
	if got.ErrorMessage != incompleteTurnError {
		t.Fatalf("ErrorMessage = %q, want %q", got.ErrorMessage, incompleteTurnError)
	}
}

func TestBuildRunResultPreservesExecErrorAndStderr(t *testing.T) {
	execErr := errors.New("exit status 1")
	stderr := []byte("authentication failed\n")

	got, err := buildRunResult(nil, stderr, execErr)
	if !errors.Is(err, execErr) {
		t.Fatalf("error = %v, want %v", err, execErr)
	}
	if got.Stderr != string(stderr) || got.ErrorMessage != execErr.Error() {
		t.Fatalf("result = %#v, want preserved stderr and exec error", got)
	}
}

func TestBuildArgsIncludesConfiguredOptionsAndResume(t *testing.T) {
	spec := domain.AgentSpec{
		StringOptions: map[string]string{
			"model":   "composer-2.5",
			"mode":    "ask",
			"sandbox": "enabled",
		},
	}
	state := domain.AgentState{BackendSessionID: "cursor_123"}

	got := buildArgs(spec, state, "review", true)
	want := []string{
		"--print",
		"--trust",
		"--output-format", "stream-json",
		"--stream-partial-output",
		"--force",
		"--model", "composer-2.5",
		"--mode", "ask",
		"--sandbox", "enabled",
		"--resume", "cursor_123",
		"review",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildArgsOmitsOptionalFlags(t *testing.T) {
	got := buildArgs(domain.AgentSpec{}, domain.AgentState{}, "review", false)
	want := []string{
		"--print",
		"--trust",
		"--output-format", "stream-json",
		"--stream-partial-output",
		"review",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestReportInvocationDiagnosticRedactsPromptAndResumeSession(t *testing.T) {
	sink := &recordingSink{}
	reportInvocationDiagnostic(sink, "/usr/local/bin/cursor-agent", []string{
		"--print", "--force", "--resume", "cursor_secret_session", "secret prompt",
	}, "/workspace")

	if len(sink.diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one record", sink.diagnostics)
	}
	if strings.Contains(sink.diagnostics[0], "secret prompt") || strings.Contains(sink.diagnostics[0], "cursor_secret_session") {
		t.Fatalf("diagnostic leaked prompt or session: %s", sink.diagnostics[0])
	}
	var diagnostic invocationDiagnostic
	if err := json.Unmarshal([]byte(sink.diagnostics[0]), &diagnostic); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--print", "--force", "--resume", "<redacted>"}
	if !slices.Equal(diagnostic.Args, wantArgs) {
		t.Fatalf("diagnostic args = %#v, want %#v", diagnostic.Args, wantArgs)
	}
}

func TestBuildEnvOnlyIncludesExplicitAndInheritedValues(t *testing.T) {
	spec := domain.AgentSpec{ListOptions: map[string][]string{
		"inherit_env": {"PATH", "HOME", "CURSOR_API_KEY", "HTTP_PROXY", "MISSING"},
		"env":         {"HTTP_PROXY=http://cursor-proxy.example:3128", "HTTPS_PROXY=http://cursor-proxy.example:3128"},
	}}
	environ := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HOME=/Users/tester",
		"CURSOR_API_KEY=secret",
		"HTTP_PROXY=http://ambient.example:3128",
		"SHOULD_NOT_PASS=leak",
	}

	got := BuildEnv(spec, environ)
	want := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HOME=/Users/tester",
		"CURSOR_API_KEY=secret",
		"HTTP_PROXY=http://ambient.example:3128",
		"HTTP_PROXY=http://cursor-proxy.example:3128",
		"HTTPS_PROXY=http://cursor-proxy.example:3128",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("env = %#v, want %#v", got, want)
	}
	if slices.Contains(got, "SHOULD_NOT_PASS=leak") {
		t.Fatalf("env unexpectedly contains ambient variable: %#v", got)
	}
}

func TestResetClearsCursorContinuity(t *testing.T) {
	spec := domain.AgentSpec{
		Name:          "CursorCritic",
		Backend:       "cursor",
		StartupPrompt: "Review only.",
		StringOptions: map[string]string{"model": "composer-2.5"},
	}
	errText := "old error"
	state := domain.AgentState{
		Name:             spec.Name,
		Backend:          spec.Backend,
		Model:            "old-model",
		StartupPrompt:    spec.StartupPrompt,
		WorkspaceDir:     t.TempDir(),
		BackendSessionID: "cursor_old",
		Status:           domain.AgentFailed,
		LastRunID:        "run_old",
		LastError:        &errText,
	}

	got, err := New(spec).Reset(context.Background(), spec, state)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendSessionID != "" || got.LastRunID != "" || got.LastError != nil {
		t.Fatalf("reset state = %#v, want cleared continuity", got)
	}
	if got.Model != "composer-2.5" || got.Status != domain.AgentIdle {
		t.Fatalf("reset state = %#v, want configured model and idle", got)
	}
}

func TestSendStreamsOutputStoresSessionAndWrapsFirstPrompt(t *testing.T) {
	stdout := `{"type":"system","subtype":"init","session_id":"cursor_abc"}
{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"cursor_abc"}`
	script, promptPath, argvPath := cursorCommandScript(t, stdout, 0)
	sink := &recordingSink{}
	spec := domain.AgentSpec{
		Name:          "CursorCritic",
		Backend:       "cursor",
		StartupPrompt: "Review only.",
		StringOptions: map[string]string{
			"command": script,
			"model":   "composer-2.5",
			"mode":    "ask",
		},
	}
	state := domain.AgentState{Name: spec.Name, WorkspaceDir: t.TempDir()}

	result, next, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID: "run_1", Agent: spec.Name, Message: "Inspect the repository.",
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "done" || next.BackendSessionID != "cursor_abc" {
		t.Fatalf("result = %#v state = %#v", result, next)
	}
	wantPrompt := "Startup prompt:\nReview only.\n\nFacilitator message:\nInspect the repository."
	if got := readTextFile(t, promptPath); got != wantPrompt {
		t.Fatalf("prompt = %q, want %q", got, wantPrompt)
	}
	args := readTextLines(t, argvPath)
	if !slices.Contains(args, "--model") || !slices.Contains(args, "composer-2.5") || !slices.Contains(args, "--mode") || !slices.Contains(args, "ask") {
		t.Fatalf("argv = %#v, want model and ask mode", args)
	}
	if !slices.Equal(sink.stderr, []string{"cursor stderr diagnostic"}) {
		t.Fatalf("stderr = %#v, want diagnostic", sink.stderr)
	}
	if len(sink.stdout) != 3 {
		t.Fatalf("stdout lines = %d, want 3", len(sink.stdout))
	}
	if len(sink.diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one invocation record", sink.diagnostics)
	}
	var diagnostic invocationDiagnostic
	if err := json.Unmarshal([]byte(sink.diagnostics[0]), &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic.Type != "adapter_invocation" || diagnostic.Backend != "cursor" || !diagnostic.MessageRedacted {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	if !slices.Contains(diagnostic.Args, "--force") || slices.Contains(diagnostic.Args, "Inspect the repository.") {
		t.Fatalf("diagnostic args = %#v, want --force without prompt", diagnostic.Args)
	}
}

func TestSendResumesAndDoesNotRepeatStartupPrompt(t *testing.T) {
	script, promptPath, argvPath := cursorCommandScript(t, `{"type":"result","subtype":"success","is_error":false,"result":"continued","session_id":"cursor_abc"}`, 0)
	spec := domain.AgentSpec{
		Name:          "CursorCritic",
		Backend:       "cursor",
		StartupPrompt: "Review only.",
		StringOptions: map[string]string{"command": script},
	}
	state := domain.AgentState{
		Name: spec.Name, WorkspaceDir: t.TempDir(), LastRunID: "run_1", BackendSessionID: "cursor_abc",
	}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{
		RunID: "run_2", Agent: spec.Name, Message: "Continue.",
	}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
	if got := readTextFile(t, promptPath); got != "Continue." {
		t.Fatalf("prompt = %q, want Continue.", got)
	}
	args := readTextLines(t, argvPath)
	resumeAt := slices.Index(args, "--resume")
	if resumeAt < 0 || resumeAt+1 >= len(args) || args[resumeAt+1] != "cursor_abc" {
		t.Fatalf("argv = %#v, want --resume cursor_abc", args)
	}
}

func TestSendReturnsCommandFailure(t *testing.T) {
	script, _, _ := cursorCommandScript(t, "", 7)
	spec := domain.AgentSpec{Name: "CursorCritic", Backend: "cursor", StringOptions: map[string]string{"command": script}}
	state := domain.AgentState{Name: spec.Name, WorkspaceDir: t.TempDir()}

	result, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: spec.Name, Message: "hello"}, domain.DiscardRunSink())
	if err == nil {
		t.Fatal("Send() error = nil, want command failure")
	}
	if result.ErrorMessage == "" || !strings.Contains(result.Stderr, "cursor stderr diagnostic") {
		t.Fatalf("result = %#v, want error and stderr", result)
	}
}

func TestRunCommandStreamingKillsProcessOnScannerError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCursorLargeLineHelperProcess", "--")
	cmd.Env = append(os.Environ(), "CURSOR_TEST_LARGE_LINE=1")

	started := time.Now()
	_, _, err := runCommandStreaming(ctx, cmd, domain.DiscardRunSink())
	if err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("error = %v, want token too long", err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("elapsed = %s, want prompt child termination", elapsed)
	}
}

func TestCursorLargeLineHelperProcess(t *testing.T) {
	if os.Getenv("CURSOR_TEST_LARGE_LINE") != "1" {
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

func cursorCommandScript(t *testing.T, stdout string, exitCode int) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	argvPath := filepath.Join(dir, "argv.txt")
	scriptPath := filepath.Join(dir, "cursor-command.sh")
	script := fmt.Sprintf(`#!/bin/sh
while [ "$#" -gt 1 ]; do
  printf '%%s\n' "$1" >> %s
  shift
done
printf '%%s' "$1" > %s
printf 'cursor stderr diagnostic\n' >&2
cat <<'EOF'
%s
EOF
exit %d
`, shellQuote(argvPath), shellQuote(promptPath), stdout, exitCode)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return scriptPath, promptPath, argvPath
}

type recordingSink struct {
	stdout      []string
	stderr      []string
	diagnostics []string
}

func (s *recordingSink) StdoutLine(line string) { s.stdout = append(s.stdout, line) }
func (s *recordingSink) StderrLine(line string) { s.stderr = append(s.stderr, line) }
func (s *recordingSink) DiagnosticLine(line string) {
	s.diagnostics = append(s.diagnostics, line)
}
func (s *recordingSink) Err() error { return nil }

func containsString(items []string, want string) bool {
	return slices.Contains(items, want)
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
	text := strings.TrimSuffix(readTextFile(t, path), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
