package kimi

import (
	"strings"
	"testing"
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
