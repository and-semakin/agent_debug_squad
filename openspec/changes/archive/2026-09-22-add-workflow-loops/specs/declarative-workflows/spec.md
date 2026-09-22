# declarative-workflows Delta

## MODIFIED Requirements

### Requirement: Workflow definitions are validated before execution
The system SHALL accept an optional YAML `workflow` with `version: 1`, nonempty `name`, positive integer `max_parallel`, and a nonempty task map. Tasks SHALL declare nonempty `agent` and `prompt`, optional `needs` defaulting to an empty list, boolean `allowed_to_fail` defaulting to false, and integer `min_successful_dependencies` defaulting to zero. Tasks SHALL also accept an optional `verdicts` map of verdict names to optional human descriptions and an optional `loop` naming a declared loop. The workflow SHALL accept optional `confidence_threshold` (a number greater than 0 and at most 1, defaulting to 0.8), `on_uncertain` (either `hold` or `error`, defaulting to `hold`), and a `loops` map of loop name to loop definition, where a loop definition declares exactly a required positive integer `max_iterations`. The system MUST reject unknown workflow/task fields, duplicate YAML keys, unsafe identifiers, unknown agents/dependencies, repeated dependency entries, self-dependencies, cycles, repeated agent references across tasks, and thresholds outside zero through the number of direct dependencies, before dispatching any task. It MUST also reject verdict maps that are empty, contain unsafe or duplicated verdict names, contain the reserved name `uncertain`, or declare fewer than two verdicts; `confidence_threshold` or `on_uncertain` values outside their allowed sets; loop definitions with a missing or non-positive `max_iterations`; `loop` references to undeclared loops; loops with no member tasks; dependencies from a task in one loop to a task in another loop; and dependency cycles that pass through a loop boundary (detected on the graph with each loop collapsed to a single node). Every loop-related rejection message SHALL name the offending loop or task and include a short corrective example, so agents authoring YAML can repair the definition without additional context. Identical model/backend configurations under different agent names SHALL be accepted. The `verdicts`, `confidence_threshold`, `on_uncertain`, `loops`, and `loop` fields SHALL participate in the definition identity used for idempotent replay; when unset, the identity MUST remain byte-identical to definitions parsed by previous versions.

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

#### Scenario: Loop configuration is validated with corrective examples
- **WHEN** a loop omits `max_iterations`, sets it to zero or a negative number, or a task's `loop` names no declared loop
- **THEN** validation rejects the definition with an error that names the loop or task, states what is wrong, and shows a short corrected example

#### Scenario: Empty loop is rejected
- **WHEN** a declared loop has no member tasks
- **THEN** validation rejects the definition with an actionable error

#### Scenario: Cross-loop dependency is rejected
- **WHEN** a task in loop A declares `needs` on a task in loop B
- **THEN** validation rejects the definition, explaining that loops may depend only on their own tasks and on tasks outside all loops

#### Scenario: Cycle through a loop boundary is rejected
- **WHEN** an outside task depends on a loop's body task while another body task in the same loop depends on that outside task
- **THEN** validation rejects the definition as a dependency cycle through the loop boundary, naming the loop

#### Scenario: Loop settings participate in replay identity
- **WHEN** the same request ID is resubmitted after a loop's `max_iterations` changed or a task's `loop` label changed
- **THEN** the submission is treated as a changed definition and rejected rather than replayed

#### Scenario: Definitions without loops hash identically
- **WHEN** a definition declares no loops
- **THEN** its definition identity is byte-identical to the same definition parsed before loops existed

## ADDED Requirements

### Requirement: Loop bodies re-arm for a bounded number of iterations
A loop SHALL execute its member tasks (the body) as an ordinary dependency graph with at most one initial automatic attempt per task per iteration. The loop MUST NOT exceed `max_iterations` iterations and SHALL complete exactly that many on normal completion; cancellation may end it earlier and intervention may hold it indefinitely. When an iteration settles acceptably — every body task succeeded or failed under the existing tolerance rules, evaluated within that iteration — the system SHALL re-arm the body: reset body task states and dispatch the next iteration's attempts on fresh runtime conversations, exactly as first attempts. Each attempt SHALL record its one-based iteration number, and prior iterations' attempts SHALL remain inspectable history. A non-tolerated body task failure or any blocked body task SHALL hold the loop at its current iteration and put the execution in `needs_attention`, rather than finalize it as failed. This includes unmet success thresholds and blocking caused by outside prerequisites. Failed attempts and blocked body tasks SHALL retain their states and reasons. Outside consumers SHALL remain pending with an explicit loop-wait reason until the loop completes acceptably. While this hold exists, no new task SHALL dispatch and no loop SHALL advance anywhere in the execution; already dispatched work MAY finish and its outcomes SHALL be preserved. Eligible explicit retries SHALL repair the current iteration under the retry consistency rules; cancellation SHALL abandon the execution under the existing cleanup rules. Resume MUST NOT waive unresolved failures or thresholds, but SHALL be allowed to resolve independent artifact or judge attention causes while preserving the loop hold. This recovery activity is not task dispatch or iteration advance. A tolerated failure that leaves no body task blocked SHALL NOT cause this hold. The number of initial automatic body dispatches SHALL be bounded by the number of body tasks multiplied by `max_iterations`. Explicit retries through the retry API, whether requested by a user or coordinator agent, SHALL be excluded from this dispatch bound; they MUST retain the current iteration and MUST NOT consume or extend the iteration limit. This change SHALL NOT introduce a limit on explicit retry count or elapsed execution time. Retry eligibility and input-consistency guards SHALL continue to apply; failures MUST NOT trigger automatic retries. The execution's final state SHALL be derived from the final iteration's task states; tolerated failures in earlier iterations remain visible in attempt history but MUST NOT degrade the final verdict.

#### Scenario: Static loop runs exactly N times
- **WHEN** a loop with `max_iterations: 3` and a two-task body completes without failures
- **THEN** each body task records three attempts with iteration numbers 1 through 3, and downstream tasks outside the loop start after the third iteration settles

#### Scenario: Explicit retries do not consume the iteration budget
- **WHEN** a one-task loop has max_iterations 1, its initial attempt fails, and two successive eligible explicit retries are requested, the first failing and the second succeeding
- **THEN** all three attempts belong to iteration 1, the loop completes after the successful retry, no iteration 2 is created, and no retry occurs without an explicit request

#### Scenario: Automatic dispatch bound excludes explicit retries
- **WHEN** a two-task loop with max_iterations 3 completes all iterations and one body task required an eligible explicit retry
- **THEN** there are six initial automatic body attempts and one explicit retry attempt, while the loop still completes exactly three iterations

#### Scenario: Tolerated failure re-runs next iteration
- **WHEN** an `allowed_to_fail` body task fails in iteration 1 and succeeds in iteration 2 of a 2-iteration loop
- **THEN** iteration 1 settles acceptably, the task is dispatched fresh in iteration 2, and the execution succeeds with the iteration-1 failure visible in history

#### Scenario: Hard failure waits for intervention
- **WHEN** a body task without `allowed_to_fail` fails in any iteration
- **THEN** the loop and execution enter needs_attention at the same iteration, outside consumers remain pending with a loop-wait reason, and new dispatch stops until intervention

#### Scenario: Threshold failure waits for repair
- **WHEN** tolerated reviewer failures leave a body consumer below its minimum successful dependency count
- **THEN** the consumer is blocked, the loop and execution need attention, and an eligible retry of a failed reviewer can restore readiness without advancing or restarting the iteration

#### Scenario: Outside prerequisite blocks a loop
- **WHEN** a failed outside prerequisite makes a body task blocked before its first attempt
- **THEN** the execution needs attention, identifies the failed prerequisite, and permits its retry only under the existing outside-task descendant guard

#### Scenario: Failure holds even without outside consumers
- **WHEN** a loop has a non-tolerated failure and all tasks are settled with no live workers or outside consumers
- **THEN** the execution remains needs_attention rather than becoming terminal failed

#### Scenario: Hold stops new independent work
- **WHEN** one loop needs intervention while independent work elsewhere in the execution is running or ready
- **THEN** running work may finish, ready work is not dispatched, and no loop advances until attention is resolved

#### Scenario: Thresholds compose per iteration
- **WHEN** a body consumer requires two of three same-loop dependencies and one fails tolerably in an iteration
- **THEN** that iteration's consumer runs on the two successful results, exactly as the same graph without a loop

#### Scenario: Sibling loops are independent
- **WHEN** a definition declares two loops and an outside task depends on both
- **THEN** each loop runs its own bounded iterations, and the outside task starts after both complete

### Requirement: Iteration handoff is scoped and carried over
A body task's input manifest SHALL resolve each `needs` entry to the same iteration's attempt for same-loop dependencies and to the single settled attempt for dependencies outside all loops. When a body re-arms for the next iteration, every body task's manifest SHALL additionally include a previous-iteration outcomes section: the settled attempts of all body tasks from the prior iteration in sorted task ID order, with task/agent/attempt/iteration identities, states, verdicts where recorded, errors, and successful result references — so a producer's output reaches an upstream body task (for example, reviewer reports reaching the implementer) without cyclic `needs`. For each prior-iteration body task, the section SHALL select its latest committed terminal attempt in that iteration, including any successful retry, rather than every historical retry attempt. Once the loop advances, prior iterations SHALL remain immutable and SHALL NOT accept new retries. Referenced artifacts remain subject to the existing verification rules, and responses MUST NOT be silently truncated in manifests.

#### Scenario: Same-iteration resolution
- **WHEN** body task B depends on body task A and iteration 2 dispatches B
- **THEN** B's manifest references A's iteration-2 attempt result, never an earlier iteration's

#### Scenario: Carry-over reaches upstream tasks
- **WHEN** a loop's body contains an implementer with no dependencies and reviewers depending on it, and iteration 2 begins
- **THEN** the implementer's iteration-2 manifest includes the reviewers' iteration-1 outcomes as readable result references

#### Scenario: Outside consumers see final iteration results
- **WHEN** a task outside a completed loop depends on a body task
- **THEN** its manifest references that task's final-iteration attempt result

#### Scenario: Outside dependencies are stable across iterations
- **WHEN** a body task depends on a task outside all loops
- **THEN** every iteration's manifest references that task's single settled attempt
