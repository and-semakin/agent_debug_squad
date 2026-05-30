package codex

import (
	"errors"
	"strings"
	"testing"

	"github.com/andrey/agent-debug-squad/internal/domain"
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
	if err != nil {
		t.Fatalf("buildRunResult() error = %v", err)
	}
	if got.Stderr != string(stderr) {
		t.Fatalf("buildRunResult().Stderr = %q, want %q", got.Stderr, string(stderr))
	}
	if got.ErrorMessage != "boom" {
		t.Fatalf("buildRunResult().ErrorMessage = %q, want %q", got.ErrorMessage, "boom")
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
	if err != nil {
		t.Fatalf("buildRunResult() error = %v", err)
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
