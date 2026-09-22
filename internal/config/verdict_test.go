package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func writeVerdictConfig(t *testing.T, workflowSection, judgeSection string) string {
	t.Helper()
	body := workflowAgentsYAML + workflowSection + judgeSection
	path := filepath.Join(t.TempDir(), "squad.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadParsesVerdictSettings(t *testing.T) {
	path := writeVerdictConfig(t, `workflow:
  version: 1
  name: judged
  max_parallel: 1
  confidence_threshold: 0.9
  on_uncertain: error
  tasks:
    review:
      agent: reviewer_a
      prompt: "Review the diff."
      verdicts:
        review_passed: "No further iteration needed"
        issues_found:
        error: "The agent failed the task"
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	def := cfg.Workflow
	if def.ConfidenceThreshold != 0.9 || def.OnUncertain != "error" {
		t.Fatalf("verdict settings mismatch: %+v", def)
	}
	verdicts := def.Tasks["review"].Verdicts
	if verdicts["review_passed"] != "No further iteration needed" {
		t.Fatalf("verdict description mismatch: %v", verdicts)
	}
	if verdicts["issues_found"] != "" {
		t.Fatalf("empty description expected: %q", verdicts["issues_found"])
	}
	if len(verdicts) != 3 {
		t.Fatalf("want 3 verdicts, got %d", len(verdicts))
	}
	if def.EffectiveConfidenceThreshold() != 0.9 || def.EffectiveOnUncertain() != domain.WorkflowOnUncertainError {
		t.Fatalf("effective values mismatch: %v %v", def.EffectiveConfidenceThreshold(), def.EffectiveOnUncertain())
	}
}

func TestVerdictSettingsDefaultWhenUnset(t *testing.T) {
	path := writeVerdictConfig(t, `workflow:
  version: 1
  name: judged
  max_parallel: 1
  tasks:
    review:
      agent: reviewer_a
      prompt: "Review the diff."
      verdicts:
        ok: "fine"
        retry: "again"
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	def := cfg.Workflow
	if def.ConfidenceThreshold != 0 {
		t.Fatalf("unset threshold must stay zero for hash stability, got %v", def.ConfidenceThreshold)
	}
	if def.OnUncertain != "" {
		t.Fatalf("unset on_uncertain must stay empty for hash stability, got %q", def.OnUncertain)
	}
	if def.EffectiveConfidenceThreshold() != domain.DefaultConfidenceThreshold {
		t.Fatalf("effective threshold: %v", def.EffectiveConfidenceThreshold())
	}
	if def.EffectiveOnUncertain() != domain.WorkflowOnUncertainHold {
		t.Fatalf("effective on_uncertain: %q", def.EffectiveOnUncertain())
	}
}

func TestLoadRejectsInvalidVerdictMaps(t *testing.T) {
	cases := []struct {
		name    string
		verdict string
		wantErr string
	}{
		{"empty map", "verdicts: {}", "at least two"},
		{"single verdict", "verdicts:\n        only: \"x\"", "at least two"},
		{"unsafe name", "verdicts:\n        ok: \"x\"\n        \"not safe\": \"y\"", "unsafe verdict name"},
		{"reserved name", "verdicts:\n        ok: \"x\"\n        uncertain: \"y\"", "reserved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeVerdictConfig(t, "workflow:\n  version: 1\n  name: judged\n  max_parallel: 1\n  tasks:\n    review:\n      agent: reviewer_a\n      prompt: \"Review the diff.\"\n      "+tc.verdict+"\n", "")
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLoadRejectsInvalidUncertaintySettings(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		wantErr string
	}{
		{"threshold above one", "confidence_threshold: 1.5", "confidence_threshold"},
		{"threshold negative", "confidence_threshold: -0.5", "confidence_threshold"},
		{"unknown policy", "on_uncertain: explode", "on_uncertain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeVerdictConfig(t, "workflow:\n  version: 1\n  name: judged\n  max_parallel: 1\n  "+tc.header+"\n  tasks:\n    review:\n      agent: reviewer_a\n      prompt: \"Review.\"\n      verdicts:\n        ok: \"\"\n        retry: \"\"\n", "")
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLoadRejectsUnknownWorkflowVerdictFields(t *testing.T) {
	path := writeVerdictConfig(t, `workflow:
  version: 1
  name: judged
  max_parallel: 1
  tasks:
    review:
      agent: reviewer_a
      prompt: "Review."
      verdicts:
        ok: ""
        retry: ""
      verdict_threshold: 0.9
`, "")
	if _, err := Load(path); err == nil {
		t.Fatal("unknown task field inside the strict workflow subtree must be rejected")
	}
}

func TestLoadParsesJudgeSection(t *testing.T) {
	path := writeVerdictConfig(t, "", `judge:
  provider: openrouter
  model: "typesafe/jev-1.13"
  api_key_file: /tmp/keys/openrouter
  proxy_url: "http://127.0.0.1:7890"
  timeout_seconds: 45
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	jc := cfg.Judge
	if jc == nil {
		t.Fatal("judge section must parse")
	}
	if jc.Provider != "openrouter" || jc.Model != "typesafe/jev-1.13" ||
		jc.APIKeyFile != "/tmp/keys/openrouter" || jc.ProxyURL != "http://127.0.0.1:7890" || jc.TimeoutSeconds != 45 {
		t.Fatalf("judge config mismatch: %+v", jc)
	}
}

func TestLoadJudgeSectionOptional(t *testing.T) {
	cfg, err := Load(writeVerdictConfig(t, "", ""))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Judge != nil {
		t.Fatalf("judge must be nil without a section, got %+v", cfg.Judge)
	}
}

func TestLoadRejectsInvalidJudgeTimeout(t *testing.T) {
	path := writeVerdictConfig(t, "", "judge:\n  timeout_seconds: 0\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected timeout_seconds validation error")
	}
}
