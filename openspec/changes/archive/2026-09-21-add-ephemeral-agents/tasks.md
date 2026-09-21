# Tasks: add-ephemeral-agents

## 1. Configuration field

- [x] 1.1 Add `Ephemeral bool` to `domain.AgentSpec` (tags `json:"ephemeral,omitempty"` and `yaml:"-"`), add `Ephemeral bool` to `config.rawAgent`, and copy it in `config.Load`. Verify with config tests: `ephemeral: true` parses to true, omitted or `false` parses to false, and a non-boolean value fails loading with an actionable error.
- [x] 1.2 Assert hash stability in a `HashWorkflowDefinition` test: agents without the flag produce the same hash as before the field existed (pin a golden hash), and flipping `ephemeral` on one referenced agent changes the hash.

## 2. Execution carriage and replay

- [x] 2.1 In `internal/workflow` tests, submit a workflow referencing an `ephemeral: true` agent and verify the created snapshot's saved agents record the flag, and that reloading the snapshot (recovery path) retains it.
- [x] 2.2 Verify replay semantics: resubmitting the same request ID with the flag unchanged replays idempotently, while resubmitting after flipping the flag returns `ErrDefinitionChanged` (surfaced as 409 by the API layer unchanged).
- [x] 2.3 Add a round-trip test through `NormalizeAgentOptions` and `agentSpecWithDefaults` (the dispatch/recovery revalidation path) asserting `Ephemeral` survives both, and that a dispatch of an ephemeral agent still receives a fresh owned runtime (existing fresh-session-per-attempt behavior).

## 3. Scope boundaries

- [x] 3.1 Add a validation test asserting a definition that references one ephemeral agent from two tasks is still rejected with the reused-agent explanation.
- [x] 3.2 Confirm facilitator behavior is untouched: the existing manual-turn session-continuity and reset tests pass unchanged, and `GET /agents` / workflow view response shapes are unchanged.

## 4. Documentation and examples

- [x] 4.1 Document `ephemeral` in README.md's agent configuration section: workflow-scoped one-shot semantics, persistence with the execution, replay-hash sensitivity, and the explicit note that graph validation and facilitator turns do not change in this version.
- [x] 4.2 Mark one reviewer agent `ephemeral: true` in an existing workflow example (e.g. `examples/workflow-review.yaml`) with a short comment explaining the intent.

## 5. Final checks

- [x] 5.1 Run `gofmt` on changed Go files, `go vet ./...`, and `go test -race -count=1 ./...`; all green.
- [x] 5.2 Run `openspec validate add-ephemeral-agents --strict` and walk every delta scenario against the implementation and tests before archiving.
