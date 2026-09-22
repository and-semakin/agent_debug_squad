# Proposal: add-workflow-loop-conditions

## Why

Static loops run blind: a loop executes exactly `max_iterations` iterations regardless of what the work produced. The verdict judge already records a machine-readable outcome per attempt, and the loop engine already bounds and recovers iterations — this change connects them. A loop gains a condition: the verdict of one designated body task decides, at each iteration's settlement, whether the loop breaks, continues, or holds for a human. This is the "repeat until the review is clean, at most N times" primitive the loops roadmap has been building toward (stage 3 of 4; nesting follows separately).

## What Changes

- Rename the YAML uncertainty policy `on_uncertain: hold` to `on_uncertain: needs_attention`, also the default; `error` remains supported. New YAML using `hold` is rejected with migration guidance. Saved definitions using `hold` are unsupported and rejected on load; no legacy alias or automatic migration is provided.

- Loop definitions accept three optional fields; a loop with none of them stays fully static (stage 2 semantics):
  - `until_task`: the loop's single condition task. It must be a member of the loop's body, must declare `verdicts`, must have `allowed_to_fail: false` (explicitly or by default), and must be the unique body sink: every other body task must be its direct or transitive predecessor through same-loop needs edges (outside consumers may depend on it).
  - `on_verdict`: a map from each verdict declared by `until_task` to an action — `break`, `continue`, or `needs_attention`. The map must cover every declared verdict; an unmapped verdict or an unknown verdict name is a validation error with a corrective example. All author-declared names, including blocked or needs_input, have only their explicitly mapped meaning.
  - `on_exhaustion`: `needs_attention` (default) or `succeed` — what happens when the condition maps to continue at the iteration cap. This field requires until_task/on_verdict; needs_attention actions retain priority even at the cap.
- **Condition evaluation** happens when the whole iteration settles acceptably (existing rules; existing `loop_failure`/`loop_blocked` holds take precedence and prevent evaluation). The settled verdict of `until_task`'s current-iteration attempt then decides: `break` completes the loop at that iteration (outside consumers proceed with its results); `needs_attention` holds the loop with an explicit reason; `continue` re-arms — or, at the budget cap, triggers `on_exhaustion`. A failed condition attempt holds the loop for intervention even with `on_uncertain: error`; it never supplies a continuation decision. Other tasks retain ordinary `allowed_to_fail` semantics. A held or judging condition attempt never evaluates: verdict settlement is part of attempt settlement, so a half-judged iteration cannot steer the loop.
- **Explicit information requests**: tasks opt into classification by declaring verdicts. A help-request outcome such as needs_input must be explicitly declared; for until_task, map it to needs_attention to wait for intervention. No verdict name automatically requests intervention on ordinary body or outside tasks. Without verdicts there is no classification or structured help-request outcome. Answer injection and resuming a successful agent attempt remain out of scope.
- **Exhaustion handling**: `succeed` completes the loop like a `break`; `needs_attention` (the default) holds with a `loop_exhausted` reason.
- **Two new loop controls**, both idempotent per `request_id` and serialized with iteration advance:
  - `POST /workflows/{id}/loops/{name}/extend` with `add_iterations` (positive integer) raises the loop's effective iteration cap beyond its declared `max_iterations` — a manual, durable, audited action. Identical accepted requests replay without another increase even after completion or restart; changing the target loop or amount under the same extend request ID is a conflict. The no-infinite-loops invariant is preserved: the system itself never loops unboundedly; every cap increase is an explicit human request.
  - `POST /workflows/{id}/loops/{name}/stop` durably requests completion after the current iteration. Remaining body tasks finish under normal dependency rules, no next iteration begins, and outside consumers wait until the whole current iteration settles acceptably. Failures, interruptions, and unresolved verdict classification still require intervention; stop does not cancel workers or waive these causes. Once the iteration is acceptable, the request overrides condition actions and exhaustion policy as a manual break.
- **Independent intervention:** stop, extend, and override resolve only their own attention causes even while another loop is held; eligible retries may be queued under condition/exhaustion holds. Dispatch and iteration advance still wait for all attention reasons to clear.
- **Views**: loop views expose `until_task`, the effective iteration cap (declared plus extensions), and the latest settled condition verdict; execution views expose `loop_exhausted` and condition-action attention reasons with intervention guidance.
- Scope boundaries, deliberately: no nesting (`parent` — stage 4); no automatic retries; no changes to confidence thresholds or the effect of uncertainty policies (only the waiting-policy spelling changes; manual overrides are extended as described above); no "answer and continue" text-injection control (deferred); the automatic dispatch bound logic is unchanged except that the effective cap includes extensions.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `declarative-workflows`: the definition-validation requirement gains the `until_task`/`on_verdict`/`on_exhaustion` surface and its rejection rules (with corrective examples); the bounded-iterations requirement is modified so completion may arrive early via `break` and the budget may grow only through the explicit extend control; a new requirement pins condition evaluation semantics (evaluation point, actions, exhaustion, explicit information-request mappings).
- `workflow-lifecycle`: a new requirement covers the extend and stop controls and exhaustion resolution; the observation requirement gains condition state in loop views and the new attention reasons.
- `verdict-judge`: the manual-override requirement is extended — a settled attempt whose verdict is load-bearing on a live hold (a verdict holding a loop condition) becomes overridable, with prior iterations immutable.

## Impact

- `internal/domain`: `WorkflowLoopDefinition` gains `UntilTask`, `OnVerdict`, `OnExhaustion` (`omitempty`); `WorkflowLoopExecution` gains an extensions counter and durable stop-request flag; `WorkflowLoopView` gains condition fields; new loop states are not needed (`needs_attention`/`done` suffice).
- `internal/config`: parse the new loop fields; validate condition rules (sink membership, verdicts declared, total `on_verdict` coverage, action enum, exhaustion enum) with the established error-plus-example format.
- `internal/workflow`: condition evaluation in the advance pass (read `until_task`'s settled current-iteration verdict, apply the mapped action); exhaustion/condition-action hold derivation; extend/stop control methods with request-id idempotency and durable effective-cap persistence.
- `internal/api`: two new loop control routes; view fields.
- README: conditions documentation, the exhaustion policies, the controls, and explicit information-request verdicts.
- Compatibility: the `on_uncertain` waiting-policy spelling changes from `hold` to `needs_attention`; omitted settings keep their existing identity and behavior. Explicitly replacing the value changes the definition identity and requires a new submission request ID. Otherwise additive YAML surface; definitions without conditions hash and run identically; snapshots gain additive loop fields for extensions and stop intent (schema version stays 2). Downgrade recovery and reading these executions with older binaries are unsupported.
