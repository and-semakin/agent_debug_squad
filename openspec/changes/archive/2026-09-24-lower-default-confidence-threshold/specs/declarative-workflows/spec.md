## MODIFIED Requirements

### Requirement: Workflow definitions are validated before execution
The system SHALL accept an optional YAML `workflow` with `version: 1`, nonempty `name`, positive integer `max_parallel`, and a nonempty task map. Tasks SHALL declare nonempty `agent` and `prompt`, optional `needs` defaulting to an empty list, boolean `allowed_to_fail` defaulting to false, and integer `min_successful_dependencies` defaulting to zero. Tasks SHALL also accept an optional `verdicts` map of verdict names to optional human descriptions and an optional `loop` naming a declared loop. The workflow SHALL accept optional `confidence_threshold` (a number greater than 0 and at most 1, defaulting to 0.7), `on_uncertain` (either `needs_attention` or `error`, defaulting to `needs_attention`), and a `loops` map of loop name to loop definition, where a loop definition declares exactly a required positive integer `max_iterations`, an optional `parent` naming an enclosing declared loop, and optional condition fields: `until_task` naming the loop's condition task, `on_verdict` mapping verdicts to actions, and `on_exhaustion` selecting an exhaustion policy.

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

#### Scenario: Omitted confidence threshold retains identity
- **WHEN** a workflow omits `confidence_threshold`
- **THEN** its effective threshold is 0.7, its serialized definition omits the threshold, and its definition hash remains identical to that produced before the default changed

#### Scenario: Explicit confidence threshold is preserved
- **WHEN** a workflow explicitly sets `confidence_threshold: 0.8`
- **THEN** its effective threshold remains 0.8 and its definition identity differs from the omitted-threshold definition
