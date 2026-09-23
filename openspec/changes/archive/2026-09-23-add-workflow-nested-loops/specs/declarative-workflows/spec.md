# declarative-workflows Delta

## MODIFIED Requirements

### Requirement: Workflow definitions are validated before execution
The system SHALL accept an optional YAML `workflow` with `version: 1`, nonempty `name`, positive integer `max_parallel`, and a nonempty task map. Tasks SHALL declare nonempty `agent` and `prompt`, optional `needs` defaulting to an empty list, boolean `allowed_to_fail` defaulting to false, and integer `min_successful_dependencies` defaulting to zero. Tasks SHALL also accept an optional `verdicts` map of verdict names to optional human descriptions and an optional `loop` naming a declared loop. The workflow SHALL accept optional `confidence_threshold` (a number greater than 0 and at most 1, defaulting to 0.8), `on_uncertain` (either `needs_attention` or `error`, defaulting to `needs_attention`), and a `loops` map of loop name to loop definition, where a loop definition declares exactly a required positive integer `max_iterations`, an optional `parent` naming an enclosing declared loop, and optional condition fields: `until_task` naming the loop's condition task, `on_verdict` mapping verdicts to actions, and `on_exhaustion` selecting an exhaustion policy.

The system MUST reject unknown workflow/task fields, duplicate YAML keys, unsafe identifiers, unknown agents/dependencies, repeated dependency entries, self-dependencies, cycles, repeated agent references across tasks, and thresholds outside zero through the number of direct dependencies, before dispatching any task. It MUST also reject verdict maps that are empty, contain unsafe or duplicated verdict names, contain the reserved name `uncertain`, or declare fewer than two verdicts; `confidence_threshold` or `on_uncertain` values outside their allowed sets; loop definitions with a missing or non-positive `max_iterations`; `loop` references to undeclared loops; loops with empty subtrees; undeclared parents, self-parenting, or cycles in parent relationships; direct dependencies between tasks in unrelated loop branches; and dependency cycles through a loop boundary at any nesting level.

A task's `loop` names its direct owner; its work also belongs to every ancestor's subtree. The body of a loop SHALL mean its whole task subtree; direct members SHALL mean only tasks whose `loop` names it. Dependency descendants SHALL mean tasks reachable through `needs`, not necessarily loop descendants. Loops with no direct tasks but a nonempty descendant subtree SHALL be accepted. Same-owner and ancestor/descendant dependencies, and dependencies to or from workflow-scope tasks, SHALL be permitted subject to cycle validation. For cycle validation, each scope (including workflow scope) SHALL treat each immediate child loop subtree as one dependency node, retain its directly owned tasks, and reject cycles among those nodes. For every dependency edge whose endpoints lie anywhere in the scope subtree, each endpoint SHALL project to its directly owned task or the immediate child loop containing it; internal edges within one child SHALL be checked recursively in that child. No fixed nesting depth limit SHALL be imposed.

It MUST additionally reject condition misconfiguration: an `until_task` whose direct `loop` owner is not the conditioned loop, does not declare `verdicts`, sets `allowed_to_fail: true`, or is not the unique sink of the loop's subtree (every other subtree task MUST reach until_task through dependency edges contained in that subtree); an `on_verdict` without `until_task` and an `until_task` without `on_verdict`; an `on_verdict` that maps a verdict not declared by `until_task`, omits a declared verdict, or uses an action other than `break`, `continue`, or `needs_attention`; an `on_exhaustion` without the until_task/on_verdict pair; and an `on_exhaustion` other than `needs_attention` or `succeed`. A singleton body with until_task as its only task SHALL satisfy the unique-sink rule.

Every loop-related rejection message SHALL name the offending loop or task and include a short corrective example, so agents authoring YAML can repair the definition without additional context. Identical model/backend configurations under different agent names SHALL be accepted. The `verdicts`, `confidence_threshold`, `on_uncertain`, `loops`, `loop`, `parent`, `until_task`, `on_verdict`, and `on_exhaustion` fields SHALL participate in the definition identity used for idempotent replay; when unset, the identity MUST remain byte-identical to definitions parsed by previous versions.

A present YAML `parent` SHALL be a nonempty string satisfying the loop identifier rules and naming a declared loop. Empty or whitespace-only strings, null, numeric or boolean values, sequences, and maps SHALL be rejected without trimming or coercion. Only omission SHALL declare a root loop and preserve absent-parent hashing. A container-only loop SHALL be static; declaring a condition for it SHALL be rejected with guidance to add a directly owned condition task.

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
- **WHEN** a declared loop has no tasks anywhere in its subtree
- **THEN** validation rejects the definition with an actionable error

#### Scenario: Cross-loop dependency is rejected
- **WHEN** a task in loop A declares `needs` on a task in loop B and neither loop is an ancestor of the other
- **THEN** validation rejects the definition and explains how to route a handoff through an explicit task in their common enclosing scope, or workflow scope for separate root loops, subject to the same boundary-cycle checks

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
- **WHEN** a loop declares `until_task` naming a task it does not directly own, a task without `verdicts`, or a task that another task in its subtree depends on
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
- **WHEN** every other subtree task reaches the directly owned until_task through needs edges contained in the subtree, including descendant-loop edges, or until_task is the only subtree task, and other validation rules hold
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

#### Scenario: Loop parents form a forest
- **WHEN** a loop names an undeclared parent, itself, or participates in a parent cycle
- **THEN** validation rejects the definition before dispatch, naming the loop and showing a valid parent example

#### Scenario: Nested review followed by outer tests
- **WHEN** an outer loop owns a test condition that depends on the inner review condition, and every subtree task reaches the outer test task
- **THEN** validation accepts the definition and the two conditions belong to different directly owning loops

#### Scenario: Ancestor input enters a nested loop
- **WHEN** an outer setup task feeds an inner task and a different outer task consumes the completed inner result, with no boundary cycle
- **THEN** validation accepts the graph

#### Scenario: Container-only loop
- **WHEN** a static outer loop directly owns no tasks but contains a nonempty child loop
- **THEN** validation accepts it; an outer loop with an empty whole subtree is rejected

#### Scenario: Condition cannot be borrowed from a child
- **WHEN** outer.until_task names a task directly owned by inner, whose parent is outer
- **THEN** validation rejects the definition and explains that outer needs its own condition task

#### Scenario: Boundary deadlock inside an outer loop
- **WHEN** inner.A feeds outer.X and outer.X feeds inner.B, while the raw task graph is acyclic
- **THEN** validation rejects the inner-to-X-to-inner boundary cycle even if collapsing the entire outer subtree would hide it

#### Scenario: Three-level boundary validation
- **WHEN** a dependency cycle crosses a grandchild loop boundary entirely within one root loop
- **THEN** validation rejects it at the enclosing scope rather than hiding it in a root-level collapse

#### Scenario: Sibling handoff through an ancestor
- **WHEN** child A feeds an outer bridge task which feeds child B, with no reverse dependency
- **THEN** validation accepts the bridged graph while rejecting a direct edge from A to B

#### Scenario: Nesting participates in identity
- **WHEN** the same request ID is submitted with a changed parent relationship
- **THEN** the definition conflicts; definitions without parent retain their previous byte-identical identity

#### Scenario: Invalid parent value is not an absent parent
- **WHEN** parent is explicitly empty, null, non-string, or whitespace-only
- **THEN** validation rejects the definition with a corrected parent example; omission alone retains the old definition identity

#### Scenario: Conditioned container requires a direct task
- **WHEN** a loop has only child-loop tasks and declares until_task on a child
- **THEN** validation rejects it and instructs the author to add a directly owned condition task depending on the child result

#### Scenario: Workflow exit from a grandchild
- **WHEN** a workflow-scope task depends on a task in a grandchild loop
- **THEN** workflow-scope boundary validation projects the producer to its root ancestor loop, retaining cycle detection at every nested scope

### Requirement: Loop bodies re-arm for a bounded number of iterations
A loop SHALL execute its directly owned tasks and child loop invocations within its current iteration. Each task SHALL have at most one initial automatic attempt per complete iteration path. A child invocation SHALL be confined to one parent iteration. Acceptable settlement SHALL require every direct task's latest current-context attempt to have succeeded or failed tolerably, and every immediate child invocation to be done; pending, queued, running, judging, interrupted, mandatory failed, or blocked work SHALL prevent settlement.

A loop SHALL NOT exceed its invocation's effective cap, consisting of `max_iterations` plus accepted extensions. Each static loop invocation SHALL complete exactly its effective cap of iterations under normal completion; a condition or explicit stop can complete it earlier, cancellation can abandon it, and intervention can hold it indefinitely. Completion SHALL mark the current invocation done and preserve its final descendant states and results without reinitializing anything. Only a permitted advance SHALL increment the loop's local counter and reset its direct tasks, recursively starting new child invocations at local iteration 1 with cleared child extensions and stop intent. The advancing loop's extensions SHALL remain associated with its invocation. Pending stop intent MUST prevent advance; a stop serialized after a committed advance SHALL apply to the new current iteration under the control rules. No backend work in the next context SHALL dispatch before the complete advance-and-reset transition commits durably. Every dispatched attempt, including retries, SHALL use a fresh runtime conversation.

A non-tolerated body failure or any blocked body task SHALL hold the execution in `needs_attention`, retaining failed/blocked states and reasons, rather than finalize it as failed. This includes unmet success thresholds and outside prerequisites. While any hold exists, no new ordinary task SHALL dispatch and no loop SHALL advance anywhere; already dispatched work SHALL be allowed to finish and its outcomes SHALL be preserved. A newly exposed ancestor condition or exhaustion hold SHALL block dispatch and further advances in the same scheduling pass. Eligible retries SHALL repair only the current context under the retry rules. Resume SHALL NOT waive unresolved failures or thresholds, but SHALL be allowed to resolve independent artifact/judge attention while retaining the failure hold. Cancellation SHALL follow existing cleanup rules. Tolerated failures with no blocked tasks SHALL NOT create a failure hold. Consumers above a loop SHALL remain pending with an explicit loop-wait reason until the required invocations complete.

Without extensions, initial automatic attempts for a task SHALL be bounded by the product of declared caps on its owner chain. With any finite set of accepted extensions, a conservative bound SHALL be the product, along that chain, of each named loop's maximum effective cap across all its invocations, including completed invocations. Every increase SHALL require an explicit idempotent audited control; resetting a child cap SHALL NOT erase historical budget accounting. Explicit retries by users or coordinator agents SHALL be excluded from this bound, preserve the exact current path, and neither consume nor extend iteration budgets. No automatic retries or limits on explicit retry count, elapsed time, or nesting depth SHALL be introduced.

Task attempt history SHALL remain inspectable. When execution can finish, its final outcome SHALL use the final task states in the final subtree iteration of each root; tolerated failures in older contexts SHALL NOT degrade that outcome.

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

#### Scenario: Child restarts only on parent advance
- **WHEN** outer advances from iteration 1 to 2 after inner completes
- **THEN** inner starts a new invocation at outer=2/inner=1, its invocation controls reset, and all outer=1 attempts remain historical

#### Scenario: Parent completion preserves the final subtree
- **WHEN** outer breaks or reaches its static cap after inner completed iteration 3
- **THEN** outer and inner stay done, inner retains its final path and results, and no new inner invocation is created

#### Scenario: Parent waits for child completion
- **WHEN** all directly owned outer tasks are settled but a child invocation remains unfinished
- **THEN** outer does not evaluate continuation or advance until that child is done

#### Scenario: Three-level restart cannot reuse old successes
- **WHEN** A=1/B=1/C=1 completed earlier and A now advances to 2
- **THEN** the new context A=2/B=1/C=1 requires fresh attempts and cannot treat the earlier C attempts as its outcomes

#### Scenario: Nested static budget
- **WHEN** an inner task has cap 3 inside a parent with cap 2 and no extensions or failures
- **THEN** it receives exactly six initial attempts, identified by six distinct paths

#### Scenario: Historical extension remains in the bound
- **WHEN** outer has cap 2 and inner has declared cap 1, inner is extended by 4 in the first outer iteration, then runs at its declared cap in the second
- **THEN** six initial inner attempts are valid, and accounting does not incorrectly claim a bound of two using only the final reset caps

#### Scenario: Newly exposed parent hold stops scheduling
- **WHEN** all children have completed and the directly owned parent condition subsequently settles with a needs_attention verdict
- **THEN** the parent remains at the same path, the execution needs attention, and no further automatic advance or task dispatch crosses that newly exposed hold

#### Scenario: Static parent still repeats after child break
- **WHEN** a static parent has cap 5 and its child breaks in its first iteration on every invocation
- **THEN** the parent normally creates five child invocations; child break does not break the parent

#### Scenario: Nesting alone does not order workspace access
- **WHEN** a static outer task has no needs edge from its child loop and parallel capacity is available
- **THEN** the outer task and child work can run concurrently; nesting adds neither an ordering dependency nor a workspace snapshot

#### Scenario: Stopped initialized child finishes its first iteration
- **WHEN** an ancestor is stopped before an initialized child has dispatched any attempts, with no unresolved holds and execution running
- **THEN** the child executes and settles its current first iteration before completing, without a second iteration; cancellation instead abandons unstarted work

### Requirement: Iteration handoff is scoped and carried over
Each dependency input SHALL identify the selected producer attempt and its iteration context. Readiness and input construction SHALL use the same selection rules:

- A producer outside all loops SHALL supply its single settled outcome under existing retry rules.
- Same-owner dependencies SHALL resolve at the consumer's exact current path.
- An ancestor-owned producer SHALL resolve at the consumer path truncated to that producer's owner; it remains stable through child iterations.
- A descendant-owned producer SHALL supply its final outcome within the consumer's current iteration, only after every intervening child invocation on the producer chain is done.
- A workflow-scope consumer of a loop producer SHALL wait for the invocation of the producer's root ancestor loop to complete and receive the final outcome under that root ancestor's final iteration.

The latest attempt at the selected context SHALL determine readiness; a queued retry SHALL keep the dependency pending instead of exposing an older committed failure. A missing current-context outcome MUST NOT fall back to historical output. Existing failure tolerance, success thresholds, and global dispatch gates SHALL still apply.

Every loop task manifest SHALL additionally carry prior outcomes for its own and enclosing levels. For the direct owner at iteration greater than 1, `previous_iteration` SHALL summarize its previous iteration's entire subtree. For each ancestor at iteration greater than 1, `ancestor_previous_iterations` SHALL contain a section for that ancestor's previous iteration, ordered nearest ancestor first. Each section SHALL retain the unchanged ancestor prefix and decrement only the summarized level's counter; no section SHALL be added for a level at iteration 1. Each ancestor section SHALL expose its summarized `iteration_path`. For each subtree task the section SHALL select the final committed attempt under that prior context, including a successful retry and the final iterations of nested invocations, rather than every historical attempt.

Entries SHALL be sorted by task ID and include task/agent/attempt/iteration identities, full paths for every loop-owned attempt in executions with nesting, including root-owned attempts, states, verdicts where recorded, errors, and successful result references. In executions without nesting, root contexts SHALL retain their existing loop identity and iteration representation. Existing artifact verification and complete readable-result guarantees SHALL cover all references without silently truncating responses. Advancing any loop SHALL make attempts in its previous iteration and subtree immutable to new retries or verdict overrides. Replay of a previously accepted request SHALL retain its existing no-new-effects semantics. Definitions without nesting SHALL retain the existing manifest shape.

In executions with nesting, a present `previous_iteration` SHALL remain an outcome array and SHALL be accompanied by `previous_iteration_path` naming the exact summarized owner context, including a one-entry path for a root owner. It SHALL be omitted together with that path at owner iteration 1. Ancestor sections SHALL each carry their own path. Handoffs SHALL expose only final task outcomes for each summarized context, not all intermediate inner attempts; earlier attempts SHALL remain inspectable history.

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

#### Scenario: Ancestor input is stable within its iteration
- **WHEN** inner runs several iterations under outer=2 and depends on outer.setup
- **THEN** every inner attempt receives outer.setup at outer=2, never its outer=1 outcome

#### Scenario: Consumer waits for intervening invocations
- **WHEN** A directly owns a consumer of a task inside C where C is inside B and B is inside A, C finishes but B can still repeat
- **THEN** the consumer waits for B as well as C and receives C's final result from B's final iteration within A's current iteration

#### Scenario: Outer test feedback reaches the next implementer
- **WHEN** inner implementer starts at outer=2/inner=1 after outer=1 tests completed
- **THEN** its ancestor carry-over includes the outer=1 test and the final inner outcomes from outer=1, and it has no own previous-iteration section

#### Scenario: Carry-over distinguishes repeated local counters
- **WHEN** a consumer starts at A=2/B=2/C=1 and the completed previous B iteration is A=2/B=1
- **THEN** the B section selects only outcomes under A=2/B=1, the A section selects final outcomes under A=1, and no section mixes those contexts despite repeated B and C counters

#### Scenario: Outer task receives previous descendant outcomes
- **WHEN** an outer task starts its second iteration and its previous subtree included an inner review loop
- **THEN** its own previous_iteration section includes the final inner review outcomes as well as direct outer outcomes

#### Scenario: Queued nested retry hides the replaced failure
- **WHEN** a failed producer has an accepted queued retry at the current path
- **THEN** its consumers remain pending until that retry settles and do not use the replaced failure for readiness

#### Scenario: Carry-over artifact damage holds execution
- **WHEN** a referenced successful result in an ancestor carry-over section is missing or changed
- **THEN** dispatch is held for artifact intervention under the existing verification rules

#### Scenario: Owner carry-over identifies its full prefix
- **WHEN** a manifest is created at A=2/B=1/C=3
- **THEN** previous_iteration_path is A=2/B=1/C=2; ancestor sections independently identify any applicable preceding ancestor iterations

#### Scenario: Target workflow transfers final review outcomes
- **WHEN** the first outer iteration contains three completed inner review iterations, then a negative outer test verdict repeats the outer loop
- **THEN** the next implementer receives the test result and final implement/review/consolidate outcomes from inner iteration 3, not a list of all three inner iterations; those earlier attempts remain available in history

#### Scenario: Bridge between roots transfers one completed result
- **WHEN** a task outside all loops bridges a producer in root A to a consumer in root B
- **THEN** the bridge runs once after the entire A invocation completes and does not implement per-iteration exchange between A and B

### Requirement: Loop conditions evaluate settled verdicts
A loop with an `until_task` SHALL decide its continuation only after the whole iteration settles acceptably under the existing rules: loop failure and blocking holds, interrupted work, and pending or held verdict classification prevent evaluation and take precedence. A failed condition attempt, including timeout, missing response, backend failure, or uncertainty resolved as failure by `on_uncertain: error`, SHALL cause the existing non-tolerated loop-failure hold and SHALL NOT be evaluated through `on_verdict`. The condition task SHALL be directly owned by the conditioned loop. The verdict evaluated is the one recorded on `until_task`'s latest committed attempt at that loop's exact current iteration path, after all child invocations have completed; a manual override of that verdict re-evaluates the condition. An accepted stop request SHALL take precedence over mapped actions and exhaustion policy once that loop's current iteration settles acceptably, even when other loops remain held; resolving that local control MUST preserve unrelated attention and MUST NOT itself release task dispatch or iteration advance; failure, interruption, and unresolved classification holds SHALL still prevent completion. The mapped action SHALL otherwise apply as follows: `break` completes the loop at the current iteration and outside consumers proceed with that iteration's results; `continue` re-arms the next iteration, or triggers the exhaustion policy when the effective iteration cap is reached; `needs_attention` holds the loop at the current iteration with an attention reason naming the loop, iteration, task, and verdict. No declared verdict name SHALL have implicit scheduling semantics. Names such as blocked or needs_input SHALL be treated as ordinary declared verdicts and SHALL require a mapping when declared by until_task. A structured information-request outcome MUST be explicitly declared; for a condition task its needs_attention mapping SHALL govern intervention. Non-condition tasks' semantic verdicts SHALL NOT independently hold execution. Tasks without verdicts SHALL NOT be classified, and free-form requests in their response SHALL NOT create a structured intervention outcome. Classification of tasks with verdicts SHALL still run even when no information-request option is declared; synthetic uncertainty remains governed by on_uncertain. A mapped needs_attention action SHALL retain priority at the cap, including when on_exhaustion is succeed. Only a continue action at the effective cap SHALL invoke exhaustion policy; manual overrides SHALL use the same decision path and MUST NOT advance beyond the effective cap. Exhaustion policies: `succeed` completes the loop exactly as a `break`; `needs_attention` (the default) holds with a `loop_exhausted` reason admitting the extend and stop controls. Exhaustion alone MUST NOT finalize the execution as failed. The succeed policy SHALL preserve the recorded condition verdict and ordinary task-outcome accounting, including tolerated failures; it completes the loop but does not guarantee workflow success or substantive correctness. Loops without condition fields keep the static behavior of completing their full budget.

A condition override SHALL validate the attempt's complete current path as well as the existing judging or live condition-hold eligibility. An old ancestor context SHALL be historical even when its local counter matches the current one. An accepted replay SHALL NOT rewrite a historical outcome.

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

#### Scenario: Outer condition uses its own attempt
- **WHEN** outer is at iteration 1, inner finishes at iteration 3, and outer.test is the directly owned outer condition
- **THEN** outer continuation uses outer.test at outer=1, while the inner condition uses its distinct final path outer=1/inner=3

#### Scenario: Historical nested override is rejected
- **WHEN** a new override targets the verdict at outer=1/inner=1 while the current context is outer=2/inner=1
- **THEN** the request returns 409 without changing history, counters, or current holds

## ADDED Requirements

### Requirement: Nested iteration contexts are unambiguous
The system SHALL identify an iteration by the complete ordered path of loop names and positive iteration numbers from the root through the direct owner. A nested loop invocation SHALL be identified by its loop name and its ancestor path. Advancing any ancestor SHALL create a distinct descendant invocation even when local counters repeat. In executions with any parent declaration, all loop states and loop-owned attempts and attempt/input views SHALL expose this identity as `iteration_path`, an ordered list of `{loop, iteration}` entries, including one-entry paths for root loops and root-owned tasks. Existing `iteration` SHALL remain the direct owner's counter and agree with the last entry. Executions without nesting SHALL omit the new path field and preserve their existing representation; their contexts remain derivable from the definition and existing iteration. Workflow-scope tasks SHALL omit iteration_path in every schema because they have no loop context. Per-task attempt numbers SHALL increase across all invocations and retries.

A current nested context SHALL match the current iteration of every ancestor. The system MUST NOT infer a missing nested context from the latest attempt or select an outcome using only local counters. History SHALL preserve complete paths. No fixed nesting depth limit SHALL be introduced.

#### Scenario: Three levels with repeated counters
- **WHEN** the same task executes at A=1/B=1/C=1 and later A=2/B=1/C=1
- **THEN** it has different complete paths and increasing attempt numbers, and neither attempt can satisfy dependencies in the other's context

#### Scenario: Flat identity remains unchanged
- **WHEN** a definition contains no parent fields
- **THEN** loop and attempt views omit iteration_path, existing local iteration identity remains sufficient, and serialization retains the previous shapes

#### Scenario: Schema three uses one loop context representation
- **WHEN** a nested execution includes root-owned test, inner-owned review, and workflow-scope report tasks
- **THEN** test carries a one-entry iteration_path, review carries its complete multi-entry path, and report omits a loop path; audit and carry-over contexts use the same ordered entry representation
