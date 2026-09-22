# Proposal: add-verdict-judge

## Why

A workflow task attempt finishes with a free-text response that carries no machine-readable outcome: the scheduler can see that a backend turn completed, but not whether the work semantically succeeded, found issues, or failed. The planned workflow loops (retry until approved, iterate review-fix) need exactly that signal to decide what happens next, and even loopless workflows benefit from a recorded, inspectable verdict per task. Rather than asking every task agent to obey an output protocol, the verdict should come from a dedicated external judge model — Typesafe's Jev on OpenRouter, a decision-only model that returns a typed choice plus a full probability distribution.

This change introduces the judge as a first-class component and records verdicts on task attempts. It deliberately does not act on them: branching and loops arrive in later changes, so the semantic foundation lands and becomes observable first.

## What Changes

- A new **judge** component classifies completed attempts of tasks that declare verdicts:
  - Workflow tasks accept an optional `verdicts` map (verdict name → optional human description). The map is carried into the execution's saved definition, participates in the definition hash (byte-identical hashes when unset), and is validated: safe identifiers, non-empty, at least two options, and the reserved name `uncertain` is rejected.
  - The workflow section accepts `confidence_threshold` (default 0.8) and `on_uncertain` (`hold`, the default, or `error`).
- The judge is **provider-pluggable** with OpenRouter as the first provider, configured via a new `judge:` section: `provider`, `model` (default `~typesafe/jev-latest`, pinnable to an exact version), `api_key_file`, `proxy_url`, and a call timeout. The OpenRouter adapter targets the Decisions API (`POST /api/alpha/decisions`) and implements all three Jev primitives — `choice`, `noul`, and `score` — as a complete adapter; workflow verdicts use `choice` only in this change.
- Classification is **asynchronous and non-blocking**: an attempt of a verdict task saves its response, enters a new `judging` attempt phase, and settles only after the verdict resolves; dependents wait for that settlement. The judge is never called under the scheduler lock.
- **Confidence gating**: the chosen verdict applies when judge confidence meets the workflow's threshold; below it the attempt gets the synthetic `uncertain` outcome. `on_uncertain: hold` moves the execution to `needs_attention` exposing the full probability distribution for human inspection; `on_uncertain: error` fails the attempt with an `uncertain_verdict` reason (retryable through the existing retry API).
- **Judge unavailability** (transport errors after bounded retries, or call timeout) never fails the task: the execution moves to `needs_attention` with a `judge_unavailable` reason.
- **Human override**: a held judging attempt can be resolved by recording a manual verdict through a small new endpoint, without re-running the agent or the judge; resume re-classifies held attempts instead.
- **Persistence and audit**: verdict, confidence, the full probability distribution, the model string, and the threshold used are recorded on the attempt; the raw response file stays untouched. Recovery re-classifies attempts caught mid-judging instead of interrupting them. Overlarge responses fed to the judge are truncated deterministically (head+tail with a cap) and the truncation is recorded.
- **Startup gating**: the OpenRouter API key lives in a file in the user's home directory by default (`~/.agent-debug-squad/openrouter-api-key`, overridable — deliberately outside the workspace so credentials never land in a git repository). Its presence is required at startup when the configured workflow contains verdict tasks; without verdict tasks the judge is optional and unused. HTTP proxy support is explicit in the judge configuration.
- Scope boundaries, deliberately:
  - Verdicts are recorded metadata only — they do not modify the graph, scheduling, or dependency evaluation in this change.
  - Facilitator manual turns, permission handling, and run APIs are untouched.
  - No loop constructs, no branching on verdicts, and no workflow schema version bump (all persisted additions are additive).

## Capabilities

### New Capabilities

- `verdict-judge`: classification of completed workflow task attempts by an external judge model — configuration and startup gating, provider adapter (OpenRouter Decisions API, all Jev primitives), asynchronous judging lifecycle, confidence threshold and uncertainty policy, unavailability handling, manual override, persistence and audit.

### Modified Capabilities

- `declarative-workflows`: the definition-validation requirement gains the task-level `verdicts` map and the workflow-level `confidence_threshold` / `on_uncertain` settings with their validation rules; the success-boundary requirement gains that a verdict task's attempt settles only after its verdict is resolved (the `judging` phase).
- `workflow-lifecycle`: the recovery requirement gains that attempts caught mid-judging are re-classified rather than interrupted; the views requirement gains verdict data, judging state, and judge-related attention reasons in workflow views.

## Impact

- `internal/judge` (new): the `Judge` interface (decision request/typed result), the OpenRouter provider implementing choice/noul/score over the Decisions API with proxy support, bounded transport retries, and input truncation.
- `internal/domain`: `WorkflowTaskDefinition` gains `Verdicts`; `WorkflowDefinition` gains `ConfidenceThreshold`/`OnUncertain`; `WorkflowAttempt` gains the `judging` state and persisted verdict fields (all `omitempty` — stable hashes and snapshots for existing definitions).
- `internal/workflow`: manager gains the judging phase between response persistence and attempt settlement, an asynchronous judge-call path outside the lock, recovery re-classification, and judge-related attention reasons.
- `internal/config`: the `judge:` section, key-file loading and validation, startup presence check tied to verdict tasks.
- `internal/api`: attempt views expose verdict state and distribution; the manual verdict override endpoint.
- `cmd/agent-debug-squad`: judge wiring at startup.
- README: judge configuration, verdict task authoring, an example workflow with verdicts.
- Compatibility: purely additive YAML/JSON surface; definitions without verdicts hash and behave identically; snapshots without verdict fields load unchanged. The OpenRouter Decisions API is alpha and may change — it is isolated behind the judge interface, and the model string plus raw decision response are recorded for audit.
