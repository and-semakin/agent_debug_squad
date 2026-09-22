# declarative-workflows Specification

## Purpose

Execute declarative graphs of independent agent conversations with predictable dependency ordering, bounded parallelism, and explicit transfer of predecessor results.

## Requirements

### Requirement: Workflow definitions are validated before execution
The system SHALL accept an optional YAML `workflow` with `version: 1`, nonempty `name`, positive integer `max_parallel`, and a nonempty task map. Tasks SHALL declare nonempty `agent` and `prompt`, optional `needs` defaulting to an empty list, boolean `allowed_to_fail` defaulting to false, and integer `min_successful_dependencies` defaulting to zero. Tasks SHALL also accept an optional `verdicts` map of verdict names to optional human descriptions. The workflow SHALL accept optional `confidence_threshold` (a number greater than 0 and at most 1, defaulting to 0.8) and `on_uncertain` (either `hold` or `error`, defaulting to `hold`). The system MUST reject unknown workflow/task fields, duplicate YAML keys, unsafe identifiers, unknown agents/dependencies, repeated dependency entries, self-dependencies, cycles, repeated agent references across tasks, and thresholds outside zero through the number of direct dependencies, before dispatching any task. It MUST also reject verdict maps that are empty, contain unsafe or duplicated verdict names, contain the reserved name `uncertain`, or declare fewer than two verdicts; and `confidence_threshold` or `on_uncertain` values outside their allowed sets. Identical model/backend configurations under different agent names SHALL be accepted. The `verdicts`, `confidence_threshold`, and `on_uncertain` fields SHALL participate in the definition identity used for idempotent replay; when unset, the identity MUST remain byte-identical to definitions parsed by previous versions.

#### Scenario: Invalid graph has no side effects
- **WHEN** a definition contains a cycle, unknown dependency, duplicate task key, or invalid threshold
- **THEN** validation returns an actionable error and no task is dispatched

#### Scenario: Independent instances of the same model
- **WHEN** two tasks reference different agent names configured with the same backend and model
- **THEN** the graph is accepted

#### Scenario: Reused agent identity
- **WHEN** two nodes reference the same agent name
- **THEN** validation rejects the graph and explains that distinct agent definitions can reuse the same model

#### Scenario: Verdict map is validated
- **WHEN** a task declares an empty verdict map, a single verdict, an unsafe verdict name, or the reserved name `uncertain`
- **THEN** validation rejects the definition with an actionable error before any dispatch

#### Scenario: Verdict settings participate in replay identity
- **WHEN** the same request ID is resubmitted after a task's verdict map, the threshold, or `on_uncertain` changed
- **THEN** the submission is treated as a changed definition and rejected rather than replayed

#### Scenario: Definitions without verdicts hash identically
- **WHEN** a definition declares no verdict settings
- **THEN** its definition identity is byte-identical to the same definition parsed before this capability existed

### Requirement: Task conversations are isolated
Every task attempt SHALL start a fresh backend conversation owned by its workflow execution, task, and attempt. It MUST NOT inherit another task's chat history, a manual agent conversation, or a previous execution's conversation. A retry SHALL also use a fresh conversation. Identical model/backend selections SHALL NOT imply a shared session. Dependency results SHALL be provided explicitly.

#### Scenario: Matching model without matching context
- **WHEN** two nodes using the same model run and a manual conversation for that model already exists
- **THEN** all three conversations remain distinct and workflow nodes receive only their own instructions and declared dependency inputs

#### Scenario: Subsequent execution
- **WHEN** a completed definition is submitted again with a new request ID
- **THEN** its nodes start fresh conversations and the previous execution's artifacts remain available

### Requirement: Program-driven scheduling respects the graph and limit
After explicit workflow submission, the system SHALL schedule tasks without additional coordinator turns. It SHALL wait for all direct dependencies to settle before evaluating a dependent task. At most `max_parallel` task attempts SHALL be dispatched or running at once within the execution, counting permission waits and attempts whose cancellation has not completed. Ready-task ties SHALL use lexicographic task ID order. Completion order of concurrently executing tasks is not guaranteed. Backend-native subagents and independent manual runs are outside this limit. The system SHALL NOT detect file-write intent, impose workspace edit locks, or create worktrees.

#### Scenario: Linear chain
- **WHEN** A succeeds in A to B to C
- **THEN** B is automatically dispatched, and C waits until B succeeds

#### Scenario: Fan-out and fan-in
- **WHEN** A succeeds, B and C depend on A, D depends on both B and C, and two slots are available
- **THEN** B and C can execute concurrently, and D starts once both settle acceptably, receiving both outcomes regardless of completion order

#### Scenario: Slot accounting
- **WHEN** two attempts occupy a limit of two and one is waiting for permission
- **THEN** a third ready task stays queued until an occupied slot is released

#### Scenario: Shared files are not coordinated
- **WHEN** two otherwise eligible tasks intend to modify the same workspace
- **THEN** the scheduler does not serialize them based on file access or create isolated worktrees

### Requirement: Dependency failure tolerance and success thresholds compose
A direct dependency SHALL be acceptable only when it succeeded or when it failed with its own `allowed_to_fail: true`. A task SHALL start only after every dependency settles acceptably and the count of succeeded direct dependencies meets its `min_successful_dependencies`. Tolerated failures MUST remain failed and MUST NOT count as successes. A threshold MUST NOT override an unacceptable dependency. Blocked tasks SHALL have explicit reasons and propagate blocking to descendants; their `allowed_to_fail` SHALL NOT convert non-execution into an acceptable failure. An interruption or cancellation SHALL NOT be waived by `allowed_to_fail`.

#### Scenario: Optional reviewer fails
- **WHEN** one optional reviewer fails, another succeeds, and their consumer requires one success
- **THEN** the consumer runs after both attempts settle and receives the successful output plus the failed reviewer's error

#### Scenario: All reviewers fail
- **WHEN** all reviewers fail with allowed failures and their consumer requires one success
- **THEN** the consumer is blocked with a success-threshold reason and is not dispatched

#### Scenario: Default zero threshold
- **WHEN** all direct dependencies are tolerated failures and the consumer's threshold is omitted
- **THEN** the consumer can run after all dependencies settle and receives their failure information

#### Scenario: Mandatory failure despite enough successes
- **WHEN** one mandatory dependency fails and another succeeds, satisfying the numerical threshold
- **THEN** the consumer remains blocked by the mandatory failure

#### Scenario: Threshold does not cause early aggregation
- **WHEN** one reviewer succeeds, another is still running, and the threshold is one
- **THEN** the consumer waits for the remaining reviewer

#### Scenario: Blocking and independent work
- **WHEN** a required predecessor fails and blocks one branch
- **THEN** blocking propagates through that branch, while independent runnable branches continue

### Requirement: Handoff is complete and bound to saved attempt results
Before dispatch, the system SHALL save the exact task message and an input manifest containing every direct dependency in sorted task ID order, with task/agent/attempt/run identities, outcome, error when present, and successful result path, byte size, and content hash. It SHALL provide the manifest and readable local result references to the receiving agent, without sharing predecessor chat history. It MUST verify committed successful result files before dispatch, MUST NOT silently truncate them, and MUST hold execution for intervention if they are missing or changed. Partial output from a failed attempt SHALL NOT be represented as successful output. Agent response text MUST NOT modify the graph or scheduling policy.

#### Scenario: Stable fan-in input
- **WHEN** B and C complete in either order and D becomes ready
- **THEN** D's saved manifest contains the same ordered dependency identities and references their exact successful attempt results

#### Scenario: Missing committed result
- **WHEN** a successful dependency's committed result file is removed or its contents change before consumer dispatch
- **THEN** the workflow enters needs_attention and the consumer is not dispatched

#### Scenario: Large responses
- **WHEN** predecessor responses are too large to embed conveniently in the next prompt
- **THEN** the complete saved responses remain readable through manifest paths without silent truncation

### Requirement: Success and timeouts have explicit boundaries
A workflow task SHALL succeed only after its backend turn completes successfully, owned execution stops, a nonempty final response is saved, and the outcome is durably committed. An empty response SHALL produce a failed task with a missing-output reason. For a task that declares verdicts, the attempt SHALL additionally pass through a `judging` phase after the response is saved and SHALL settle only after its verdict is resolved; dependent tasks wait for that settlement. The task timeout SHALL NOT extend into the judging phase; judging is bounded by the judge's own decision timeout. The workflow SHALL have `task_timeout_seconds` defaulting to 1800 and tasks SHALL accept an optional positive `timeout_seconds` override. The timeout SHALL count wall time from dispatch, including permissions and backend subagents, excluding dependency/queue wait. Expiry SHALL cancel owned work and produce a failed timeout outcome only after cleanup is confirmed; uncertain cleanup SHALL produce interruption requiring intervention. V1 SHALL NOT claim that a successful textual response proves substantive task correctness, and in this change the resolved verdict value MUST NOT alter graph scheduling, dependency evaluation, or the execution's final state.

#### Scenario: Empty completed response
- **WHEN** the adapter completes without a nonempty final response
- **THEN** the task fails with missing_output and failure policy determines its dependents

#### Scenario: Optional timeout
- **WHEN** an allowed-to-fail task exceeds its timeout and its owned work is confirmed stopped
- **THEN** it remains failed with a timeout reason and can be waived by its dependents

#### Scenario: Unconfirmed cleanup
- **WHEN** cancellation after timeout cannot confirm the owned execution stopped
- **THEN** the attempt is interrupted and no new workflow task starts until intervention resolves the uncertainty

#### Scenario: Verdict settlement precedes dependents
- **WHEN** a verdict task's response is saved and the judge has not yet returned
- **THEN** the attempt is in the judging phase, its dependents are not dispatched, and the task timeout does not apply to the pending judge call

#### Scenario: Verdict value does not drive scheduling
- **WHEN** a verdict task settles with any declared verdict
- **THEN** dependency evaluation, dispatch decisions, and the execution's final state are exactly those of the same definition without verdicts

### Requirement: Reviewer quorum is an ordinary workflow
The repository skill and examples SHALL demonstrate translating a familiar reviewer-quorum request into parallel reviewer tasks and a dependent verifier task without requiring the user to author YAML. The verifier SHALL be instructed to check evidence against code, deduplicate findings, align severity, produce the final report, and identify missing reviews. User-specified models, backends, and constraints SHALL be preserved. Optional reviewer failures and a positive success threshold SHALL be demonstrated explicitly. The Go service MUST NOT require a special review execution mode.

#### Scenario: Familiar review request
- **WHEN** a facilitator follows the skill for a request to run specified reviewers and rank findings
- **THEN** it can prepare and submit an ordinary workflow, wait for completion, and deliver the verifier result without manually dispatching each dependency transition

#### Scenario: No review results
- **WHEN** every reviewer in the documented optional-reviewer example fails
- **THEN** the positive threshold prevents a verifier report being presented as a completed review

### Requirement: Ephemeral agents declare a one-shot lifecycle
Agent definitions SHALL accept an optional boolean `ephemeral` field, defaulting to false. An ephemeral agent declares a one-shot execution model for workflows: every invocation — every task attempt, every retry, and any future repeated visit such as a loop iteration — MUST execute on a newly created runtime whose backend session starts empty, and MUST NOT inherit conversation history, backend session identity, or runtime state from any earlier invocation of that agent, whether within the same execution or another. The declaration SHALL be captured in the execution's immutable saved agent configuration at submission, SHALL participate in the definition identity used for idempotent replay, and MUST be preserved through recovery so a recovered execution keeps its original flag value. In this version the flag MUST NOT relax graph validation: one agent may still be referenced by at most one task. It MUST NOT alter facilitator-driven manual turns, which retain backend session continuity exactly as for non-ephemeral agents, and it MUST NOT change HTTP response shapes.

#### Scenario: Declaration persists with the execution
- **WHEN** a workflow referencing an agent configured with `ephemeral: true` is submitted
- **THEN** the execution's saved agent configuration records the flag, and the persisted snapshot retains that value across recovery restarts

#### Scenario: Flag participates in definition identity
- **WHEN** the same request ID is resubmitted after the referenced agent's `ephemeral` value changed
- **THEN** the submission is treated as a changed definition and rejected, rather than replayed as the original execution

#### Scenario: Fresh runtime on every invocation
- **WHEN** a task attempt of an ephemeral agent is dispatched, and later a retry of that task is dispatched
- **THEN** each attempt runs on a new runtime whose backend session starts empty, with no conversation or session state carried between the attempts

#### Scenario: Default preserves behavior
- **WHEN** the `ephemeral` field is omitted or set to false
- **THEN** configuration loading, graph validation, scheduling, and observable results are indistinguishable from the behavior without this field

#### Scenario: One reference per task still enforced
- **WHEN** a definition references the same ephemeral agent from two different tasks
- **THEN** validation rejects the graph with the same reused-agent explanation as for non-ephemeral agents

#### Scenario: Facilitator continuity unchanged
- **WHEN** a facilitator sends consecutive manual turns to an agent declared ephemeral
- **THEN** the manual conversation preserves backend session continuity exactly as it does for non-ephemeral agents

#### Scenario: Non-boolean flag value is rejected
- **WHEN** an agent definition sets `ephemeral` to a non-boolean value
- **THEN** configuration loading fails with an actionable error and no workflow execution starts
