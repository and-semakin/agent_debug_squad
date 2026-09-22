# declarative-workflows Delta

## MODIFIED Requirements

### Requirement: Loop bodies re-arm for a bounded number of iterations
A loop SHALL execute its member tasks (the body) as an ordinary dependency graph with at most one initial automatic attempt per task per iteration. The loop MUST NOT exceed its effective iteration cap — the declared `max_iterations` plus extensions granted only through the explicit extend control — and SHALL complete exactly that many iterations on normal completion unless its condition breaks the loop or an explicit stop completes the current iteration; cancellation may end it earlier and intervention may hold it indefinitely. When an iteration settles acceptably — every body task succeeded or failed under the existing tolerance rules, evaluated within that iteration — the system SHALL re-arm the body: reset body task states and dispatch the next iteration's attempts on fresh runtime conversations, exactly as first attempts. Each attempt SHALL record its one-based iteration number, and prior iterations' attempts SHALL remain inspectable history. A non-tolerated body task failure or any blocked body task SHALL hold the loop at its current iteration and put the execution in `needs_attention`, rather than finalize it as failed. This includes unmet success thresholds and blocking caused by outside prerequisites. Failed attempts and blocked body tasks SHALL retain their states and reasons. Outside consumers SHALL remain pending with an explicit loop-wait reason until the loop completes acceptably. While this hold exists, no new task SHALL dispatch and no loop SHALL advance anywhere in the execution; already dispatched work MAY finish and its outcomes SHALL be preserved. Eligible explicit retries SHALL repair the current iteration under the retry consistency rules; cancellation SHALL abandon the execution under the existing cleanup rules. Resume MUST NOT waive unresolved failures or thresholds, but SHALL be allowed to resolve independent artifact or judge attention causes while preserving the loop hold. This recovery activity is not task dispatch or iteration advance. A tolerated failure that leaves no body task blocked SHALL NOT cause this hold. The number of initial automatic body dispatches SHALL be bounded by the number of body tasks multiplied by the effective iteration cap; every cap increase is an explicit, idempotent, audited human action, so the system itself never dispatches unboundedly. Explicit retries through the retry API, whether requested by a user or coordinator agent, SHALL be excluded from this dispatch bound; they MUST retain the current iteration and MUST NOT consume or extend the iteration cap. This change SHALL NOT introduce a limit on explicit retry count or elapsed execution time. Retry eligibility and input-consistency guards SHALL continue to apply; failures MUST NOT trigger automatic retries. The execution's final state SHALL be derived from the final iteration's task states; tolerated failures in earlier iterations remain visible in attempt history but MUST NOT degrade the final verdict.

#### Scenario: Static loop runs exactly N times
- **WHEN** a loop with `max_iterations: 3` and a two-task body completes without failures
- **THEN** each body task records three attempts with iteration numbers 1 through 3, and downstream tasks outside the loop start after the third iteration settles

#### Scenario: Explicit retries do not consume the iteration budget
- **WHEN** a one-task loop has max_iterations 1, its initial attempt fails, and two successive eligible explicit retries are requested, the first failing and the second succeeding
- **THEN** all three attempts belong to iteration 1, the loop completes after the successful retry, no iteration 2 is created, and no retry occurs without an explicit request

#### Scenario: Automatic dispatch bound excludes explicit retries
- **WHEN** a two-task loop with max_iterations 3 completes all iterations and one body task required an eligible explicit retry
- **THEN** there are six initial automatic body attempts and one explicit retry attempt, while the loop still completes exactly three iterations

#### Scenario: Extended budget stays explicit and bounded
- **WHEN** a 2-iteration loop is extended by 3 iterations through the control and completes the extended budget
- **THEN** the loop runs five iterations in total, the extension is recorded as an audited control action, and the automatic dispatch bound grows to the extended cap only

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

### Requirement: Workflow definitions are validated before execution
The system SHALL accept an optional YAML `workflow` with `version: 1`, nonempty `name`, positive integer `max_parallel`, and a nonempty task map. Tasks SHALL declare nonempty `agent` and `prompt`, optional `needs` defaulting to an empty list, boolean `allowed_to_fail` defaulting to false, and integer `min_successful_dependencies` defaulting to zero. Tasks SHALL also accept an optional `verdicts` map of verdict names to optional human descriptions and an optional `loop` naming a declared loop. The workflow SHALL accept optional `confidence_threshold` (a number greater than 0 and at most 1, defaulting to 0.8), `on_uncertain` (either `needs_attention` or `error`, defaulting to `needs_attention`), and a `loops` map of loop name to loop definition, where a loop definition declares exactly a required positive integer `max_iterations` and optional condition fields: `until_task` naming the loop's condition task, `on_verdict` mapping verdicts to actions, and `on_exhaustion` selecting an exhaustion policy. The system MUST reject unknown workflow/task fields, duplicate YAML keys, unsafe identifiers, unknown agents/dependencies, repeated dependency entries, self-dependencies, cycles, repeated agent references across tasks, and thresholds outside zero through the number of direct dependencies, before dispatching any task. It MUST also reject verdict maps that are empty, contain unsafe or duplicated verdict names, contain the reserved name `uncertain`, or declare fewer than two verdicts; `confidence_threshold` or `on_uncertain` values outside their allowed sets; loop definitions with a missing or non-positive `max_iterations`; `loop` references to undeclared loops; loops with no member tasks; dependencies from a task in one loop to a task in another loop; and dependency cycles that pass through a loop boundary (detected on the graph with each loop collapsed to a single node). It MUST additionally reject condition misconfiguration: an `until_task` that is not a member of the loop's body, does not declare `verdicts`, sets `allowed_to_fail: true`, or is not the unique sink within the body (every other body task MUST reach until_task through same-loop dependency edges); an `on_verdict` without `until_task` and an `until_task` without `on_verdict`; an `on_verdict` that maps a verdict not declared by `until_task`, omits a declared verdict, or uses an action other than `break`, `continue`, or `needs_attention`; an `on_exhaustion` without the until_task/on_verdict pair; and an `on_exhaustion` other than `needs_attention` or `succeed`. A singleton body with until_task as its only task SHALL satisfy the unique-sink rule. Every loop-related rejection message SHALL name the offending loop or task and include a short corrective example, so agents authoring YAML can repair the definition without additional context. Identical model/backend configurations under different agent names SHALL be accepted. The `verdicts`, `confidence_threshold`, `on_uncertain`, `loops`, `loop`, `until_task`, `on_verdict`, and `on_exhaustion` fields SHALL participate in the definition identity used for idempotent replay; when unset, the identity MUST remain byte-identical to definitions parsed by previous versions.

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

#### Scenario: Uncertainty policy uses the execution-state vocabulary
- **WHEN** YAML sets on_uncertain to needs_attention or omits it
- **THEN** the policy waits for intervention on low confidence, while on_uncertain hold is rejected with an error showing on_uncertain needs_attention as the replacement

#### Scenario: Condition configuration is validated with corrective examples
- **WHEN** a loop declares `until_task` naming a task outside its body, a task without `verdicts`, or a task that another body task depends on
- **THEN** validation rejects the definition with an error naming the loop and condition task, stating the rule, and showing a corrected example

#### Scenario: Condition task cannot tolerate failure
- **WHEN** until_task names a body task with allowed_to_fail true
- **THEN** validation rejects the definition before dispatch, naming the loop and task and showing allowed_to_fail false or omission as the correction

#### Scenario: Mandatory condition and optional reviewers are accepted
- **WHEN** an otherwise valid loop has optional reviewers with allowed_to_fail true and a condition task with allowed_to_fail omitted or false
- **THEN** validation accepts the definition and retains existing tolerance and success-threshold rules for the reviewers

#### Scenario: Incomplete on_verdict map is rejected
- **WHEN** `until_task` declares verdicts `clean`, `issues`, and `inconclusive` but `on_verdict` maps only `clean` and `issues`
- **THEN** validation rejects the definition, naming the unmapped verdict `inconclusive` and showing the missing mapping in the corrective example

#### Scenario: Blocked is an ordinary mapped verdict
- **WHEN** until_task declares blocked and on_verdict maps it to continue, break, or needs_attention along with all other declared verdicts
- **THEN** validation accepts the mapping and scheduling follows the selected action without any built-in blocked behavior

#### Scenario: Condition fields pair up
- **WHEN** a loop declares `on_verdict` without `until_task`, or `until_task` without `on_verdict`
- **THEN** validation rejects the definition with a corrective example showing both fields together

#### Scenario: Every body branch reaches the condition task
- **WHEN** one body reviewer is not a direct or transitive predecessor of until_task even though until_task has no body dependents
- **THEN** validation rejects the graph, names the uncovered reviewer, and shows how to connect the branch to the condition task

#### Scenario: Aggregating condition accepts transitive dependencies
- **WHEN** every other body task reaches until_task through same-loop needs edges, or until_task is the only body task, and other validation rules hold
- **THEN** validation accepts the graph

#### Scenario: Exhaustion policy requires a condition
- **WHEN** a loop declares on_exhaustion but no until_task/on_verdict pair
- **THEN** validation rejects the definition with a corrective example rather than ignoring the policy

#### Scenario: Condition settings participate in replay identity
- **WHEN** the same request ID is resubmitted after a loop's `until_task`, `on_verdict`, or `on_exhaustion` changed
- **THEN** the submission is treated as a changed definition and rejected rather than replayed

#### Scenario: Definitions without conditions hash identically
- **WHEN** a definition declares loops without condition fields
- **THEN** its definition identity is byte-identical to the same definition parsed before conditions existed

### Requirement: Success and timeouts have explicit boundaries
A workflow task SHALL succeed only after its backend turn completes successfully, owned execution stops, a nonempty final response is saved, and the outcome is durably committed. An empty response SHALL produce a failed task with a missing-output reason. For a task that declares verdicts, the attempt SHALL additionally pass through a `judging` phase after the response is saved and SHALL settle only after its verdict is resolved; dependent tasks wait for that settlement. The task timeout SHALL NOT extend into the judging phase; judging is bounded by the judge's own decision timeout. The workflow SHALL have `task_timeout_seconds` defaulting to 1800 and tasks SHALL accept an optional positive `timeout_seconds` override. The timeout SHALL count wall time from dispatch, including permissions and backend subagents, excluding dependency/queue wait. Expiry SHALL cancel owned work and produce a failed timeout outcome only after cleanup is confirmed; uncertain cleanup SHALL produce interruption requiring intervention. V1 SHALL NOT claim that a successful textual response proves substantive task correctness, and a resolved semantic verdict SHALL influence loop continuation only through the declared until_task/on_verdict mapping. Other tasks' semantic verdicts MUST NOT alter scheduling or dependency evaluation, and no declared verdict name has implicit intervention semantics. Existing classifier uncertainty and outage policies remain applicable.

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
- **WHEN** a non-condition verdict task settles with any declared verdict
- **THEN** dependency evaluation, dispatch decisions, and the execution's final state are exactly those of the same definition without verdicts

## ADDED Requirements

### Requirement: Loop conditions evaluate settled verdicts
A loop with an `until_task` SHALL decide its continuation only after the whole iteration settles acceptably under the existing rules: loop failure and blocking holds, interrupted work, and pending or held verdict classification prevent evaluation and take precedence. A failed condition attempt, including timeout, missing response, backend failure, or uncertainty resolved as failure by `on_uncertain: error`, SHALL cause the existing non-tolerated loop-failure hold and SHALL NOT be evaluated through `on_verdict`. The verdict evaluated is the one recorded on `until_task`'s latest committed current-iteration attempt; a manual override of that verdict re-evaluates the condition. An accepted stop request SHALL take precedence over mapped actions and exhaustion policy once that loop's current iteration settles acceptably, even when other loops remain held; resolving that local control MUST preserve unrelated attention and MUST NOT itself release task dispatch or iteration advance; failure, interruption, and unresolved classification holds SHALL still prevent completion. The mapped action SHALL otherwise apply as follows: `break` completes the loop at the current iteration and outside consumers proceed with that iteration's results; `continue` re-arms the next iteration, or triggers the exhaustion policy when the effective iteration cap is reached; `needs_attention` holds the loop at the current iteration with an attention reason naming the loop, iteration, task, and verdict. No declared verdict name SHALL have implicit scheduling semantics. Names such as blocked or needs_input SHALL be treated as ordinary declared verdicts and SHALL require a mapping when declared by until_task. A structured information-request outcome MUST be explicitly declared; for a condition task its needs_attention mapping SHALL govern intervention. Non-condition tasks' semantic verdicts SHALL NOT independently hold execution. Tasks without verdicts SHALL NOT be classified, and free-form requests in their response SHALL NOT create a structured intervention outcome. Classification of tasks with verdicts SHALL still run even when no information-request option is declared; synthetic uncertainty remains governed by on_uncertain. A mapped needs_attention action SHALL retain priority at the cap, including when on_exhaustion is succeed. Only a continue action at the effective cap SHALL invoke exhaustion policy; manual overrides SHALL use the same decision path and MUST NOT advance beyond the effective cap. Exhaustion policies: `succeed` completes the loop exactly as a `break`; `needs_attention` (the default) holds with a `loop_exhausted` reason admitting the extend and stop controls. Exhaustion alone MUST NOT finalize the execution as failed. The succeed policy SHALL preserve the recorded condition verdict and ordinary task-outcome accounting, including tolerated failures; it completes the loop but does not guarantee workflow success or substantive correctness. Loops without condition fields keep the static behavior of completing their full budget.

#### Scenario: Repeat until clean honors the budget
- **WHEN** a review loop maps `issues_found` to `continue` and `review_passed` to `break` with `max_iterations: 3`, and the verdicts are issues, issues, then review_passed
- **THEN** the loop runs three iterations, breaks on the third, and an outside consumer receives the third iteration's results

#### Scenario: Break at the first iteration
- **WHEN** the condition task returns the `break`-mapped verdict in iteration 1 of a 3-iteration loop
- **THEN** the loop completes at iteration 1, no further iterations dispatch, and outside consumers receive iteration-1 results

#### Scenario: Needs_attention action holds the loop
- **WHEN** the condition task returns a verdict mapped to `needs_attention`
- **THEN** the loop and execution hold at the current iteration with a reason naming loop, iteration, task, and verdict, and no dispatch or advance occurs until intervention

#### Scenario: Non-condition verdict has no implicit intervention
- **WHEN** an ordinary body or outside task settles with a declared verdict named blocked or needs_input
- **THEN** the verdict is recorded without independently holding execution, and normal dependency rules apply

#### Scenario: Information request is explicitly mapped
- **WHEN** until_task declares needs_input, maps it to needs_attention, and receives that verdict after acceptable iteration settlement
- **THEN** the loop waits for intervention according to the mapped action

#### Scenario: No verdict declaration means no classifier
- **WHEN** a task has no verdicts and its successful response contains a request for more information
- **THEN** no classification is requested and the text alone does not create a structured help-request hold

#### Scenario: Override resolves a mapped information-request hold
- **WHEN** the condition task's needs_input verdict mapped to needs_attention is overridden to a declared verdict mapping to continue and budget remains
- **THEN** the condition hold clears and the loop re-arms under the replacement verdict without rerunning the completed condition attempt

#### Scenario: Uncertain condition verdicts never steer the loop
- **WHEN** the condition task's attempt is held on an uncertain verdict or judge unavailability
- **THEN** the iteration does not settle, no action applies, and the existing hold machinery governs until the verdict resolves

#### Scenario: Condition failure cannot advance without a verdict
- **WHEN** the condition task fails due to timeout, backend failure, or missing response
- **THEN** the execution needs attention under the loop-failure policy, no condition action or exhaustion policy applies, and an eligible explicit retry can repair the same iteration

#### Scenario: Uncertainty as error holds the mandatory condition task
- **WHEN** the condition classification is below the confidence threshold and on_uncertain is error
- **THEN** the condition attempt fails with uncertain_verdict, the loop needs attention, and the synthetic uncertain value is not looked up in on_verdict

#### Scenario: Exhaustion default holds for intervention
- **WHEN** the cap is reached with the `continue`-mapped verdict and `on_exhaustion` is unset
- **THEN** the execution enters needs_attention with a `loop_exhausted` reason offering extend, stop, or cancel

#### Scenario: Exhaustion succeed proceeds
- **WHEN** on_exhaustion is succeed and the condition maps to continue at the effective cap with no pending stop or unresolved attention
- **THEN** the loop completes like a break and outside consumers receive the final iteration's results

#### Scenario: Unsupported exhaustion policy is rejected
- **WHEN** a loop declares on_exhaustion fail or another unsupported value
- **THEN** validation rejects the definition before dispatch with a corrective example using needs_attention or succeed

#### Scenario: Exhaustion completion preserves the actual verdict
- **WHEN** on_exhaustion is succeed, the final condition verdict maps to continue, and the iteration settled acceptably
- **THEN** the loop completes and releases outside consumers without changing the condition verdict or hiding tolerated task failures

#### Scenario: Static loops keep full-budget completion
- **WHEN** a loop declares no condition fields and completes without failures
- **THEN** it runs exactly its `max_iterations` iterations exactly as before conditions existed


#### Scenario: Attention action takes precedence at the cap
- **WHEN** the final iteration's condition maps to needs_attention and on_exhaustion is succeed
- **THEN** the loop holds for intervention rather than completing through exhaustion policy
