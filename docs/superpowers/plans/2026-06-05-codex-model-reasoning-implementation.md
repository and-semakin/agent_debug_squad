# Codex Model And Reasoning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-agent Codex model and reasoning selection through YAML options.

**Architecture:** Keep YAML parsing generic because string options are already supported. Extend only the Codex adapter argument builder to translate `options.model` and `options.reasoning` into Codex CLI arguments. Document the Codex-specific options and demonstrate them in the bundled code-review config.

**Tech Stack:** Go, standard library tests, Codex CLI argument construction, YAML configuration.

---

## File Structure

- Modify `internal/adapters/codex/codex_test.go`: add focused argument builder tests before changing production code.
- Modify `internal/adapters/codex/codex.go`: add `--model` and `-c model_reasoning_effort=...` arguments when options are present.
- Modify `README.md`: document Codex `model` and `reasoning` YAML options.
- Modify `configs/code-review-squad.yaml`: demonstrate model/reasoning options for Codex agents.

### Task 1: Codex Argument Tests

**Files:**
- Modify: `internal/adapters/codex/codex_test.go`
- Test: `internal/adapters/codex/codex_test.go`

- [ ] **Step 1: Write failing tests**

Add these tests near the existing `TestBuildArgs...` tests:

```go
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
	if !containsString(args, "-c") || !containsString(args, `model_reasoning_effort='"medium"'`) {
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
		`model_reasoning_effort='"high"'`,
		"resume",
		"thread_123",
		"hello",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}
```

- [ ] **Step 2: Run tests to verify red**

Run:

```sh
go test ./internal/adapters/codex
```

Expected: FAIL because `buildArgs` does not yet add `--model` or `model_reasoning_effort`.

### Task 2: Codex Argument Implementation

**Files:**
- Modify: `internal/adapters/codex/codex.go`
- Test: `internal/adapters/codex/codex_test.go`

- [ ] **Step 1: Implement minimal argument additions**

Change `buildArgs` so it appends model and reasoning options after YOLO and before resume:

```go
func buildArgs(spec domain.AgentSpec, state domain.AgentState, message string, yolo bool) []string {
	args := []string{"exec", "--json"}
	if yolo {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if model := spec.StringOptions["model"]; model != "" {
		args = append(args, "--model", model)
	}
	if reasoning := spec.StringOptions["reasoning"]; reasoning != "" {
		args = append(args, "-c", `model_reasoning_effort='"`+reasoning+`"'`)
	}
	if state.BackendSessionID != "" {
		args = append(args, "resume", state.BackendSessionID)
	}
	args = append(args, message)
	return args
}
```

- [ ] **Step 2: Run Codex adapter tests**

Run:

```sh
go test ./internal/adapters/codex
```

Expected: PASS.

### Task 3: Documentation And Example Config

**Files:**
- Modify: `README.md`
- Modify: `configs/code-review-squad.yaml`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Document Codex options**

Extend the Codex environment section in `README.md` with this YAML:

```yaml
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review the proposed fix and identify risks.
    options:
      command: codex
      model: gpt-5.5
      reasoning: medium
      inherit_env:
        - OPENAI_API_KEY
        - CODEX_HOME
        - PATH
```

Add a short note that `reasoning` maps to Codex `model_reasoning_effort`, and omitted keys fall back to Codex defaults.

- [ ] **Step 2: Update project config example**

Add model/reasoning options to the `Facilitator` and `Implementer` Codex agents in `configs/code-review-squad.yaml`:

```yaml
      model: gpt-5.5
      reasoning: high
```

Use `reasoning: medium` for `Implementer` if lower cost/speed is preferred.

- [ ] **Step 3: Run config tests**

Run:

```sh
go test ./internal/config
```

Expected: PASS. Existing config tests should continue to parse the bundled config.

### Task 4: Full Verification

**Files:**
- Test: all Go packages

- [ ] **Step 1: Run full Go test suite**

Run:

```sh
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Review git diff**

Run:

```sh
git diff -- docs/superpowers/specs/2026-06-05-codex-model-reasoning-design.md docs/superpowers/plans/2026-06-05-codex-model-reasoning-implementation.md internal/adapters/codex/codex.go internal/adapters/codex/codex_test.go README.md configs/code-review-squad.yaml
```

Expected: diff is limited to the model/reasoning feature, tests, and documentation.
