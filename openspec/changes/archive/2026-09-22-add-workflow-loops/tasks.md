# Tasks: add-workflow-loops

## 1. Domain and snapshot model

- [x] 1.1 Add `WorkflowLoopDefinition{MaxIterations int}`, `WorkflowDefinition.Loops` and `WorkflowTaskDefinition.Loop` (JSON `omitempty`), `WorkflowAttempt.Iteration` (`omitempty`), `WorkflowLoopExecution{Iteration, State}` with running/needs_attention/done states, and `WorkflowSnapshot.Loops`; split the schema constants into `WorkflowDefinitionVersion = 1` and `WorkflowSnapshotSchemaVersion = 2`. Verify snapshot round-trip tests retain loop state and iteration numbers, and that the definition hash of a loopless definition is byte-identical to the pre-change golden.
- [x] 1.2 Extend `LoadWorkflowSnapshot` to accept schema versions 1 and 2 (v1 loads loopless) and reject others. Verify store tests for both versions, newly written schema-2 snapshots, and the fail-closed path for unknown versions. Do not implement downgrade conversion or relabel new snapshots as schema 1; compatibility is upgrade-only.

## 2. Configuration and validation

- [x] 2.1 Parse `loops` and task `loop` labels in `rawWorkflow`/`rawWorkflowTask` with strict-field enforcement inside the workflow subtree. Verify config tests: loops parse, unknown loop fields fail, `max_iterations` is required.
- [x] 2.2 Extend `ValidateWorkflowDefinition`: reject missing/non-positive `max_iterations`, `loop` references to undeclared loops, loops with no member tasks, dependencies between tasks of different loops, and dependency cycles through loop boundaries via condensed-graph detection (each loop collapsed to one node). Every loop rejection names the offending loop/task and includes a corrective example via a shared error helper. Verify table-driven validation tests covering each rejection, each error's name-and-example content, and acceptance of valid single-level, sibling-loop, and outside-dependency shapes.
- [x] 2.3 Assert replay identity: changing `max_iterations` or a task's `loop` label changes the definition hash; loopless definitions keep the golden hash.

## 3. Scheduler iteration engine

- [x] 3.1 Make task-state recomputation iteration-aware: "last attempt" for body tasks means the last attempt of the loop's current iteration; body tasks without a current-iteration attempt evaluate as pending with dependencies checked against current-iteration settlement. Verify loopless behavior is unchanged (existing suite) and body tasks re-evaluate after an iteration advance.
- [x] 3.2 Implement loop advance and re-arm: when every body task has an acceptable settled current-iteration attempt and the iteration is not final, increment the loop's iteration (re-arm) and let recomputation reset body tasks to pending; on the final iteration mark the loop done so outside consumers' readiness resolves against final-iteration attempts. Stamp dispatched attempts with the loop's current iteration. Verify manager tests: a 3-iteration loop produces three numbered attempts per body task; a non-tolerated body failure or blocked body task holds the execution for intervention; tolerated failures re-run next iteration; sibling loops run independently.
- [x] 3.3 Keep pause, cancellation, and timeouts per-attempt and iteration-neutral: pause holds new dispatch while the current iteration drains; cancellation closes the current iteration's unsettled work; `enforceTimeoutsLocked` unchanged. Verify manager tests for pause-mid-iteration and cancel-mid-iteration.

- [x] 3.4 Implement durable loop failure/blocking holds before final-state derivation and dispatch, with loop/task/iteration attention reasons. Keep outside consumers pending with loop-wait reasons, stop new dispatch and all loop advance, and allow live work to finish. Verify mandatory failure with/without outside consumers, unmet threshold after tolerated failures, failed outside prerequisite, independent live/ready branches, and no hold for acceptable tolerated failures.

- [x] 3.5 Verify the automatic dispatch bound independently of explicit retries: a two-task, three-iteration loop has six initial attempts even with an additional eligible retry; a one-task, one-iteration loop accepts successive eligible explicit retries at iteration 1 and completes without creating iteration 2. Verify failures never cause automatic retries and no retry-count cap is introduced.

## 4. Iteration-scoped handoff and carry-over

- [x] 4.1 Extend manifest building: same-loop `needs` resolve to the dependency's current-iteration committed attempt; outside `needs` keep single-attempt resolution; dependency entries carry the iteration number. Verify manifest tests for same-iteration resolution and outside stability across iterations.
- [x] 4.2 Add the previous-iteration outcomes section: for body tasks dispatching in iteration k > 1, include all body tasks' iteration-(k−1) committed attempts (sorted task ID, states, verdicts, errors, result references) in the manifest, rendered by the prompt builder as a delimited readable-references block. Verify a manager test where an upstream implementer's iteration-2 manifest contains the reviewers' iteration-1 result files.
- [x] 4.3 Confirm existing artifact verification covers carry-over references (missing/changed prior-iteration results hold the execution). Verify a fault-injection test via the recording store.

## 5. Recovery and retries

- [x] 5.1 Preserve loop iteration counters across restart; an interrupted body attempt holds the execution as today. Verify a recovery test with implement → review: after both finish iteration 1, crash during implement in iteration 2 of 3; confirmed retry is accepted despite review's iteration-1 attempt, preserves iteration 1 and counter 2, and review in iteration 2 consumes the retried result before iteration 3 starts.
- [x] 5.2 Make task retries iteration-aware: new retries target the latest failed/interrupted attempt in the current iteration and retain that iteration. Same-loop transitive descendant reservations block only in that iteration; any outside descendant reservation still blocks. Preserve the all-history guard for tasks outside loops. Serialize validation/reservation with advance and dispatch, and reopen an eligible done loop as running at the same iteration. Verify tests accepting retry after earlier-iteration descendants, rejecting same-iteration and outside descendant consumption, rejecting historical retries after advance, replaying an accepted retry request after advance without new work, and reopening a done loop before outside consumption. Keep existing retry, cleanup-confirmation, pause, and loopless tests passing.

- [x] 5.3 Extend retry attention gating to allow eligible retries under loop failure/blocking holds and recoverable interruptions. Recompute blocked descendants against queued retries and clear only resolved reasons. Verify multiple queued repairs, paused repair, failed outside predecessor repair, unchanged consumed-result guards, and that unrelated artifact/judge uncertainty still prevents dispatch.
- [x] 5.4 Preserve failure holds across restart and separate recovery actions from permission to dispatch. For loop executions, accept valid resume with 200/current view while unresolved causes keep needs_attention; independently revalidate restored artifacts and re-attempt safely persistable held classifications with verified responses. Retain unrelated reasons and the classification hold until settlement, deduplicate concurrent judge calls, and preserve cancellation precedence and existing loopless behavior. Verify failure-only resume without dispatch, combined loop failure + judge outage followed by resume then retry, restored artifact + loop failure, repeated resume during classification, actual storage errors, and cancellation cleanup requirements.
- [x] 5.5 Commit iteration advance before next-iteration backend work, with atomic or derivable task reset. Add fault-injection recovery tests immediately before and after the advance commit, before the first next-iteration reservation, after that reservation (normal interruption recovery), and on advance save failure. Verify no repeated completed iteration, skipped iteration, duplicate initial attempt, or dispatch from unsaved state.

## 6. Views and API

- [x] 6.1 Expose `iteration` on attempt views and per-loop state (`name`, `iteration`, `max_iterations`, `state`) on the execution view, with task counts still counting tasks. Verify API view tests for mid-loop observation (iteration 2 of 3, numbered attempts), loopless views unchanged, and needs_attention with actionable loop/task/iteration causes that wake workflow wait.

## 7. Documentation and examples

- [x] 7.1 Document loops in README.md: syntax, the mandatory `max_iterations`, the automatic dispatch bound with explicit user/coordinator retries excluded, no retry-count or elapsed-time guarantee, iteration-scoped handoff, carry-over, per-iteration tolerance composition, recovery semantics, iteration-scoped retry eligibility and immutable prior iterations, failure holds with retry/cancel intervention and independent resume recovery (200 may still mean needs_attention), iteration-boundary recovery guarantees, upgrade-only snapshot compatibility with downgrade support out of scope, and the stage boundaries (no conditions or nesting yet).
- [x] 7.2 Add `examples/workflow-loop.yaml` (implement → review ×3 with carry-over, then an outside report) and confirm all example configs still load.

## 8. Final checks

- [x] 8.1 Run `gofmt` on changed Go files, `go vet ./...`, and `go test -race -count=1 ./...`; all green.
- [x] 8.2 Run `openspec validate add-workflow-loops --strict` and walk every delta scenario against the implementation and tests before archiving.
