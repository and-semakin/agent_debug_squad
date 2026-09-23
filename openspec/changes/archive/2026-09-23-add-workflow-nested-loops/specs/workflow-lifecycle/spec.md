# workflow-lifecycle Delta

## MODIFIED Requirements

### Requirement: Recovery never silently repeats uncertain work
Saved definitions containing `on_uncertain: hold` SHALL be rejected with an actionable unsupported-policy error before scheduling. The system SHALL NOT alias this value to `needs_attention`, silently fall back to the default, or automatically migrate the definition. On restart, the system SHALL preserve committed completed tasks and their artifacts, recompute readiness, and automatically continue an otherwise running execution with no uncertain attempts. Paused executions SHALL stay paused. Attempts reserved or running without a committed terminal outcome SHALL become interrupted and put the execution in needs_attention with new dispatch stopped. No such attempt SHALL be automatically resent or treated as an allowed failure. Attempts in the `judging` phase are an exception: their backend work is complete and their response artifact is committed, so recovery SHALL NOT interrupt them and SHALL re-run their verdict classification. For executions with loops, the persisted loop iteration counters SHALL survive restart: committed iterations and their artifacts are preserved, an interrupted body attempt recovers as any interrupted attempt, and retrying it continues the same iteration — the loop neither advances to the next iteration nor restarts from the first.

The new binary SHALL load supported nonnested snapshots of schema 1 or 2, and nested snapshots of schema 3. Newly saved nonnested executions SHALL use schema 2; executions with any parent declaration SHALL use schema 3. Schema-1/2 definitions containing nesting, schema-3 definitions without nesting, unknown schema versions, and damaged authoritative state SHALL fail closed before scheduling with an actionable error. Nested state MUST NOT be relabeled, flattened, or silently interpreted as nonnested state. Downgrade conversion SHALL NOT be supported; existing readers that support only schemas 1 and 2 SHALL reject schema 3.

Cancellation intent SHALL survive restart. Loop failure/blocking holds SHALL survive restart with the same iteration, failed outcomes, and actionable attention reasons; recovery MUST NOT automatically retry failed work or turn the hold into terminal failure. Iteration advance SHALL be durably committed before next-iteration backend dispatch, with task states atomically saved or derivable from the committed counter. Recovery around this commit MUST neither repeat a completed iteration nor skip an iteration. Persistence failure during advance MUST stop new dispatch.

For nested executions, recovery SHALL preserve complete iteration paths, invocation-local extensions and stop intent, and accepted control records. Current loop paths SHALL match the saved ancestry and current parent prefixes. In schema 3, every loop-owned attempt, including a root-owned attempt, SHALL carry a path matching its task's saved ancestry and local iteration; workflow-scope attempts SHALL omit it; historical paths SHALL remain distinct from the live context. Nonterminal attempts and unresolved latest interruptions SHALL match their owner's current path, and a done parent SHALL NOT contain an unfinished child invocation. An interrupted attempt superseded by a later accepted retry SHALL remain historical and SHALL NOT become unresolved again after an ancestor advances. Missing required loop paths, malformed or inconsistent paths, and loop paths attached to workflow-scope attempts SHALL cause recovery to fail closed rather than infer identities from attempt order.

An interrupted attempt SHALL recover at its original complete path and require the existing explicit confirmed retry; recovery SHALL NOT restart the child invocation or advance its parent. A parent's permitted advance and all descendant path/counter/control/task resets SHALL commit as one transition before any next-context reservation. Recovery before that commit SHALL advance once from the previous authoritative settled state; recovery after it SHALL continue the new context without matching old attempts with repeated local counters. A failed save SHALL release no descendant work. Parent completion SHALL preserve done descendants and never be recovered as an advance.

The authoritative snapshot SHALL contain exactly one current loop-state record for every declared loop, including done loops, and no undeclared loop records. Duplicate, missing, or extra loop records and competing unresolved attempts from different invocations of the same loop SHALL fail recovery before dispatch. Historical attempts SHALL NOT constitute additional current invocations.

#### Scenario: Crash between predecessors and consumer
- **WHEN** predecessor successes are committed and the process crashes before reserving their consumer
- **THEN** recovery dispatches the consumer without rerunning predecessors

#### Scenario: Crash around backend dispatch
- **WHEN** an attempt was reserved and the process crashes before committing its outcome, whether or not the backend received it
- **THEN** recovery marks it interrupted, stops new scheduling, and does not automatically repeat external work

#### Scenario: Restart while paused or cancelling
- **WHEN** a paused or cancelling execution is recovered
- **THEN** paused work stays paused and cancelling work does not start new tasks

#### Scenario: Crash during judging
- **WHEN** the process stops while an attempt is in the judging phase with its response already committed
- **THEN** recovery re-runs classification for that attempt without re-running the agent work and without marking the attempt interrupted

#### Scenario: Crash mid-iteration continues that iteration
- **WHEN** the process stops during iteration 2 of a 3-iteration loop with one body attempt left interrupted
- **THEN** recovery preserves iterations 1's committed attempts, keeps the loop at iteration 2, holds the interrupted attempt for retry, and a retried task completes iteration 2 before iteration 3 begins

#### Scenario: Pre-loop snapshots load unchanged
- **WHEN** a snapshot saved by a binary without loops (schema version 1) with supported definition values is recovered
- **THEN** it loads and recovers exactly as before loops existed

#### Scenario: Crash before iteration advance commits
- **WHEN** all iteration-1 outcomes are committed and the process stops before the advance to iteration 2 is committed
- **THEN** recovery advances to iteration 2 once without rerunning iteration 1 or skipping iteration 2

#### Scenario: Crash after iteration advance commits
- **WHEN** the advance to iteration 2 is committed and the process stops before any iteration-2 attempt reservation
- **THEN** recovery keeps the counter at 2 and starts eligible iteration-2 initial attempts without repeating iteration 1 or advancing to 3

#### Scenario: Advance persistence failure
- **WHEN** saving an iteration advance fails
- **THEN** no next-iteration backend work starts on the unsaved state, a storage error is observable, and recovery follows the last authoritative snapshot

#### Scenario: Removed uncertainty policy is rejected in saved state
- **WHEN** an execution saved with explicit on_uncertain hold is loaded by the new binary
- **THEN** loading fails with an actionable unsupported-policy error, no task dispatches, and the saved definition is neither rewritten nor silently interpreted as needs_attention

#### Scenario: Three-level interruption keeps its identity
- **WHEN** the process restarts with an unfinished attempt at A=2/B=1/C=1 and historical successes at A=1/B=1/C=1
- **THEN** the unfinished attempt becomes interrupted at A=2/B=1/C=1; old successes do not settle it, and a confirmed retry stays at that exact path

#### Scenario: Crash before parent advance commits
- **WHEN** a parent iteration and its subtree settled, but the process stops before the advance-and-reset snapshot commits
- **THEN** recovery preserves the old outcomes and performs the permitted transition once without repeating completed work

#### Scenario: Crash after parent advance commits
- **WHEN** A advances to 2 and resets B and C in one saved snapshot, then the process stops before new reservations
- **THEN** recovery keeps A=2/B=1/C=1 and starts only eligible work missing from that context

#### Scenario: Crash after parent completion
- **WHEN** the saved parent and descendant invocations are done when the process stops
- **THEN** recovery retains their final paths and outputs and creates no new child invocation

#### Scenario: Subtree advance save failure
- **WHEN** saving a parent advance with descendant resets fails
- **THEN** no backend dispatch uses any new subtree path; recovery follows the last authoritative snapshot

#### Scenario: Nested snapshot version is explicit
- **WHEN** an execution contains parent declarations and is persisted
- **THEN** it is saved as schema 3 with complete nested identities, and a schema-2-only reader rejects it

#### Scenario: Malformed nested state fails closed
- **WHEN** a schema-3 snapshot lacks a required path on a loop-owned attempt (root or nested), has the wrong ancestry or local counter, or has a current child prefix inconsistent with its parent
- **THEN** loading fails with an actionable error before backend dispatch

#### Scenario: Nonnested snapshots retain compatibility
- **WHEN** a supported schema-1 or schema-2 nonnested execution is loaded and later saved
- **THEN** it retains existing recovery behavior and is saved as schema 2 without new nesting fields

#### Scenario: Historical interruption stays repaired
- **WHEN** an interrupted inner attempt was successfully retried, its invocation completed, and the parent advanced before restart
- **THEN** recovery retains the interrupted attempt as history, does not demand it match the new current path, and does not recreate its resolved hold

#### Scenario: Duplicate current invocation is corrupt
- **WHEN** saved state has duplicate loop records or unresolved work for one loop under two ancestor prefixes
- **THEN** recovery rejects the state before dispatch rather than choosing a latest record

#### Scenario: Malformed snapshot JSON terminates recovery
- **WHEN** a saved snapshot contains an invalid array element, a malformed nested object, or truncated JSON
- **THEN** loading returns a corruption error without looping indefinitely or dispatching work

#### Scenario: Workflow-scope attempt has no path in schema three
- **WHEN** a schema-3 execution includes a workflow-scope task whose attempt omits iteration_path
- **THEN** recovery accepts that omission while still requiring paths on root-owned and nested loop-owned attempts

### Requirement: Execution observation includes outcomes and intervention
`GET /workflows` SHALL list historical and active execution summaries. `GET /workflows/{id}` SHALL expose execution state/revision, task and attempt identities/states, dependency-blocking reasons, active/ready counts, errors, output paths, and current run progress including pending permissions. Attempt states SHALL include the `judging` phase, and views SHALL expose per-attempt verdict data — verdict name, confidence, probability distribution, model, and source — together with judge-related attention reasons for uncertain verdicts and judge unavailability. Attempt views SHALL expose the iteration number for loop body attempts and the complete `iteration_path` for every loop-owned attempt in executions with nesting, and the execution view SHALL expose per-loop state: loop name, current iteration, and `max_iterations`. In executions with nesting, every loop view SHALL expose iteration_path, including root-only paths; child loop views SHALL additionally expose parent. For loops with conditions, loop views SHALL additionally expose the `until_task` name, the effective iteration cap (declared plus extensions), and the latest settled condition verdict. Unknown IDs SHALL return 404. Task states SHALL include pending, ready, dispatching, running, succeeded, failed, interrupted, blocked, and cancelled. Execution states SHALL include running, paused, needs_attention, cancelling, cancelled, succeeded, completed_with_errors, and failed.

An unresolved loop failure/blocking hold SHALL take precedence over final-state derivation and keep the execution in `needs_attention`, even when all tasks are settled. Loop views SHALL expose state `running`, `needs_attention`, or `done`; execution attention reasons SHALL identify the loop, iteration, and failed or blocked task, expose the underlying error or blocking reason, and indicate retry or cancellation as intervention options. Condition-related holds are observable the same way: `loop_exhausted` reasons SHALL name the loop and indicate extend, stop, or cancel; a `needs_attention` verdict action SHALL name the loop, iteration, task, and verdict and indicate override, stop, or cancel. When work settles and no loop hold or other attention reason remains, any blocked task or non-tolerated failed task SHALL make the execution failed; otherwise any tolerated failed task SHALL make it completed_with_errors; otherwise it SHALL succeed; for executions with loops these verdicts SHALL use the final task states in the final subtree iteration of each root; exhaustion itself introduces no terminal-failure outcome, and `on_exhaustion: succeed` completes the loop without overriding ordinary workflow outcome derivation. Uncertain attempts SHALL require attention rather than produce a final success. Workflow long-polling SHALL return on a terminal outcome, intervention, or wait expiry; expiry SHALL NOT cancel the execution. Waits SHALL accept integer `timeout_seconds` from 1 to 600, default 30, and return 400 otherwise. Permission intervention SHALL be exposed within one second of local publication under normal operation.

Task counts SHALL count tasks, not iterations or invocations. For executions with nesting, loop-related attention details SHALL identify the complete current path together with the existing cause, task, and intervention options; a loop name plus its local counter alone SHALL NOT represent the context. Control audit records in executions with nesting SHALL retain the accepted target path and, for propagated stops, every affected descendant path. Root control targets in such executions SHALL record their root-only path. Historical paths and contexts SHALL remain observable after resets. Existing nonnested response and audit shapes SHALL remain unchanged.

For executions with nesting, loop-related reason strings SHALL use a canonical path token: root-to-owner `name=iteration` entries separated by `/`, with positive base-10 counters, no leading zeroes, and no spaces. Loop identifiers SHALL follow the existing safe identifier grammar, which excludes `=`, `/`, and `:`. A path SHALL occupy exactly one colon-delimited field. The reason templates SHALL be:

- `loop_failure:<path>:<task>:retry_or_cancel`
- `loop_blocked:<path>:<task>:retry_or_cancel`
- `loop_attention:<path>:<task>:<verdict>:override_or_stop_or_cancel`
- `loop_exhausted:<path>:extend_or_stop_or_cancel`
- `waiting_loop:<barrier-path>`

Underlying task errors and blocking details SHALL remain exposed in the task view. Existing non-loop reason families SHALL retain their formats and link to attempts through the existing unique task/attempt identities. For loop exit waits, the barrier SHALL be the immediate child loop of the consumer's scope containing the producer; for workflow-scope consumers it SHALL be the producer's root ancestor loop. The reason SHALL show that barrier's current path while awaiting invocation completion, not promise release when the innermost producer first completes. Multiple eligible loop-wait barriers SHALL select deterministically by lexicographic direct dependency task ID. Executions without nesting SHALL retain their existing reason strings.

#### Scenario: Degraded review completes
- **WHEN** one optional reviewer fails and the verifier succeeds using the remaining results, with no blocked tasks
- **THEN** the workflow is completed_with_errors and exposes both the final report and failed reviewer details

#### Scenario: Permission wait
- **WHEN** an active task needs manual permission or automatic approval fails
- **THEN** workflow observation shows the current run/request details and wakes waiting callers while independent branches can continue

#### Scenario: Poll expiry or client disconnect
- **WHEN** an HTTP wait expires or its client disconnects
- **THEN** workflow execution continues and expiry returns the current state without cancellation

#### Scenario: Judging and verdict data are observable
- **WHEN** a verdict task's attempt is judging, or has settled with a verdict, or the execution is held on an uncertain verdict or judge unavailability
- **THEN** the execution view shows the attempt's judging state or recorded verdict data, and the corresponding attention reason when held

#### Scenario: Loop progress is observable
- **WHEN** a 3-iteration loop is mid-second-iteration
- **THEN** the execution view shows the loop at iteration 2 of 3, body attempts carry iteration numbers, and task counts reflect tasks rather than iterations

#### Scenario: Condition state is observable
- **WHEN** a conditioned loop is held on exhaustion or a needs_attention verdict action
- **THEN** the loop view shows until_task, the effective cap, and the latest settled condition verdict, and attention reasons indicate the available interventions

#### Scenario: Nested progress identifies every level
- **WHEN** C is at A=2/B=1/C=3
- **THEN** loop and attempt views show the complete ordered path, C's local iteration is 3, and loop views expose direct parent names

#### Scenario: Nested attention identifies the invocation
- **WHEN** C fails at A=2/B=1/C=1 after the same local counters occurred under A=1
- **THEN** attention details identify the A=2 path and do not conflate it with historical A=1 failures

#### Scenario: Control audit survives reinitialization
- **WHEN** a nested extension or propagated stop was accepted before an ancestor advanced
- **THEN** historical audit records still identify the affected paths while the current views describe the new invocations

#### Scenario: Nested reason has an unambiguous path token
- **WHEN** task repair fails at outer=2/inner=1
- **THEN** its loop failure reason is loop_failure:outer=2/inner=1:repair:retry_or_cancel, with the underlying error in the task view

#### Scenario: Workflow wait names the root barrier
- **WHEN** a workflow-scope consumer waits on a producer at A=2/B=1/C=3
- **THEN** its loop-wait reason is waiting_loop:A=2 until the A invocation completes, even if C has already completed

#### Scenario: Ancestor consumer wait names the intervening barrier
- **WHEN** a consumer directly in A=2 waits for a task at A=2/B=1/C=3
- **THEN** its loop-wait reason names A=2/B=1 as the barrier, and finishing C alone does not release it

#### Scenario: Flat reason formatting is stable
- **WHEN** a nonnested workflow enters loop failure, blocking, condition attention, exhaustion, or an outside loop wait
- **THEN** its reason strings retain their existing formats without path tokens

### Requirement: Explicit retries preserve history and input consistency
`POST /workflows/{id}/tasks/{task}/retry` SHALL accept a nonempty `request_id` and `expected_attempt`, creating a new queued attempt identity only for a failed/interrupted task that passes the descendant-reservation guard. For a task outside all loops, every transitive dependency descendant through `needs` MUST have no attempt reservations, as before. For a loop task, a new retry MUST target its latest failed/interrupted attempt at the exact current complete iteration path, with all ancestor iterations still current. A historical target SHALL return 409 even if its local counter equals the current one. The retry SHALL retain that path and receive the next monotonically increasing per-task attempt number.

For every transitive dependency descendant, a reservation SHALL block retry if producer and descendant have no common enclosing loop, or if the reservation matches the producer's current path through their deepest common loop, inclusive. The same direct owner counts as a common loop. Earlier shared enclosing iterations SHALL NOT block retry. Every existing recorded attempt of a transitive dependency descendant in the relevant shared context SHALL count as a reservation, regardless of state: queued, dispatching, running, judging, cancelling, succeeded, failed, interrupted, or cancelled. A blocked task with no attempt SHALL NOT count as a reservation. This guard SHALL remain conservative over transitive dependency descendants, including paths through an ancestor bridge between branches; it SHALL NOT reject because of independent tasks. In particular, same-owner consumers block only at the exact current path, nested consumers of an ancestor producer block within that ancestor iteration, ancestor consumers of a nested producer block within the shared ancestor iteration, and workflow-scope consumers block on any reservation.

If an eligible retry reopens a done owning invocation, that invocation and any done ancestors SHALL return to running at the same paths, atomically with the retry reservation. Counters, extensions, and stop intent SHALL be preserved; descendants SHALL NOT be reinitialized. Outside consumers SHALL wait until the required invocations settle again.

Guard evaluation and retry reservation SHALL be serialized with iteration advance and dispatch. Repeated identical retry requests SHALL return the originally created attempt; stale or conflicting requests SHALL return 409. Retry SHALL preserve prior artifacts, reset derived blocked descendants for reevaluation, preserve independent completed work, and use a fresh backend conversation.

Interrupted retries SHALL additionally require `confirm_previous_stopped: true`, recorded as the caller's cleanup assertion rather than a guarantee by Squad; a known active worker MUST NOT be bypassed. Cancelled/cancelling executions SHALL reject retries. A failed or completed_with_errors execution can reopen only if no other execution is active; a paused execution SHALL remain paused after retry. No retries SHALL happen automatically. Explicit retries requested by users or coordinator agents SHALL NOT count against the automatic dispatch bound or consume additional loop iterations; this change imposes no explicit-retry count limit, while all eligibility and consistency guards remain enforced.

Loop failure/blocking, condition-action, and loop-exhausted attention reasons MUST NOT by themselves reject an otherwise eligible retry; multiple eligible retries SHALL be queueable while those reasons or recoverable interruptions remain. Queuing a retry MUST NOT clear unrelated condition or exhaustion causes; actual dispatch SHALL wait for every attention cause to resolve. Artifact/storage and judge-related holds retain their resolution requirements; resume SHALL be able to address those causes independently of loop failures, as specified by the control requirement. After reserving a retry, the system SHALL recompute dependent readiness using the queued attempt as pending, remove only resolved failure/blocking reasons, and resume dispatch only when every attention reason is cleared and the execution is not paused. Blocked tasks without attempts SHALL be repaired by retrying eligible failed causal predecessors, not by retrying the blocked tasks themselves.

After an eligible retry repairs an invocation, settlement SHALL apply the ordinary stop, condition, and effective-cap rules again at the retained path. Retry SHALL NOT clear the completion-causing stop, raise the cap, or grant another iteration. Reopened static invocations already at their cap and stopped invocations SHALL complete again at the same path once acceptable settlement is restored. Completed independent work SHALL remain preserved.

#### Scenario: Retry before consumption
- **WHEN** a mandatory failed task is explicitly retried before any descendant has a reserved attempt
- **THEN** a new attempt is created and blocked descendants can proceed if its result later satisfies their conditions

#### Scenario: Failed output already consumed
- **WHEN** a tolerated failure was already included in a downstream attempt's inputs and the caller requests retry
- **THEN** retry returns 409 and does not invalidate or silently replace the consumed result

#### Scenario: Retry interrupted work
- **WHEN** a caller retries an interrupted task without confirming previous work stopped
- **THEN** the request is rejected without backend dispatch

#### Scenario: Concurrent retry submissions
- **WHEN** clients submit the same retry request twice or competing retries with the same expected attempt
- **THEN** at most one new attempt is created, with identical requests replaying it and conflicting requests rejected


#### Scenario: Previous-iteration descendants do not block recovery
- **WHEN** implement and its dependent review completed iteration 1, implement is interrupted in iteration 2, review has no iteration-2 reservation, no outside descendant has a reservation, and a valid retry confirms previous work stopped
- **THEN** the retry is accepted in iteration 2 despite review's iteration-1 attempt, prior history is preserved, and review's iteration-2 manifest references the retried implement result

#### Scenario: Same-iteration consumption blocks retry
- **WHEN** an allowed-to-fail body task fails and a same-loop descendant reserves an attempt in that iteration before a retry is requested
- **THEN** retry returns 409 without changing the consumed outcome or reserving another attempt

#### Scenario: Outside consumption blocks retry
- **WHEN** a loop finishes with a tolerated failed body task and a dependency descendant outside all loops reserves an attempt before that body task is retried
- **THEN** retry returns 409 and the outside attempt's saved inputs remain unchanged

#### Scenario: Historical outcomes cannot be retried after advance
- **WHEN** a loop advances from iteration 1 to iteration 2 and a new retry request targets a tolerated failure from iteration 1
- **THEN** retry returns 409 even if the task has no attempt in iteration 2 yet, and previous-iteration inputs remain unchanged

#### Scenario: Accepted retry request replays after advance
- **WHEN** a retry accepted in iteration 1 completes and the loop advances, and the client repeats that identical retry request
- **THEN** the original retry identity is returned without a new attempt, changing iteration counters, or rewriting history

#### Scenario: Eligible final-iteration retry reopens loop readiness
- **WHEN** a loop is done with a tolerated failed body task in its final iteration, no blocking descendant reservations exist, and a valid retry is accepted
- **THEN** the loop returns to running at the same iteration, outside consumers wait for settlement, and no extra iteration is created

#### Scenario: Repair a held iteration
- **WHEN** a loop is held by a failed producer with blocked body descendants and waiting outside consumers, and an eligible retry is accepted
- **THEN** the queued retry replaces the failure for readiness evaluation, derived blocked descendants become pending, resolved loop attention clears, and execution continues in the same iteration once no attention reasons remain

#### Scenario: Queue repairs for multiple failures
- **WHEN** two independent body tasks failed and the caller retries each while the execution needs attention
- **THEN** both eligible retries can be queued, the first does not waive the second failure, and dispatch resumes only after all attention causes are resolved

#### Scenario: Failure hold is durable and observable
- **WHEN** a loop is held on a committed task failure and the process restarts
- **THEN** the same iteration and failure hold are restored, no failed work is resent, and workflow wait returns needs_attention with the loop, task, cause, and intervention options

#### Scenario: Stop and retry across loops in either order
- **WHEN** loop A settled acceptably and is held on a condition action, loop B has a retryable mandatory failure, and the caller requests stop for A and retry for B in either order
- **THEN** both controls are accepted under their normal local guards, A's stop resolves its own cause without waiting for B, B's retry can be queued while A is held, and no work dispatches until all causes clear

#### Scenario: Retry queues under an exhausted sibling
- **WHEN** loop A is exhausted and loop B has an otherwise retryable failed task
- **THEN** retry of B may be queued, A's exhaustion cause remains, and dispatch waits until extension or stop resolves A as well

#### Scenario: Previous outer consumer does not block inner repair
- **WHEN** outer.test consumed inner.review in outer=1, inner.review fails at outer=2/inner=1, and no dependency descendant has a reservation in the relevant current shared context
- **THEN** retry succeeds at outer=2/inner=1 despite outer.test history from outer=1

#### Scenario: Previous inner consumer does not block outer repair
- **WHEN** inner consumed outer.setup under outer=1, outer.setup fails in outer=2, and current-context dependency descendants have no reservations
- **THEN** retry of outer.setup succeeds despite the old inner reservations

#### Scenario: Current nested consumption blocks outer retry
- **WHEN** a tolerated failed outer producer has a transitive inner consumer reserved anywhere within the same outer iteration
- **THEN** retry returns 409, including when that consumer is already on a later inner iteration

#### Scenario: Current outer consumption blocks inner retry
- **WHEN** a tolerated failed inner producer has an outer consumer reserved in their current shared outer iteration
- **THEN** retry returns 409 and preserves the consumed result

#### Scenario: Sibling consumption through a bridge is scoped
- **WHEN** a nested producer reaches a sibling consumer through an ancestor bridge
- **THEN** a reservation in their current common ancestor iteration blocks retry, while a reservation only in a previous common ancestor iteration does not

#### Scenario: Repeated local counter does not make history retryable
- **WHEN** a new retry names an old failed attempt at A=1/B=1/C=1 while the current context is A=2/B=1/C=1
- **THEN** retry returns 409 without a new reservation or history changes

#### Scenario: Retry reopens completed ancestors without reset
- **WHEN** a static nested workflow finished with a tolerated failed leaf, no dependency descendant has consumed it, and no other execution is active
- **THEN** an eligible retry reopens the leaf owner and done ancestors at their final paths, preserves independent completed tasks and invocation controls, and creates no new invocation

#### Scenario: Historical retry request still replays
- **WHEN** an accepted retry completed under outer=1, outer advanced to 2, and that identical request is repeated
- **THEN** the original attempt identity is returned without reserving work in either context

#### Scenario: Repaired capped invocation completes again
- **WHEN** a tolerated failed task in a completed static nested invocation at its cap is eligible for retry and that retry succeeds
- **THEN** the owning invocation and eligible reopened ancestors settle again at their retained final paths without an extra iteration or new child invocation

#### Scenario: Repaired stopped invocation remains stopped
- **WHEN** a stopped static invocation below its cap is reopened by an eligible retry and the retry succeeds
- **THEN** the preserved stop intent completes it again at the same path without spending the remaining budget

#### Scenario: Terminal descendant attempt still blocks retry
- **WHEN** a transitive dependency descendant has a failed, interrupted, cancelled, or succeeded attempt in the producer's relevant shared context
- **THEN** the recorded reservation blocks retry just as a queued or running attempt would

### Requirement: Loop conditions admit human intervention
`POST /workflows/{id}/loops/{name}/extend` SHALL accept a nonempty `request_id` and a positive integer `add_iterations`, durably raising the loop's effective iteration cap by that amount on a nonterminal, non-cancelling execution. Accepted extend requests SHALL be identified within the execution by request_id and bound to the loop name and add_iterations. Repeating an identical accepted request SHALL return the current execution view without a second increase, including after loop completion, execution completion, cancellation, or restart. This replay check SHALL precede lifecycle eligibility checks. Reusing the same extend request_id with a different loop name or add_iterations SHALL return 409 without mutation. Non-positive or non-integer amounts SHALL return 400; unknown execution or loop identifiers SHALL return 404; new requests against done loops or terminal or cancelling executions SHALL return 409. A successful extension SHALL resolve the target loop's loop_exhausted cause independently of other holds; the loop SHALL continue at the next iteration only once all attention causes are resolved and the execution is in running mode. Extension SHALL NOT clear a condition-action needs_attention cause or unpause the execution; extensions of a still-running loop raise its future cap. Extensions are audited as control events and participate in the automatic dispatch bound only after being granted.

`POST /workflows/{id}/loops/{name}/stop` SHALL accept a nonempty `request_id` and durably record stop intent for an unfinished loop of a nonterminal, non-cancelling execution. It SHALL let remaining tasks in the current iteration execute under normal dependency and pause rules, forbid the next iteration, and mark the loop done only after that loop's whole current iteration settles acceptably. The local stop decision and removal of its condition/exhaustion cause SHALL be permitted even if another loop needs attention; unrelated causes SHALL remain and still block all ordinary dispatch and iteration advance. Outside consumers SHALL wait until that completion and all global dispatch gates permit execution, then receive the current iteration's results. Stop SHALL NOT cancel workers, unpause the execution, waive failures/blocking or unresolved verdicts, or turn a failure hold into terminal failure. Accepted intent SHALL survive recovery and retries in the same iteration. When completion prerequisites are met, stop SHALL take precedence over mapped condition actions and exhaustion policy, clearing their holds; unresolved classification and other independent attention reasons still require resolution. Views SHALL expose pending stop intent as stop_requested.

Identical accepted stop requests SHALL replay successfully before checking current lifecycle state, even after loop or execution completion. Reusing the same stop request ID for another loop SHALL return 409. New stop requests against done loops, terminal executions, or cancelling executions SHALL return 409; missing request IDs SHALL return 400 and unknown identifiers SHALL return 404. A new request while stop intent is already pending SHALL succeed without advancing or restarting the loop. Both controls are serialized with iteration advance and dispatch and persisted before effects. A stop that wins serialization prevents advance; if advance committed first, stop applies to the new current iteration. Controls never bypass cleanup, retry, or verdict-hold rules.

For a nested loop, extensions SHALL belong to the invocation identified by its ancestor path. They SHALL persist across that loop's own iteration advances and clear only when an ancestor advance starts a new invocation. Root extensions SHALL persist for the execution. Every fresh child invocation SHALL start at its declared cap with no inherited stop intent.

Stopping a loop SHALL atomically record intent for it and all currently unfinished descendants. Stopping a nested loop SHALL leave ancestors and siblings unaffected; its own descendants SHALL still receive intent. Child-first stop resolution SHALL complete every eligible stopped invocation, including a stopped parent whose children have just become done, even while unrelated holds exist. It SHALL NOT dispatch work or advance iterations through any remaining hold. Parent completion SHALL preserve final descendant state; it SHALL NOT create new child invocations. Failed, interrupted, blocked, or unresolved judging work SHALL still require normal repair or cancellation.

Accepted control records SHALL identify the invocation current when the request was serialized. Existing request IDs SHALL remain execution-wide with the existing conflict fields. Identical replay after ancestor advance SHALL acknowledge the original action without extending or stopping the new invocation; a fresh request ID SHALL target the current invocation under normal eligibility rules. Audit records for executions with nesting SHALL retain the accepted target path and all affected descendant paths. A failed control save SHALL leave neither a partial subtree stop, a raised cap, nor a replay record.

A propagated stop SHALL have exactly the same priority as a directly requested stop on each affected invocation. If an affected child has an otherwise acceptably settled iteration held only by its condition action or exhaustion policy, propagated intent SHALL clear that child hold by completing the invocation without extension. It MUST NOT waive failures, unresolved judging, interruptions, or unrelated holds.

An unfinished held child SHALL prevent its parent from settling. A condition-action hold SHALL admit the existing eligible override, child stop, ancestor stop, or cancellation; extension alone SHALL NOT clear it. Exhaustion SHALL admit child extension, child stop, ancestor stop, or cancellation. None of these controls SHALL promise progress through independent unresolved causes.

A fresh stop/extend request SHALL address the named loop invocation current when serialized, without comparing it to the caller's earlier observation. An ancestor advance that commits first can change that target invocation. This is separate from observation identity and idempotent replay: replay prevents duplicate effects after acceptance but SHALL NOT provide an expected-context precondition for a first request.

#### Scenario: Extend releases an exhausted loop
- **WHEN** a loop is held on `loop_exhausted` and the caller extends by two iterations
- **THEN** its exhaustion cause clears and the extension is audited; the loop continues with the raised cap only when no other attention reason remains and execution is not paused

#### Scenario: Extend is idempotent per request
- **WHEN** the same extend request ID and amount are posted twice
- **THEN** the cap rises once and the second call returns the current view without another increase

#### Scenario: Conflicting extend replay is rejected
- **WHEN** the same extend request ID is reused with a different amount or loop name
- **THEN** the request returns 409 and the cap is unchanged

#### Scenario: Stop acts as a manual break
- **WHEN** a caller stops a loop held on a needs_attention verdict action
- **THEN** the loop completes at the current iteration, outside consumers receive that iteration's results, and the execution proceeds to its derived final state

#### Scenario: Stop on a done loop conflicts
- **WHEN** a new stop request, rather than replay of an accepted request, targets a loop already done
- **THEN** the request returns 409 and nothing changes

#### Scenario: Invalid control input is rejected
- **WHEN** extend is called with a zero or negative amount, or either control names an unknown loop
- **THEN** the request returns 400 or 404 respectively and no state changes


#### Scenario: Stop finishes the current iteration
- **WHEN** implement has completed, review is running, and another body task is pending when stop is accepted in iteration 2 of 3
- **THEN** normal scheduling finishes iteration 2, outside consumers wait until its acceptable settlement, and iteration 3 never begins

#### Scenario: Stop preserves failure intervention
- **WHEN** stop is accepted while the loop is held on a mandatory failure, interruption, or unresolved verdict classification
- **THEN** intent is recorded but the hold remains, no outside consumer is released, and repair is required before the iteration can complete or cancellation can abandon it

#### Scenario: Stop clears only condition and exhaustion holds
- **WHEN** all body attempts settled acceptably and a stop is accepted on a condition-action or exhaustion hold with no independent attention reasons
- **THEN** the loop becomes done at the same iteration without needing an override or extension while preserving independent attention requirements

#### Scenario: Stop survives restart and retry
- **WHEN** stop intent was committed and the process restarts during the current iteration
- **THEN** recovery retains the intent, interrupted work requires the normal confirmed retry, and completing the repaired iteration never starts another iteration

#### Scenario: Stop replay after completion
- **WHEN** an accepted stop completes the loop and the client repeats its request after losing the response
- **THEN** the current execution view is returned successfully without new effects even if the workflow is terminal

#### Scenario: Stop while paused
- **WHEN** stop is accepted while the execution is paused with unfinished body work
- **THEN** intent is persisted but new body dispatch waits for resume


#### Scenario: Extend replay after completion or cancellation
- **WHEN** an accepted extension is repeated with the same request ID, loop, and amount after the loop or execution finishes or cancellation begins
- **THEN** the current execution view is returned successfully and the cap is not increased again, while a new request in those states is rejected

#### Scenario: Extend replay survives restart
- **WHEN** an extension and its request record were committed before a restart and the client repeats the request afterward
- **THEN** the persisted record prevents a second increase and the current execution view is returned

#### Scenario: Extension and replay record commit together
- **WHEN** persistence fails while accepting an extension
- **THEN** no work is released on an unsaved raised cap, and recovery observes either both the cap increase and its replay record or neither


#### Scenario: Two held loops can both be stopped
- **WHEN** two loops have acceptable settled iterations held on condition actions and a stop is requested for each
- **THEN** each local stop removes only its own hold independently of the other, and downstream dispatch waits until all remaining attention causes clear

#### Scenario: Extension preserves pause and other causes
- **WHEN** an exhausted loop is extended while execution is paused or another loop needs attention
- **THEN** its exhaustion cause clears and the cap increases, but no iteration advances or task dispatches until running mode and all other attention requirements permit it

#### Scenario: Ancestor stop reaches all unfinished levels
- **WHEN** a stop targets A with unfinished descendants B and C and another done descendant
- **THEN** A, B, and C receive stop intent in one durable transition, the done descendant is preserved, and each stopped invocation completes its current iteration without advancing

#### Scenario: Nested stop remains local to its subtree
- **WHEN** a stop targets B inside A and B has child C
- **THEN** B and unfinished C receive intent, A and siblings do not, and a later A iteration initializes B and C fresh

#### Scenario: Nested extension lasts for one invocation
- **WHEN** B under A=1 is extended by two and advances its own iteration before A eventually advances to 2
- **THEN** the extension persists across B's local advance but B under A=2 starts at its declared cap

#### Scenario: Extension replay cannot extend a new invocation
- **WHEN** an extension ID accepted for A=1/B is repeated after A advances to 2
- **THEN** the request replays successfully without changing A=2/B's cap; a new extension requires a new ID

#### Scenario: Stop replay cannot stop a new invocation
- **WHEN** a stop ID accepted for A=1/B is repeated while A=2/B is running
- **THEN** the request replays successfully without setting stop intent on the new B or its descendants

#### Scenario: Stop resolution ignores unrelated holds but preserves dispatch gates
- **WHEN** a stopped child settles, its stopped parent now also settles, and an independent loop still needs attention
- **THEN** both stopped invocations become done without another control request, while the unrelated hold remains and no ordinary dispatch or advance occurs

#### Scenario: Propagated stop save is atomic
- **WHEN** persistence fails while recording an ancestor stop and its descendant intents
- **THEN** no partial stop or replay record is adopted, and recovery observes the last authoritative state

#### Scenario: New control after advance targets the current invocation
- **WHEN** parent advance commits before a fresh nested stop or extend request wins serialization
- **THEN** the new request applies to the new current invocation, and its audit record identifies that context

#### Scenario: Ancestor stop releases an exhausted child
- **WHEN** a child is held solely on loop_exhausted with acceptable settled work and a stop is accepted for its ancestor
- **THEN** propagated intent completes the child without raising its cap, removes its exhaustion hold, and allows normal remaining ancestor work subject to all other gates

#### Scenario: Ancestor stop releases a child condition hold
- **WHEN** a child iteration is settled and held solely on a needs_attention condition action when its ancestor is stopped
- **THEN** the child completes at that path with its verdict preserved and its condition hold removed; unrelated causes remain

#### Scenario: Held child prevents parent completion
- **WHEN** a child needs attention on exhaustion while its ancestor still has pending work
- **THEN** the parent cannot settle; extending the child or stopping the child or an ancestor can resolve that exhaustion under normal guards, and cancellation can abandon the execution

#### Scenario: Extension does not resolve child condition attention
- **WHEN** a child is held on a needs_attention verdict action and an extension is accepted
- **THEN** the cap increases but the child hold remains until eligible override, stop, or cancellation, and parent settlement remains blocked
