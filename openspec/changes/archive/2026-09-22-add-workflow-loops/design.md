# Design: add-workflow-loops

## Context

The scheduler is a serialized reconciler over a persisted snapshot. Three invariants today exclude repetition: task states are settled-terminal (`recomputeTaskStatesLocked` maps the last committed attempt to a terminal task state and never revisits it), readiness and manifests resolve through `lastCommittedAttempt` with no iteration dimension, and `refreshExecutionStateLocked` derives the execution's final state once all tasks settle. Attempts append monotonically per task (retries add entries), artifacts are per-attempt (`tasks/<task>/attempts/<n>/`), and every attempt already runs on a fresh owned runtime — the runtime side of iteration is free; the state-machine side is the work. See `proposal.md` for scope: static bounded loops only.

## Goals / Non-Goals

**Goals:**

- At most `max_iterations` iterations per loop, completing all of them on normal completion. Initial automatic body dispatches are bounded by body task count × `max_iterations`; explicit user/coordinator retries are excluded and retain the current iteration.
- Iteration identity as a first-class dimension: attempts carry it, manifests resolve by it, views expose it, recovery preserves it.
- File-based carry-over: the prior iteration's outcomes reach every body task's manifest, so feedback flows upstream without cyclic `needs`.
- Preserve fresh conversations, dependency tolerance, verdicts, timeouts, and cancellation. Adapt manual retries to iterations and hold loop-blocking failures for intervention instead of finalizing the execution as failed.

**Non-Goals:**

- Conditions (`until_task`, `on_verdict`), `on_exhaustion`, and the reserved `blocked` verdict — stage 3. A static loop always completes all iterations, so exhaustion policy is meaningless until early exit exists.
- Nesting (`parent`), and with it scoped dependency rules beyond the single-level boundary rules — stage 4.
- Automatic retries of any kind; the manual retry API remains the only retry path, now iteration-aware. A limit on explicit retry count or total elapsed execution time is out of scope; pause and needs_attention may last indefinitely.
- Downgrade support and schema-2 to schema-1 snapshot conversion. Upgrade loading of schema-1 snapshots remains supported.

## Decisions

### 1. Flat membership: `loop:` label on tasks, `loops:` map on the workflow

```yaml
workflow:
  loops:
    refine:
      max_iterations: 3
  tasks:
    implement: { agent: dev, loop: refine, prompt: ... }
    review:    { agent: rev, loop: refine, needs: [implement], ... }
    report:    { agent: rep, needs: [review], ... }   # outside; runs after exit
```

Tasks stay keyed by task ID — snapshot, manifests, and artifact paths do not move. A loop definition has exactly `max_iterations` (required, positive); unknown loop fields fail the strict workflow subtree. The label names the innermost loop, which is forward-compatible with stage 4's `parent` without syntax changes. *Alternative rejected:* nesting tasks under loop blocks — moves task IDs, breaks the flat snapshot layout for a readability gain YAML comments already provide.

### 2. Iteration state lives in the snapshot; attempts carry `Iteration`

`WorkflowSnapshot.Loops: map[string]*WorkflowLoopExecution{Iteration int, State: "running"|"needs_attention"|"done"}` (current 1-based iteration). `WorkflowAttempt.Iteration int` (`omitempty`, 0 = not in a loop). The authoritative question "has task T settled in iteration k?" becomes "does T have a committed terminal attempt with `Iteration == k`" — one dimension, no second bookkeeping. A new manual retry of a body task must target its latest failed/interrupted attempt in the current iteration; it retains that iteration and appends a monotonically increasing attempt number. Earlier iterations cannot be retried or rewritten. An identical previously accepted retry request still replays its original response after iteration advance. If an eligible retry reopens a done loop, reset its state to running without changing its iteration; outside readiness must wait for the retried iteration to settle again.

### 3. Re-arm: iteration-aware recomputation instead of a parallel mechanism

The core change is in `recomputeTaskStatesLocked`: for a body task, "the last attempt" becomes "the last attempt of the loop's current iteration". A body task with no current-iteration attempt evaluates as pending (dependencies re-checked against current-iteration settlement); with a committed current-iteration attempt it takes that attempt's state as today. Settled-terminal remains true within an iteration except for an explicit retry; between iterations the task simply has no current-iteration attempt yet. Loop-blocking failures additionally hold the execution as described below. Re-arm is therefore not a special transition but the natural consequence of advancing `Loops[name].Iteration` and recomputing: task states flip back to pending/ready and `dispatchReadyTasksLocked` dispatches with fresh run IDs. Loop advance happens when every body task has an acceptable settled current-iteration attempt (the existing tolerance evaluation, scoped by iteration) and `Iteration < MaxIterations`; on the final iteration the loop marks `done` and outside consumers' readiness resolves against final-iteration attempts. A non-tolerated failed body task or a blocked body task (including an unmet success threshold or failed outside prerequisite) holds the loop as `needs_attention` without advancing its iteration. Preserve failed attempts and blocked body states; outside consumers remain pending with a loop-wait reason until the loop is done, instead of propagating a temporary body failure across the boundary. Record `loop_failure:<loop>:<iteration>:<task>` or `loop_blocked:<loop>:<iteration>:<task>` attention reasons, retaining underlying task errors and blocking reasons. These reasons take precedence over `finalState`, even with no outside consumers or live workers. All new dispatch and loop advance in the execution stop while attention remains; already dispatched work may finish and persist outcomes. Judging composes untouched: a verdict task's iteration-attempt settles only after its verdict, so an iteration cannot complete on a half-judged task.

The bound covers initial dispatches only: for loop L it is `len(body(L)) * max_iterations(L)`; across a workflow, add these bounds and the number of outside tasks. An explicit retry adds an attempt within its existing iteration without spending or extending the iteration budget. There is no automatic failure retry. A one-task, one-iteration loop may therefore record several attempts after explicit retries, but can never enter iteration 2. The bound does not promise completion within a fixed time or under unlimited intervention.

### 4. Manifest resolution: same-iteration, outside, and the carry-over section

`buildManifest` gains an iteration filter: for a body task's `needs`, same-loop dependencies resolve to the dependency's current-iteration committed attempt (missing → not settled, readiness holds); outside dependencies keep today's single-attempt resolution. On top of `needs`, when a body task dispatches in iteration k > 1, the manifest includes a `previous_iteration` section: all body tasks' iteration-(k−1) committed attempts in sorted task ID order, carrying the same fields as dependency entries plus recorded verdicts. The prompt builder renders it as a clearly delimited "previous iteration outcomes" block of readable local file references, mirroring the existing dependency block. This is the channel by which reviewer reports reach an upstream implementer: the graph stays acyclic, the feedback rides files. Manifest JSON gains an `iteration` field on dependency entries; `WorkflowDependencyInput` gains `Iteration` and the new section uses the same type. Artifact verification applies unchanged to every referenced result.

### 5. Validation: condensed-graph cycles, boundary rules, and error quality

- Body-internal cycles are ordinary cycles (existing detection).
- Boundary cycles (body → outside → same body) are invisible in the raw graph, so `findWorkflowCycle` additionally runs on a condensed graph where each loop's body collapses to one node with outside edges merged. The cycle error names the loop.
- Cross-loop dependencies are rejected outright in v1 with a message that routes through an outside task; this keeps stage 4's scoping rules unencumbered.
- Unknown `loop` references, empty bodies, and missing/non-positive `max_iterations` are rejected with corrective examples, per the spec's error-quality requirement. A small `loopValidationError` helper formats `loop %q: <problem>\nexample:\n  loops:\n    %s:\n      max_iterations: 3` so every loop error is uniform.
- Loop names share the existing task-identifier pattern; `loops`/`loop` participate in the definition hash (`omitempty` keeps pre-loop hashes byte-identical).

### 6. Snapshot schema 2, definition version stays 1

The definition's `version: 1` is unchanged — `loops` is additive and old YAML must keep loading (the ephemeral and verdict precedents). The *snapshot* schema bumps to 2 because v1 snapshots have no `Loops` map and no iteration semantics; `LoadWorkflowSnapshot` accepts 1 and 2 (v1 loads as a loopless execution), still failing closed on anything else. This splits today's shared constant into `WorkflowDefinitionVersion = 1` and `WorkflowSnapshotSchemaVersion = 2`.

### 7. Recovery, cancellation, pause

Loop state is ordinary snapshot state: counters survive restart, committed iterations verify through the existing artifact revalidation, and an interrupted body attempt holds the execution exactly as today — after retry it settles within its iteration, and only then does the loop advance. Pause blocks new dispatch but lets the current iteration drain (unchanged per-attempt semantics). Cancellation cancels the current iteration's live work and marks unsettled tasks cancelled; the loop's state is left as-is in the terminal snapshot for audit. Timeouts stay per-attempt — an iteration has no separate budget in this change.

Iteration advance must be committed before any next-iteration backend dispatch. Save the new counter and the corresponding task-state reset as one authoritative snapshot transition, or derive task states from that counter on recovery. A crash before that commit leaves the completed previous iteration and recovery advances it once; a crash after it but before any reservation leaves next-iteration tasks eligible for their initial attempts. If a next-iteration reservation already exists, ordinary interruption recovery applies. A failed advance save stops dispatch; never use uncommitted progress to release work.

### 7a. Intervention on a failed or blocked iteration

Failure holds are durable, recomputed from current-iteration states, and preserved on restart without automatically repeating failed work. Retry must accept eligible failed tasks while attention consists of loop-failure/loop-blocked reasons and/or recoverable interruptions; the existing `onlyInterruptionsRemain` gate must be extended. Artifact/storage damage or unresolved judge attention retain their resolution requirements, but loop failures must not prevent resume from performing those recovery actions. Revalidate artifacts first and clear only reasons proven resolved. Re-attempt each held classification whose own response is verified and whose state can be safely persisted; preserve unrelated attention reasons. While classification is in flight, keep its attention hold until a committed outcome resolves it, so it cannot prematurely release ordinary tasks. Repeated resume must not start duplicate concurrent classification calls for the same attempt. A blocked task without an attempt cannot itself be retried: the caller retries its failed causal predecessor, which may be outside the loop, using the applicable descendant guard. An insufficient threshold is repaired by retrying an eligible tolerated-failed predecessor. If a guard prevents repair because inputs were already consumed, expose the conflict and offer cancellation; never silently invalidate consumers.

After each accepted retry reservation, reevaluate blocked descendants against the queued retry: its outcome is pending, not the superseded failure. Remove only the resolved failure/blocking reasons; other failures or uncertainty still hold dispatch. This allows multiple eligible retries to be queued before work resumes. Once all attention reasons clear, return to the execution's existing mode (running or paused), at the same iteration. For a paused or needs_attention execution with loops, resume is an accepted recovery request (HTTP 200 with the current execution view), even if loop failures remain. It requests running mode, independently performs safe artifact/judge recovery, and returns needs_attention with remaining reasons when dispatch cannot resume. It never clears unresolved failure reasons, bypasses thresholds, or retries agent work. Invalid lifecycle transitions still return 409; actual storage failures remain errors. Existing loopless control behavior stays unchanged. Cancellation takes precedence over the hold and follows normal cleanup rules. Loopless failures retain their current terminal behavior.

### 8. Views

`WorkflowAttemptView` gains `iteration` (`omitempty`). `WorkflowExecutionView` gains `loops: [{name, iteration, max_iterations, state}]` in sorted name order. `countTaskStates` is unchanged — it counts tasks, and body task states reflect the current iteration. Expose loop failure/blocking attention reasons, their underlying causes, and retry-or-cancel guidance; wait returns on the hold rather than waiting for terminal failure.

## Risks / Trade-offs

- [Iteration-aware recompute is the risky surgery] → It is a filter change ("last attempt of the current iteration"), not a rewrite; the full existing scheduler suite runs against it, and loopless definitions must behave byte-identically (golden hash + behavior tests pin that).
- [Retry × iteration interplay] → For a body-task retry, `assertNoDescendantAttempts` considers transitive descendants through `needs`: same-loop reservations block only in the target iteration, while descendants outside that loop block if they have any reservation. Earlier same-loop iterations are immutable history and must not block current-iteration retries. For a task outside all loops, retain the existing check across all descendant attempts. Keep the check and retry reservation serialized with loop advance and dispatch. Tests cover iteration-2 recovery after iteration-1 descendants completed, same-iteration consumption, outside consumption, old-iteration rejection, and idempotent replay after advance.
- [Carry-over grows prompts on each re-arm] → The section references files (existing no-pasting rule) and is bounded by body size, not response size.
- [Condensed-graph cycle detection misses exotic shapes] → It reduces to ordinary DFS on a smaller graph; property-style table tests cover body-internal, boundary, and cross-loop shapes.

## Migration Plan

Additive. Deploy: nothing to do — definitions without loops hash and run identically; v1 snapshots recover as loopless executions. Adopting loops requires adding `loops:` and `loop:` labels with an explicit `max_iterations`. Compatibility is upgrade-only. Older binaries reject schema-2 snapshots; removing loop declarations from YAML does not convert persisted execution state. Downgrade recovery and snapshot down-conversion are not supported or required by this change. Persist snapshots with their actual schema version; never relabel schema-2 state as schema 1 to make an older reader accept it.
