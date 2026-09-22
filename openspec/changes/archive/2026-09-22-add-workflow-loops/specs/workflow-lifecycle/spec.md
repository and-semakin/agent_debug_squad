# workflow-lifecycle Delta

## MODIFIED Requirements

### Requirement: Recovery never silently repeats uncertain work
On restart, the system SHALL preserve committed completed tasks and their artifacts, recompute readiness, and automatically continue an otherwise running execution with no uncertain attempts. Paused executions SHALL stay paused. Attempts reserved or running without a committed terminal outcome SHALL become interrupted and put the execution in needs_attention with new dispatch stopped. No such attempt SHALL be automatically resent or treated as an allowed failure. Attempts in the `judging` phase are an exception: their backend work is complete and their response artifact is committed, so recovery SHALL NOT interrupt them and SHALL re-run their verdict classification. For executions with loops, the persisted loop iteration counters SHALL survive restart: committed iterations and their artifacts are preserved, an interrupted body attempt recovers as any interrupted attempt, and retrying it continues the same iteration — the loop neither advances to the next iteration nor restarts from the first. The new binary SHALL load snapshots saved with schema version 1 or 2; this is an upgrade compatibility guarantee only. Reading schema-2 snapshots with older binaries and downgrade conversion are outside this change's supported contract. Newly written snapshots SHALL carry schema version 2 and MUST NOT be relabeled as schema 1 to accommodate old readers; unknown other schema versions or damaged authoritative state SHALL fail closed with an actionable error. Cancellation intent SHALL survive restart. Loop failure/blocking holds SHALL survive restart with the same iteration, failed outcomes, and actionable attention reasons; recovery MUST NOT automatically retry failed work or turn the hold into terminal failure. Iteration advance SHALL be durably committed before next-iteration backend dispatch, with task states atomically saved or derivable from the committed counter. Recovery around this commit MUST neither repeat a completed iteration nor skip an iteration. Persistence failure during advance MUST stop new dispatch.

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
- **WHEN** a snapshot saved by a binary without loops (schema version 1) is recovered
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

### Requirement: Execution observation includes outcomes and intervention
`GET /workflows` SHALL list historical and active execution summaries. `GET /workflows/{id}` SHALL expose execution state/revision, task and attempt identities/states, dependency-blocking reasons, active/ready counts, errors, output paths, and current run progress including pending permissions. Attempt states SHALL include the `judging` phase, and views SHALL expose per-attempt verdict data — verdict name, confidence, probability distribution, model, and source — together with judge-related attention reasons for uncertain verdicts and judge unavailability. Attempt views SHALL expose the iteration number for loop body attempts, and the execution view SHALL expose per-loop state: loop name, current iteration, and `max_iterations`. Task counts SHALL count tasks, not iterations. Unknown IDs SHALL return 404. Task states SHALL include pending, ready, dispatching, running, succeeded, failed, interrupted, blocked, and cancelled. Execution states SHALL include running, paused, needs_attention, cancelling, cancelled, succeeded, completed_with_errors, and failed.

An unresolved loop failure/blocking hold SHALL take precedence over final-state derivation and keep the execution in `needs_attention`, even when all tasks are settled. Loop views SHALL expose state `running`, `needs_attention`, or `done`; execution attention reasons SHALL identify the loop, iteration, and failed or blocked task, expose the underlying error or blocking reason, and indicate retry or cancellation as intervention options. When work settles and no loop hold or other attention reason remains, any blocked task or non-tolerated failed task SHALL make the execution failed; otherwise any tolerated failed task SHALL make it completed_with_errors; otherwise it SHALL succeed; for executions with loops these verdicts are derived from the final iteration's task states. Uncertain attempts SHALL require attention rather than produce a final success. Workflow long-polling SHALL return on a terminal outcome, intervention, or wait expiry; expiry SHALL NOT cancel the execution. Waits SHALL accept integer `timeout_seconds` from 1 to 600, default 30, and return 400 otherwise. Permission intervention SHALL be exposed within one second of local publication under normal operation.

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

### Requirement: Explicit retries preserve history and input consistency
`POST /workflows/{id}/tasks/{task}/retry` SHALL accept a nonempty `request_id` and `expected_attempt`, creating a new queued attempt identity only for a failed/interrupted task that passes the descendant-reservation guard. For a task outside all loops, every transitive descendant through `needs` MUST have no attempt reservations, as before. For a loop body task, a new retry MUST target its latest failed/interrupted attempt in the loop's current iteration: same-loop transitive descendants MUST have no reservations in that iteration, and descendants outside that loop MUST have no reservations in any iteration. Reservations from earlier iterations of the same loop MUST NOT block the retry. New retries targeting earlier iterations SHALL return 409 without changing history or loop counters. The retry attempt SHALL retain the target iteration and receive the next monotonically increasing per-task attempt number. If an eligible retry reopens a done loop, that loop SHALL return to running at the same iteration, and outside consumers SHALL wait until it settles again. Guard evaluation and retry reservation SHALL be serialized with iteration advance and dispatch. Repeated identical retry requests SHALL return the originally created attempt; stale or conflicting requests SHALL return 409. Retry SHALL preserve prior artifacts, reset derived blocked descendants for reevaluation, preserve independent completed work, and use a fresh backend conversation. Interrupted retries SHALL additionally require `confirm_previous_stopped: true`, recorded as the caller's cleanup assertion rather than a guarantee by Squad; a known active worker MUST NOT be bypassed. Cancelled/cancelling executions SHALL reject retries. A failed or completed_with_errors execution can reopen only if no other execution is active; a paused execution SHALL remain paused after retry. No retries SHALL happen automatically. Explicit retries requested by users or coordinator agents SHALL NOT count against the automatic dispatch bound or consume additional loop iterations; this change imposes no explicit-retry count limit, while all eligibility and consistency guards remain enforced. Loop failure/blocking attention reasons MUST NOT by themselves reject an otherwise eligible retry; multiple eligible retries SHALL be queueable while other loop failures or interruption reasons remain. Artifact/storage and judge-related holds retain their resolution requirements; resume SHALL be able to address those causes independently of loop failures, as specified by the control requirement. After reserving a retry, the system SHALL recompute dependent readiness using the queued attempt as pending, remove only resolved failure/blocking reasons, and resume dispatch only when every attention reason is cleared and the execution is not paused. Blocked tasks without attempts SHALL be repaired by retrying eligible failed causal predecessors, not by retrying the blocked tasks themselves.

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
- **WHEN** a loop finishes with a tolerated failed body task and an outside descendant reserves an attempt before that body task is retried
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

### Requirement: Pause and cancellation have distinct effects
`POST /workflows/{id}/pause` SHALL durably prevent new dispatch reservations while allowing already dispatched tasks to finish. Repeated pause while paused SHALL be idempotent. `POST /workflows/{id}/resume` SHALL revalidate state and artifacts and resume paused or needs_attention work only when no recovery/artifact uncertainty remains. `POST /workflows/{id}/cancel` SHALL durably prevent new scheduling, cancel active owned runs, and mark remaining unstarted tasks cancelled. It SHALL reach cancelled only after owned workers stop or, for interrupted attempts whose cleanup cannot be checked after restart, the caller explicitly supplies `confirm_previous_stopped: true`. The system SHALL record that assertion and MUST NOT allow it to override known active workers in the current process. Unconfirmed cleanup SHALL remain observable and MUST NOT be reported as completed cancellation. Cancellation SHALL override allowed_to_fail and success thresholds. For a paused or needs_attention execution with loops, a valid resume request SHALL be accepted with HTTP 200 and the current execution view even if loop failure/blocking reasons remain. It SHALL request running mode and independently revalidate artifacts, clear only reasons proven resolved, and re-attempt held verdict classifications whose own response artifacts are verified and whose state can be durably persisted. An unrelated loop failure or interruption MUST NOT prevent these safe recovery actions. Pending classification recovery SHALL retain its attention hold until its committed outcome resolves it, and repeated resume MUST NOT create concurrent duplicate judge calls for the same attempt. Resume MUST NOT waive failed dependencies, unmet thresholds, or unresolved uncertainty, and MUST NOT repeat agent work. While any attention reason remains, the execution SHALL remain needs_attention with no ordinary task dispatch or loop advance. A successful HTTP response acknowledges the recovery request, not that execution has resumed. Actual storage failures SHALL remain errors. Existing loopless control behavior SHALL remain unchanged. Cancellation SHALL be accepted from that hold and take precedence over failure attention while following the same cleanup requirements. Invalid state transitions SHALL return 409; repeated cancellation while cancelling/cancelled SHALL be idempotent.

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
