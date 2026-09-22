package config

import (
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// validConditionYAML is the canonical conditioned-loop shape: the "refine"
// body runs implement -> review, review is the until_task sink declaring the
// three verdict names, and the outside report consumes the loop's final result.
const validConditionYAML = `workflow:
  version: 1
  name: loop-conditions
  max_parallel: 1
  loops:
    refine:
      max_iterations: 3
      until_task: review
      on_verdict:
        review_passed: break
        issues_found: continue
        needs_human: needs_attention
  tasks:
    implement:
      agent: reviewer_a
      prompt: p
      loop: refine
    review:
      agent: reviewer_b
      prompt: q
      loop: refine
      needs: [implement]
      verdicts:
        review_passed: "clean"
        issues_found: "issues remain"
        needs_human: "escalate"
    report:
      agent: verifier
      prompt: r
      needs: [review]
`

func TestLoadParsesLoopConditionFields(t *testing.T) {
	cfg, err := Load(writeWorkflowConfig(t, validConditionYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	loop := cfg.Workflow.Loops["refine"]
	if loop.UntilTask != "review" {
		t.Fatalf("until_task: %q", loop.UntilTask)
	}
	if !loop.HasCondition() {
		t.Fatal("conditioned loop must report HasCondition")
	}
	if got := loop.OnVerdict; len(got) != 3 ||
		got["review_passed"] != domain.WorkflowLoopActionBreak ||
		got["issues_found"] != domain.WorkflowLoopActionContinue ||
		got["needs_human"] != domain.WorkflowLoopActionNeedsAttention {
		t.Fatalf("on_verdict parse: %+v", got)
	}
	if loop.OnExhaustion != "" || loop.EffectiveOnExhaustion() != domain.WorkflowExhaustionNeedsAttention {
		t.Fatalf("omitted on_exhaustion must default to needs_attention: %q", loop.OnExhaustion)
	}
}

func TestLoadAcceptsValidLoopConditions(t *testing.T) {
	cases := []struct {
		name     string
		workflow string
	}{
		{
			name:     "explicit succeed exhaustion",
			workflow: strings.Replace(validConditionYAML, "      until_task: review\n", "      on_exhaustion: succeed\n      until_task: review\n", 1),
		},
		{
			name:     "explicit needs_attention exhaustion",
			workflow: strings.Replace(validConditionYAML, "      until_task: review\n", "      on_exhaustion: needs_attention\n      until_task: review\n", 1),
		},
		{
			name: "aggregate transitive sink",
			workflow: `workflow:
  version: 1
  name: aggregate
  max_parallel: 1
  loops:
    refine:
      max_iterations: 3
      until_task: review
      on_verdict:
        review_passed: break
        issues_found: continue
  tasks:
    seed:
      agent: reviewer_a
      prompt: p
      loop: refine
    implement:
      agent: reviewer_b
      prompt: p
      loop: refine
      needs: [seed]
    review:
      agent: verifier
      prompt: q
      loop: refine
      needs: [implement]
      verdicts:
        review_passed: "clean"
        issues_found: "issues"
`,
		},
		{
			name: "singleton body sink",
			workflow: `workflow:
  version: 1
  name: singleton
  max_parallel: 1
  loops:
    refine:
      max_iterations: 3
      until_task: review
      on_verdict:
        review_passed: break
        issues_found: continue
  tasks:
    review:
      agent: reviewer_a
      prompt: q
      loop: refine
      verdicts:
        review_passed: "clean"
        issues_found: "issues"
`,
		},
		{
			name: "optional sibling reviewer stays tolerated",
			workflow: `workflow:
  version: 1
  name: optional-sibling
  max_parallel: 1
  loops:
    refine:
      max_iterations: 3
      until_task: review
      on_verdict:
        review_passed: break
        issues_found: continue
  tasks:
    implement:
      agent: reviewer_a
      prompt: p
      loop: refine
    second_review:
      agent: reviewer_b
      prompt: p
      loop: refine
      allowed_to_fail: true
      needs: [implement]
    review:
      agent: verifier
      prompt: q
      loop: refine
      needs: [implement, second_review]
      verdicts:
        review_passed: "clean"
        issues_found: "issues"
`,
		},
		{
			name:     "explicit information-request verdict mapping",
			workflow: strings.Replace(validConditionYAML, "needs_human: needs_attention", "needs_human: needs_attention", 1),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeWorkflowConfig(t, tc.workflow)); err != nil {
				t.Fatalf("valid conditions must load: %v", err)
			}
		})
	}
}

func TestLoadRejectsInvalidLoopConditions(t *testing.T) {
	cases := []struct {
		name      string
		workflow  string
		wantParts []string
	}{
		{
			name:      "until_task without on_verdict",
			workflow:  strings.Replace(validConditionYAML, "      on_verdict:\n        review_passed: break\n        issues_found: continue\n        needs_human: needs_attention\n", "", 1),
			wantParts: []string{`loop "refine"`, `until_task "review"`, "without on_verdict", "on_verdict"},
		},
		{
			name:      "on_verdict without until_task",
			workflow:  strings.Replace(validConditionYAML, "      until_task: review\n", "", 1),
			wantParts: []string{`loop "refine"`, "on_verdict without until_task"},
		},
		{
			name:      "on_exhaustion without a condition",
			workflow:  strings.Replace(validLoopYAML, "      max_iterations: 3\n", "      max_iterations: 3\n      on_exhaustion: succeed\n", 1),
			wantParts: []string{`loop "refine"`, "on_exhaustion without a condition"},
		},
		{
			name:      "until_task not a body member",
			workflow:  strings.Replace(validConditionYAML, "      until_task: review\n", "      until_task: report\n", 1),
			wantParts: []string{`loop "refine"`, `until_task "report"`, "member of the loop body"},
		},
		{
			name:      "until_task without declared verdicts",
			workflow:  strings.Replace(validConditionYAML, "      until_task: review\n", "      until_task: implement\n", 1),
			wantParts: []string{`loop "refine"`, `until_task "implement"`, "must declare verdicts"},
		},
		{
			name:      "until_task mandatory",
			workflow:  strings.Replace(validConditionYAML, "      loop: refine\n      needs: [implement]\n      verdicts:", "      loop: refine\n      needs: [implement]\n      allowed_to_fail: true\n      verdicts:", 1),
			wantParts: []string{`loop "refine"`, `until_task "review"`, "must not set allowed_to_fail", "mandatory"},
		},
		{
			name:      "on_verdict omits a declared verdict",
			workflow:  strings.Replace(validConditionYAML, "        needs_human: needs_attention\n", "", 1),
			wantParts: []string{`loop "refine"`, `omits the declared verdict "needs_human"`},
		},
		{
			name:      "on_verdict maps an unknown verdict",
			workflow:  strings.Replace(validConditionYAML, "        review_passed: break\n", "        review_passed: break\n        nonexistent: continue\n", 1),
			wantParts: []string{`loop "refine"`, `maps verdict "nonexistent"`, "does not declare"},
		},
		{
			name:      "on_verdict invalid action",
			workflow:  strings.Replace(validConditionYAML, "        review_passed: break\n", "        review_passed: explode\n", 1),
			wantParts: []string{`loop "refine"`, `maps verdict "review_passed"`, "must be break, continue, or needs_attention"},
		},
		{
			name:      "on_exhaustion invalid value",
			workflow:  strings.Replace(validConditionYAML, "      until_task: review\n", "      on_exhaustion: fail\n      until_task: review\n", 1),
			wantParts: []string{`loop "refine"`, "on_exhaustion must be", "got \"fail\""},
		},
		{
			name: "uncovered body branch",
			workflow: `workflow:
  version: 1
  name: uncovered
  max_parallel: 1
  loops:
    refine:
      max_iterations: 3
      until_task: review
      on_verdict:
        review_passed: break
        issues_found: continue
  tasks:
    implement:
      agent: reviewer_a
      prompt: p
      loop: refine
    side:
      agent: reviewer_b
      prompt: p
      loop: refine
    review:
      agent: verifier
      prompt: q
      loop: refine
      needs: [implement]
      verdicts:
        review_passed: "clean"
        issues_found: "issues"
`,
			wantParts: []string{`loop "refine"`, "unique body sink", `task "side"`, "does not reach"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeWorkflowConfig(t, tc.workflow))
			if err == nil {
				t.Fatalf("expected an error containing %v, got nil", tc.wantParts)
			}
			for _, part := range tc.wantParts {
				if !strings.Contains(err.Error(), part) {
					t.Fatalf("error %q does not contain %q", err.Error(), part)
				}
			}
		})
	}
}

func TestLoadRejectsHoldUncertaintyPolicy(t *testing.T) {
	path := writeWorkflowConfig(t, strings.Replace(validConditionYAML, "  max_parallel: 1\n", "  max_parallel: 1\n  on_uncertain: hold\n", 1))
	_, err := Load(path)
	if err == nil {
		t.Fatal("the retired hold spelling must be rejected")
	}
	if !strings.Contains(err.Error(), "needs_attention") || !strings.Contains(err.Error(), "no longer supported") {
		t.Fatalf("hold rejection must name the replacement policy, got %v", err)
	}
}
