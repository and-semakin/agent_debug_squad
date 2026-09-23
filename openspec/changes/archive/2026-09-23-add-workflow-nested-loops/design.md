## Context

The existing engine in `internal/workflow` stores a flat map of loop executions, selects attempts by a local iteration number, and rejects cross-loop dependencies. Retry guards treat every reservation outside the producer's loop as consumption. These assumptions must change together; adding a second integer cannot represent arbitrary nesting. See proposal.md for the user-visible workflow.

## Goals / Non-Goals

**Goals:** one context model shared by scheduling, manifests, retries, conditions, recovery, and views; explicit transition boundaries; unchanged behavior for definitions without nesting.

**Non-Goals:** dynamic graph mutation, automatic retries, backend session reuse, workspace isolation, new verdict policies, or new control request parameters. The existing loopless versus loop recovery/resume behavior remains unchanged. A loop invocation is a grouping of fresh task conversations, not a backend session.

## Decisions

### 1. Use complete iteration paths

Definitions form a forest of globally named loops. A task has one direct owner; `subtree(L)` includes L's directly owned tasks and all descendant tasks. The body of a loop is its whole task subtree; direct members are only tasks whose `loop` names it. Distinguish dependency descendants (reachable through `needs`) from loop descendants throughout the implementation.

An iteration path is an ordered list of `{loop, iteration}` entries from the root loop to the task's direct owner. For example:

```json
[{"loop":"outer","iteration":2},{"loop":"review","iteration":1},{"loop":"check","iteration":3}]
```

A nested loop invocation is identified by its loop name plus the ancestor prefix. Its individual iterations append that loop's own counter. Root loops have one invocation per execution. Counters start at 1. Advancing any ancestor creates a distinct descendant invocation even if local counters repeat. Keep per-task attempt numbers monotonically increasing across all contexts and retries.

In schema-3 executions, persist and expose `iteration_path` for **all loop-owned** loop states, attempts, views, manifest consumers, and dependency/carry-over entries, including root-owned tasks with one-entry paths. Keep `iteration` as the direct owner's counter and validate agreement with the last entry. Workflow-scope tasks have no loop path and omit this field. Schema-1/2 nonnested executions retain their old wire format; normalize root identities internally as `[owner:iteration]` and workflow-scope identities as `[]`. Compatibility is per execution schema, not per nesting depth within schema 3. Control targets and summarized contexts use the same complete path representation. Do not add `Round`.

The snapshot has exactly one current loop-state record per declared loop, including done loops. Validate duplicate/missing/extra loop keys, path ancestry, positive counters, local counter agreement, and current parent/child prefix agreement before recovery. Missing required paths on schema-3 loop-owned attempts are corruption; omitted workflow-scope paths are valid. Historical attempts may retain earlier prefixes; current queued/running/judging work must belong to the current path. An interrupted attempt superseded by a later accepted retry is historical, even after an ancestor advances. Only an unresolved latest interruption must match the current path. Reject competing live contexts and done parents with unfinished children. Never reconstruct missing identities from append order.

Alternatives: a `(round, iteration)` pair collides at depth three. Opaque invocation IDs with parent links also work, but require an additional identity graph to interpret history; explicit paths fit the existing readable snapshots.

### 2. Validate each scope separately

Only omission means no parent. A present YAML `parent` must be a nonempty string naming a declared loop; reject empty strings, whitespace-only strings, null, numbers, booleans, sequences, and maps. Do not trim or coerce values. Include valid parent names in definition hashing; omitted fields keep legacy hashes. Reject missing parents, self-parenting, cycles, and empty subtrees. A loop containing only child loops is valid when its subtree has tasks; a conditioned loop must directly own its `until_task`.

For each scope (workflow root and every loop), construct a dependency graph whose nodes are directly owned tasks and immediate child loops. For each dependency edge with both endpoints anywhere in the scope subtree, map each endpoint to its directly owned task or to the immediate child loop containing it; omit edges internal to the same child node, because that child is checked separately. Reject cycles at any scope, as well as raw task cycles. This catches `inner.A -> outer.X -> inner.B`: the outer graph contains `inner -> X -> inner`, even when the task graph is acyclic. At workflow scope, an edge from a grandchild task to a workflow task projects from the producer's root ancestor loop to that workflow task.

Allow direct dependency edges between equal or ancestor-related owners, and between workflow-scope tasks and loop tasks. Reject direct edges between unrelated loop branches; explain how an explicit task in the least common enclosing scope can transfer results. Such a bridge remains subject to boundary-cycle checks.

Require `until_task.loop == loopName`, declared verdicts, mandatory success, and existing complete action mappings. Every other task in that loop's subtree must reach the condition task through `needs` edges within the subtree. Keep helpers for direct members and subtree members separate. The condition task reads its own exact current path. Sharing one nested condition as an ancestor's condition is deliberately rejected: a separate ancestor task makes the decision boundary explicit. A container-only loop is therefore static; an author who wants a condition must add a directly owned condition task consuming the child result.

### 3. Define transitions before scheduling

At submission, initialize each root at iteration 1 and recursively initialize children under the current parent paths. A loop's current iteration settles acceptably only when all directly owned tasks have acceptable committed current-context outcomes and every immediate child invocation is done. This is recursive; queued, running, judging, interrupted, failed mandatory, and blocked work prevents settlement.

There are two distinct transitions:

- **Complete:** mark the invocation done at its existing path. Preserve descendant counters, done states, outputs, extensions, and audit history. Never initialize children here.
- **Advance:** permitted only after acceptable settlement and the loop's continuation decision allows another iteration within its cap. Increment this loop's local counter; reset its direct tasks; recursively create fresh child invocations at local iteration 1, clearing each child's invocation-local extensions and stop intent and resetting descendant tasks. Preserve all attempt and control history. The advancing loop retains its invocation-local extensions. Pending stop intent forbids this transition; it is never carried through a permitted advance. If advance commits before a new stop is accepted, that stop targets the new current iteration.

Process transitions in deterministic postorder (siblings sorted by loop name). Derive holds before each advance decision and again after committed transitions or outcomes, before further advance or dispatch. A static parent may advance in the same pass its final child completes. A conditioned parent must first run and settle its directly owned condition task: subtree sink and exit gates prevent that task from settling before the children are done. Once that task settles with an attention verdict, its hold blocks further advance and dispatch. A loop advances at most once per pass; descendant reinitialization is part of its ancestor's advance, not an extra iteration advance.

Serialize controls, reservations, completion, and transitions with the existing manager lock. Apply advance and all subtree resets to a candidate snapshot and adopt it only after a successful save. A failed save releases no work. Recompute readiness and global holds before dispatch. Existing cancellation, pause, storage, judging, and failure gates remain in force. Local stop resolution runs child-first to a fixed point even while unrelated holds exist; it completes eligible stopped invocations without advancing any loop or bypassing dispatch gates.

### 4. Resolve inputs within a context

Use one context resolver for readiness, manifest construction, condition selection, and attempt lookup. A dependency is selected as follows:

| Producer relative to consumer | Selected outcome and completion gate |
| --- | --- |
| Outside all loops | Existing single settled outcome |
| Same direct owner | Latest attempt at the exact current path, after acceptable settlement |
| Ancestor owner | Outcome at the consumer path truncated to that owner |
| Descendant owner | Final outcome inside the consumer's current iteration; every intervening child invocation must be done |
| Loop producer, workflow-scope consumer | Final outcome after the invocation of the producer's root ancestor loop completes |

A selected failed outcome is acceptable only under existing tolerance and success-threshold rules. Never fall back to another context when the selected context has no outcome. Use the latest attempt at a context to determine readiness: a queued retry supersedes an earlier committed failure and remains pending.

For a completed subtree iteration with path P, select each direct task's final committed attempt at P and each descendant task's final committed attempt under prefix P. This produces one outcome per task, including the successful retry when present. Per-task append order can select the final matching attempt only after the context/completion gates have established the intended completed subtree.

For carry-over, take the consumer's owner and then each ancestor from nearest to farthest. For each level whose current local counter exceeds 1, decrement only that level's counter, retaining its ancestor prefix, and summarize that previous iteration's entire subtree. The owner's section retains `previous_iteration`; ancestor sections use a new `ancestor_previous_iterations` collection containing the summarized `iteration_path` and ordered outcomes. No section exists for a level at iteration 1. Sections carry task/agent/attempt/path/state/verdict/error and verified successful result references, sorted by task ID. In schema 3, add `previous_iteration_path` alongside the existing `previous_iteration` outcome array when that array is present, so both owner and ancestor sections identify the summarized context explicitly. The split retains the existing array and flat manifest contract; `previous_iteration` is not an object. Do not replace it with a new unified wire-format collection.

Handoffs contain final outcomes only, not a transcript of every inner iteration. For the target example, the next implementer receives the last inner implement/review/consolidate outcomes and the outer test result. Earlier inner attempts remain inspectable history. Artifact checks cover every referenced result, not all unrelated historical files. Prompts point to the saved manifest and result files without embedding result bodies; implementations can cache successful verification of identical path/hash pairs within a dispatch decision, but cannot omit entries or silently bypass a missing reference. This is explicit result transfer, not inherited chat history.

### 5. Scope retries by the shared enclosing iteration

Replace the flat guard in `assertNoDescendantAttempts`; changing attempt lookup alone is insufficient. A retry must target the latest failed/interrupted attempt at the producer's exact current path, with every ancestor prefix still current. A changed ancestor makes the target historical even if its local iteration is again 1.

For each transitive dependency descendant with reservations, find the deepest loop common to the producer's and consumer's owner chains (the same owner counts). A reservation blocks retry if its path prefix through that common loop equals the producer's prefix. If there is no common loop, any reservation blocks. Thus:

- Same-loop consumers block only within the same exact iteration path.
- Inner consumers of an outer producer block anywhere within that outer iteration.
- Outer consumers of an inner producer block within the same outer iteration, regardless of the inner local counter.
- Consumers in another branch reached through a bridge block within their common ancestor iteration.
- Workflow-scope consumers and producers retain the existing whole-history guard.

This remains a conservative transitive reservation guard, not just a direct-manifest scan. It preserves the established guard for flat definitions and ignores reservations from previous shared enclosing iterations. Every recorded descendant attempt counts as a reservation regardless of state (queued, dispatching, running, judging, cancelling, succeeded, failed, interrupted, or cancelled); independent tasks do not.

On an eligible retry, reopen a done owning invocation and any done ancestors at the same paths, atomically with the queued reservation. Keep counters, extensions, and stop intent; do not reinitialize descendants. Recompute blocked tasks and holds. Do not reset independent successful work. Replay accepted retry IDs before current-context eligibility checks, as today. After the retry settles, use the ordinary stop/condition/cap decision again. A capped static or stopped invocation completes again at the same path; retry does not grant another iteration. Do not invent a bypass around the reservation guard to reopen conditioned loops whose condition task already consumed the failed result.

Condition overrides use the same exact-context check plus existing judging/load-bearing guards. Old-context mutations are rejected, but replay of an already accepted control returns its existing result without rewriting history. A settled verdict in a done owning loop has no live condition hold: a new override returns 409 and never reopens the loop or its ancestors. A replay of an already accepted override still succeeds without new effects. Keep this contract in the verdict-judge delta; no judge provider changes are required.

### 6. Bind controls to the invocation accepted at serialization

Stop the target and all currently unfinished descendants in one saved transition. A nested target may itself have descendants; “local” means it does not stop ancestors or siblings. Stop completes each current iteration normally and never cancels workers or waives failures. Parent completion still waits for children. Propagated intent has exactly the same precedence as direct intent: it clears a child's condition-action/exhaustion hold once that child iteration is otherwise acceptably settled, but not failure or judging holds. A future parent iteration initializes a stopped child fresh. Even a child initialized at iteration 1 with no dispatched attempt finishes that iteration on stop; cancellation is the operation for abandoning unstarted work.

Extensions belong to the target invocation, persist across its local iteration advances, and clear only when an ancestor creates a new invocation. Root extensions therefore last for the execution. Preserve execution-wide replay IDs and existing conflict fields. Store the accepted target path and affected descendant paths in control audit records. Replaying a control after parent advance acknowledges the old action and does not apply it to the new invocation. New IDs apply to the invocation current when serialized; no compare-and-set parameter is added.

Without extensions, each task's automatic attempts are bounded by the product of declared caps on its owner chain. With a finite set of accepted extensions, use the maximum effective cap ever granted to an invocation of each named loop for a conservative product bound. Do not multiply only current caps: a reset may have cleared a larger historical extension. Explicit retries remain excluded; indefinite external intervention is not a finite execution guarantee.

### 7. Version nested persisted state explicitly

Keep YAML workflow version 1, endpoint paths, and control request bodies. Add optional domain fields; backend adapter interfaces stay unchanged. Definitions without parents keep their existing hashes and omit all new public fields/manifest sections.

Select schema 3 for any execution whose definition contains a parent. Select schema 2 for newly saved nonnested executions, including recovered schema-1 executions as today. The new loader supports schema 1/2 for supported nonnested definitions and schema 3 for nested definitions with valid paths. Reject mismatched schema/definition combinations and malformed paths before dispatch. Existing binaries reject schema 3 using their existing version check.

### 8. Make reason strings deterministic

In schema-3 executions, render a loop path as `name=positiveDecimal/name=positiveDecimal`, root first, without spaces or leading zeroes. Existing identifier syntax excludes `=`, `/`, and `:`, so this occupies one colon-delimited field. Use the five exact reason templates specified in workflow-lifecycle for loop failure, blocking, condition attention, exhaustion, and loop waiting. Keep schema-1/2 reason strings unchanged and make internal hold classification accept both schemas by family prefix.

A loop-wait reason identifies the actual completion barrier: the immediate child of the consumer's scope containing the producer, or the producer's root ancestor for a workflow-scope consumer. Render that barrier's current path, not the producer's innermost loop. A root bridge waits for the entire producer root to finish; it is not a channel for iteration-by-iteration exchange between roots.

### 9. Check the authoring model on three levels

A compact valid dependency graph is `implement -> review -> consolidate -> test -> accept`. `implement`, `review`, and `consolidate` belong to `review_loop`; its condition is `consolidate`. `test` belongs to its parent `test_loop` and is that loop's condition. `accept` belongs to the root `delivery_loop` and is the root condition. Every task reaches each conditioned ancestor's sink without extra bridge tasks. Each task uses its own agent name. The two-level target is the same graph without `delivery_loop` and `accept`.

This example needs explicit exit dependencies. Merely setting `parent` adds no ordering edge and no workspace snapshot: a directly owned static-loop task without a dependency on a child may run concurrently with that child. A `break` completes only its owning invocation; static ancestors still repeat their declared budgets.

## Risks / Trade-offs

- [Cross-cutting context selection] -> Centralize normalization, exact match, prefix match, and subtree outcome selection; cover scheduling, retries, conditions, and recovery with the same three-level fixtures.
- [Condition outcomes expose new holds] -> Re-derive gates before each advance decision and dispatch; test a parent condition settling after its children complete.
- [Long paths and repeated ancestor context] -> Store result references instead of response copies. The number of carry-over entries is bounded by the sum of the included subtree sizes, up to task count times nesting depth; no constant-size context guarantee is claimed.
- [Conservative retries reject some theoretically safe repairs] -> Preserve reservation safety and document the shared-context rule; finer input invalidation is outside this change.
- [Nested state cannot be downgraded] -> Use schema 3 and fail closed rather than presenting parent removal as migration.

- [Controls follow a moving target] -> Stop/extend mean “the named loop current at serialization”, not “the invocation observed by the caller”. A fresh request can affect a new invocation after an ancestor race. This preserves the existing endpoint contract; no expected-path/CAS field is added here. Pause and inspect before a context-sensitive fresh control; reuse the same ID after a lost response. Neither replay nor observation alone provides CAS protection.
- [Whole-subtree sinks constrain authoring] -> Optional tasks still need a path to each ancestor condition; use a consolidator and explicit exit edges. The three-level example above exercises the rule. Relaxing sinks is deferred because it changes what a condition claims to summarize.
- [Global attention freezes independent branches] -> Keep the existing global intervention barrier for a predictable repair boundary. Document that a held child prevents ancestor settlement and unrelated dispatch. Per-branch intervention is a separate scheduler change.
- [Large finite budgets] -> Four levels with cap 5 permit 625 initial attempts per leaf before retries. The historical maximum-cap product is a conservative upper bound, not a remaining-work estimate or a spending limit. Document author budgeting; a global cap is outside this change.
- [Carry-over requires referenced historical artifacts] -> A damaged referenced output holds dispatch until restored, or the user cancels and starts another execution. No implicit omission or opt-out is added: that would silently change declared inputs. Unreferenced historical files are not checked merely because they exist.
- [Bridges and completion barriers cost work] -> Sibling handoff uses an explicit agent task; consumers above a loop receive its final result, with no intermediate-iteration pipelining. At workflow scope a bridge transfers a completed root result once. Document this limit rather than claiming arbitrary inter-loop messaging.
- [Consumed producer repair is conservative] -> Workflow-scope producers are immutable to retry after any descendant reservation; ancestor producers are similarly protected within a consumed ancestor iteration. Advancing may be impossible while held, so the fallback is cancellation and a fresh execution, not a promise to wait for advance. No rollback/invalidation protocol is introduced.
- [Controls are invocation-local] -> Stopping or extending a child does not change future invocations, and break does not propagate upward. Stop the ancestor to wind down its subtree, or cancel to abandon unfinished work. Extending each invocation remains an explicit decision.

## Migration Plan

Deploy the new binary without changing existing YAML. Existing nonnested executions need no manual conversion. Opt into nesting with `parent` and a directly owned condition for each conditioned loop.

A rollback can continue supported nonnested state. Nested executions require the new binary to finish or cancel; archive their session state separately before serving an older binary against a different compatible state directory. Do not delete `parent`, strip paths, or relabel schema 3 to make nested history load on an older binary.

## Review dispositions

The identifiers below refer to the three supplied reviews in attachment order: A (11 points, opening with the strict-validation assessment), B (19 points, opening with the whole-change assessment), and C (18 points, opening with the iteration-path assessment). “Clarified” retains the behavior and makes its boundary explicit; “Retained” declines the proposed semantic expansion and documents its cost. This record is rationale; the capability deltas remain the normative contracts.

| Review | Decision | Resolution or rationale |
| --- | --- | --- |
| A1 | Accepted | Canonical `name=iteration/name=iteration` reason tokens; all five loop reason families, including blocking and the actual wait barrier, are specified. Flat formats remain unchanged. |
| A2 | Accepted | Add the verdict-judge delta for complete-path eligibility, done-loop rejection, and replay. The classifier is unchanged. |
| A3 | Accepted | Extensions survive local advance; pending stop prohibits advance. A stop arriving after advance targets the new iteration. |
| A4 | Accepted | Explicitly reject conditioned containers without a directly owned condition and provide corrective guidance. |
| A5 | Retained | Whole-subtree sinks ensure conditions summarize all declared work. Document authoring cost and verify the three-level consolidator example. |
| A6 | Revised | Remove mixed root/nested representation inside schema 3. Every loop-owned object carries a full path; only old-schema executions and workflow-scope objects omit it. |
| A7 | Retained | Workflow-scope producers cannot be retried after consumption. Document the limit; changing consumed inputs requires a separate invalidation protocol. |
| A8 | Accepted | Remove the duplicate task-count sentence. |
| A9 | Accepted | Align both capabilities on final task states in each root's final subtree iteration. |
| A10 | Accepted | Define endpoint projection for the entire scope subtree, including a grandchild-to-workflow edge. |
| A11 | Confirmed | Replacing the earlier Round proposal is intentional, authorized by the request to rewrite the model, and necessary to avoid depth-three collisions. Loopless resume asymmetry remains outside scope. |

| Review | Decision | Resolution or rationale |
| --- | --- | --- |
| B1 | Accepted | Uniform schema-3 paths eliminate mixed representations within an execution while preserving nonnested compatibility. |
| B2 | Clarified; CAS deferred | Observation describes exact contexts; control endpoints intentionally address the current invocation at serialization. Document the race and pause/inspect workflow. Adding an expected-path precondition is a separate API change. |
| B3 | Clarified | Existing load-bearing eligibility already rejects done loops; path equality alone never authorizes override. Add explicit 409/no-reopen and replay scenarios in verdict-judge. |
| B4 | Accepted | Propagated stop has the same precedence as direct stop and completes otherwise settled children held on condition/exhaustion, preserving other holds. |
| B5 | Accepted with correction | Add held-child scenarios. Exits also include eligible verdict override for condition attention and cancellation; extension alone does not clear a condition-action hold. |
| B6 | Accepted | Every recorded attempt counts as a reservation regardless of state; a blocked task without an attempt does not. |
| B7 | Accepted | State re-settlement explicitly; test capped and stopped static invocations completing again without another iteration. Do not construct an otherwise forbidden retry through a consumed condition result. |
| B8 | Accepted | Require exactly one current record per declared loop and reject competing live contexts. Superseded interrupted attempts remain valid history, not extra live invocations. |
| B9 | Accepted | Static caps apply per invocation. |
| B10 | Accepted | Use “invocation of the producer's root ancestor loop”. |
| B11 | Accepted | Align projection on endpoints anywhere in the scope subtree, retaining direct tasks and immediate child nodes. |
| B12 | Accepted | Only omitted parent means root; reject empty/null/non-string values and do not coerce or trim. |
| B13 | Clarified; unified list deferred | previous_iteration is an existing array, not an object. Preserve it for compatibility and add explicit previous_iteration_path in schema 3; ancestor sections keep their context-bearing collection. |
| B14 | Retained with mitigation | Full final-outcome carry-over stays deterministic. Use manifests/result references and reuse identical artifact verification within a dispatch decision. Selective context/opt-out policies are a separate feature, not a silent truncation rule. |
| B15 | Retained with clarification | Verify every referenced artifact, not all historical files. Restore damaged required inputs or cancel and start a fresh execution; do not silently change inputs. |
| B16 | Retained | Global attention remains the shared repair barrier, even for independent roots. Document the scheduling cost; branch-local holds need separate semantics. |
| B17 | Retained | Finite is not affordable: document 5^4 = 625 attempts per leaf and that historical-cap products are conservative estimates. No global cost cap is added. |
| B18 | Retained | Explicit bridges cost an agent task and transfer completed invocations, not streaming iterations. Document expressiveness and ordering limits. |
| B19 | Accepted as verification | Walk the three-level implement/review/consolidate/test/accept example now; require a validated fixture during implementation. Sink semantics stay unchanged. |

| Review | Decision | Resolution or rationale |
| --- | --- | --- |
| C1 | Clarified | The old same-loop acceptance scenario was sufficient, not an exclusive rejection rule, so it did not logically reject nesting. Broaden it to subtree paths to remove misleading coverage. |
| C2 | Accepted | Narrow the legacy outside-consumption scenario to consumers outside all loops; ancestor consumers use shared-context guards. |
| C3 | Accepted | Required paths are explicit for every schema-3 loop-owned attempt; pathless workflow-scope attempts remain valid. Add recovery scenarios for both. |
| C4 | Accepted | Specify canonical reason templates and schema-specific compatibility. |
| C5 | Accepted | Define body as the whole subtree, distinguish direct membership and dependency descendants, and use direct ownership in condition-validation scenarios. |
| C6 | Accepted | Wait reasons name the actual root/intermediate barrier. A workflow bridge transfers a completed root once and does not implement iteration-level root exchange. |
| C7 | Accepted | Specify target handoff contents as final implement/review/consolidate plus outer test; intermediate attempts stay inspectable history. |
| C8 | Accepted | Add previous_iteration_path alongside the existing owner outcome array in schema 3. |
| C9 | Accepted | Remove the impossible child-completion/parent-verdict premise. A conditioned parent's own sink settles after children; re-derive holds before every further advance/dispatch. Static parents may still advance immediately after child completion. |
| C10 | Retained | Child controls apply to one invocation. Stop an ancestor to wind down future repeats; extend future invocations explicitly. Document that child exhaustion holds the parent before its test task runs. |
| C11 | Retained | Break is local. Add the five-invocation static-parent scenario; no implicit multi-level break is introduced. |
| C12 | Retained with scenario | Stop drains the current iteration even if not yet dispatched; cancellation abandons unfinished work. |
| C13 | Clarified; CAS deferred | A fresh control can target a new invocation after an ancestor race. Replay protects against duplicate acceptance, not stale first requests. Document this distinction. |
| C14 | Accepted | Uniform schema-3 root and nested paths across state, views, manifests, and audit. |
| C15 | Retained | Preserve global holds and explain their effect on unrelated roots and queued retries. |
| C16 | Retained with clarification | Document multiplicative cost and conservative historical bounds; neither is a remaining-work prediction. No execution-wide cap is added. |
| C17 | Retained with correction | Consumed ancestor inputs cannot be replaced in place. Waiting for advance is not a guaranteed repair path when held; cancel and restart if normal progression cannot reach a fresh context. |
| C18 | Accepted as explicit boundary | Parent declarations provide no task ordering or filesystem snapshot. Add a concurrency scenario and document the required needs edge. |
