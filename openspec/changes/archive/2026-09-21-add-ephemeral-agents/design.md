# Design: add-ephemeral-agents

## Context

Workflow attempts already run on fresh, workflow-owned runtimes: `SubmitOwnedRun` initializes each attempt's adapter with an empty backend identity, and retries get a new attempt record with a new run ID. What does not exist is a *declared, durable* per-agent lifecycle. The validation layer additionally forbids referencing one agent name from two tasks, which is the rule a future loops change will relax for agents whose recreation is contractual.

See `proposal.md` for motivation: this change lands the declaration, not the relaxation.

The data path a workflow execution already uses for agent configuration is the one to ride:

- `internal/config.Load` parses `agents:` entries into `domain.AgentSpec` (non-strict YAML outside the `workflow` subtree; the only strictness comes from field types).
- `resolvedAgents` (internal/workflow/workflow.go) copies the specs referenced by a definition into the execution's saved agents, pinning effective `Yolo` at creation time.
- `HashWorkflowDefinition` marshals `{definition, agents}` to JSON and hashes it; this hash is the replay identity for idempotent submission.
- `savedAgentSpec` → `SubmitOwnedRun(Spec: …)` → `NormalizeAgentOptions` → `agentSpecWithDefaults` revalidates and enriches the saved spec on dispatch and recovery without rebuilding it from scratch.

## Goals / Non-Goals

**Goals:**

- A top-level, typed `ephemeral` field on agent definitions that flows — unchanged — into saved execution state, the replay hash, and recovered executions.
- Zero behavioral delta at dispatch time; the flag is data plus a pinned contract that the loops change will consume.

**Non-Goals:**

- Relaxing the one-task-per-agent validation rule (reserved for loops).
- Any per-iteration, loop, or repeated-visit scheduling semantics.
- Exposing the flag in HTTP responses or agent state projections.
- Any effect on facilitator turns, permission handling, or cancellation paths.

## Decisions

### 1. Top-level agent field, plain bool, `omitempty`

`domain.AgentSpec` gains `Ephemeral bool` with tags `json:"ephemeral,omitempty"` and `yaml:"-"` (YAML decoding goes through `config.rawAgent`, matching every other field). `rawAgent` gains `Ephemeral bool yaml:"ephemeral"`, copied in `Load`.

- *Why top-level and not under `options`:* `options` is the backend-facing bag — it is normalized into `StringOptions`/`ListOptions`, forwarded to adapters, and partially redacted in API responses. `ephemeral` is scheduler lifecycle policy and must never reach an adapter. The `options.yolo` precedent exists because yolo is genuinely a backend execution mode; lifecycle is not.
- *Why plain bool, unlike `Yolo *bool`:* `Yolo` is tri-state because its effective value is resolved against session defaults at creation time. `ephemeral` has a fixed default (false) and nothing to inherit; a pointer would add a nil case with no meaning.
- *Why `omitempty`:* the JSON encoding of `AgentSpec` participates in `HashWorkflowDefinition` and in the persisted snapshot. With `omitempty`, agents that do not set the flag marshal byte-identically to today, so existing executions' hashes, idempotent replays, and recovered snapshots stay stable. Setting the flag intentionally changes the hash — which is exactly the replay-distinguishing behavior the spec requires.

Type strictness comes free: the agents subtree decodes through typed struct fields, so `ephemeral: "yes"` fails configuration loading with a YAML type error, satisfying the spec's rejection scenario.

### 2. No scheduler or orchestrator changes

Every function on the carriage path (`resolvedAgents`, `HashWorkflowDefinition`, snapshot save/load, `savedAgentSpec`, `SubmitOwnedRun`, `NormalizeAgentOptions`, `agentSpecWithDefaults`) copies or mutates the spec struct as a whole, so a new field rides along without code changes. The dispatch path stays exactly as is: an ephemeral agent's attempt gets the same fresh owned runtime every agent gets today. The difference is contractual, not mechanical — the loops change will gate its validation relaxation and per-iteration freshness on this persisted flag instead of introducing semantics then.

Concurrency, cancellation, and failure handling are untouched: the flag introduces no new goroutines, locks, or state transitions, and recovery merely reloads a field from the snapshot.

### 3. Observability stays at the snapshot

The flag is observable through the persisted execution snapshot (a readable artifact by design) and through hash-sensitive replay behavior. `GET /agents` returns runtime state projections and `GET /workflows/{id}` returns definition plus task state; neither changes shape. Adding the flag to views now would be speculative surface for a consumer that does not exist yet.

## Risks / Trade-offs

- [A squad author marks an agent `ephemeral: true` expecting repeated references to validate today] → The spec and README state explicitly that the one-reference-per-task rule is unchanged in this version; validation error text already points at declaring distinct agent names.
- [Flipping the flag between a submission and its replay yields a changed-definition error the author did not anticipate] → This is the same compatibility contract as any agent-configuration change (model, backend, yolo) and is already the documented meaning of the definition hash.
- [A future loops change silently diverges from this contract] → The delta spec pins the recreation requirement now; the loops change must extend, not weaken, it.

## Migration Plan

Additive only. Deploy: nothing to do — squads without `ephemeral` behave and hash identically. Rollback: drop the field from the YAML; since unset marshals identically, hashes and snapshots produced in between remain valid for binaries without the field (the JSON field is simply absent when false; a snapshot saved with `ephemeral: true` loads on older binaries with the field ignored, though replay hashes would then differ — acceptable for a local single-session tool).
