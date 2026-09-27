## 1. Definition and graph contracts

- [x] 1.0 Update the implementation checkout to main baseline 1c444a2 or newer without losing these artifacts; compare intervening specs and revalidate this change before editing Go code.
- [x] 1.1 Add version-2 task control/action types and strict YAML parsing in domain/config; verify total maps, strict types, mandatory ownership, unchanged current threshold precedence (workflow, machine, then 0.7) and omitted-field identity, removed-field-before-version diagnostics and version-1 loopless compatibility with table tests.
- [x] 1.2 Replace terminal-condition validation with shared typed scope DAG projection and control comparability checks; verify head/middle/tail/singleton controls, bypass branches, unordered controls, sibling restrictions and three-level boundary cycles.
- [x] 1.3 Include version and control semantics in canonical definition identity; verify changed action/version conflicts, key-order independence and unchanged loopless version-1 hash fixtures.

## 2. Versioned state and exact outcomes

- [x] 2.1 Define schema-4 paths, admitted/planned loop contexts, iterations_started, decision revisions, close summaries and task skip outcomes without synthetic attempts; verify flat/nested round trips and monotonic real attempt numbering.
- [x] 2.2 Implement authoritative storage/validation for decisions, complete suffix skips and historical invocation final paths; verify rejection of missing/duplicate loops, inconsistent paths, recursively skipped child/grandchild invocations, budget counts, delimiter-safe stable decision IDs and orphan decisions, incomplete skip sets and attempts in skipped contexts.
- [x] 2.3 Separate legacy read-only decoding from new execution validation and schema selection; verify active legacy static/conditioned/nested executions fail closed without file changes, terminal history remains readable, unknown terminal schema fails startup, new mutations return 409 and accepted legacy controls replay read-only, and loopless schemas 1/2 still recover correctly.
- [x] 2.4 Preserve submission replay identity across the version boundary; verify legacy read-only replay never activates work and converted version-2 definitions conflict with old request IDs.

## 3. Scheduling and local control

- [x] 3.1 Introduce recursive projected child-admission and phase gates in readiness/reservation; verify an independent internal root cannot start before its parent's head control and predecessor child invocations fully finish before an outer control.
- [x] 3.2 Implement proceed/continue/break/needs_attention resolution on the exact current attempt before dependent release; verify all positions, sequential controls, skipped later controls, local child break and independent work outside the owner scope.
- [x] 3.3 Build control candidates using copy-save-adopt transitions, including judgment/manual outcome publication, all skips and close/hold state; verify save-failure injection never exposes partial state or releases work.
- [x] 3.4 Implement bounded natural repeat, early continue, controlled exhaustion and fixed-count completion with overflow checks; verify empty-queue first pass, always-continue cap, proceed at cap, all-proceed natural completion with both exhaustion policies, attention at cap, and extension after closed/skipped exhaustion.
- [x] 3.5 Persist close-then-advance consumption and atomic descendant reset; verify duplicate callbacks/reconcile notifications, parent final-state preservation, child cap reset and historical extensions in automatic-attempt bounds.
- [x] 3.6 Integrate skipped outcomes with pass settlement, final workflow accounting and active-phase holds; verify skips alone do not fail execution, mandatory prefix failures still hold, unopened suffixes do not create speculative holds, and no quorum check releases work early.

## 4. Handoff and retry consistency

- [x] 4.1 Replace latest-under-prefix selection with recursive exact final-context resolution shared by readiness and manifests; verify final nested skip cannot select an earlier inner/outer success and queued retries hide replaced outcomes.
- [x] 4.2 Render explicit skip/absence outcomes and preserve labelled previous_iteration/ancestor_previous_iterations with exact paths; verify skips have no run/attempt/result fields, no historical fallback, stable ordering, and referenced successful artifacts remain hash-verified.
- [x] 4.3 Add dependency_skipped blocking and intervention guidance; verify outside report on selector can run while report on skipped review blocks, allowed_to_fail/threshold cannot waive absence, and inner skip blocking an outer consumer holds its owner.
- [x] 4.4 Extend retry consumption guards to projected admission and committed control decisions; verify prefix retries after proceed/attention return 409 even while paused, skipped tasks cannot retry, and failed controls before decision can retry at the same path.
- [x] 4.5 Preserve eligible fixed-loop/ancestor reopening without context reset when no consumer/decision fences it; verify old-context retry replay remains inert after advance and confirmed interrupted retries retain existing safety guards.

## 5. Judge and human controls

- [x] 5.1 Integrate existing asynchronous judging with atomic control settlement and manual outcome revisions; verify outages, low confidence, on_uncertain error, missing response, timeout and backend failure never accidentally select a control action.
- [x] 5.2 Implement current-live-hold overrides and reject committed release/close overrides; verify proceed preserves the pass, continue at cap exhausts with skips, break closes atomically, stop-resolved attention returns 409 on a new override, duplicate requests replay and late judge results cannot replace a manual decision.
- [x] 5.3 Preserve graceful stop with audited effective proceed for middle attention holds and skip-and-close for continue; verify pause, propagated ancestor stop, unopened children, unrelated holds, failure/uncertainty preservation and no next-pass dispatch.
- [x] 5.4 Keep extend invocation-local and bounded, with replay before lifecycle checks; verify overflow, skipped/done targets, post-parent-advance replay, exhaustion-only clearance and no suffix resurrection.
- [x] 5.5 Integrate pause/resume/cancel serialization with decisions; verify pause can record results/skips but cannot advance, resume does not waive semantic holds, cancel-before-judge wins, and already committed history survives cancellation.

## 6. Recovery and observable interfaces

- [x] 6.1 Recover schema-4 interrupted/judging/closed/held/advanced contexts conservatively; inject crashes before/after decision and advance commits and verify no repeated backend work, double decision, skipped pass or duplicate child initialization.
- [x] 6.2 Expose skip history, planned versus consumed counts, ordered controls, decision evidence, full flat/nested paths, effective actions and exact unavailable-result reasons in API/CLI views; verify response fixtures, historical views, task counts and polling behavior.
- [x] 6.3 Exercise HTTP stop/extend/override/retry idempotency and conflicts across restart, completion, cancellation and ancestor advance; verify accepted records keep their original target context and legacy mutation rejection remains explicit.
- [x] 6.4 Add a fake-backend end-to-end queue workflow with head selector, parallel reviews and tail consolidation; verify zero-review empty queue, multiple MRs, human hold, mid-pass continue, final report choice and persisted restart history.
- [x] 6.5 Preserve main backend preflight contracts across v2 submission/recovery/resume/retry/batches: verify 503 before acceptance, concurrent admissions, checking/failed diagnostics, skipped-but-rearmable agents, permanently skipped exclusions, managed failure latches and stale-success races.
- [x] 6.6 Verify current threshold precedence and history with main regression fixtures: workflow over machine over 0.7, omitted identity unchanged, unresolved recovery uses the startup default, and settled evidence remains immutable.

## 7. Documentation and completion checks

- [x] 7.1 Update README and existing loop/nested/condition examples to version 2 and task.control, and add a queue example; verify each example parses and documents phase barriers, cap counting, all-proceed default exhaustion holding at the cap, final skipped outputs and graceful stop versus break.
- [x] 7.2 Document upgrade preflight, read-only legacy history, previous-binary finish/cancel, new-request-ID migration and schema-4 rollback limits; verify instructions do not suggest editing snapshots or automatically replaying effects.
- [x] 7.3 Run gofmt on every changed Go file and verify no formatting diff remains; run go vet ./... and go test -race -count=1 ./... and resolve failures.
- [x] 7.4 Run openspec validate unified-loop-control-nodes --strict and review implementation against every delta requirement/scenario, including the design counterexamples; validate against the intended main-spec baseline as well as the local worktree, verify no shipped main scenarios or preflight/threshold clauses are lost, record evidence and reconcile artifacts before any archive.
