# declarative-workflows Delta

## MODIFIED Requirements

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
