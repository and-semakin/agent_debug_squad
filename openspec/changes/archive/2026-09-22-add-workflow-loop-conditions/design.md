# Design: add-workflow-loop-conditions

## Context

The loop engine settles whole iterations before advancing (`iterationSettledAcceptably`), holds on failure/blocking, and advances durably in the reconcile pass (`advanceLoopsLocked`). Verdict tasks settle only after classification, so a settled attempt always carries its verdict. Conditions are therefore a decision layered onto the existing advance pass — no new state-machine phase. See `proposal.md` for scope; nesting, auto-retries, and the "answer and continue" control stay out.

## Goals / Non-Goals

**Goals:**

- `until_task` + total `on_verdict` (break / continue / needs_attention) + `on_exhaustion` over the existing engine; static loops unchanged.
- Explicitly declared information-request verdicts on the condition task, mapped to needs_attention through the same action table.
- Extend/stop controls, idempotent per request ID, durably raising the effective cap or requesting a manual break after the current iteration.

**Non-Goals:**

- Nesting (`parent`) — stage 4. Text injection into prompts ("answer and continue") — deferred. Any change to judging itself (thresholds, uncertainty, distributions) — composes as holds. Loop-level auto-retry of the condition task — the manual retry API already covers it.

## Decisions

### 1. Condition evaluation lives inside `advanceLoopsLocked`

The condition task must be mandatory (`allowed_to_fail` omitted or false). Its timeout, backend failure, missing response, or `on_uncertain: error` failure creates the existing non-tolerated loop-failure hold, so a missing or synthetic uncertain verdict is never looked up in `on_verdict`. Other tasks may still tolerate failures. When an iteration settles acceptably, the advance pass reads the condition verdict — the verdict on `until_task`'s latest committed current-iteration attempt — before deciding:

```
acceptable settlement of iteration k
  -> verdict V of until_task (settled, override-aware)
     break            -> loop.State = done, stay at k      (outside consumers resolve against k)
     continue         -> k < effective cap ? k++ (re-arm)  : exhaustion policy
     needs_attention  -> hold (derived reason, no advance)
```

Static loops (no `until_task`) skip the verdict read and keep today's behavior exactly. Exhaustion: `succeed` marks done at k (identical to break); `needs_attention` (default) derives a `loop_exhausted:<loop>` attention reason. All holds are derived from persisted state (counter, cap, verdicts), so recovery re-derives them without new persistence.

### 2. Effective cap and the extend control

`WorkflowLoopExecution` gains `ExtendedIterations int` (`omitempty`); effective cap = `MaxIterations + ExtendedIterations`. The dispatch-bound reasoning uses the effective cap — extensions are explicit, audited, idempotent human actions, so the system never dispatches unboundedly on its own. `ExtendLoop(executionID, loop, req)` follows the established control pattern: request-ID idempotency by scanning `snapshot.Controls` (`type: loop_extend`), 400/404/409 mapping as specified, `ExtendedIterations += n` and its request record (loop name and amount) committed atomically in the same snapshot before the released hold re-evaluates. Look up accepted requests before lifecycle checks: identical replay succeeds even after completion/cancellation or restart, while reusing an extend request ID with another loop or amount returns 409. Reject new extensions of done loops and terminal/cancelling executions. Scope request IDs per execution and control type, consistently with verdict control events. Extending a `loop_exhausted` hold simply clears the derived reason on the next reconcile (the counter is below the raised cap again), so extension needs no special un-hold path.

### 3. The stop control is a manual break

`StopLoop(executionID, loop, req)` persists `StopRequested bool` on an unfinished loop at its current iteration under the manager lock; it does not immediately set done or cancel workers. Normal body scheduling continues, including tasks not yet started, until the whole iteration settles acceptably. No next iteration may start. Paused executions remain paused until resume. Failure/blocking, interruption, artifact, and judging reasons remain in force and may require retry, override, resume, or cancellation. Stop from a failure hold only records intent; it neither clears the failure nor finalizes the execution as failed.

Once the current iteration is locally acceptable, a pending stop resolves that loop's condition/exhaustion hold and marks it done at that iteration before applying on_verdict or on_exhaustion, even if another loop still needs attention. This is a local control resolution, not permission to dispatch or advance other loops. Thus stop can resolve a condition-action needs_attention hold or exhaustion hold, but cannot waive unfinished classification. Derive that loop's condition/exhaustion reasons as absent when its stop prerequisites are met. Perform local control resolution before the global attention-free dispatch/advance gate, preserving every unrelated reason; two independently held loops must not prevent each other's stop requests from resolving. Outside consumers remain pending until actual completion. Expose stop_requested in loop views and preserve it on recovery, including across explicit retries in the current iteration.

Use `snapshot.Controls` (type loop_stop) for idempotency. Check an identical accepted request before lifecycle rejection, including after the loop or execution finishes; reuse of its request ID for a different loop is a conflict. New requests reject cancelling or terminal executions and done loops. Repeated new stop requests while intent is already pending succeed without changing the iteration. Stop and advance serialize: if advance committed first, stop applies to the newly current iteration; if stop committed first, advance is forbidden.

### 4. Explicit verdicts and condition override

Verdict names carry no built-in scheduling semantics; blocked is an ordinary allowed name. A task without verdicts is not classified. A task declaring verdicts is classified among those options; if no information-request option is declared, no such structured outcome is available (low-confidence uncertain remains a separate classifier outcome). For until_task, an explicit needs_input → needs_attention mapping holds the loop after acceptable iteration settlement. Other tasks' semantic verdicts remain observational; neither the name nor a textual request for help automatically changes scheduling. This stage does not implement answer injection or rerunning a successful attempt after a help request.

The manual override endpoint currently accepts only `judging` attempts. It is extended with a narrow rule: a *settled* attempt is overridable when its recorded verdict is load-bearing on a live hold — a verdict whose loop condition is currently held by a `needs_attention` action. The override rewrites the attempt's verdict (source `manual`), leaves the attempt settled, and the next reconcile re-derives: holds clear, the condition re-evaluates under the replacement verdict. Attempts from iterations the loop has advanced past are never overridable (history immutability); the guard compares the attempt's iteration against the loop's current counter.

### 4a. Independent intervention and boundary precedence

Extend and override rederive only the affected attention causes, regardless of other loop holds. Extend resolves exhaustion when the raised cap permits more iterations, but cannot waive a condition-action needs_attention result. Extend while paused does not unpause. Stop is resolved locally as above, including when both loops have pending stops. Extend/stop/override must persist their decisions before any effects and preserve unrelated failures, interruptions, artifact/judge uncertainty, and pause mode.

Extend `onlyRetryRepairableReasons` to admit condition-action and loop-exhausted reasons for queuing otherwise eligible failed/interrupted retries; preserve descendant, cleanup-confirmation, persistence, and lifecycle guards. These semantic holds do not invalidate a retry reservation, but still prevent dispatch. Artifact/storage and unresolved-classification requirements remain in force. Recompute causes after each control/reservation, then gate ordinary dispatch and iteration advance on the complete remaining attention set. Tests must exercise stop-then-retry and retry-then-stop across two loops, as well as two simultaneous condition holds.

Without pending stop, a mapped needs_attention action wins over exhaustion even on the final iteration. Only continue at the effective cap invokes on_exhaustion. Override to continue uses exactly this same rule: below cap it can advance once attention clears; at cap it either holds on exhaustion or completes under succeed, never creates an extra iteration. A separate extend is required to raise the cap.

### 5. Validation with the established error format

`loopErrorf` extends to condition errors, always naming the loop (and task where relevant) with a corrective example: `until_task` must be a body member declaring `verdicts`, must not set `allowed_to_fail: true`, and must be the unique body sink (every other body task reaches it through same-loop needs edges; a singleton body qualifies). Validate by traversing its same-loop predecessors and rejecting any uncovered task with a corrective example; `on_verdict` keys must exactly equal the declared verdict set (missing and unknown keys are both named); actions are enum-checked; `on_exhaustion` accepts only `needs_attention` (default) or `succeed`, rejecting `fail`; `until_task` and `on_verdict` must appear together, and explicit `on_exhaustion` requires that pair. Static loops reject on_exhaustion instead of silently ignoring it. All fields are `omitempty` and join the definition hash; condition-free definitions hash byte-identically.

### 6. Views

`WorkflowLoopView` gains `until_task`, `effective_max_iterations` (declared plus extensions), and `last_condition_verdict` (the latest settled current-iteration verdict of the condition task, `omitempty`), plus `stop_requested` (omitted when false). Attention reasons carry the intervention hint in the established suffix style (`:extend_or_stop_or_cancel`, `:override`).

## Risks / Trade-offs

- [Two decision sites in one pass (settlement + verdict)] → The verdict is read only from committed current-iteration attempts, and every hold (failure, blocked, judging, uncertain, condition needs_attention) is derived and re-derived from persisted state; the advance pass stays the single serialization point.
- [Override of settled attempts weakens immutability] → Narrowly guarded: only load-bearing verdicts, never past iterations, idempotent per request ID, audited as control events; the previous verdict remains visible in the decision artifact and attempt history is not rewritten (only the current verdict field moves).
- [Extend multiplies cost quietly] → Each extension is a manual action with an audit trail and is visible in the effective cap; no automatic extension path exists.
- [Budget completion may leave substantive issues] → `succeed` explicitly permits moving on with the final iteration's results; it does not rewrite the recorded condition verdict or certify correctness. The default remains `needs_attention`. Exhaustion alone never finalizes failure.

### Uncertainty-policy naming and saved definitions

Use `on_uncertain: needs_attention` in newly loaded YAML and as the effective default, keeping `error` unchanged. Reject `hold` in new YAML with a corrected example. Rename the domain constant and update validation, judge handling, tests, README, examples, and maintained skill instructions that document this option. Do not globally replace the English word "hold": it also describes waiting, rather than an enum value.

Reject saved definitions containing `on_uncertain: hold` with an actionable unsupported-policy error before scheduling. Do not add a legacy alias, compatibility branch, or automatic migration. Do not silently interpret an unknown value as the default. An omitted policy must remain omitted in serialized identity; substituting the new explicit spelling for an old explicit value changes the definition hash, so submissions with that edited definition require a new request ID. This is a naming change only, with no additional uncertainty behavior.

## Migration Plan

Update explicit `on_uncertain: hold` in YAML to `needs_attention`; omitted policies need no edit. Saved executions with explicit `hold` are not supported after this rename; no compatibility or automatic migration is required. Apart from this policy rename, condition-free loops and loopless workflows hash and run identically; snapshots gain additive loop fields for extensions and stop intent, schema stays 2. Adopting conditions requires adding the three loop fields. Downgrade recovery is unsupported: older binaries must not be relied on to preserve condition semantics, extensions, or pending stop intent. No backward-reader compatibility or snapshot down-conversion is promised.
