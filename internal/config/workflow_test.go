package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

const workflowAgentsYAML = `
session_name: review
workspace_dir: .
agents:
  - name: reviewer_a
    backend: fake
    startup_prompt: "Review independently."
  - name: reviewer_b
    backend: fake
    startup_prompt: "Review independently."
  - name: verifier
    backend: fake
    startup_prompt: "Verify findings."
`

const defaultWorkflowYAML = `workflow:
  version: 1
  name: review-and-verify
  max_parallel: 2
  task_timeout_seconds: 1800
  tasks:
    review_a:
      agent: reviewer_a
      prompt: "Review the diff."
      allowed_to_fail: true
    review_b:
      agent: reviewer_b
      prompt: "Review the diff."
      allowed_to_fail: true
    verify:
      agent: verifier
      needs: [review_a, review_b]
      min_successful_dependencies: 1
      prompt: "Validate findings."
`

func writeWorkflowConfig(t *testing.T, workflowSection string) string {
	t.Helper()
	if workflowSection == "" {
		workflowSection = defaultWorkflowYAML
	}
	body := workflowAgentsYAML + workflowSection
	path := filepath.Join(t.TempDir(), "squad.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadParsesWorkflowDefinition(t *testing.T) {
	cfg, err := Load(writeWorkflowConfig(t, ""))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Workflow == nil {
		t.Fatal("workflow must be parsed")
	}
	def := *cfg.Workflow
	if def.Version != 1 || def.Name != "review-and-verify" || def.MaxParallel != 2 || def.TaskTimeoutSeconds != 1800 {
		t.Fatalf("workflow fields mismatch: %+v", def)
	}
	if len(def.Tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d", len(def.Tasks))
	}
	verify := def.Tasks["verify"]
	if verify.MinSuccessfulDependencies != 1 || len(verify.Needs) != 2 {
		t.Fatalf("verify task mismatch: %+v", verify)
	}
	if !def.Tasks["review_a"].AllowedToFail || def.Tasks["verify"].AllowedToFail {
		t.Fatal("allowed_to_fail mismatch")
	}
	if def.Tasks["review_a"].TimeoutSeconds != 0 {
		t.Fatalf("task timeout override must default to zero, got %d", def.Tasks["review_a"].TimeoutSeconds)
	}
}

func TestLoadAppliesWorkflowDefaults(t *testing.T) {
	path := writeWorkflowConfig(t, `workflow:
  version: 1
  name: minimal
  max_parallel: 1
  tasks:
    solo:
      agent: reviewer_a
      prompt: "Go."
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Workflow.TaskTimeoutSeconds != domain.DefaultWorkflowTaskTimeoutSec {
		t.Fatalf("task_timeout_seconds must default to %d, got %d", domain.DefaultWorkflowTaskTimeoutSec, cfg.Workflow.TaskTimeoutSeconds)
	}
	if cfg.Workflow.Tasks["solo"].Needs != nil {
		t.Fatalf("needs must default to empty, got %v", cfg.Workflow.Tasks["solo"].Needs)
	}
}

func TestLoadWithoutWorkflowKeepsManualBehavior(t *testing.T) {
	path := writeWorkflowConfig(t, "not-a-workflow-section\n")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	trimmed := body[:strings.Index(string(body), "not-a-workflow-section")]
	path = filepath.Join(t.TempDir(), "squad.yaml")
	if err := os.WriteFile(path, trimmed, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Workflow != nil {
		t.Fatal("workflow must stay nil when absent")
	}
}

func TestLoadRejectsMalformedWorkflowValues(t *testing.T) {
	cases := []struct {
		name     string
		workflow string
		wantErr  string
	}{
		{"wrong version", "workflow:\n  version: 2\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n", "version must be 1"},
		{"missing version", "workflow:\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n", "version must be 1"},
		{"empty name", "workflow:\n  version: 1\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n", "name is required"},
		{"zero max_parallel", "workflow:\n  version: 1\n  name: x\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n", "max_parallel"},
		{"negative max_parallel", "workflow:\n  version: 1\n  name: x\n  max_parallel: -1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n", "max_parallel"},
		{"empty tasks", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks: {}\n", "tasks must not be empty"},
		{"zero timeout", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  task_timeout_seconds: 0\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n", "task_timeout_seconds must be a positive integer"},
		{"negative task timeout override", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n      timeout_seconds: -5\n", "timeout_seconds"},
		{"empty prompt", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: \"\"\n", "prompt is required"},
		{"unknown workflow field", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  concurrency: 4\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n", "field concurrency"},
		{"unknown task field", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n      retries: 3\n", "field retries"},
		{"duplicate task key", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n    a:\n      agent: reviewer_b\n      prompt: q\n", "already defined"},
		{"duplicate workflow key at root", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\nworkflow:\n  version: 1\n  name: y\n  max_parallel: 1\n  tasks:\n    b:\n      agent: reviewer_b\n      prompt: q\n", "already defined"},
		{"workflow not a mapping", "workflow: 7\n", "workflow must be a mapping"},
		{"unknown agent", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: ghost\n      prompt: p\n", "unknown agent"},
		{"duplicate agent reference", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n    b:\n      agent: reviewer_a\n      prompt: q\n", "distinct agent names"},
		{"self dependency", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n      needs: [a]\n", "depends on itself"},
		{"unknown dependency", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n      needs: [ghost]\n", "unknown dependency"},
		{"repeated dependency entry", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n    b:\n      agent: reviewer_b\n      prompt: q\n      needs: [a, a]\n", "repeats dependency"},
		{"threshold above dependency count", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n    b:\n      agent: reviewer_b\n      prompt: q\n      needs: [a]\n      min_successful_dependencies: 2\n", "min_successful_dependencies"},
		{"negative threshold", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n      min_successful_dependencies: -1\n", "min_successful_dependencies"},
		{"cycle", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    a:\n      agent: reviewer_a\n      prompt: p\n      needs: [b]\n    b:\n      agent: reviewer_b\n      prompt: q\n      needs: [a]\n", "cycle"},
		{"unsafe task id slash", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    \"a/b\":\n      agent: reviewer_a\n      prompt: p\n", "unsafe workflow task identifier"},
		{"unsafe task id dotdot", "workflow:\n  version: 1\n  name: x\n  max_parallel: 1\n  tasks:\n    \"..\":\n      agent: reviewer_a\n      prompt: p\n", "unsafe workflow task identifier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeWorkflowConfig(t, tc.workflow)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLoadAcceptsIdenticalModelsUnderDistinctAgents(t *testing.T) {
	path := writeWorkflowConfig(t, `workflow:
  version: 1
  name: same-model
  max_parallel: 2
  tasks:
    first:
      agent: reviewer_a
      prompt: p
    second:
      agent: reviewer_b
      prompt: q
      needs: [first]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("identical model configurations must be accepted: %v", err)
	}
	if cfg.Workflow == nil || len(cfg.Workflow.Tasks) != 2 {
		t.Fatalf("workflow mismatch: %+v", cfg.Workflow)
	}
}

// The ephemeral flag declares lifecycle intent only; in this version it must
// not relax the one-reference-per-task graph rule.
func TestLoadStillRejectsRepeatedEphemeralAgentReference(t *testing.T) {
	agents := strings.Replace(workflowAgentsYAML, `- name: reviewer_a
    backend: fake
    startup_prompt: "Review independently."`, `- name: reviewer_a
    backend: fake
    startup_prompt: "Review independently."
    ephemeral: true`, 1)
	body := agents + `workflow:
  version: 1
  name: twice-ephemeral
  max_parallel: 1
  tasks:
    first:
      agent: reviewer_a
      prompt: p
    second:
      agent: reviewer_a
      prompt: q
      needs: [first]
`
	path := filepath.Join(t.TempDir(), "squad.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("a repeated ephemeral agent reference must still be rejected")
	}
	if !strings.Contains(err.Error(), "distinct agent names") {
		t.Fatalf("error %q does not contain the reused-agent explanation", err.Error())
	}
}

func TestValidateWorkflowDefinitionAcceptsChainsAndDiamonds(t *testing.T) {
	agents := []domain.AgentSpec{
		{Name: "a1", Backend: "fake"},
		{Name: "a2", Backend: "fake"},
		{Name: "a3", Backend: "fake"},
		{Name: "a4", Backend: "fake"},
	}
	chain := domain.WorkflowDefinition{
		Version: 1, Name: "chain", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "p"},
			"b": {Agent: "a2", Prompt: "p", Needs: []string{"a"}},
			"c": {Agent: "a3", Prompt: "p", Needs: []string{"b"}},
		},
	}
	if err := ValidateWorkflowDefinition(chain, agents); err != nil {
		t.Fatalf("chain must validate: %v", err)
	}
	diamond := domain.WorkflowDefinition{
		Version: 1, Name: "diamond", MaxParallel: 2, TaskTimeoutSeconds: 60,
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "p"},
			"b": {Agent: "a2", Prompt: "p", Needs: []string{"a"}},
			"c": {Agent: "a3", Prompt: "p", Needs: []string{"a"}},
			"d": {Agent: "a4", Prompt: "p", Needs: []string{"b", "c"}},
		},
	}
	if err := ValidateWorkflowDefinition(diamond, agents); err != nil {
		t.Fatalf("diamond must validate: %v", err)
	}
}

const validLoopYAML = `workflow:
  version: 1
  name: loop-review
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
    report:
      agent: verifier
      prompt: r
      needs: [review]
`

func TestLoadParsesLoopDefinitions(t *testing.T) {
	cfg, err := Load(writeWorkflowConfig(t, validLoopYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	def := *cfg.Workflow
	if len(def.Loops) != 1 || def.Loops["refine"].MaxIterations != 3 {
		t.Fatalf("loops mismatch: %+v", def.Loops)
	}
	if def.Tasks["implement"].Loop != "refine" || def.Tasks["review"].Loop != "refine" {
		t.Fatalf("task loop labels mismatch: %+v", def.Tasks)
	}
	if def.Tasks["report"].Loop != "" {
		t.Fatalf("outside task must stay unlabeled: %+v", def.Tasks["report"])
	}
}

func TestLoadAcceptsSiblingLoopsAndOutsideDependencies(t *testing.T) {
	agents := []domain.AgentSpec{
		{Name: "a1", Backend: "fake"}, {Name: "a2", Backend: "fake"},
		{Name: "a3", Backend: "fake"}, {Name: "a4", Backend: "fake"},
		{Name: "a5", Backend: "fake"},
	}
	siblings := domain.WorkflowDefinition{
		Version: 1, Name: "siblings", MaxParallel: 2, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"left":  {MaxIterations: 2},
			"right": {MaxIterations: 3},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"seed":   {Agent: "a1", Prompt: "p"},
			"l1":     {Agent: "a2", Prompt: "p", Loop: "left", Needs: []string{"seed"}},
			"r1":     {Agent: "a3", Prompt: "p", Loop: "right", Needs: []string{"seed"}},
			"r2":     {Agent: "a4", Prompt: "p", Loop: "right", Needs: []string{"r1"}},
			"report": {Agent: "a5", Prompt: "p", Needs: []string{"l1", "r2"}},
		},
	}
	if err := ValidateWorkflowDefinition(siblings, agents); err != nil {
		t.Fatalf("sibling loops with outside seed and consumer must validate: %v", err)
	}
}

func TestLoadRejectsInvalidLoopDefinitions(t *testing.T) {
	cases := []struct {
		name      string
		workflow  string
		wantParts []string
	}{
		{
			name:      "missing max_iterations",
			workflow:  strings.Replace(validLoopYAML, "      max_iterations: 3\n", "      {}\n", 1),
			wantParts: []string{`loop "refine"`, "max_iterations is required", "example:", "max_iterations: 3"},
		},
		{
			name:      "zero max_iterations",
			workflow:  strings.Replace(validLoopYAML, "max_iterations: 3", "max_iterations: 0", 1),
			wantParts: []string{`loop "refine"`, "positive integer", "example:"},
		},
		{
			name:      "negative max_iterations",
			workflow:  strings.Replace(validLoopYAML, "max_iterations: 3", "max_iterations: -2", 1),
			wantParts: []string{`loop "refine"`, "positive integer", "example:"},
		},
		{
			name:      "unknown loop field",
			workflow:  strings.Replace(validLoopYAML, "      max_iterations: 3\n", "      max_iterations: 3\n      until: perfect\n", 1),
			wantParts: []string{"field until"},
		},
		{
			name:      "undeclared loop reference",
			workflow:  strings.Replace(validLoopYAML, "loop: refine", "loop: forever", 1),
			wantParts: []string{`loop "forever"`, `referenced by task "implement"`, "example:"},
		},
		{
			name:      "empty loop body",
			workflow:  strings.ReplaceAll(validLoopYAML, "      loop: refine\n", ""),
			wantParts: []string{`loop "refine"`, "has no member tasks", "example:"},
		},
		{
			name: "cross-loop dependency",
			workflow: `workflow:
  version: 1
  name: crossed
  max_parallel: 1
  loops:
    left:
      max_iterations: 2
    right:
      max_iterations: 2
  tasks:
    l1:
      agent: reviewer_a
      prompt: p
      loop: left
    r1:
      agent: reviewer_b
      prompt: q
      loop: right
      needs: [l1]
`,
			wantParts: []string{`loop "right"`, "depends on task", "outside all loops", "example:"},
		},
		{
			name: "cycle through loop boundary",
			workflow: `workflow:
  version: 1
  name: boundary-cycle
  max_parallel: 1
  loops:
    refine:
      max_iterations: 2
  tasks:
    implement:
      agent: reviewer_a
      prompt: p
      loop: refine
      needs: [report]
    review:
      agent: reviewer_b
      prompt: q
      loop: refine
      needs: [implement]
    report:
      agent: verifier
      prompt: r
      needs: [review]
`,
			wantParts: []string{`loop "refine"`, "cycle through the loop boundary", "example:"},
		},
		{
			name:      "unsafe loop name",
			workflow:  strings.Replace(validLoopYAML, "    refine:\n      max_iterations: 3", "    \"bad/name\":\n      max_iterations: 3", 1),
			wantParts: []string{"unsafe loop name", "example:"},
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

func TestValidateRejectsBodyInternalCycleWithTaskError(t *testing.T) {
	agents := []domain.AgentSpec{{Name: "a1", Backend: "fake"}, {Name: "a2", Backend: "fake"}}
	def := domain.WorkflowDefinition{
		Version: 1, Name: "self-cycle", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{"refine": {MaxIterations: 2}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "p", Loop: "refine", Needs: []string{"b"}},
			"b": {Agent: "a2", Prompt: "p", Loop: "refine", Needs: []string{"a"}},
		},
	}
	err := ValidateWorkflowDefinition(def, agents)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("body-internal cycle must be rejected as a cycle, got %v", err)
	}
}

func TestLoadExistingExampleConfigsStillLoad(t *testing.T) {
	for _, name := range []string{
		"../../examples/squad.yaml",
		"../../examples/zcode-squad.yaml",
		"../../examples/cursor-squad.yaml",
		"../../examples/workflow-chain.yaml",
		"../../examples/workflow-review.yaml",
		"../../examples/workflow-loop.yaml",
		"../../examples/workflow-loop-conditions.yaml",
		"../../examples/workflow-nested-loop.yaml",
		"../../configs/code-review-squad.yaml",
	} {
		if _, err := Load(name); err != nil {
			t.Fatalf("%s must still load: %v", name, err)
		}
	}
}
