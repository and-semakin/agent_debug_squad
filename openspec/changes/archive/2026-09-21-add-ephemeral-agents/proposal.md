# Proposal: add-ephemeral-agents

## Why

Workflow tasks today always start a fresh backend conversation per attempt, but nothing in the configuration records *that an agent is designed to be one-shot*. The next planned step for workflows is looping control flow, where the same task — and therefore the same agent — is invoked repeatedly. Before loops land, the agent lifecycle intent must be declarable and durable: some agents are meant to be recreated from scratch on every visit so that a later iteration never inherits an earlier iteration's conversation or runtime state.

Making that intent an explicit, persisted, versioned property now means loop scheduling (a separate change) can rely on it instead of inventing per-iteration semantics later, and squad authors can start marking agents accordingly.

## What Changes

- Agent definitions in the squad YAML accept a new optional boolean field `ephemeral` (default `false`), marking the agent as a one-shot execution unit.
- The flag is captured into a workflow execution's immutable saved agent configuration at submission time, participates in the definition hash, and is preserved across recovery — so a replayed request with a changed `ephemeral` value is a changed definition, and a recovered execution keeps its original flag.
- The contract is pinned: every invocation of an ephemeral agent — every task attempt, every retry, and future repeated visits such as loop iterations — runs on a newly created runtime whose backend session starts empty, inheriting no conversation, session identity, or runtime state from any earlier invocation of that agent.
- Scope boundaries, deliberately: in this change the flag is declarative only.
  - It does **not** relax validation: a definition still may reference one agent from at most one task; allowing repeated references to ephemeral agents is reserved for the loops change.
  - It does **not** change dispatch mechanics — every workflow attempt already runs on a fresh owned runtime; for ephemeral agents this becomes a contractual property rather than an incidental one.
  - It does **not** affect facilitator turns: manual conversations with an ephemeral agent keep backend session continuity exactly as before.
  - It does **not** change any HTTP response shape.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `declarative-workflows`: adds a requirement covering the `ephemeral` agent declaration — its configuration surface, persistence in the execution's saved agents and definition hash, the per-invocation recreation contract, and the explicit scope boundaries (validation and facilitator behavior unchanged).

## Impact

- `internal/domain`: `AgentSpec` gains an `Ephemeral` field (JSON `ephemeral`, omitempty), carried into `WorkflowSnapshot.Agents` and `HashWorkflowDefinition` payloads automatically.
- `internal/config`: `rawAgent` parses the new field; `Load` copies it into the resolved spec. No new validation rules beyond type decoding.
- `internal/workflow`, `internal/orchestrator`: no behavioral changes expected; covered by tests asserting the flag's persistence and the unchanged one-reference-per-task rule.
- Documentation: README agent configuration section and one example configuration demonstrate the field.
- Compatibility: purely additive YAML field; agent parsing outside the strict workflow subtree stays non-strict, so older binaries ignore it. Definition hashes are byte-identical when the flag is unset, keeping idempotent replays and recovered snapshots stable.
