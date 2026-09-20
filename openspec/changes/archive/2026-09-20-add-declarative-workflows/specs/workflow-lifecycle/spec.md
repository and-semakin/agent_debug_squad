## Purpose

Make declarative workflow executions durable, inspectable, and controllable while preventing accidental replay of uncertain agent work and retaining usable manual sessions.

## ADDED Requirements

### Requirement: Workflow submission is explicit and idempotent
Serving a configuration SHALL NOT automatically start a new workflow. `POST /workflows` with a nonempty `request_id` SHALL create an execution of the configured definition and return 202. Repeating the same request ID with the same resolved definition SHALL return the original execution with 200 without new work, including after restart. Reusing an ID with a changed resolved definition SHALL return 409. Only one nonterminal workflow execution per server session SHALL be admitted in v1; competing submissions SHALL return 409. New executions SHALL retain an immutable copy of their resolved definition and agent configuration. Invalid submissions SHALL return 400 without dispatch.

#### Scenario: Lost creation response
- **WHEN** a client retries a submission after losing its HTTP response
- **THEN** it receives the original execution and no additional agent attempt is created

#### Scenario: Restart does not create a new execution
- **WHEN** the server restarts with the same configured workflow
- **THEN** it recovers saved executions without treating startup as a new submission

#### Scenario: Definition changes
- **WHEN** configuration is edited after an execution was created
- **THEN** that execution retains its original definition and the changed definition requires a new request ID

### Requirement: Dispatch and completion are durable decisions
The system SHALL durably reserve an attempt identity and its exact inputs before sending work to a backend, SHALL never dispatch that identity more than once in a live owner, and SHALL publish successful completion durably before releasing dependent tasks. Duplicate or lost completion notifications SHALL NOT cause duplicate or missing scheduling. Persistence failures SHALL stop new dispatch and surface an error rather than claim successful completion. A second server owner for the same session state SHALL be rejected before it mutates state or starts work.

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

### Requirement: Recovery never silently repeats uncertain work
On restart, the system SHALL preserve committed completed tasks and their artifacts, recompute readiness, and automatically continue an otherwise running execution with no uncertain attempts. Paused executions SHALL stay paused. Attempts reserved or running without a committed terminal outcome SHALL become interrupted and put the execution in needs_attention with new dispatch stopped. No such attempt SHALL be automatically resent or treated as an allowed failure. Unknown saved schema versions or damaged authoritative state SHALL fail closed with an actionable error. Cancellation intent SHALL survive restart.

#### Scenario: Crash between predecessors and consumer
- **WHEN** predecessor successes are committed and the process crashes before reserving their consumer
- **THEN** recovery dispatches the consumer without rerunning predecessors

#### Scenario: Crash around backend dispatch
- **WHEN** an attempt was reserved and the process crashes before committing its outcome, whether or not the backend received it
- **THEN** recovery marks it interrupted, stops new scheduling, and does not automatically repeat external work

#### Scenario: Restart while paused or cancelling
- **WHEN** a paused or cancelling execution is recovered
- **THEN** paused work stays paused and cancelling work does not start new tasks

### Requirement: Execution observation includes outcomes and intervention
`GET /workflows` SHALL list historical and active execution summaries. `GET /workflows/{id}` SHALL expose execution state/revision, task and attempt identities/states, dependency-blocking reasons, active/ready counts, errors, output paths, and current run progress including pending permissions. Unknown IDs SHALL return 404. Task states SHALL include pending, ready, dispatching, running, succeeded, failed, interrupted, blocked, and cancelled. Execution states SHALL include running, paused, needs_attention, cancelling, cancelled, succeeded, completed_with_errors, and failed.

When work settles, any blocked task or non-tolerated failed task SHALL make the execution failed; otherwise any tolerated failed task SHALL make it completed_with_errors; otherwise it SHALL succeed. Uncertain attempts SHALL require attention rather than produce a final success. Workflow long-polling SHALL return on a terminal outcome, intervention, or wait expiry; expiry SHALL NOT cancel the execution. Waits SHALL accept integer `timeout_seconds` from 1 to 600, default 30, and return 400 otherwise. Permission intervention SHALL be exposed within one second of local publication under normal operation.

#### Scenario: Degraded review completes
- **WHEN** one optional reviewer fails and the verifier succeeds using the remaining results, with no blocked tasks
- **THEN** the workflow is completed_with_errors and exposes both the final report and failed reviewer details

#### Scenario: Permission wait
- **WHEN** an active task needs manual permission or automatic approval fails
- **THEN** workflow observation shows the current run/request details and wakes waiting callers while independent branches can continue

#### Scenario: Poll expiry or client disconnect
- **WHEN** an HTTP wait expires or its client disconnects
- **THEN** workflow execution continues and expiry returns the current state without cancellation

### Requirement: Pause and cancellation have distinct effects
`POST /workflows/{id}/pause` SHALL durably prevent new dispatch reservations while allowing already dispatched tasks to finish. Repeated pause while paused SHALL be idempotent. `POST /workflows/{id}/resume` SHALL revalidate state and artifacts and resume paused or needs_attention work only when no recovery/artifact uncertainty remains. `POST /workflows/{id}/cancel` SHALL durably prevent new scheduling, cancel active owned runs, and mark remaining unstarted tasks cancelled. It SHALL reach cancelled only after owned workers stop or, for interrupted attempts whose cleanup cannot be checked after restart, the caller explicitly supplies `confirm_previous_stopped: true`. The system SHALL record that assertion and MUST NOT allow it to override known active workers in the current process. Unconfirmed cleanup SHALL remain observable and MUST NOT be reported as completed cancellation. Cancellation SHALL override allowed_to_fail and success thresholds. Invalid state transitions SHALL return 409; repeated cancellation while cancelling/cancelled SHALL be idempotent.

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

### Requirement: Explicit retries preserve history and input consistency
`POST /workflows/{id}/tasks/{task}/retry` SHALL accept a nonempty `request_id` and `expected_attempt`, creating a new queued attempt identity only for a failed/interrupted task whose transitive descendants have no attempt reservations. Repeated identical retry requests SHALL return the originally created attempt; stale or conflicting requests SHALL return 409. Retry SHALL preserve prior artifacts, reset derived blocked descendants for reevaluation, preserve independent completed work, and use a fresh backend conversation. Interrupted retries SHALL additionally require `confirm_previous_stopped: true`, recorded as the caller's cleanup assertion rather than a guarantee by Squad; a known active worker MUST NOT be bypassed. Cancelled/cancelling executions SHALL reject retries. A failed or completed_with_errors execution can reopen only if no other execution is active; a paused execution SHALL remain paused after retry. No retries SHALL happen automatically.

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

### Requirement: Workflow ownership preserves manual and backend contracts
Existing configurations without workflow, manual run/reset APIs, manual follow-up session continuity, and saved artifact readability SHALL remain usable. Workflow-owned attempts SHALL be observable through run APIs and use existing permission-reply APIs and approval policies. Manual run/reset mutations targeting workflow-owned runtimes SHALL return 409. Workflow isolation MUST NOT be implemented by resetting a user's existing manual conversation. Existing loopback-only access and constrained child-process environment handling SHALL remain in force.

#### Scenario: Legacy manual review
- **WHEN** a facilitator uses an old configuration to send a review and a follow-up to the same manual agent
- **THEN** both turns use the existing manual behavior and preserve backend conversation continuity

#### Scenario: Permission reply routing
- **WHEN** a workflow task exposes an owned backend permission request
- **THEN** the existing run-scoped reply endpoint resolves only that active request, with existing stale-request and approval rules

#### Scenario: External mutation of owned task
- **WHEN** a caller attempts manual reset or an extra turn on a workflow-owned runtime
- **THEN** the call returns 409 without changing the task session or graph state
