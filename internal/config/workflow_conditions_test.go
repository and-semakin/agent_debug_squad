package config

import (
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"strings"
	"testing"
)

const validConditionYAML = `workflow:
  version: 2
  name: task-controls
  max_parallel: 1
  loops:
    refine:
      max_iterations: 3
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
      verdicts: {clean: Clean, issues: Issues, human: Escalate}
      control: {clean: break, issues: continue, human: needs_attention}
    report:
      agent: verifier
      prompt: r
      needs: [review]
`

func TestTaskControlParsing(t *testing.T) {
	for _, action := range []string{"proceed", "break", "continue", "needs_attention"} {
		cfg, err := Load(writeWorkflowConfig(t, strings.Replace(validConditionYAML, "clean: break", "clean: "+action, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Workflow.Tasks["review"].Control["clean"] != action {
			t.Fatal("action lost")
		}
		if cfg.Workflow.Loops["refine"].EffectiveOnExhaustion() != domain.WorkflowExhaustionNeedsAttention {
			t.Fatal("wrong default exhaustion")
		}
	}
}

func TestTaskControlStrictContract(t *testing.T) {
	cases := []struct{ name, old, new, want string }{
		{"v1 loops", "version: 2", "version: 1", "version: 1 loops"},
		{"removed before version", "version: 2", "version: 1\n  loops: {other: {max_iterations: 1, until_task: x}}", "already defined"},
		{"null", "control: {clean: break, issues: continue, human: needs_attention}", "control: null", "nonempty"},
		{"empty", "control: {clean: break, issues: continue, human: needs_attention}", "control: {}", "nonempty"},
		{"list", "control: {clean: break, issues: continue, human: needs_attention}", "control: [break]", "nonempty"},
		{"number", "clean: break", "clean: 7", "strings"},
		{"bool", "clean: break", "clean: true", "strings"},
		{"duplicate", "clean: break", "clean: break, clean: proceed", "duplicate"},
		{"unknown", "clean: break", "clean: explode", "requires proceed"},
		{"missing", "clean: break, ", "", "requires proceed"},
		{"extra", "clean: break", "clean: break, uncertain: break", "undeclared"},
		{"optional", "      control:", "      allowed_to_fail: true\n      control:", "allowed_to_fail"},
		{"unowned", "      loop: refine\n      needs: [implement]", "      needs: [implement]", "direct loop owner"},
		{"no verdicts", "      verdicts: {clean: Clean, issues: Issues, human: Escalate}\n", "", "at least two"},
		{"bad exhaustion", "      max_iterations: 3", "      max_iterations: 3\n      on_exhaustion: fail", "on_exhaustion"},
		{"removed empty", "      max_iterations: 3", "      max_iterations: 3\n      until_task: null", "removed"},
		{"removed map", "      max_iterations: 3", "      max_iterations: 3\n      on_verdict: {}", "removed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(validConditionYAML, tc.old, tc.new, 1)
			_, err := Load(writeWorkflowConfig(t, input))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	for _, field := range []string{"until_task: review", "on_verdict: {clean: break}"} {
		input := strings.Replace(validConditionYAML, "version: 2", "version: 1", 1)
		input = strings.Replace(input, "max_iterations: 3", "max_iterations: 3\n      "+field, 1)
		_, err := Load(writeWorkflowConfig(t, input))
		if err == nil || !strings.Contains(err.Error(), "removed") {
			t.Fatalf("removed diagnostic must precede version error: %v", err)
		}
	}
}

func TestControlBarrierPositions(t *testing.T) {
	for _, position := range []string{"head", "middle", "tail", "singleton", "bypass", "unordered"} {
		t.Run(position, func(t *testing.T) {
			d := domain.WorkflowDefinition{Version: 2, Name: "barrier", MaxParallel: 3, TaskTimeoutSeconds: 10, Loops: map[string]domain.WorkflowLoopDefinition{"l": {MaxIterations: 2}}, Tasks: map[string]domain.WorkflowTaskDefinition{"c": {Agent: "a1", Prompt: "p", Loop: "l", Verdicts: map[string]string{"yes": "yes", "no": "no"}, Control: map[string]string{"yes": "proceed", "no": "break"}}}}
			c := d.Tasks["c"]
			if position != "singleton" {
				d.Tasks["a"] = domain.WorkflowTaskDefinition{Agent: "a2", Prompt: "p", Loop: "l"}
				d.Tasks["b"] = domain.WorkflowTaskDefinition{Agent: "a3", Prompt: "p", Loop: "l"}
				a, b := d.Tasks["a"], d.Tasks["b"]
				switch position {
				case "head":
					a.Needs = []string{"c"}
					b.Needs = []string{"c"}
				case "middle":
					c.Needs = []string{"a"}
					b.Needs = []string{"c"}
				case "tail":
					c.Needs = []string{"a", "b"}
				case "bypass":
					c.Needs = []string{"a"}
				case "unordered":
					b.Verdicts = c.Verdicts
					b.Control = c.Control
				}
				d.Tasks["a"], d.Tasks["b"], d.Tasks["c"] = a, b, c
			}
			err := ValidateWorkflowDefinition(d, nestedAgents(3))
			invalid := position == "bypass" || position == "unordered"
			if (err != nil) != invalid {
				t.Fatalf("%s: %v", position, err)
			}
			if invalid && !strings.Contains(err.Error(), "incomparable") {
				t.Fatal(err)
			}
		})
	}
}

func TestLoadRejectsHoldUncertaintyPolicy(t *testing.T) {
	_, err := Load(writeWorkflowConfig(t, strings.Replace(validConditionYAML, "  max_parallel: 1\n", "  max_parallel: 1\n  on_uncertain: hold\n", 1)))
	if err == nil || !strings.Contains(err.Error(), "no longer supported") {
		t.Fatalf("retired policy: %v", err)
	}
}
