package kimi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrey/agent-debug-squad/internal/domain"
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
	})
	if err != nil {
		t.Fatal(err)
	}

	gotPrompt := readTextFile(t, promptPath)
	wantPrompt := "Startup prompt:\nImplement directly.\n\nFacilitator message:\nFix the failing test."
	if gotPrompt != wantPrompt {
		t.Fatalf("prompt = %q, want %q", gotPrompt, wantPrompt)
	}
}

func kimiCommandScript(t *testing.T, stdout string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	scriptPath := filepath.Join(dir, "kimi-command.sh")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s' "$2" > %s
cat <<'EOF'
%s
EOF
`, shellQuote(promptPath), stdout)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return scriptPath, promptPath
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
