## 1. Definition tree and iteration identity

- [x] 1.1 Add optional `parent` parsing and hashing; verify absent-parent golden identities and tests for undeclared parents, self-parenting, cycles, valid forests, and explicit empty/null/non-string parent rejection without coercion.
- [x] 1.2 Add loop ancestry, direct-member, subtree, and common-ancestor helpers plus normalized iteration paths; verify exact/prefix matching at three levels with repeated local counters and container-only loops.
- [x] 1.3 Add schema-3 path fields to every loop-owned loop/attempt/input/view, including roots, and to controls and previous_iteration_path while retaining local iteration; verify JSON round trips, increasing attempt numbers, and unchanged nonnested serialized shapes.

## 2. Definition validation

- [x] 2.1 Implement allowed dependency relations and actionable unrelated-branch errors; verify same-owner, ancestor input, descendant output, separate roots, and sibling handoff through an ancestor bridge.
- [x] 2.2 Validate boundary cycles independently at workflow and every loop scope; verify raw-acyclic `inner.A -> outer.X -> inner.B` is rejected, including below a third level, while valid entry/exit tasks are accepted.
- [x] 2.3 Require directly owned condition tasks and whole-subtree sink reachability; verify borrowed child conditions, conditioned containers without direct tasks, and uncovered branches are rejected, and an inner review followed by an outer test condition is accepted.

## 3. Scheduling and condition transitions

- [x] 3.1 Centralize exact-context attempt selection for settlement, readiness, dispatch, and queued retry slots; verify a new A=2/B=1/C=1 cannot reuse A=1/B=1/C=1 outcomes.
- [x] 3.2 Separate invocation completion from advance; implement child-first settlement and candidate-snapshot advance/reset commits. Verify parent completion never resets children, parent advance resets all descendant tasks and controls, and failed persistence releases no work.
- [x] 3.3 Resolve dependency inputs with exact paths and completion gates across every intervening loop; verify ancestor stability, multi-level exit waits, and queued retries superseding committed failures.
- [x] 3.4 Evaluate conditions and overrides in their directly owned current context, re-deriving holds before further advance or dispatch; verify outer iteration 1 with inner iteration 3, parent attention after its own condition settles following child completion, historical override rejection, done-loop override rejection and accepted replay, and existing pause/cancel/failure/judge gates.
- [x] 3.5 Verify bounded automatic execution with three-level static caps, early breaks, and invocation-specific extensions; include the six-attempt example whose final reset caps alone would incorrectly imply two.

## 4. Carry-over

- [x] 4.1 Build whole-subtree previous-iteration outcomes and nearest-first ancestor sections using complete prefixes; verify outer test feedback, differing inner iteration counts, successful retry selection, and A=2/B=2/C=1 versus earlier repeated local counters.
- [x] 4.2 Render readable references and verify every carry-over artifact before dispatch; verify sorted entries, missing/changed artifact holds, and unchanged nonnested manifest shape.

## 5. Retry consistency and controls

- [x] 5.1 Replace flat descendant retry guards with shared-enclosing-context reservation checks; verify previous outer/inner consumers do not block repairs, current consumers do, bridged sibling guards are scoped, and workflow-scope guards remain unchanged.
- [x] 5.2 Preserve exact retry paths and atomically reopen done owning/ancestor invocations without resets; verify stale-context rejection, replay after ancestor advance, independent work preservation, queued repairs, interrupted-worker confirmation requirements, terminal descendant reservations, and same-path re-completion after successful repair of capped/stopped invocations.
- [x] 5.3 Propagate stops atomically and resolve stopped invocations child-first to a fixed point; verify three-level propagation releasing child condition/exhaustion holds, local subtree stops, unrelated holds, paused execution, initialized-but-unstarted children, and failed-save atomicity.
- [x] 5.4 Scope extensions and control audit records to accepted invocations; verify persistence across local advances, reset on ancestor advance, replay without affecting a new invocation, fresh controls after advance, and existing replay conflicts.

## 6. Persistence and recovery

- [x] 6.1 Select schema 3 for nested executions and schema 2 for nonnested saves; validate ancestry and path consistency at load. Verify supported schema-1/2 recovery, schema-3 round trips, mismatched schema/definition rejection, malformed or missing required schema-3 loop paths, valid pathless workflow tasks, duplicate/missing loop records, prompt rejection of malformed and truncated JSON, competing live contexts, repaired historical interruptions, and old-reader version rejection.
- [x] 6.2 Verify recovery before/after parent advance, after parent completion, during a three-level interruption, and after control persistence; assert no repeated/skipped contexts or accidental descendant reinitialization.

## 7. Observation and examples

- [x] 7.1 Expose uniform schema-3 loop paths and canonical path-bearing loop failure/blocking/attention/exhaustion/wait reason strings, and retain accepted paths in audit records; verify actual root/intermediate wait barriers, deterministic dependency selection, identifiers containing digits, historical identities after resets, internal reason-family gates, and unchanged nonnested strings, responses, and task counts.
- [x] 7.2 Document syntax, schema-specific context identity, final-only carry-over and artifact retention, workflow-scope/shared-context retry limits, whole-subtree sinks, global holds, current-at-serialization control races, invocation-local budgets, stop versus cancel, bridge costs, explicit needs ordering, and the 5^4=625 budget example in README; add `examples/workflow-nested-loop.yaml` with inner implement/review/consolidate and outer test. Include a three-level fixture following design section 9 to verify sink ergonomics. Verify all examples load and demonstrate a negative test verdict repeating the outer loop without misrepresenting backend failure as continuation.

## 8. Final verification

- [x] 8.1 Run `gofmt` on changed Go files, `go vet ./...`, and `go test -race -count=1 ./...`; require all checks to pass.
- [x] 8.2 Run `openspec validate add-workflow-nested-loops --strict` and review every delta requirement and scenario against implementation and tests, including unchanged flat-workflow behavior, before archive.
