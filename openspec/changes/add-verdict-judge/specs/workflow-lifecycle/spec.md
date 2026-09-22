# workflow-lifecycle Delta

## MODIFIED Requirements

### Requirement: Recovery never silently repeats uncertain work
On restart, the system SHALL preserve committed completed tasks and their artifacts, recompute readiness, and automatically continue an otherwise running execution with no uncertain attempts. Paused executions SHALL stay paused. Attempts reserved or running without a committed terminal outcome SHALL become interrupted and put the execution in needs_attention with new dispatch stopped. No such attempt SHALL be automatically resent or treated as an allowed failure. Attempts in the `judging` phase are an exception: their backend work is complete and their response artifact is committed, so recovery SHALL NOT interrupt them and SHALL re-run their verdict classification. Unknown saved schema versions or damaged authoritative state SHALL fail closed with an actionable error. Cancellation intent SHALL survive restart.

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

### Requirement: Execution observation includes outcomes and intervention
`GET /workflows` SHALL list historical and active execution summaries. `GET /workflows/{id}` SHALL expose execution state/revision, task and attempt identities/states, dependency-blocking reasons, active/ready counts, errors, output paths, and current run progress including pending permissions. Attempt states SHALL include the `judging` phase, and views SHALL expose per-attempt verdict data — verdict name, confidence, probability distribution, model, and source — together with judge-related attention reasons for uncertain verdicts and judge unavailability. Unknown IDs SHALL return 404. Task states SHALL include pending, ready, dispatching, running, succeeded, failed, interrupted, blocked, and cancelled. Execution states SHALL include running, paused, needs_attention, cancelling, cancelled, succeeded, completed_with_errors, and failed.

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

#### Scenario: Judging and verdict data are observable
- **WHEN** a verdict task's attempt is judging, or has settled with a verdict, or the execution is held on an uncertain verdict or judge unavailability
- **THEN** the execution view shows the attempt's judging state or recorded verdict data, and the corresponding attention reason when held
