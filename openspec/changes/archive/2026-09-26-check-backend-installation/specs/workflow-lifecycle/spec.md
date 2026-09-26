## MODIFIED Requirements

### Requirement: Workflow submission is explicit and idempotent
Serving a configuration SHALL NOT automatically start a new workflow. `POST /workflows` with a nonempty `request_id` SHALL create an execution of the configured definition and return 202 only after the backend-installation preflight succeeds for every agent referenced by its task graph. This includes downstream, loop and allowed-to-fail tasks, but excludes unreferenced agent definitions and machine backend sections. Preflight failure SHALL return 503 with an aggregate report before persisting an execution, accepting its request ID, reserving an attempt or dispatching any task. Structural invalidity and existing idempotency/conflict checks SHALL be resolved before preflight. An identical accepted replay SHALL return the existing execution without rechecking installations. Repeating the same request ID with the same resolved definition SHALL return the original execution with 200 without new work, including after restart. Reusing an ID with a changed resolved definition SHALL return 409. Only one nonterminal workflow execution per server session SHALL be admitted in v1; competing submissions SHALL return 409. New executions SHALL retain an immutable copy of their resolved definition and agent configuration. Invalid submissions SHALL return 400 without dispatch.

#### Scenario: Lost creation response
- **WHEN** a client retries a submission after losing its HTTP response
- **THEN** it receives the original execution and no additional agent attempt is created

#### Scenario: Restart does not create a new execution
- **WHEN** the server restarts with the same configured workflow
- **THEN** it recovers saved executions without treating startup as a new submission

#### Scenario: Definition changes
- **WHEN** configuration is edited after an execution was created
- **THEN** that execution retains its original definition and the changed definition requires a new request ID

#### Scenario: A downstream installation is missing
- **WHEN** the first task is runnable but a downstream or allowed-to-fail task references an unavailable backend
- **THEN** submission returns 503 naming every failed installation and affected agent, and even the first task remains unstarted

#### Scenario: Repair and resubmit rejected admission
- **WHEN** local installation preflight rejects a request ID before managed startup and the missing installation is repaired
- **THEN** resubmitting that request ID performs fresh checks and can create the execution because the rejection did not consume the ID

#### Scenario: Replay after installation disappears
- **WHEN** an already accepted request ID is replayed after an executable is removed
- **THEN** the original execution is returned with 200 without new checks or attempts

#### Scenario: Concurrent admissions
- **WHEN** two submissions race while preflight is in flight
- **THEN** no more than one execution is committed; an identical request waits for that admission and then replays success or receives its rejection, while competing requests retain 409 behavior


### Requirement: Recovery never silently repeats uncertain work
Saved definitions containing `on_uncertain: hold` SHALL be rejected with an actionable unsupported-policy error before scheduling. The system SHALL NOT alias this value to `needs_attention`, silently fall back to the default, or automatically migrate the definition. On restart, the system SHALL preserve committed completed tasks and their artifacts, recompute readiness, and automatically continue an otherwise running execution with no uncertain attempts only after a fresh backend-installation preflight passes for agents that can still execute. Preflight failure SHALL preserve committed results and pause intent, set needs_attention with backend_preflight_failed and sanitized issue details, and keep the API available without dispatching new agent work. Structural recovery SHALL reject more than one nonterminal saved execution before environment checks; terminal history SHALL not be checked. The listener SHALL serve observation/control before asynchronous recovery preflight begins, with dispatch gated until success and backend_preflight.status=checking exposed while it is in flight. Slow checks SHALL NOT delay listener availability; shutdown SHALL cancel them. Paused executions SHALL defer preflight to resume; terminal and cancelling executions SHALL require no installation check. Existing judging-only recovery remains outside installation preflight. Paused executions SHALL stay paused. Attempts reserved or running without a committed terminal outcome SHALL become interrupted and put the execution in needs_attention with new dispatch stopped. No such attempt SHALL be automatically resent or treated as an allowed failure. Attempts in the `judging` phase are an exception: their backend work is complete and their response artifact is committed, so recovery SHALL NOT interrupt them and SHALL re-run their verdict classification. For executions with loops, the persisted loop iteration counters SHALL survive restart: committed iterations and their artifacts are preserved, an interrupted body attempt recovers as any interrupted attempt, and retrying it continues the same iteration — the loop neither advances to the next iteration nor restarts from the first.

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

#### Scenario: Recovery installation hold
- **WHEN** an otherwise runnable recovered execution needs a backend whose executable is now absent
- **THEN** no new task starts, completed artifacts are preserved, and the API exposes needs_attention with the aggregate report

#### Scenario: Recovery ignores permanently completed agents
- **WHEN** a completed workflow-scope task uses an absent backend and all remaining tasks use installed backends
- **THEN** the completed agent is excluded from recovery preflight, whereas agents that can re-arm inside unfinished loops remain included


### Requirement: Execution observation includes outcomes and intervention
`GET /workflows` SHALL list historical and active execution summaries. `GET /workflows/{id}` SHALL expose execution state/revision, task and attempt identities/states, dependency-blocking reasons, active/ready counts, errors, output paths, and current run progress including pending permissions. Attempt states SHALL include the `judging` phase, and views SHALL expose per-attempt verdict data — verdict name, confidence, probability distribution, model, and source — together with judge-related attention reasons for uncertain verdicts and judge unavailability. Attempt views SHALL expose the iteration number for loop body attempts and the complete `iteration_path` for every loop-owned attempt in executions with nesting, and the execution view SHALL expose per-loop state: loop name, current iteration, and `max_iterations`. In executions with nesting, every loop view SHALL expose iteration_path, including root-only paths; child loop views SHALL additionally expose parent. For loops with conditions, loop views SHALL additionally expose the `until_task` name, the effective iteration cap (declared plus extensions), and the latest settled condition verdict. Unknown IDs SHALL return 404. Task states SHALL include pending, ready, dispatching, running, succeeded, failed, interrupted, blocked, and cancelled. Execution states SHALL include running, paused, needs_attention, cancelling, cancelled, succeeded, completed_with_errors, and failed.

An unresolved loop failure/blocking hold SHALL take precedence over final-state derivation and keep the execution in `needs_attention`, even when all tasks are settled. Loop views SHALL expose state `running`, `needs_attention`, or `done`; execution attention reasons SHALL identify the loop, iteration, and failed or blocked task, expose the underlying error or blocking reason, and indicate retry or cancellation as intervention options. Condition-related holds are observable the same way: `loop_exhausted` reasons SHALL name the loop and indicate extend, stop, or cancel; a `needs_attention` verdict action SHALL name the loop, iteration, task, and verdict and indicate override, stop, or cancel. When work settles and no loop hold or other attention reason remains, any blocked task or non-tolerated failed task SHALL make the execution failed; otherwise any tolerated failed task SHALL make it completed_with_errors; otherwise it SHALL succeed; for executions with loops these verdicts SHALL use the final task states in the final subtree iteration of each root; exhaustion itself introduces no terminal-failure outcome, and `on_exhaustion: succeed` completes the loop without overriding ordinary workflow outcome derivation. Uncertain attempts SHALL require attention rather than produce a final success. Workflow long-polling SHALL return on a terminal outcome, intervention, or wait expiry; expiry SHALL NOT cancel the execution. Waits SHALL accept integer `timeout_seconds` from 1 to 600, default 30, and return 400 otherwise. Permission intervention SHALL be exposed within one second of local publication under normal operation.

Task counts SHALL count tasks, not iterations or invocations. For executions with nesting, loop-related attention details SHALL identify the complete current path together with the existing cause, task, and intervention options; a loop name plus its local counter alone SHALL NOT represent the context. Control audit records in executions with nesting SHALL retain the accepted target path and, for propagated stops, every affected descendant path. Root control targets in such executions SHALL record their root-only path. Historical paths and contexts SHALL remain observable after resets. Existing nonnested response and audit shapes SHALL remain unchanged except for the additive optional backend_preflight diagnostics defined below.

For executions with nesting, loop-related reason strings SHALL use a canonical path token: root-to-owner `name=iteration` entries separated by `/`, with positive base-10 counters, no leading zeroes, and no spaces. Loop identifiers SHALL follow the existing safe identifier grammar, which excludes `=`, `/`, and `:`. A path SHALL occupy exactly one colon-delimited field. The reason templates SHALL be:

- `loop_failure:<path>:<task>:retry_or_cancel`
- `loop_blocked:<path>:<task>:retry_or_cancel`
- `loop_attention:<path>:<task>:<verdict>:override_or_stop_or_cancel`
- `loop_exhausted:<path>:extend_or_stop_or_cancel`
- `waiting_loop:<barrier-path>`

Underlying task errors and blocking details SHALL remain exposed in the task view. Existing non-loop reason families SHALL retain their formats and link to attempts through the existing unique task/attempt identities. For loop exit waits, the barrier SHALL be the immediate child loop of the consumer's scope containing the producer; for workflow-scope consumers it SHALL be the producer's root ancestor loop. The reason SHALL show that barrier's current path while awaiting invocation completion, not promise release when the innermost producer first completes. Multiple eligible loop-wait barriers SHALL select deterministically by lexicographic direct dependency task ID. Executions without nesting SHALL retain their existing reason strings.


Execution views SHALL expose optional backend_preflight diagnostics with status checking or failed and sanitized structured issues, including phase, affected agents and restart_required. Failed checks SHALL add backend_preflight_failed to attention reasons and wake intervention waiters. Ordinary installation/service repair SHALL indicate resume (or an eligible target retry) as the next action; latched managed failures SHALL indicate explicit Squad restart after repair, followed by normal recovery/resume. A target-only retry check SHALL NOT erase unrelated diagnostics. Checking status SHALL be transient and SHALL NOT introduce a new persisted execution state or reusable admission success.

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

#### Scenario: Preflight intervention is visible
- **WHEN** a recovered execution is held because a managed runtime start failed and latched
- **THEN** its view and waiting observers expose backend_preflight_failed, the safe issues and restart_required:true with restart guidance rather than promising that resume alone will repair it

#### Scenario: Recovery listener stays responsive
- **WHEN** the single eligible recovered execution has a slow preflight
- **THEN** GET observation remains available with checking diagnostics, cancellation is accepted, and no backend task dispatches before success


## ADDED Requirements

### Requirement: Preflight gates every release of new backend work
Resume SHALL perform fresh preflight for all agents that can still execute, including agents in unfinished loops that can re-arm. An otherwise eligible new retry admission SHALL check only its target agent, including that agent's service readiness; an unrelated agent's installation failure SHALL NOT reject the retry. The queued retry SHALL still wait for every existing attention/dispatch gate. Failed checks SHALL return 503 without releasing work or accepting a new retry record, retain pending execution state and publish backend_preflight_failed with sanitized issue details. Accepted idempotent retry replays SHALL remain successful without rechecking. A full successful pass SHALL clear only backend preflight diagnostics/attention causes; a target-only success SHALL clear at most independently attributable target issues and SHALL preserve the aggregate cause while unrelated issues remain and SHALL NOT waive pause, uncertain work, cleanup, artifact, verdict or loop requirements.

Before each later scheduling batch the system SHALL check all ready candidates before reserving new attempts or initializing sessions. Failed checks SHALL hold the execution in needs_attention without new attempt reservations; already running work SHALL be allowed to settle. Every owned-run entry SHALL enforce a check for its effective selected backend before external initialization. Check results SHALL be revalidated against the current execution revision and lifecycle gates before dispatch; cancellation, pause or stale state SHALL prevent dispatch. Preflight SHALL NOT alter definition identity, schema numbering, manual session continuity or fresh workflow conversation rules. Persisted preflight data SHALL be optional sanitized diagnostics, never reusable success authority.

#### Scenario: Repair then resume
- **WHEN** an installation hold is repaired and resume is requested
- **THEN** fresh checks clear only the installation hold, and execution continues only if no other cause blocks it

#### Scenario: Failed retry is not accepted
- **WHEN** a new retry request targets a missing installation
- **THEN** the request returns 503 without committing its retry ID or creating an attempt, allowing the same ID after repair

#### Scenario: Removal before a later batch
- **WHEN** a backend disappears after initial workflow admission but before its task becomes ready
- **THEN** the later batch creates no new attempts, exposes the installation hold, and preserves outcomes of previously running tasks

#### Scenario: Cancel or pause wins a preflight race
- **WHEN** cancellation or pause is committed while a dispatch preflight is running
- **THEN** its stale successful result cannot reserve or launch a task

#### Scenario: Old snapshots have no cached evidence
- **WHEN** a supported saved execution has no preflight diagnostic field
- **THEN** it loads without migration and undergoes fresh required checks instead of treating absence as prior success


#### Scenario: Unrelated installation cannot reject retry acceptance
- **WHEN** an eligible retry targets healthy agent Y while agent X has an installation hold
- **THEN** Y's retry is accepted after its own checks but remains queued behind X's unresolved execution hold, and X's diagnostics remain visible

#### Scenario: Managed latch survives request ID reuse
- **WHEN** initial readiness fails or is cancelled during managed startup before an execution is committed
- **THEN** the request ID remains unconsumed, but a subsequent request cannot restart that runtime and reports restart_required until Squad is explicitly restarted

### Requirement: One-shot execution uses backend preflight
The existing run command SHALL use the same installation/readiness gates as workflow admission and recovery. A pre-selection preflight failure SHALL create no execution, consume no request ID, print the concise aggregate with official links and restart guidance to stderr without CLI usage, and exit 2 after successful cleanup. It SHALL emit the existing version-1 JSON summary with null execution identity/state and SHALL NOT create a workflow summary file. Existing summary/output/cleanup failures SHALL override that result with exit 1. Once an existing execution is selected, an installation hold SHALL remain needs_attention with the process/API alive for repair/resume or cancellation. Terminal request replay SHALL perform no installation checks. Existing one-shot terminal, signal, reporting and resource-ownership rules SHALL remain in force; this requirement adds no new summary schema or exit-code family.

#### Scenario: One-shot rejects missing installation before selection
- **WHEN** run requests a new execution and a referenced backend is missing
- **THEN** it prints the aggregate on stderr, emits the pre-selection JSON summary, exits 2 after cleanup and leaves no execution or workflow summary file

#### Scenario: One-shot recovers an installation hold
- **WHEN** run selects a nonterminal execution whose remaining backend is missing
- **THEN** the listener is available during preflight, the process stays alive on needs_attention after failure, and repair/resume or ordinary cancellation remains possible

#### Scenario: One-shot terminal replay needs no tools
- **WHEN** run selects an already terminal request on a host without its former backend installations
- **THEN** it reports the committed outcome with the existing exit mapping without checking installations or changing workflow history
