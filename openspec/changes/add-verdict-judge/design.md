# Design: add-verdict-judge

## Context

The workflow manager is a serialized reconciliation loop over a persisted snapshot: completions are committed in `handleCompletion` under the manager lock, attempts settle through `Committed()` states, and dependents become dispatchable only when a dependency settles. Responses are saved as verifiable artifacts (`WriteWorkflowResponse` → path/size/sha256) and the definition identity is `HashWorkflowDefinition` over `{definition, agents}` JSON. Recovery reloads the snapshot and re-derives scheduling; attempts without a committed terminal outcome are interrupted. Configuration loads from one YAML file; runtime state lives under `.agent-debug-squad/` by default; the agents subtree is non-strict while the `workflow` subtree is strict (`KnownFields(true)`).

See `proposal.md` for why an external judge is introduced; the specs pin the behavioral contract. This design covers how the judge plugs into that machinery.

## Goals / Non-Goals

**Goals:**

- A provider-pluggable judge with a complete OpenRouter Decisions API adapter (choice, noul, score), proxy and key-file support, and bounded transport retries.
- The judging phase as a small, explicit extension of the existing attempt lifecycle: response commit → judging → settlement, with no network calls under the manager lock.
- Verdict data as persisted, auditable, additive snapshot fields plus one readable sidecar artifact per classified attempt.
- Manual override as a control event following the established request-id idempotency pattern.

**Non-Goals:**

- Any control flow on verdict values (loops, branching, retries driven by verdicts) — later changes.
- Structured-output enforcement on task agents; verdict quality is the judge's concern, not the agent's.
- A second provider implementation; the interface exists so one can be added without renaming anything OpenRouter-shaped.
- Changes to facilitator turns, permissions, run APIs, or the workflow schema version (additive fields only).

## Decisions

### 1. `internal/judge` package with a small interface; OpenRouter is one implementation

```go
type Decision struct {
    Choice        string             // chosen option name
    Confidence    float64
    Probabilities map[string]float64 // full distribution
    Raw           json.RawMessage    // complete provider response, persisted as the audit artifact
}
type Judge interface {
    Decide(ctx context.Context, req Request) (Decision, error)
}
// Request carries the evidence map (state), the question (type,
// instructions, criteria), and the model string resolved from config.
```

The adapter implements all three Jev question types (`choice`, `noul`, `score`) as request structs and typed answers, so the adapter is complete even though the workflow path only ever builds a `choice` request from a task's `verdicts` map. *Why an interface rather than a concrete client:* the Decisions API is alpha and may change shape; the model may be swapped or served by another provider later; and workflow tests need a fake judge without an HTTP stub for the manager-level scenarios (an `httptest` server covers the adapter's own tests). The interface is named after the role (`Judge`), never after OpenRouter — the provider is a config value, not an identity.

### 2. Endpoint and transport

`POST https://openrouter.ai/api/alpha/decisions`, `Authorization: Bearer <key>` from the key file. The default key location is `~/.agent-debug-squad/openrouter-api-key` in the user's home directory — deliberately outside the workspace, because the state directory resolves inside `WorkspaceDir` and a key there could land in a git repository; overridable via `judge.api_key_file`. The file's format is a single line holding the bare token (no `Bearer` prefix, no quoting); the loader trims surrounding whitespace and ignores at most one trailing newline. The client uses a dedicated `http.Client` with a custom `http.Transport` whose `Proxy` is set from `judge.proxy_url` when present; otherwise the transport default (environment proxy) applies. Retries: bounded (3 attempts) with exponential backoff for 429/5xx/network errors and a per-call context timeout (`judge.timeout_seconds`, default 30). The model string from config is sent verbatim, so `~typesafe/jev-latest` routing or an exact pin such as `typesafe/jev-1.13` are both just configuration. *Alternative considered:* the chat-completions `response_format: questions` path — rejected as primary because it requires parsing prose-embedded JSON; the dedicated endpoint returns structured answers.

### 3. The judging phase: a new attempt state, settled through the completion path

`WorkflowAttemptState` gains `judging`. Flow: in `handleCompletion`, when a verdict task's attempt has saved its response (the existing `hasOutput` success branch), instead of committing `succeeded` the attempt transitions to `judging`, the snapshot is persisted, and a classification is handed to a background worker. The worker calls the judge outside the lock and posts a `judgement` onto the same completion channel the executor uses; a new `handleJudgement` commits under the lock exactly like `handleCompletion` does today. `Committed()` continues to exclude `judging`, so dependents (which gate on settled attempts) wait automatically — no changes to dependency evaluation. Task-state mapping treats `judging` like `running`.

Settlement rules (pinned in specs): confidence ≥ threshold → `succeeded` with verdict recorded; `on_uncertain: error` → `failed` with reason `uncertain_verdict`; hold cases keep the attempt in `judging` and add an attention reason (`uncertain_verdict:<task>:<n>`, `judge_unavailable`), moving the execution to needs_attention through the existing reason machinery. Cancellation of the execution closes `judging` attempts as cancelled (no live worker exists at that point). The task timeout is unaffected: `enforceTimeoutsLocked` tracks live dispatched runs, and by judging time the run is no longer live.

*Why reuse the completion channel rather than a new one:* Stop/drain semantics and the serialized commit point already exist there; a judgement is just another worker-stopped-style outcome.

### 4. Recovery: judging attempts re-classify instead of interrupting

The recovery rule interrupts attempts without a committed terminal outcome because their backend work is uncertain. A `judging` attempt is the opposite: its backend work is committed (response artifact written and hashed) and only a side-effect-free classification is pending. Recovery therefore re-dispatches classification for snapshot attempts in `judging` — the same code path as the initial hand-off — and the execution continues. The interruption rule itself is untouched for attempts in `queued`/`dispatching`/`running`.

### 5. Data model: additive fields with `omitempty`, sidecar artifact for the raw decision

- `WorkflowTaskDefinition` gains `Verdicts map[string]string` (name → description, empty description allowed); `WorkflowDefinition` gains `ConfidenceThreshold float64` and `OnUncertain string`. All `omitempty` in JSON, so definitions without verdicts hash byte-identically (same technique as `ephemeral`), and YAML strictness extends to the new fields through `rawWorkflowTask`/`rawWorkflow`.
- `WorkflowAttempt` gains `Verdict *AttemptVerdict` with `{Value, Confidence, Probabilities, Model, Threshold, Source, Truncated bool, JudgedAt}` — a pointer keeps "not classified" absent rather than zero-valued. The raw provider response is written once via the store as `tasks/<task>/attempts/<n>/decision.json` (readable artifact, next to input manifest and response); only the parsed summary lives in the snapshot to keep snapshots small.
- Attention reasons reuse the existing `AttentionReasons` slice; `uncertain_verdict` holds keep the attempt in `judging`, matching how `interrupted_attempt` holds executions today.
- Config: `judge:` section (`provider`, `model`, `api_key_file`, `proxy_url`, `timeout_seconds`) resolved in `config.Load`; the home-relative default for the key file is computed in main wiring (where the user's home directory is resolvable), not hardcoded in the judge package.

### 6. Judge input construction and truncation

The `state` map sent to the judge: `task_id`, `agent`, `task_prompt` (trimmed), `agent_response` (the final message), and `verdict_options` descriptions are expressed as the choice `criteria` (name → description, empty descriptions passed as empty strings rather than omitted, keeping the criteria map the source of truth for the option set). The `instructions` string is fixed boilerplate generated by the workflow package ("classify the outcome of this agent task into exactly one of the declared verdicts"). `agent_response` beyond a byte cap (default 64 KiB, not configurable in v1) is truncated head+tail with a recorded marker `[...truncated N bytes...]`; the truncation flag is persisted on the verdict record. The saved response file and manifest are never touched — truncation exists only in the judge request.

### 7. Manual override as a control event with request-id idempotency

`POST /workflows/{id}/tasks/{task}/attempts/{n}/verdict` follows the retry API's shape: a `WorkflowControlEvent` (`type: verdict_override`) appended to the snapshot's `Controls`, a `request_id` for idempotency (replays return the recorded result; conflicting verdicts for the same ID return 409), and validation that the verdict name is declared on the task (400) and the attempt is in `judging` (409). Settlement marks the attempt `succeeded` with `Source: manual`. Resume (`needs_attention` → running) additionally re-classifies any attempts still in `judging` after their hold reasons are gone, mirroring the recovery path.

### 8. Startup gating and failure mode

Startup order: load config → resolve the state dir → if the workflow contains verdict tasks or a `judge:` section exists, read the key file; missing/unreadable → exit with an error naming the path. The check runs before the API server binds, so a misconfigured squad never starts half-alive. A squad without verdicts and without `judge:` never constructs a judge at all — the manager receives a nil judge and verdict tasks cannot exist in that configuration by construction.

## Risks / Trade-offs

- [The Decisions API is alpha and may change without deprecation] → It is isolated behind `Judge`; requests/responses are versioned by the model string and the raw response is persisted per attempt, so a provider change is a one-package fix and past executions remain auditable.
- [Judge latency delays dependents of verdict tasks] → Deliberate: settlement-before-dependents is the semantic later control flow needs; typical decision calls are small and fast, and holds are visible (needs_attention) rather than silent waits.
- [A flaky judge could pin executions in needs_attention] → Bounded retries with backoff absorb transient errors; holds are operable via resume (re-classify) or manual override, and never destroy agent work.
- [Snapshot growth from distributions] → Distributions are a handful of floats per classified attempt and only for verdict tasks; the raw response stays in a sidecar file, not the snapshot.
- [Confidence threshold tuning is per-workflow guesswork] → The threshold and the full distribution are recorded per attempt, so operators can audit near-misses and adjust the workflow value; `on_uncertain` gives an automation-friendly escape hatch.

## Migration Plan

Additive only. Deploy: nothing to do — squads without verdict tasks and without a `judge:` section behave and hash identically. Adopting verdicts requires creating the key file and adding `judge:` plus task-level `verdicts`. Rollback: drop the `judge:` section and verdict declarations; snapshots written in between load on older binaries with verdict fields ignored (the JSON fields are simply absent when unused), though definition hashes then differ for definitions that had verdicts — acceptable for a local single-session tool.
