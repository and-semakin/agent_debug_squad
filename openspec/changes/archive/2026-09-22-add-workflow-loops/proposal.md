# Proposal: add-workflow-loops

## Why

Workflow definitions today are acyclic: a task graph runs once and settles. Real development loops — iterate implement→review until clean, repeat a whole chain a bounded number of times — cannot be expressed, which is the gap the roadmap has been building toward (ephemeral agents declared the lifecycle, the verdict judge made outcomes machine-readable). This change introduces the loop construct itself: a bounded, declarative repetition of a task subgraph with explicit iteration identity, so that every iteration is inspectable and recoverable, and automatic repetition is bounded by construction.

This is stage 2 of the loops roadmap: static loops only. A loop runs its body exactly `max_iterations` times; verdict-driven conditions (`break`/`continue`) and nested loops arrive in later changes.

## What Changes

- The workflow section accepts a `loops` map: loop name → definition. A loop definition has exactly one field in this change: `max_iterations`, a **required** positive integer. Tasks accept an optional `loop: <name>` labeling membership (the innermost loop; nesting is a later change). Automatic repetition is bounded: each body task has at most one initial attempt per iteration, and a loop never exceeds `max_iterations`. Explicit retries requested by a user or coordinator are excluded from this dispatch bound and do not consume or increase the iteration limit. No retry-count or elapsed-time bound is introduced.
- **Iteration semantics.** A loop executes its body as an ordinary DAG once per iteration, for exactly `max_iterations` iterations. When an iteration settles acceptably (all body tasks succeeded or tolerated-failed under existing `allowed_to_fail` / `min_successful_dependencies` rules evaluated per iteration), the body re-arms: task states reset and the next iteration dispatches on fresh runtime conversations. Attempts carry an iteration number; prior attempts remain visible history. A non-tolerated body failure or a blocked body task holds the loop at its current iteration and puts the execution in `needs_attention` until explicit intervention. Outside consumers keep waiting; new dispatch across the execution stops while already dispatched work may finish. An eligible manual retry repairs the current iteration; cancellation abandons the execution. Resume cannot waive a failed dependency or unmet threshold, but it can independently revalidate restored artifacts and re-attempt held verdict classification while preserving the loop hold. Successful acceptance of resume does not imply that task dispatch has resumed.
- **Iteration-scoped handoff and carry-over.** A body task's manifest resolves same-loop dependencies to the same iteration's attempts and outside dependencies to their single settled attempts. When an iteration re-arms, every body task's manifest additionally includes a previous-iteration outcomes section — the settled attempts (states, verdicts where present, result references, errors) of all body tasks from the prior iteration — so downstream-upstream feedback (reviewer reports reaching the implementer) works without cyclic `needs`.
- **Validation.** Unknown loop references, loops with no tasks, cross-loop dependencies between tasks of different loops, and dependency cycles passing through loop boundaries are rejected before dispatch. `allowed_to_fail` and thresholds are permitted inside bodies and compose per iteration. Validation errors are detailed English messages naming the offending construct and including a corrective example — a general requirement for all loop validation errors, designed for agents authoring YAML.
- **Persistence.** The snapshot gains loop execution state (current iteration per loop) and per-attempt iteration numbers under snapshot schema version 2; snapshots with schema version 1 (no loops) load unchanged. The workflow definition version stays `1`: `loops`/`loop` are additive fields, and definitions without them hash byte-identically.
- **Recovery.** Loop iteration counters survive restart. An interrupted body attempt recovers exactly as today (interrupted, needs_attention); retrying it continues the same iteration — the loop neither advances nor restarts. Committed prior iterations and their artifacts are preserved. Iteration advance is a durable decision: recovery on either side of its commit must neither repeat a completed iteration nor skip the next one. Retry eligibility is iteration-scoped: earlier same-loop descendant attempts do not block a current-iteration retry, but current-iteration descendants and any outside descendant reservations do. New retries cannot rewrite earlier iterations; tasks outside loops retain the existing retry guard.
- **Views.** Attempt views expose the iteration number; the execution view exposes per-loop state (name, current iteration, max). Task counts count tasks, not iterations.
- Scope boundaries, deliberately:
  - No conditions: no `until_task`, no `on_verdict`, no `on_exhaustion` (a static loop always completes all its iterations; the exhaustion policy matters only once early exit exists). Stage 3.
  - No nesting: `parent` is not accepted yet. Stage 4.
  - No changes to verdict machinery, facilitator turns, permissions, cancellation, or timeouts. Retries remain manual-only, with eligibility scoped to the current iteration for loop body tasks.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `declarative-workflows`: the definition-validation requirement gains the `loops`/`loop` surface, its rejection rules, the automatic-repetition bound, and the detailed-error requirement; new requirements cover bounded iteration semantics (re-arm, per-iteration tolerance composition, final-state derivation) and iteration-scoped handoff with carry-over.
- `workflow-lifecycle`: the recovery requirement gains loop-iteration continuity and schema-1/2 snapshot loading; the retry requirement gains iteration-scoped descendant guards and immutable prior iterations; the observation requirement gains iteration numbers, loop state, and actionable loop-failure holds in views; recovery and controls preserve these holds until their causes are resolved or the execution is cancelled.

## Impact

- `internal/domain`: `WorkflowLoopDefinition` (`MaxIterations`), `WorkflowDefinition.Loops`, `WorkflowTaskDefinition.Loop`, `WorkflowAttempt.Iteration`, `WorkflowSnapshot.Loops` execution state; snapshot schema constant split from the definition version (snapshot → 2 accepting 1, definition stays 1).
- `internal/config`: `rawWorkflow`/`rawWorkflowTask` parsing; `ValidateWorkflowDefinition` gains loop rules and condensed-graph cycle detection (each loop collapsed to one node); error messages with corrective examples.
- `internal/workflow`: the core surgery — iteration-aware task-state recomputation and re-arm (breaking the "settled is terminal" invariant for body tasks between iterations), iteration-aware manifest building with the carry-over section, loop state transitions, durable failure holds, retry/resume handling, and recovery of loop state.
- `internal/store`: snapshot schema acceptance 1–2.
- `internal/api`: attempt view iteration field; execution view loop states.
- README: loops documentation with the automatic-repetition guarantee and explicit-retry exclusion; a new example workflow.
- Compatibility: additive YAML surface; definitions without loops validate, hash, and execute identically; in-flight v1 snapshots recover unchanged on the new binary. Compatibility is upgrade-only: older binaries are not expected to read schema-2 snapshots, and downgrade support or snapshot down-conversion is out of scope.
