package codex

import (
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
