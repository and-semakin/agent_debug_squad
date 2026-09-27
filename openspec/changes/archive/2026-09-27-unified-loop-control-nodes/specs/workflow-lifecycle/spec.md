## MODIFIED Requirements

Scenario titles inherited from the current main spec are retained as stable regression identifiers for OpenSpec replacement validation. In their updated scenario bodies, control means the version-2 task.control model; condition refers only to explicitly described legacy syntax/history.

### Requirement: Workflow submission is explicit and idempotent
Serving a configuration SHALL NOT automatically start a new workflow. `POST /workflows` with a nonempty `request_id` SHALL create an execution of the configured definition and return 202 only after the backend-installation preflight succeeds for every agent referenced by its task graph. This includes downstream, loop and allowed-to-fail tasks, but excludes unreferenced agent definitions and machine backend sections. Preflight failure SHALL return 503 with an aggregate report before persisting an execution, accepting its request ID, reserving an attempt or dispatching any task. Structural invalidity and existing idempotency/conflict checks SHALL be resolved before preflight. An identical accepted replay SHALL return the existing execution without rechecking installations. Repeating the same request ID with the same resolved definition SHALL return the original execution with 200 without new work, including after restart. Reusing an ID with a changed resolved definition SHALL return 409. Only one nonterminal workflow execution per server session SHALL be admitted regardless of workflow version; competing submissions SHALL return 409. New executions SHALL retain an immutable copy of their resolved definition and agent configuration. Invalid submissions SHALL return 400 without dispatch.

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

### Requirement: Dispatch and completion are durable decisions
The system SHALL durably reserve an attempt identity and its exact inputs before sending work to a backend, SHALL never dispatch that identity more than once in a live owner, and SHALL publish successful completion durably before releasing dependent tasks. Duplicate or lost completion notifications SHALL NOT cause duplicate or missing scheduling. Persistence failures SHALL stop new dispatch and surface an error rather than claim successful completion. A second server owner for the same session state SHALL be rejected before it mutates state or starts work.

For version-2 controls, settled verdict publication, decision/hold, complete skip set, pass closure or owner completion/exhaustion, and any accepting manual request record SHALL commit atomically before dependent release. A separate advance SHALL consume that persisted close decision once and atomically save the new path and all subtree resets before any new reservation. Failed persistence SHALL leave the live authoritative state and idempotency records unchanged and stop new dispatch. Auxiliary event logs SHALL NOT determine whether a decision already applied.

#### Scenario: Duplicate completion
- **WHEN** a completion notification is delivered twice
- **THEN** each dependent task still has at most one initial attempt

#### Scenario: Lost wake-up
- **WHEN** a completion is durably saved but its scheduler notification is lost
- **THEN** subsequent reconciliation detects readiness and dispatches the dependent task

#### Scenario: Save failure
- **WHEN** storing a dispatch reservation or successful completion fails
- **THEN** no downstream work is released on the unsaved state and the error is observable

#### Scenario: Competing process
- **WHEN** another server attempts to use the same session directory
- **THEN** it fails before modifying execution records or calling backends

#### Scenario: No partial break publication
- **WHEN** saving a break decision fails after response artifacts were written
- **THEN** no skips, completion, accepted replay record or suffix dispatch becomes visible from the uncommitted candidate

#### Scenario: Duplicate decision is inert
- **WHEN** the same completion/judge callback or reconcile notification arrives twice
- **THEN** the same control outcome closes/skips/advances at most once

### Requirement: Recovery never silently repeats uncertain work
On restart the system SHALL preserve authoritative committed outcomes, decisions, skip records, full paths, budgets, stop intents and accepted request records. It SHALL continue otherwise running supported executions without uncertain backend attempts only after a fresh backend-installation preflight passes for agents that can still execute, and SHALL keep paused executions paused. Dispatch-reserved/running attempts without committed outcomes SHALL become interrupted and require explicit confirmed retry; they SHALL NOT be automatically resent or treated as allowed failures. Judging attempts with saved responses SHALL be reclassified without rerunning backend work, unless their decision already committed. A committed control decision SHALL never be classified again or mapped a second time.

Schema-4 recovery SHALL preserve the same entered or planned loop contexts and historical final invocation paths. It SHALL validate one current state per declared loop and no extra/duplicate records, complete ancestry and current ancestor prefixes, local counter/path agreement, entered counts and caps, real attempts only in entered contexts, and workflow-scope path omission. Historical unresolved attempts SHALL not masquerade as current work; an interruption superseded by an accepted retry SHALL remain repaired history. Closed parents SHALL contain only completed or explicitly skipped children. A skipped unentered child SHALL consume zero passes.

Decision records SHALL identify the exact current or historical task/attempt/outcome revision, selected verdict and mapped/effective action, consistent owner/pass closure and complete suffix skip set. Skips SHALL have a valid causal decision/path, no invented backend identities, and no real attempt in the same skipped task context. A decision and its skips SHALL not partially appear or be applied twice. A persisted advance SHALL reset its whole subtree atomically and preserve the previous close decision/history. Recovery before that advance SHALL perform it once if permitted; recovery after it SHALL not repeat or skip a pass. Paused/held mode SHALL still prevent advance.

Cancellation intent SHALL survive restart and prevent dispatch/late control application. Loop failure, blocking, mapped attention and exhaustion holds SHALL survive with the same paths/reasons; recovery SHALL neither auto-retry failures nor convert them to terminal success/failure. Storage failure SHALL stop dispatch. Unknown schema versions, malformed/duplicate JSON, inconsistent definitions/paths/decisions/skips or missing authoritative data SHALL fail closed with an actionable error before scheduling, without modifying saved files. Saved on_uncertain: hold SHALL remain explicitly unsupported, not defaulted or aliased. Supported legacy execution reading and activation SHALL follow the version compatibility requirement.

Preflight failure SHALL preserve committed results and pause intent, set needs_attention with backend_preflight_failed and sanitized issue details, and keep the API available without dispatching new agent work. Structural recovery SHALL reject more than one nonterminal saved execution before environment checks; terminal history SHALL not be checked. The listener SHALL serve observation/control before asynchronous recovery preflight begins, with dispatch gated until success and backend_preflight.status=checking exposed while it is in flight. Slow checks SHALL NOT delay listener availability; shutdown SHALL cancel them. Paused executions SHALL defer preflight to resume; terminal and cancelling executions SHALL require no installation check. Existing judging-only recovery remains outside installation preflight.

Version/schema compatibility and authoritative-state validation SHALL precede environment checks. Legacy loop executions SHALL not be activated by preflight success. In supported schema-4 recovery, an agent whose current outcome is skipped SHALL remain in the preflight set if an unfinished enclosing loop can re-arm that task; a task permanently skipped by a completed root SHALL require no recovery installation check. Existing preflight gates for resume, target retries, later batches and owned-run entry SHALL continue to apply unchanged.

#### Scenario: Crash around backend dispatch
- **WHEN** an attempt was reserved before a crash without a committed outcome
- **THEN** it becomes interrupted and no external work is automatically repeated

#### Scenario: Crash during judging
- **WHEN** a control response was saved but no decision committed before crash
- **THEN** classification can run again, with no backend rerun and no suffix release before a decision commits

#### Scenario: Crash after break
- **WHEN** a break and all skips committed before crash
- **THEN** the owner remains closed, skips/results persist, and no decision is reapplied

#### Scenario: Crash between close and advance
- **WHEN** continue and its skips committed but the next pass did not
- **THEN** recovery advances once when gates allow and never runs the skipped suffix

#### Scenario: Crash after nested advance
- **WHEN** outer advances to 2 and resets children in one snapshot before crashing
- **THEN** recovery uses outer=2/inner=1 and cannot match attempts from outer=1/inner=1

#### Scenario: Paused pending advance
- **WHEN** continue closed a pass while paused and the process restarts
- **THEN** the closed pass and skips persist, and advance waits for resume

#### Scenario: Malformed decision or skip
- **WHEN** a schema-4 snapshot lacks a required suffix skip or links a skip to a different pass
- **THEN** recovery fails closed rather than guessing a decision

#### Scenario: Contradictory reserved suffix
- **WHEN** a saved control-skipped task also has a real reserved attempt at that path
- **THEN** recovery rejects the state before backend activity

#### Scenario: Historical interruption remains repaired
- **WHEN** an interrupted child attempt was retried successfully before an ancestor advanced
- **THEN** restart preserves that old interruption as resolved history

#### Scenario: Unsupported old uncertainty spelling
- **WHEN** saved on_uncertain is hold
- **THEN** loading reports an unsupported policy without aliasing it to needs_attention

#### Scenario: Crash between predecessors and consumer
- **WHEN** a supported execution is in use and predecessor successes are committed and the process crashes before reserving their consumer
- **THEN** recovery dispatches the consumer without rerunning predecessors

#### Scenario: Restart while paused or cancelling
- **WHEN** a supported execution is in use and a paused or cancelling execution is recovered
- **THEN** paused work stays paused and cancelling work does not start new tasks

#### Scenario: Crash mid-iteration continues that iteration
- **WHEN** a supported execution is in use and the process stops during iteration 2 of a 3-iteration loop with one body attempt left interrupted
- **THEN** recovery preserves iterations 1's committed attempts, keeps the loop at iteration 2, holds the interrupted attempt for retry, and a retried task completes iteration 2 before iteration 3 begins

#### Scenario: Pre-loop snapshots load unchanged
- **WHEN** a snapshot saved by a binary without loops (schema version 1) with supported definition values is recovered
- **THEN** it loads and recovers exactly as before loops existed

#### Scenario: Crash before iteration advance commits
- **WHEN** a supported execution is in use and all iteration-1 outcomes are committed and the process stops before the advance to iteration 2 is committed
- **THEN** recovery advances to iteration 2 once without rerunning iteration 1 or skipping iteration 2

#### Scenario: Crash after iteration advance commits
- **WHEN** a supported execution is in use and the advance to iteration 2 is committed and the process stops before any iteration-2 attempt reservation
- **THEN** recovery keeps the counter at 2 and starts eligible iteration-2 initial attempts without repeating iteration 1 or advancing to 3

#### Scenario: Advance persistence failure
- **WHEN** a supported execution is in use and saving an iteration advance fails
- **THEN** no next-iteration backend work starts on the unsaved state, a storage error is observable, and recovery follows the last authoritative snapshot

#### Scenario: Removed uncertainty policy is rejected in saved state
- **WHEN** a supported execution is in use and an execution saved with explicit on_uncertain hold is loaded by the new binary
- **THEN** loading fails with an actionable unsupported-policy error, no task dispatches, and the saved definition is neither rewritten nor silently interpreted as needs_attention

#### Scenario: Three-level interruption keeps its identity
- **WHEN** a supported execution is in use and the process restarts with an unfinished attempt at A=2/B=1/C=1 and historical successes at A=1/B=1/C=1
- **THEN** the unfinished attempt becomes interrupted at A=2/B=1/C=1; old successes do not settle it, and a confirmed retry stays at that exact path

#### Scenario: Crash before parent advance commits
- **WHEN** a supported execution is in use and a parent iteration and its subtree settled, but the process stops before the advance-and-reset snapshot commits
- **THEN** recovery preserves the old outcomes and performs the permitted transition once without repeating completed work

#### Scenario: Crash after parent advance commits
- **WHEN** a supported execution is in use and A advances to 2 and resets B and C in one saved snapshot, then the process stops before new reservations
- **THEN** recovery keeps A=2/B=1/C=1 and starts only eligible work missing from that context

#### Scenario: Crash after parent completion
- **WHEN** a supported execution is in use and the saved parent and descendant invocations are done when the process stops
- **THEN** recovery retains their final paths and outputs and creates no new child invocation

#### Scenario: Subtree advance save failure
- **WHEN** a supported execution is in use and saving a parent advance with descendant resets fails
- **THEN** no backend dispatch uses any new subtree path; recovery follows the last authoritative snapshot

#### Scenario: Nested snapshot version is explicit
- **WHEN** a version-2 execution with parents is persisted
- **THEN** it uses schema 4 with complete paths and an older schema-3-only reader rejects it

#### Scenario: Malformed nested state fails closed
- **WHEN** a schema-4 snapshot lacks a required root/nested path or has wrong ancestry/counters/current prefixes
- **THEN** loading fails with an actionable error before dispatch

#### Scenario: Nonnested snapshots retain compatibility
- **WHEN** a supported loopless version-1 schema-1/2 execution is loaded and later saved
- **THEN** it keeps existing behavior and is saved as schema 2; old flat loops remain read-only and cannot be activated

#### Scenario: Historical interruption stays repaired
- **WHEN** a supported execution is in use and an interrupted inner attempt was successfully retried, its invocation completed, and the parent advanced before restart
- **THEN** recovery retains the interrupted attempt as history, does not demand it match the new current path, and does not recreate its resolved hold

#### Scenario: Duplicate current invocation is corrupt
- **WHEN** a supported execution is in use and saved state has duplicate loop records or unresolved work for one loop under two ancestor prefixes
- **THEN** recovery rejects the state before dispatch rather than choosing a latest record

#### Scenario: Malformed snapshot JSON terminates recovery
- **WHEN** a supported execution is in use and a saved snapshot contains an invalid array element, a malformed nested object, or truncated JSON
- **THEN** loading returns a corruption error without looping indefinitely or dispatching work

#### Scenario: Workflow-scope attempt has no path in schema three
- **WHEN** a terminal schema-3 history contains a workflow-scope attempt without a path
- **THEN** read-only observation accepts that omission; schema-4 execution likewise omits paths only for workflow-scope tasks

#### Scenario: Skipped work can still require recovery preflight
- **WHEN** continue skipped a reviewer in a closed schema-4 pass but a bounded next pass remains possible
- **THEN** recovery includes that reviewer agent in preflight before new backend work can run

#### Scenario: Final skip needs no backend installation
- **WHEN** a root broke and permanently skipped a reviewer while a workflow-scope report remains runnable
- **THEN** recovery does not check the permanently skipped reviewer and still checks the report agent

#### Scenario: Recovery installation hold
- **WHEN** an otherwise runnable recovered execution needs a backend whose executable is now absent
- **THEN** no new task starts, completed artifacts are preserved, and the API exposes needs_attention with the aggregate report

#### Scenario: Recovery ignores permanently completed agents
- **WHEN** a completed workflow-scope task uses an absent backend and all remaining tasks use installed backends
- **THEN** the completed agent is excluded from recovery preflight, whereas agents that can re-arm inside unfinished loops remain included

### Requirement: Execution observation includes outcomes and intervention
GET /workflows SHALL list active and historical summaries. GET /workflows/{id} SHALL expose state/revision, task/outcome/attempt identities, dependency/phase waiting and blocking reasons, active/ready task counts, errors, output paths, run progress and permission requests. Unknown IDs SHALL return 404. Execution states SHALL remain running, paused, needs_attention, cancelling, cancelled, succeeded, completed_with_errors and failed. Task states SHALL retain pending, ready, dispatching, running, succeeded, failed, interrupted, blocked and cancelled and add skipped; judging SHALL remain an attempt phase with its recorded verdict evidence and attention details.

Schema-4 views SHALL expose full paths on every loop-owned outcome, attempt and loop, direct parent, planned local iteration, entered, iterations_started, declared and effective caps, stop_requested, state and close reason. Loop states SHALL include running, needs_attention, done and skipped (unadmitted control-skipped child). Views SHALL expose ordered control task IDs and current/historical decision records including decision ID, task/attempt, outcome revision, owner path, verdict/source, mapped action, effective action and resolution source. until_task SHALL not be emitted for schema 4. Skipped task views SHALL include reason loop_break/loop_continue, planned path and causal decision, with no fake attempt or output. Skip history SHALL remain visible after continue/ancestor resets. Counts SHALL count tasks, not invocations, and include skipped tasks separately from successes/failures.

Unresolved attention SHALL take precedence over final-state derivation. Failure/blocking details SHALL identify task and path and offer eligible retry/cancel; dependency_skipped SHALL identify the producer and decision and SHALL NOT offer retry of the skipped task. Control attention SHALL offer eligible verdict override, stop or cancel. Exhaustion SHALL offer extend, stop or cancel. When all root invocations and task outcomes settle with no attention, any blocked or mandatory failed task SHALL fail the execution, tolerated failures SHALL produce completed_with_errors, and otherwise it SHALL succeed. Control skips alone SHALL not degrade success; they do not waive blocked outside consumers. Final derivation SHALL use exact final subtree contexts, and exhaustion succeed SHALL not hide failures or claim substantive correctness.

Schema-4 reasons SHALL use root-to-owner name=positive-counter entries joined by slash, with no spaces/leading zeroes, as one colon-delimited field. Safe identifiers SHALL exclude equals, slash and colon. Reason families SHALL include loop_failure:<path>:<task>:retry_or_cancel, loop_blocked:<path>:<task>:retry_or_cancel for repairable failures, loop_attention:<path>:<task>:<verdict>:override_or_stop_or_cancel, loop_exhausted:<path>:extend_or_stop_or_cancel, waiting_loop:<barrier-path>, and dependency_skipped:<producer>:<decision-id>. Nonrepairable skip-caused blocking SHALL expose cancellation/new-definition guidance rather than a false retry option. Pending phase waits SHALL expose the gating control task/path. Cross-scope completion waits SHALL name the immediate child barrier of the consumer scope, or the root barrier for workflow-scope consumers; multiple waits SHALL choose lexicographically by dependency task ID. Existing non-loop reasons SHALL retain their formats.

Long polling SHALL return on terminal outcome, attention or expiry; timeout_seconds SHALL be an integer 1..600, default 30, with 400 otherwise. Expiry/disconnect SHALL not cancel work. Permission intervention SHALL be visible within one second of local publication under normal operation. Legacy read-only views SHALL retain original representations and expose execution_supported false with compatibility guidance.

Execution views SHALL expose optional backend_preflight diagnostics with status checking or failed and sanitized structured issues, including phase, affected agents and restart_required. Failed checks SHALL add backend_preflight_failed to attention reasons and wake intervention waiters. Ordinary installation/service repair SHALL indicate resume (or an eligible target retry) as the next action; latched managed failures SHALL indicate explicit Squad restart after repair, followed by normal recovery/resume. A target-only retry check SHALL NOT erase unrelated diagnostics. Checking status SHALL be transient and SHALL NOT introduce a new persisted execution state or reusable admission success.

Every decision ID SHALL match `lcd_[1-9][0-9]*` and be unique within its execution. The positive decimal sequence SHALL be allocated and persisted with the authoritative decision, never reused after committed allocation, and remain stable across restart, replay and ancestor advance. It SHALL contain no colon, slash or equals; a decision ID SHALL be opaque to clients. Within an execution, lookup SHALL identify exactly one owner path, task, real attempt and outcome revision; source path identity SHALL not be inferred from the producer task name alone. ID counter overflow SHALL hold with a storage/invariant error rather than reuse an ID. A new manual outcome revision SHALL receive a new decision ID; resolving an existing held decision through stop SHALL retain its ID and append the resolution evidence.

#### Scenario: Skip is visible without a fake attempt
- **WHEN** a head break skips reviewers
- **THEN** reviewers have skipped outcomes and causal decision paths, zero new attempts and no result references

#### Scenario: Continue history remains inspectable
- **WHEN** a later pass is active after continue skipped the preceding suffix
- **THEN** observation still exposes the prior decision and skips at the prior path

#### Scenario: Control hold exposes both identities
- **WHEN** a middle control holds at outer=2/inner=1
- **THEN** the view names its task/attempt, full path, verdict, mapped action and intervention choices

#### Scenario: Stop resolution is auditable
- **WHEN** stop clears a mapped attention hold as effective proceed
- **THEN** the view retains the original verdict/mapped needs_attention and records effective proceed sourced from that stop

#### Scenario: Unavailable outside result fails visibly
- **WHEN** a workflow-scope consumer requires a producer skipped in the final pass
- **THEN** it exposes dependency_skipped and finalization fails rather than reporting success or offering a skipped-task retry

#### Scenario: Unentered child counter
- **WHEN** a break skips a not-admitted child planned at local iteration 1
- **THEN** its view distinguishes planned iteration 1 from iterations_started zero

#### Scenario: Polling and permissions unchanged
- **WHEN** a wait expires or an active backend needs permission
- **THEN** expiry does not cancel, and permission details remain observable through existing APIs

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
- **WHEN** a version-2 controlled loop is held on exhaustion or a mapped attention action
- **THEN** its view exposes control task IDs, decision/verdict evidence, effective cap and intervention details, without until_task

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
- **WHEN** a terminal legacy flat-loop execution is observed
- **THEN** its saved reason representation remains intact; new schema-4 flat loops use one-entry path tokens

#### Scenario: Preflight intervention is visible
- **WHEN** a recovered execution is held because a managed runtime start failed and latched
- **THEN** its view and waiting observers expose backend_preflight_failed, the safe issues and restart_required:true with restart guidance rather than promising that resume alone will repair it

#### Scenario: Recovery listener stays responsive
- **WHEN** the single eligible recovered execution has a slow preflight
- **THEN** GET observation remains available with checking diagnostics, cancellation is accepted, and no backend task dispatches before success

#### Scenario: Decision IDs safely occupy reason fields
- **WHEN** a skipped producer at outer=2/inner=1 is referenced by dependency_skipped
- **THEN** the decision field is a stable lcd_ decimal identifier with no path delimiters and resolves within that execution to the exact causal path/task/attempt/revision

#### Scenario: Decision IDs survive repeated local counters
- **WHEN** an ancestor advances and a new decision uses the same task and local child counter
- **THEN** it has a distinct execution-local ID, while old reasons still resolve to the original decision

### Requirement: Pause and cancellation have distinct effects
`POST /workflows/{id}/pause` SHALL durably prevent new dispatch reservations while allowing already dispatched tasks to finish. Repeated pause while paused SHALL be idempotent. `POST /workflows/{id}/resume` SHALL revalidate state and artifacts and resume paused or needs_attention work only when no recovery/artifact uncertainty remains. `POST /workflows/{id}/cancel` SHALL durably prevent new scheduling, cancel active owned runs, and mark remaining unstarted non-skipped tasks cancelled. It SHALL reach cancelled only after owned workers stop or, for interrupted attempts whose cleanup cannot be checked after restart, the caller explicitly supplies `confirm_previous_stopped: true`. The system SHALL record that assertion and MUST NOT allow it to override known active workers in the current process. Unconfirmed cleanup SHALL remain observable and MUST NOT be reported as completed cancellation. Cancellation SHALL override allowed_to_fail and success thresholds. For a paused or needs_attention execution with loops, a valid resume request SHALL be accepted with HTTP 200 and the current execution view even if loop failure/blocking reasons remain. It SHALL request running mode and independently revalidate artifacts, clear only reasons proven resolved, and re-attempt held verdict classifications whose own response artifacts are verified and whose state can be durably persisted. An unrelated loop failure or interruption MUST NOT prevent these safe recovery actions. Pending classification recovery SHALL retain its attention hold until its committed outcome resolves it, and repeated resume MUST NOT create concurrent duplicate judge calls for the same attempt. Resume MUST NOT waive failed dependencies, unmet thresholds, or unresolved uncertainty, and MUST NOT repeat agent work. While any attention reason remains, the execution SHALL remain needs_attention with no ordinary task dispatch or loop advance. A successful HTTP response acknowledges the recovery request, not that execution has resumed. Actual storage failures SHALL remain errors. Existing loopless control behavior SHALL remain unchanged. Cancellation SHALL be accepted from that hold and take precedence over failure attention while following the same cleanup requirements. Invalid state transitions SHALL return 409; repeated cancellation while cancelling/cancelled SHALL be idempotent.

A supported version-2 execution SHALL permit already-running control outcomes to commit decisions/skips and local completion while paused, but SHALL NOT advance any loop or reserve a suffix/child attempt until running mode and all attention gates permit. Resume SHALL NOT clear a semantic control hold, undo skips or reset counters. If cancellation intent commits before a pending judge decision, that decision SHALL NOT release, skip or advance work; late callbacks SHALL be ignored. Cancellation SHALL preserve decisions, skips and results committed before its intent and SHALL not rewrite them as cancelled.

#### Scenario: Pause races with completion
- **WHEN** pause is committed while a predecessor finishes
- **THEN** its result is preserved but no subsequent reservation occurs until resume

#### Scenario: Cancel optional work
- **WHEN** an execution is cancelled while an optional reviewer is active
- **THEN** that cancellation cannot release the verifier as though it were a tolerated reviewer failure

#### Scenario: Cancel after crash cleanup
- **WHEN** prior interrupted backend work has been stopped externally and the caller cancels with explicit cleanup confirmation
- **THEN** cancellation can finish, the assertion is recorded, and no dependent task is released

#### Scenario: Restored result
- **WHEN** a missing committed output is restored with its original hash and resume is requested
- **THEN** the system revalidates it and resumes only if no other attention reason remains


#### Scenario: Resume cannot waive a loop failure
- **WHEN** the caller resumes an execution whose loop still has a failed mandatory task or unmet dependency threshold
- **THEN** resume returns 200 with a needs_attention execution view and the unresolved reasons, without retrying agent work, dispatching tasks, or advancing the loop

#### Scenario: Cancel a held loop
- **WHEN** the caller cancels an execution held on a loop failure
- **THEN** cancellation stops owned work, cancels remaining unstarted tasks, and reaches cancelled once cleanup requirements are met instead of remaining stuck in needs_attention


#### Scenario: Loop failure and judge outage can be repaired independently
- **WHEN** a loop has a failed mandatory task and an independent attempt held on judge unavailability, the provider recovers, and resume is requested
- **THEN** resume accepts the request and re-attempts classification without rerunning the independent agent; the loop failure continues to hold task dispatch, classification settlement clears only its own attention reason, and an eligible explicit retry can then repair the failed task

#### Scenario: Restored artifact can be revalidated during a loop failure
- **WHEN** a loop failure coexists with a missing-artifact attention reason, the original artifact is restored, and resume is requested
- **THEN** resume revalidates and clears the repaired artifact reason while retaining the loop failure hold, permitting an otherwise eligible retry without requiring restart or cancellation

#### Scenario: Repeated recovery does not duplicate judging
- **WHEN** resume is repeated while a recovery classification is already in flight and a loop failure remains unresolved
- **THEN** only one classification call for that attempt runs concurrently, attention continues to block ordinary dispatch, and remaining reasons stay observable

#### Scenario: Pause after head control dispatch
- **WHEN** a paused execution receives proceed from its already-running control
- **THEN** the decision commits but the suffix stays unreserved until resume

#### Scenario: Cancellation wins a judge race
- **WHEN** cancel intent commits before a control judge callback
- **THEN** the callback cannot apply its mapped action and cancellation follows normal cleanup rules

### Requirement: Explicit retries preserve history and input consistency
`POST /workflows/{id}/tasks/{task}/retry` SHALL accept a nonempty `request_id` and `expected_attempt`, creating a new queued attempt identity only for a failed/interrupted task that passes the descendant-reservation guard. For a task outside all loops, every transitive dependency descendant through `needs` MUST have no attempt reservations, as before. For a loop task, a new retry MUST target its latest failed/interrupted attempt at the exact current complete iteration path, with all ancestor iterations still current. A historical target SHALL return 409 even if its local counter equals the current one. The retry SHALL retain that path and receive the next monotonically increasing per-task attempt number.

For every transitive dependency descendant, a reservation SHALL block retry if producer and descendant have no common enclosing loop, or if the reservation matches the producer's current path through their deepest common loop, inclusive. The same direct owner counts as a common loop. Earlier shared enclosing iterations SHALL NOT block retry. Every existing recorded attempt of a transitive dependency descendant in the relevant shared context SHALL count as a reservation, regardless of state: queued, dispatching, running, judging, cancelling, succeeded, failed, interrupted, or cancelled. A blocked task with no attempt SHALL NOT count as a reservation. This guard SHALL remain conservative over transitive dependency descendants, including paths through an ancestor bridge between branches; it SHALL NOT reject because of independent tasks. In particular, same-owner consumers block only at the exact current path, nested consumers of an ancestor producer block within that ancestor iteration, ancestor consumers of a nested producer block within the shared ancestor iteration, and workflow-scope consumers block on any reservation.

If an eligible retry not fenced by a control decision reopens a done owning invocation, that invocation and any done ancestors SHALL return to running at the same paths, atomically with the retry reservation. Counters, extensions, and stop intent SHALL be preserved; descendants SHALL NOT be reinitialized. Outside consumers SHALL wait until the required invocations settle again.

Guard evaluation and retry reservation SHALL be serialized with iteration advance and dispatch. Repeated identical retry requests SHALL return the originally created attempt; stale or conflicting requests SHALL return 409. Retry SHALL preserve prior artifacts, reset derived blocked descendants for reevaluation, preserve independent completed work, and use a fresh backend conversation.

Interrupted retries SHALL additionally require `confirm_previous_stopped: true`, recorded as the caller's cleanup assertion rather than a guarantee by Squad; a known active worker MUST NOT be bypassed. Cancelled/cancelling executions SHALL reject retries. A failed or completed_with_errors execution can reopen only if no other execution is active; a paused execution SHALL remain paused after retry. No retries SHALL happen automatically. Explicit retries requested by users or coordinator agents SHALL NOT count against the automatic dispatch bound or consume additional loop iterations; this change imposes no explicit-retry count limit, while all eligibility and consistency guards remain enforced.

Loop failure/blocking, control-action, and loop-exhausted attention reasons MUST NOT by themselves reject an otherwise eligible retry; multiple eligible retries SHALL be queueable while those reasons or recoverable interruptions remain. Queuing a retry MUST NOT clear unrelated control or exhaustion causes; actual dispatch SHALL wait for every attention cause to resolve. Artifact/storage and judge-related holds retain their resolution requirements; resume SHALL be able to address those causes independently of loop failures, as specified by the control requirement. After reserving a retry, the system SHALL recompute dependent readiness using the queued attempt as pending, remove only resolved failure/blocking reasons, and resume dispatch only when every attention reason is cleared and the execution is not paused. Blocked tasks without attempts SHALL be repaired by retrying eligible failed causal predecessors, not by retrying the blocked tasks themselves.

After an eligible retry repairs an invocation, settlement SHALL apply the ordinary stop, control, and effective-cap rules again at the retained path. Retry SHALL NOT clear the completion-causing stop, raise the cap, or grant another iteration. Reopened static invocations already at their cap and stopped invocations SHALL complete again at the same path once acceptable settlement is restored. Completed independent work SHALL remain preserved.

For version-2 loops, the guard SHALL additionally treat committed control decisions (including held attention decisions) as consumption of all their effective projected predecessors in that pass, recursively including predecessor child outcomes. A retry SHALL NOT invalidate such a decision, reopen a controlled invocation past its committed close, or undo control skips, even if no suffix backend reservation exists. Projected admission dependencies SHALL participate in reservation-consumption guards, so an internal child root reservation can fence an incoming ancestor producer even without a raw task path. A skipped task SHALL not be retryable and SHALL not acquire a fictitious attempt. A failed control without a committed decision SHALL remain retryable at its exact current path. Eligibility and any invocation reopening SHALL be checked atomically against enclosing decisions and current paths. Accepted retry requests SHALL replay before these new eligibility checks.

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
- **WHEN** a loop is done with a tolerated failed body task in its final iteration, no blocking descendant reservations or committed control decision fences exist, and a valid retry is accepted
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
- **WHEN** loop A settled acceptably and is held on a control action, loop B has a retryable mandatory failure, and the caller requests stop for A and retry for B in either order
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

#### Scenario: Decision fences retry while paused
- **WHEN** a control has committed proceed or attention after a tolerated prefix failure and no suffix has dispatched
- **THEN** retry of that prefix failure returns 409 because it would invalidate the committed decision

#### Scenario: Skipped task cannot retry
- **WHEN** a caller requests retry of a task skipped by break/continue
- **THEN** the call returns 409 and no attempt or invocation is created

#### Scenario: Unresolved failed control can be repaired
- **WHEN** a control fails before any semantic decision and normal guards pass
- **THEN** an explicit retry uses the same path with a new real attempt and no budget increment

#### Scenario: Projected consumer fences an ancestor
- **WHEN** an allowed failed outer producer feeds inner.a and inner.b without direct needs is reserved after admission
- **THEN** retry of the outer producer is rejected as consumed by the child admission

### Requirement: Loop controls admit human intervention
`POST /workflows/{id}/loops/{name}/extend` SHALL accept a nonempty `request_id` and a positive integer `add_iterations` whose addition cannot overflow the effective cap, durably raising the loop's effective iteration cap by that amount on a nonterminal, non-cancelling execution. Accepted extend requests SHALL be identified within the execution by request_id and bound to the loop name and add_iterations. Repeating an identical accepted request SHALL return the current execution view without a second increase, including after loop completion, execution completion, cancellation, or restart. This replay check SHALL precede lifecycle eligibility checks. Reusing the same extend request_id with a different loop name or add_iterations SHALL return 409 without mutation. Non-positive, non-integer or overflowing amounts SHALL return 400; unknown execution or loop identifiers SHALL return 404; new requests against done or skipped loops or terminal or cancelling executions SHALL return 409. A successful extension SHALL resolve the target loop's loop_exhausted cause independently of other holds; the loop SHALL continue at the next iteration only once all attention causes are resolved and the execution is in running mode. Extension SHALL NOT clear a control-action needs_attention cause or unpause the execution; extensions of a still-running loop raise its future cap. Extensions are audited as control events and participate in the automatic dispatch bound only after being granted.

`POST /workflows/{id}/loops/{name}/stop` SHALL accept a nonempty `request_id` and durably record stop intent for an unfinished loop of a nonterminal, non-cancelling execution. It SHALL forbid the next iteration and let the current pass finish under ordinary dependencies, phase gates, pause rules and the action rules below. It SHALL complete only after acceptable natural settlement or a valid control close decision. The local stop decision and removal of its control/exhaustion cause SHALL be permitted even if another loop needs attention; unrelated causes SHALL remain and still block all ordinary dispatch and iteration advance. Outside consumers SHALL wait until that completion and all global dispatch gates permit execution, then receive available exact-context results under the handoff rules, with skipped required producers remaining unavailable. Stop SHALL NOT cancel workers, unpause the execution, waive failures/blocking or unresolved verdicts, or turn a failure hold into terminal failure. Accepted intent SHALL survive recovery and retries in the same iteration. At a resolved control, proceed SHALL release its suffix normally; break SHALL skip and complete; continue SHALL skip and complete without repetition; needs_attention SHALL become an audited effective proceed, clearing that local hold so its suffix can finish when global gates allow. The original verdict and mapped action SHALL remain recorded. On natural completion or an already closed exhausted pass, stop SHALL complete without another iteration. Stop SHALL NOT itself fabricate break skips; unresolved classification and other independent attention reasons still require resolution. Views SHALL expose pending stop intent as stop_requested.

Identical accepted stop requests SHALL replay successfully before checking current lifecycle state, even after loop or execution completion. Reusing the same stop request ID for another loop SHALL return 409. New stop requests against done or skipped loops, terminal executions, or cancelling executions SHALL return 409; missing request IDs SHALL return 400 and unknown identifiers SHALL return 404. A new request while stop intent is already pending SHALL succeed without advancing or restarting the loop. Both controls are serialized with iteration advance and dispatch and persisted before effects. A stop that wins serialization prevents advance; if advance committed first, stop applies to the new current iteration. Controls never bypass cleanup, retry, or verdict-hold rules.

For a nested loop, extensions SHALL belong to the invocation identified by its ancestor path. They SHALL persist across that loop's own iteration advances and clear only when an ancestor advance starts a new invocation. Root extensions SHALL persist for the execution. Every fresh child invocation SHALL start at its declared cap with no inherited stop intent.

Stopping a loop SHALL atomically record intent for it and all currently unfinished non-skipped descendants. Stopping a nested loop SHALL leave ancestors and siblings unaffected; its own descendants SHALL still receive intent. Child-first stop resolution SHALL complete every eligible stopped invocation, including a stopped parent whose children have just become done, even while unrelated holds exist. It SHALL NOT dispatch work or advance iterations through any remaining hold. Parent completion SHALL preserve final descendant state; it SHALL NOT create new child invocations. Failed, interrupted, blocked, or unresolved judging work SHALL still require normal repair or cancellation.

Accepted control records SHALL identify the invocation current when the request was serialized. Existing request IDs SHALL remain execution-wide with the existing conflict fields. Identical replay after ancestor advance SHALL acknowledge the original action without extending or stopping the new invocation; a fresh request ID SHALL target the current invocation under normal eligibility rules. Audit records for executions with nesting SHALL retain the accepted target path and all affected descendant paths. A failed control save SHALL leave neither a partial subtree stop, a raised cap, nor a replay record.

A propagated stop SHALL have exactly the same priority as a directly requested stop on each affected invocation. If an affected child has an otherwise acceptably settled iteration held only by its control action or exhaustion policy, propagated intent SHALL clear that child hold by completing the invocation without extension. It MUST NOT waive failures, unresolved judging, interruptions, or unrelated holds.

An unfinished held child SHALL prevent its parent from settling. A control-action hold SHALL admit the existing eligible override, child stop, ancestor stop, or cancellation; extension alone SHALL NOT clear it. Exhaustion SHALL admit child extension, child stop, ancestor stop, or cancellation. None of these controls SHALL promise progress through independent unresolved causes.

A fresh stop/extend request SHALL address the named loop invocation current when serialized, without comparing it to the caller's earlier observation. An ancestor advance that commits first can change that target invocation. This is separate from observation identity and idempotent replay: replay prevents duplicate effects after acceptance but SHALL NOT provide an expected-context precondition for a first request.

Propagated stop SHALL use the same effective-action rules at every affected child barrier. A pending child not yet admitted SHALL execute its first pass when its phase opens, with stop intent preventing its repetition, unless an actual control break/continue skips that child. Already done or skipped children SHALL remain unchanged. Stop/extend SHALL not bypass full-path retry fences or unresolved classification. An extension of a pass closed by continue SHALL start a fresh pass when gates allow, never reopen that skipped suffix. Schema-4 controls SHALL always expose complete paths, including flat loops.

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
- **WHEN** a caller stops a loop held at a tail control on a needs_attention verdict action
- **THEN** the tail hold resolves through audited effective proceed and the settled loop completes at the current iteration, outside consumers receive that iteration's available results, and the execution proceeds to its derived final state

#### Scenario: Stop on a done loop conflicts
- **WHEN** a new stop request, rather than replay of an accepted request, targets a loop already done or skipped
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
- **WHEN** all body attempts settled acceptably and a stop is accepted on a control-action or exhaustion hold with no independent attention reasons
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
- **WHEN** two loops have acceptable settled iterations held on control actions and a stop is requested for each
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
- **WHEN** a child iteration is settled and held solely on a needs_attention control action when its ancestor is stopped
- **THEN** the child completes at that path with its verdict preserved and its control hold removed; unrelated causes remain

#### Scenario: Held child prevents parent completion
- **WHEN** a child needs attention on exhaustion while its ancestor still has pending work
- **THEN** the parent cannot settle; extending the child or stopping the child or an ancestor can resolve that exhaustion under normal guards, and cancellation can abandon the execution

#### Scenario: Extension does not resolve child condition attention
- **WHEN** a child is held on a needs_attention verdict action and an extension is accepted
- **THEN** the cap increases but the child hold remains until eligible override, stop, or cancellation, and parent settlement remains blocked

#### Scenario: Stop at a middle attention barrier
- **WHEN** stop is accepted while a middle control holds on needs_attention with a pending suffix
- **THEN** the hold clears as effective proceed with stop audit, the suffix runs only under normal gates, and the pass completes without repetition

#### Scenario: Stop with early continue
- **WHEN** stop intent is present when a head control resolves to continue
- **THEN** the suffix is skipped with loop_continue and the owner completes without another pass

#### Scenario: Stop cannot classify a response
- **WHEN** stop is requested while a control is held in judging for uncertainty
- **THEN** the judging hold remains until override/reclassification or cancellation; no effective proceed is invented

## ADDED Requirements

### Requirement: Execution versions are not silently reinterpreted
All new version-2 executions SHALL use snapshot schema 4 irrespective of nesting and SHALL be executed only by the new control engine. Supported loopless version-1 snapshots of schema 1/2 SHALL retain existing recovery behavior and save as schema 2. Schema 4 SHALL require definition version 2; legacy schemas SHALL reject version 2 or control fields. Unknown or inconsistent version/schema combinations SHALL fail closed before scheduling.

Every saved legacy execution with loops (schemas 2/3, definition version 1), including fixed-count loops, SHALL remain readable under its original serialization and semantics as history, with execution_supported false. A nonterminal legacy loop execution SHALL refuse recovery/activation before scheduling or modifying authoritative files, with execution ID/schema and instructions to finish/cancel using the previous binary. Structurally valid terminal legacy loop history in recognized schemas 2/3 SHALL not prevent startup or new supported submissions. New mutation requests to such legacy executions SHALL return 409 with compatibility guidance. Identical previously accepted stop, extend, retry and verdict-override requests SHALL instead replay their saved result under the existing route response contract before this compatibility rejection, without writing state, running preflight or activating work; conflicting reuse SHALL return 409. This read-only replay exception SHALL not bypass nonterminal legacy startup/activation refusal: if startup refuses the directory, no HTTP replay is served by that process. No automatic conversion, deletion, cancellation, hash relabelling or interpretation as control nodes SHALL occur.

Observation and historical submission-request replay SHALL return original saved data without triggering scheduling, preserving original hashes and accepted records. Version-2 submission using an old request ID SHALL conflict, not cross-match a converted definition. Manual YAML conversion SHALL create a new execution with a new request ID; it SHALL not copy old attempts as proof that work has run in the new execution. Old binaries SHALL reject schema 4; rollback SHALL not involve editing a schema number.

Unknown-schema or corrupt records SHALL not qualify as supported terminal history even if they contain a terminal-looking state field. Discovering any such authoritative execution record in the session directory during startup SHALL fail startup with an actionable unsupported-schema/corruption error, before listener/backend activation, and preserve the files. Direct observation of such an unreadable record SHALL return an error, not a fabricated read-only execution. The terminal-history exemption SHALL apply only after recognized-schema structural validation.

#### Scenario: Legacy active condition does not change meaning
- **WHEN** restart finds a nonterminal schema-2 or schema-3 until_task execution
- **THEN** activation fails with previous-binary guidance and no task/decision/save occurs

#### Scenario: Legacy static loop is also protected
- **WHEN** restart finds an unfinished legacy fixed-count loop
- **THEN** activation is refused rather than silently applying version-2 admission and result rules

#### Scenario: Legacy terminal history remains available
- **WHEN** a state directory contains only terminal legacy loop records and supported current configuration
- **THEN** startup succeeds, history is readable and mutations to those records return 409

#### Scenario: Loopless recovery remains compatible
- **WHEN** a supported schema-1 loopless execution is recovered and later saved
- **THEN** its behavior is unchanged and it is saved as schema 2

#### Scenario: Wrong schema pairing
- **WHEN** a schema-3 file contains a version-2 definition or a schema-4 file contains version 1
- **THEN** recovery rejects it without coercion

#### Scenario: New version cannot replay old side effects
- **WHEN** a converted version-2 definition reuses an old submission request ID
- **THEN** it conflicts; an explicit new ID starts a fresh execution

#### Scenario: Accepted legacy control replay is read-only
- **WHEN** a supported terminal legacy execution has an accepted stop/extend/retry/override request and its exact payload is replayed
- **THEN** the existing route returns its saved result without mutation or preflight; a new request or conflicting reuse returns 409

#### Scenario: Legacy replay does not bypass startup refusal
- **WHEN** a directory contains a nonterminal legacy loop with accepted control records
- **THEN** startup still refuses activation and does not start an API merely to replay those controls; the previous binary is required

#### Scenario: Unknown terminal schema still blocks startup
- **WHEN** a saved record claims terminal state but uses an unknown schema version
- **THEN** startup fails before activation and preserves it rather than treating it as recognized legacy history

## RENAMED Requirements

- FROM: `### Requirement: Loop conditions admit human intervention`
- TO: `### Requirement: Loop controls admit human intervention`
