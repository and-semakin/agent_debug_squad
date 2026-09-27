## MODIFIED Requirements

Scenario titles inherited from the current main spec are retained as stable regression identifiers for OpenSpec replacement validation. In their updated scenario bodies, control means the version-2 task.control model; condition refers only to explicitly described legacy syntax/history.

### Requirement: Workflow definitions are validated before execution
The system SHALL accept an optional YAML workflow with a nonempty name, positive integer max_parallel, and nonempty task map. New definitions with loops SHALL require version: 2. Loopless version: 1 definitions SHALL remain accepted with their existing identity and defaults; version 2 SHALL also accept loopless graphs. Unknown versions SHALL be rejected. Tasks SHALL declare nonempty agent and prompt, optional needs defaulting to an empty list, allowed_to_fail defaulting to false, min_successful_dependencies defaulting to zero, optional verdicts, optional direct-owner loop, and optional control. Workflow confidence_threshold SHALL remain greater than zero and at most one. Its effective value SHALL follow the existing verdict-judge precedence: explicit workflow threshold, otherwise the startup-loaded machine judge threshold, otherwise 0.7. Machine defaults SHALL remain outside saved definition identity, and omission SHALL remain omitted rather than materializing a default. on_uncertain SHALL remain needs_attention or error, default needs_attention.

A version-2 loop SHALL declare positive integer max_iterations, optional parent, and optional on_exhaustion. A loop directly owning one or more control tasks SHALL be controlled; other loops SHALL be fixed-count. on_exhaustion SHALL accept needs_attention (default for controlled loops) or succeed and SHALL be rejected on fixed-count loops. Control tasks SHALL declare verdicts, directly belong to a loop, and SHALL NOT set allowed_to_fail true. A present control SHALL be a nonempty map with exactly one entry per declared verdict and no other entries; values SHALL be proceed, continue, break, or needs_attention. Omission alone SHALL mean no control. No action SHALL implicitly target an ancestor. Multiple controls SHALL be allowed subject to phase-barrier validation.

The system MUST reject unknown workflow/task/loop fields, duplicate YAML keys, unsafe identifiers, unknown agents/dependencies, repeated dependencies, self-dependencies, raw dependency cycles, repeated agent references, invalid success thresholds, empty/singleton verdict maps, unsafe or duplicate verdict names, and reserved uncertain. It MUST reject missing/non-positive caps, undeclared loop owners/parents, empty loop subtrees, parent cycles/self-parenting, and direct dependencies between unrelated loop branches. Identical model/backend configurations under distinct agent names SHALL remain valid.

The parent field, when present, SHALL be a nonempty safe identifier string naming a declared loop; empty, whitespace-only, null and non-string values SHALL be rejected without coercion. Omission alone means a root. Direct tasks belong to their owner and all ancestor subtrees. A loop with no direct tasks but a nonempty child subtree SHALL be valid and fixed-count. Same-owner, ancestor/descendant and workflow-scope dependencies SHALL remain permitted subject to boundary validation. At each scope, project tasks to direct members or immediate child-loop subtrees and reject cycles recursively, without a fixed nesting-depth limit.

Any until_task or on_verdict field SHALL be rejected explicitly as removed, even alongside control. Version-1 loop submissions SHALL be rejected with migration guidance. Loop/control errors SHALL name the offending loop/task and include a short corrected example. Definition identity SHALL include version, needs, ownership/parent, caps, policies, verdicts and control maps; identical maps in different YAML key orders SHALL have identical identity. Changing a control action or version under an existing request ID SHALL conflict rather than replay. Historical saved definitions SHALL follow the lifecycle compatibility contract, not new-submission validation.

After YAML syntax/type/duplicate-key checks, removed until_task/on_verdict diagnostics SHALL take precedence over version-1 loop rejection when both apply, naming the field and showing version-2 task.control. If neither removed field is present, version-1 loops SHALL receive the version diagnostic. The complete barrier acceptance rule SHALL be the requirement Control tasks form complete phase barriers; validation scenarios here exercise that same rule.

#### Scenario: Invalid graph has no side effects
- **WHEN** a definition contains a cycle, unknown dependency, duplicate key, invalid threshold or reused agent identity
- **THEN** validation rejects it before backend dispatch and explains a correction

#### Scenario: Distinct instances of the same model
- **WHEN** different tasks use distinct configured agents with identical backend/model settings
- **THEN** validation accepts them

#### Scenario: Control mapping is total and typed
- **WHEN** control is null, empty, non-map, omits a verdict, adds uncertain, or uses an unknown action
- **THEN** validation names the task and rejects the mapping before execution

#### Scenario: Mandatory directly owned control
- **WHEN** a control is workflow-scoped, lacks verdicts, or sets allowed_to_fail true
- **THEN** validation rejects it with guidance to declare a loop, verdicts and mandatory execution

#### Scenario: Old syntax is not an alias
- **WHEN** a submitted definition contains until_task or on_verdict
- **THEN** validation explains moving the map to task.control under version 2 and does not silently convert it

#### Scenario: Version boundary is explicit
- **WHEN** a new version-1 definition declares a loop even without legacy condition fields
- **THEN** validation requires version 2; an otherwise valid loopless version-1 definition remains accepted unchanged

#### Scenario: Control identity changes
- **WHEN** the same request ID is resubmitted with proceed changed to continue
- **THEN** the definition conflicts; mere map key reordering does not change identity

#### Scenario: Nested structural rules remain
- **WHEN** a parent is empty/null, loops form a parent cycle, or unrelated sibling tasks have a direct edge
- **THEN** validation rejects with a corrective example; an ancestor bridge is allowed if all boundary graphs remain acyclic

#### Scenario: Container and exhaustion
- **WHEN** a loop has only child tasks and declares on_exhaustion
- **THEN** validation rejects the policy and explains that only a directly owned control makes a loop controlled

#### Scenario: Boundary deadlock remains invalid
- **WHEN** inner.a feeds outer.c and outer.c feeds inner.b in an otherwise raw DAG
- **THEN** validation rejects the projected inner-to-c-to-inner cycle

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
- **WHEN** a supported loopless version-1 definition declares no verdict settings
- **THEN** its identity remains byte-identical to its previous version-1 identity

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
- **WHEN** a supported version-1 definition declares no loops
- **THEN** its identity remains byte-identical to its previous version-1 identity

#### Scenario: Uncertainty policy uses the execution-state vocabulary
- **WHEN** YAML sets on_uncertain to needs_attention or omits it
- **THEN** the policy waits for intervention on low confidence, while on_uncertain hold is rejected with an error showing on_uncertain needs_attention as the replacement

#### Scenario: Condition configuration is validated with corrective examples
- **WHEN** a task declares control without direct loop ownership or without verdicts
- **THEN** validation names the task and explains the required direct loop and verdict declaration; a middle control is otherwise legal when it is a complete barrier

#### Scenario: Condition task cannot tolerate failure
- **WHEN** a control task sets allowed_to_fail true
- **THEN** validation rejects before dispatch with a correction showing false or omission

#### Scenario: Mandatory condition and optional reviewers are accepted
- **WHEN** an otherwise valid phase graph has optional reviewers with allowed_to_fail true and a mandatory control
- **THEN** validation accepts and reviewers retain normal tolerance and success-threshold rules

#### Scenario: Incomplete on_verdict map is rejected
- **WHEN** a loop still declares the removed on_verdict field, whether complete or incomplete
- **THEN** validation rejects that field and shows task.control; an incomplete replacement control map identifies the missing verdict

#### Scenario: Blocked is an ordinary mapped verdict
- **WHEN** a control declares blocked and maps it to proceed, continue, break or needs_attention with all other verdicts mapped
- **THEN** validation accepts and only that mapping supplies behavior

#### Scenario: Condition fields pair up
- **WHEN** until_task, on_verdict or both appear on a submitted loop
- **THEN** validation rejects obsolete syntax and shows a task.control migration, without accepting a legacy pair

#### Scenario: Every body branch reaches the condition task
- **WHEN** a tail control has an incomparable reviewer or child-loop vertex in its projected scope
- **THEN** validation rejects and names the uncovered vertex with a needs correction

#### Scenario: Aggregating condition accepts transitive dependencies
- **WHEN** all projected scope vertices transitively precede a directly owned tail control, or it is the only vertex
- **THEN** validation accepts; predecessor child invocations must finish in full before that control runs

#### Scenario: Exhaustion policy requires a condition
- **WHEN** a loop declares on_exhaustion without any directly owned control
- **THEN** validation rejects the unused policy with a correction

#### Scenario: Condition settings participate in replay identity
- **WHEN** the same request ID is resubmitted after task.control, its owner or loop.on_exhaustion changes
- **THEN** the definition conflicts rather than replaying

#### Scenario: Definitions without conditions hash identically
- **WHEN** a saved legacy loop definition has no condition fields
- **THEN** read-only history retains its original identity; submitting converted version-2 YAML intentionally has a different identity and requires a new request ID

#### Scenario: Loop parents form a forest
- **WHEN** a loop names an undeclared parent, itself, or participates in a parent cycle
- **THEN** validation rejects the definition before dispatch, naming the loop and showing a valid parent example

#### Scenario: Nested review followed by outer tests
- **WHEN** an outer control depends on a completed inner control producer and both scopes satisfy barrier comparability
- **THEN** validation accepts; each control affects only its directly owning loop

#### Scenario: Ancestor input enters a nested loop
- **WHEN** an outer setup task feeds an inner task and a different outer task consumes the completed inner result, with no boundary cycle
- **THEN** validation accepts the graph

#### Scenario: Container-only loop
- **WHEN** a static outer loop directly owns no tasks but contains a nonempty child loop
- **THEN** validation accepts it; an outer loop with an empty whole subtree is rejected

#### Scenario: Condition cannot be borrowed from a child
- **WHEN** an accepted workflow has a child-owned control and its ancestor has no directly owned control
- **THEN** the child control cannot direct the ancestor; the ancestor remains fixed-count unless a separate ancestor-owned control is added

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
- **THEN** the definition conflicts; key reordering without semantic changes preserves identity within a definition version

#### Scenario: Invalid parent value is not an absent parent
- **WHEN** parent is explicitly empty, null, non-string or whitespace-only
- **THEN** validation rejects with a corrected example; only omission declares a root

#### Scenario: Conditioned container requires a direct task
- **WHEN** a container-only loop tries to configure on_exhaustion based on a child control
- **THEN** validation rejects and explains adding its own directly owned control; a container without such policy remains fixed-count

#### Scenario: Workflow exit from a grandchild
- **WHEN** a workflow-scope task depends on a task in a grandchild loop
- **THEN** workflow-scope boundary validation projects the producer to its root ancestor loop, retaining cycle detection at every nested scope

#### Scenario: Omitted confidence threshold retains identity
- **WHEN** a workflow omits `confidence_threshold` and no machine judge threshold exists
- **THEN** its effective threshold is 0.7, its serialized definition omits the threshold, and its definition hash remains identical to that produced before the default changed

#### Scenario: Explicit confidence threshold is preserved
- **WHEN** a workflow explicitly sets `confidence_threshold: 0.8`
- **THEN** its effective threshold remains 0.8 and its definition identity differs from the omitted-threshold definition

#### Scenario: Removed field diagnosis precedes old loop version
- **WHEN** a structurally readable version-1 loop contains until_task and on_verdict
- **THEN** validation reports removed condition fields with the version-2 task.control replacement before the general old-version loop error

#### Scenario: Child control does not require an ancestor control
- **WHEN** an inner-owned control exists under an outer loop with no directly owned controls and all graph rules hold
- **THEN** validation accepts; inner actions affect only inner and outer remains fixed-count

### Requirement: Program-driven scheduling respects the graph and limit
After explicit workflow submission, the system SHALL schedule tasks without additional coordinator turns. It SHALL wait for all direct dependencies to settle before evaluating a dependent task. At most `max_parallel` task attempts SHALL be dispatched or running at once within the execution, counting permission waits and attempts whose cancellation has not completed. Ready-task ties SHALL use lexicographic task ID order. Completion order of concurrently executing tasks is not guaranteed. Backend-native subagents and independent manual runs are outside this limit. The system SHALL NOT detect file-write intent, impose workspace edit locks, or create worktrees.

For version-2 loops, dispatch and retry reservations SHALL additionally satisfy every enclosing scope admission and control phase barrier. A resolved control task SHALL NOT release dependents until its control decision is durably committed. Barrier-implied ordering SHALL NOT add undeclared dependency results to task manifests.

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

A control-skipped producer SHALL be unavailable, not a tolerated failure or a success. A dependent outside the skipped suffix SHALL block with dependency_skipped:<producer>:<decision-id>, even if the producer is allowed_to_fail or other successes meet the threshold. Control-skipped suffix tasks themselves SHALL retain their skip outcome rather than becoming dependency-blocked. A pending unopened phase SHALL NOT create speculative failure/blocking holds before its barrier is released.

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

#### Scenario: Skip is not an optional failure
- **WHEN** a final-iteration producer was skipped by break and its outside dependent has threshold zero
- **THEN** the dependent remains blocked with dependency_skipped and cannot receive an older result or an empty success

### Requirement: Success and timeouts have explicit boundaries
A workflow task SHALL succeed only after its backend turn completes successfully, owned execution stops, a nonempty final response is saved, and the outcome is durably committed. An empty response SHALL produce a failed task with a missing-output reason. For a task that declares verdicts, the attempt SHALL additionally pass through a `judging` phase after the response is saved and SHALL settle only after its verdict is resolved; dependent tasks wait for that settlement. The task timeout SHALL NOT extend into the judging phase; judging is bounded by the judge's own decision timeout. The workflow SHALL have `task_timeout_seconds` defaulting to 1800 and tasks SHALL accept an optional positive `timeout_seconds` override. The timeout SHALL count wall time from dispatch, including permissions and backend subagents, excluding dependency/queue wait. Expiry SHALL cancel owned work and produce a failed timeout outcome only after cleanup is confirmed; uncertain cleanup SHALL produce interruption requiring intervention. The system SHALL NOT claim that a successful textual response proves substantive task correctness, and a resolved semantic verdict SHALL influence loop continuation only through the task's declared control mapping. Other tasks' semantic verdicts MUST NOT alter scheduling or dependency evaluation, and no declared verdict name has implicit intervention semantics. Existing classifier uncertainty and outage policies remain applicable.

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
- **WHEN** a verdict task without control settles with any declared verdict
- **THEN** dependency evaluation, dispatch decisions, and the execution's final state are exactly those of the same definition without verdicts

### Requirement: Loop bodies re-arm for a bounded number of iterations
A version-2 loop SHALL execute direct tasks and admitted child invocations in its current pass. Each task SHALL have at most one initial automatic attempt per complete iteration path. Each child invocation SHALL be confined to one parent pass. Ordinary settlement SHALL require direct tasks to succeed or fail tolerably and admitted child invocations to complete; pending, running, judging, interrupted, blocked or mandatory failed work SHALL prevent ordinary settlement. A committed break/continue SHALL instead close its pass using acceptable completed prefix outcomes and explicit control skips for its unstarted suffix.

The positive max_iterations plus accepted finite extensions SHALL bound entered passes, not completed useful tasks, judge calls or control checks. Root pass 1 SHALL be entered at submission. A child pass 1 SHALL be entered only on admission; an unadmitted skipped child SHALL consume zero passes. Every committed advance SHALL increment the local counter once, including after an early continue. A head break SHALL consume one pass and run no suffix tasks. Multiple controls, retries and judge reclassification SHALL NOT independently consume passes. Cap arithmetic SHALL reject overflow rather than wrap.

Natural acceptable completion SHALL advance below the effective cap unless stop is pending. At the cap it SHALL complete fixed-count loops and invoke on_exhaustion for controlled loops. A proceed at the cap SHALL still release the remaining current pass. Continue SHALL skip its suffix and request advance or exhaustion. Break SHALL complete its owner without exhaustion. needs_attention SHALL hold even at the cap with on_exhaustion succeed. Exhaustion needs_attention SHALL hold with extend/stop/cancel interventions; succeed SHALL complete without changing verdicts or guaranteeing workflow success.

Only committed advance SHALL reset direct task current outcomes and initialize fresh child invocation paths, clearing child extensions and stop intent while preserving the advancing invocation's extension and all historical outcomes/decisions. Completion SHALL preserve final descendant states, including skipped unadmitted children. No next-context attempt SHALL be reserved before the full advance-and-reset is durable. Each attempt including retry SHALL have a fresh backend conversation.

Non-tolerated failures or active-phase blocking inside an unfinished loop SHALL hold execution in needs_attention at the same path, not terminal failure. No new ordinary dispatch or advance anywhere SHALL pass any unresolved attention cause; already dispatched work SHALL finish and retain its outcomes. Tolerated failures without blocked work SHALL NOT independently hold. Outside consumers SHALL wait for required loop invocations to close. Controlled skips SHALL not degrade final outcome by themselves; final-state derivation SHALL otherwise retain existing mandatory/tolerated failure and blocking rules, using exact final subtree contexts.

Without extensions, initial automatic attempts SHALL be bounded by the product of caps along their owner chain. With finitely many extensions, a conservative bound SHALL use each loop's maximum effective cap over its historical invocations. Explicit retries SHALL be excluded from this bound and SHALL NOT extend budgets. No automatic unlimited iteration mode, retry, elapsed-time cap or nesting-depth cap SHALL be introduced.

Natural acceptable completion of a controlled pass SHALL be an implicit request to repeat, with the same cap/exhaustion decision as continue but without skipping completed work. Therefore an all-proceed controlled loop SHALL reach needs_attention at its cap under the default on_exhaustion; explicit succeed SHALL instead complete. A stop or break SHALL retain its defined precedence.

#### Scenario: Fixed-count parity
- **WHEN** a no-control loop has cap 3 and completes normally
- **THEN** every body task executes at paths 1 through 3 and the loop completes

#### Scenario: Empty queue before useful work
- **WHEN** the first head selector maps no_mrs_left to break
- **THEN** one pass is counted, its suffix is skipped without dispatch, and the owner completes

#### Scenario: Continue before useful work consumes budget
- **WHEN** a head control always continues with cap 3 and default exhaustion
- **THEN** exactly three selector attempts run and execution holds on exhaustion without a fourth pass

#### Scenario: Multiple checks share a pass
- **WHEN** two ordered controls proceed in pass 1
- **THEN** the counter remains 1 until the whole pass completes

#### Scenario: Proceed at the cap
- **WHEN** a head control proceeds in pass 3 of cap 3
- **THEN** its suffix still runs, then controlled exhaustion applies

#### Scenario: Continue at cap preserves skips
- **WHEN** continue skips a suffix at the cap and the loop is then extended
- **THEN** the skipped suffix stays historical and the next pass starts only after global gates clear

#### Scenario: Attention wins at cap
- **WHEN** a control returns needs_attention at the cap with on_exhaustion succeed
- **THEN** it holds rather than silently completing

#### Scenario: Parent repeats after child break
- **WHEN** a fixed parent has cap 2 and its child breaks at local pass 1
- **THEN** two distinct child invocations occur; child break never terminates the parent

#### Scenario: Failure cannot be bypassed
- **WHEN** a mandatory prefix task fails or an active prefix consumer misses its success threshold
- **THEN** the control does not run and the loop holds for repair or cancellation

#### Scenario: Budget arithmetic is finite
- **WHEN** an extension would overflow the effective-cap representation
- **THEN** the request is rejected without changing the cap or recording an accepted extension

#### Scenario: Historical extension remains accounted
- **WHEN** inner cap 1 is extended by 4 under outer=1 and runs at cap 1 under outer=2
- **THEN** six inner attempts are permitted and history retains the earlier effective cap

#### Scenario: Static loop runs exactly N times
- **WHEN** a fixed-count loop with `max_iterations: 3` and a two-task body completes without failures
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
- **WHEN** a failed outside prerequisite makes an admitted active-phase body task blocked before its first attempt
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
- **WHEN** ordinary parent pass completion is considered while a child remains unfinished
- **THEN** natural repeat waits for that child; an earlier valid parent control can only skip an unadmitted successor child

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
- **WHEN** all children have completed and the directly owned parent control subsequently settles with a needs_attention verdict
- **THEN** the parent remains at the same path, the execution needs attention, and no further automatic advance or task dispatch crosses that newly exposed hold

#### Scenario: Static parent still repeats after child break
- **WHEN** a static parent has cap 5 and its child breaks in its first iteration on every invocation
- **THEN** the parent normally creates five child invocations; child break does not break the parent

#### Scenario: Nesting alone does not order workspace access
- **WHEN** a static outer task is incomparable with its child loop and has no controlling phase gate and parallel capacity is available
- **THEN** the outer task and child work can run concurrently; nesting adds neither an ordering dependency nor a workspace snapshot

#### Scenario: Stopped initialized child finishes its first iteration
- **WHEN** stop reaches an unadmitted child, its phase later opens without a control skip and no other hold remains
- **THEN** the child enters and finishes its first pass without repetition; a parent break or continue instead skips it without consuming a child pass

#### Scenario: All-proceed natural completion at the cap
- **WHEN** every control maps every declared verdict to proceed, cap is 2, and both passes complete normally with default on_exhaustion
- **THEN** two passes run, then the implicit repeat request holds on loop_exhausted without a third pass

#### Scenario: All-proceed explicit successful exhaustion
- **WHEN** the same all-proceed controlled loop sets on_exhaustion succeed
- **THEN** it completes after the cap without a loop-exhausted hold, retaining ordinary outcome accounting

### Requirement: Iteration handoff is scoped and carried over
Readiness and manifests SHALL use the same exact-context outcome selection. Outside-all-loops producers SHALL supply their single settled outcome. Same-owner producers SHALL resolve at the consumer's exact current path. Ancestor-owned producers SHALL resolve at the consumer path truncated to the producer owner. Descendant producers SHALL resolve only after intervening child invocations complete, following each invocation's recorded final pass inside the consumer context. Workflow-scope consumers of loop producers SHALL wait for the root invocation to complete and recursively follow its final subtree paths. No selection SHALL search backward for the most recent successful or committed attempt across different passes.

The latest attempt at that exact context SHALL determine readiness; a queued retry SHALL hide an older outcome. A control-skipped producer SHALL return an explicit unavailable outcome with skip reason, decision identity and planned full path, without a run, attempt, result or verdict. A missing required outcome without a skip record SHALL hold as inconsistent state, never fall back to history. Consumers of actual prefix results SHALL receive those exact saved results when completion barriers open. Skipped dependencies SHALL follow the dependency failure contract.

Every loop manifest SHALL retain explicit historical carry-over: previous_iteration for the immediately preceding owner pass and ancestor_previous_iterations for each ancestor whose counter exceeds one, nearest ancestor first. Each section SHALL retain the unchanged ancestor prefix and decrement only the summarized counter, then recursively follow recorded final child paths. Each subtree task SHALL contribute its final outcome at that exact context, including explicit skips and latest retries, rather than every historical attempt. Sections SHALL be omitted for counters at one; schema-4 previous_iteration SHALL be an outcome array accompanied by previous_iteration_path. Historical carry-over SHALL NOT satisfy needs or replace an absent current output.

Entries SHALL be sorted by task ID and contain task/agent, status/error, exact path and any real attempt/run identity, verdict and successful result reference. Skips SHALL omit fictitious attempt/run/result fields. Referenced successful artifacts in both dependency and historical sections SHALL be verified and provided completely without silent truncation. Paths SHALL distinguish repeated local counters. Past contexts SHALL remain immutable to new retries and overrides; accepted request replay SHALL retain its no-new-effects semantics. Legacy loopless manifests SHALL retain their original shape.

#### Scenario: Same-iteration resolution
- **WHEN** B depends on A and dispatches in pass 2
- **THEN** its dependency references A at pass 2 only

#### Scenario: No stale result on final skip
- **WHEN** review succeeded in pass 1 but was skipped in terminal pass 2
- **THEN** an outside dependent of review is blocked; pass-1 success remains history and is not supplied as the final result

#### Scenario: Final nested skip beats earlier child success
- **WHEN** inner.review succeeded at outer=1/inner=1 and is skipped at inner=2 before inner completes
- **THEN** the outer consumer sees the skip at outer=1/inner=2, never the earlier success

#### Scenario: Skipped child subtree has no previous-parent fallback
- **WHEN** a child ran under outer=1 and is skipped before admission under outer=2
- **THEN** all final child producer outcomes under outer=2 are explicit skips, never outer=1 results

#### Scenario: Actual prefix producer remains usable
- **WHEN** a loop breaks after selector succeeds and an outside report needs only selector
- **THEN** the report receives that final selector response after the owner completes

#### Scenario: Carry-over labels unavailable history
- **WHEN** pass 2 skips review and pass 3 begins
- **THEN** previous_iteration identifies pass 2 and includes review as skipped without filling it from pass 1

#### Scenario: Ancestor feedback remains available
- **WHEN** inner starts at outer=2/inner=1 after outer tests in pass 1
- **THEN** its ancestor_previous_iterations identifies outer=1 tests and final inner outcomes, while own previous_iteration is absent

#### Scenario: Historical artifact damage
- **WHEN** a referenced successful previous-pass response is changed
- **THEN** dispatch holds for artifact intervention

#### Scenario: Queued retry is pending
- **WHEN** a failed exact-context producer has an accepted queued retry
- **THEN** the consumer waits and cannot use the prior failure or success

#### Scenario: Carry-over reaches upstream tasks
- **WHEN** a loop's body contains an implementer with no dependencies and reviewers depending on it, and iteration 2 begins
- **THEN** the implementer's iteration-2 manifest includes the reviewers' iteration-1 outcomes as readable result references

#### Scenario: Outside consumers see final iteration results
- **WHEN** a task outside a completed loop depends on a body producer that succeeded in its exact final context
- **THEN** its manifest references that exact final attempt; a final skip instead blocks it without a historical fallback

#### Scenario: Outside dependencies are stable across iterations
- **WHEN** a body task depends on a task outside all loops
- **THEN** every iteration's manifest references that task's single settled attempt

#### Scenario: Ancestor input is stable within its iteration
- **WHEN** inner runs several iterations under outer=2 and depends on outer.setup
- **THEN** every inner attempt receives outer.setup at outer=2, never its outer=1 outcome

#### Scenario: Consumer waits for intervening invocations
- **WHEN** A consumes C inside B, C finished but B may repeat
- **THEN** the consumer waits for B, then selects C at B's recorded final child context; if that final C producer was skipped it blocks

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

### Requirement: Nested iteration contexts are unambiguous
For version-2 executions, every loop-owned attempt, current outcome, skip, decision, loop state, manifest and control record SHALL identify its complete root-to-owner iteration_path as ordered loop/positive-iteration entries, including one-entry paths for flat loops. The existing iteration counter SHALL agree with the final entry. Workflow-scope tasks SHALL omit loop paths. A loop invocation SHALL be identified by its loop name and ancestor path; advancing an ancestor SHALL create a new descendant invocation even when local counters repeat. Planned paths for unadmitted children SHALL be distinguished from entered passes by entered and iterations_started. Per-task attempt numbers SHALL increase only across real attempts/retries, not skips.

Current paths SHALL match every current ancestor. The system MUST NOT infer missing paths from attempt order or local counters. Historical paths, close decisions and final child contexts SHALL remain inspectable after resets. No fixed nesting-depth limit SHALL be imposed. Older read-only histories SHALL preserve their original flat/nested representations under lifecycle compatibility rules.

#### Scenario: Repeated local counters
- **WHEN** a task runs at A=1/B=1/C=1 and later A=2/B=1/C=1
- **THEN** paths and real attempt numbers differ, and neither attempt satisfies the other context

#### Scenario: Flat version two is explicit
- **WHEN** a flat version-2 loop dispatches a task
- **THEN** its schema-4 view includes a one-entry iteration_path

#### Scenario: Skipped unentered child
- **WHEN** outer breaks before admitting inner
- **THEN** inner exposes its planned path, entered false and iterations_started zero, and no backend attempt is invented

#### Scenario: Three levels with repeated counters
- **WHEN** the same task executes at A=1/B=1/C=1 and later A=2/B=1/C=1
- **THEN** it has different complete paths and increasing attempt numbers, and neither attempt can satisfy dependencies in the other's context

#### Scenario: Flat identity remains unchanged
- **WHEN** a terminal legacy flat-loop execution is observed read-only
- **THEN** its original flat serialization stays intact; a new version-2 flat execution instead always exposes a full one-entry path

#### Scenario: Schema three uses one loop context representation
- **WHEN** a terminal schema-3 history contains root-owned test, nested review and workflow-scope report
- **THEN** read-only observation preserves root and nested paths and report path omission; active schema-3 loop recovery is unsupported

## ADDED Requirements

### Requirement: Control tasks form complete phase barriers
For every loop scope, the projected DAG SHALL contain its directly owned tasks and each immediate child subtree as one vertex, with projected dependency edges between distinct vertices. For each directly owned control c, every other vertex SHALL be either a strict dependency ancestor or a strict dependency descendant of c using paths inside that scope. Incomparable vertices SHALL cause rejection even with max_parallel one. All directly owned controls SHALL therefore be totally ordered by reachability. Recursive boundary-cycle checks SHALL apply before this rule, including edges crossing grandchildren.

At runtime a control SHALL wait for all projected ancestors to settle acceptably; a predecessor child vertex SHALL mean its entire invocation has completed, not merely a referenced internal task. Every projected successor SHALL remain unreserved until a committed proceed-equivalent release. Admission of a child vertex SHALL gate all its descendant task reservations on all incoming projected predecessors and enclosing phase gates. Internal tasks without explicit incoming needs SHALL NOT escape this gate. Ordering edges SHALL NOT implicitly supply undeclared results. No control decision SHALL rely on cancelling already-started suffix work. Unexpected active suffix work SHALL hold as an invariant violation.

#### Scenario: Head middle and tail barriers
- **WHEN** c1 precedes parallel a/b which join at c2 followed by tail
- **THEN** validation accepts and both controls release their respective phases in order

#### Scenario: Bypass rejected
- **WHEN** a feeds c and x independently, and both feed b
- **THEN** validation rejects x as incomparable to c despite a common source/sink

#### Scenario: Independent controls rejected
- **WHEN** c1 and c2 independently feed a common join
- **THEN** validation rejects both being unordered

#### Scenario: Child entry gates hidden roots
- **WHEN** c feeds inner.a while inner.b has no direct needs
- **THEN** neither inner.a nor inner.b is reserved before c proceeds

#### Scenario: Child exit waits for hidden work
- **WHEN** inner.a feeds outer.c and inner.b is independently still running in that child invocation
- **THEN** outer.c waits for all of inner to complete

#### Scenario: Child straddling a control rejected
- **WHEN** inner.a feeds c which feeds inner.b
- **THEN** the projected boundary cycle is rejected

#### Scenario: Outer work does not belong to inner barrier
- **WHEN** an inner control is independent of an ordinary outer task and no outer control requires ordering
- **THEN** the graph is accepted and inner break neither skips nor cancels that outer task

#### Scenario: No cancellation substitute
- **WHEN** saved/runtime state unexpectedly has a reserved successor when a control resolves
- **THEN** execution holds without applying the action or cancelling work to pretend the barrier held

### Requirement: Control decisions govern the remaining local pass
A control SHALL apply exactly the action mapped from its successfully resolved current-context verdict, before releasing its suffix. proceed SHALL release the current suffix; continue SHALL skip it and close the pass requesting repetition within the cap; break SHALL skip it and complete only the directly owning loop; needs_attention SHALL hold the suffix pending and identify owner path, task, attempt and verdict. Multiple controls SHALL execute in graph order; a skipped later control SHALL not be classified or evaluated. Natural acceptable pass completion SHALL repeat under the cap/exhaustion rules. Accepted graceful stop SHALL modify effective actions only as specified by workflow-lifecycle.

Backend failure, timeout, missing output, interruption, unresolved classification and uncertainty resolved as error SHALL NOT be mapped to semantic actions. A failed mandatory control SHALL use ordinary loop-failure intervention and explicit retry. No verdict name or free-form response SHALL have implicit control semantics; tasks without control SHALL retain ordinary verdict behavior.

Break/continue SHALL persist each remaining task as skipped with reason loop_break/loop_continue, decision identity and full planned path, without a backend attempt or successful result. Unadmitted child invocations and their subtree task outcomes SHALL be explicitly skipped; hypothetical later child passes SHALL NOT be created. Completed prefix outcomes SHALL be retained. Control skips SHALL close that pass without being successes, failures, cancellations or retryable work.

Skipping an unadmitted child SHALL recursively record every unadmitted descendant loop invocation as skipped with entered false and iterations_started zero. Each planned path SHALL append local counter 1 at every skipped descendant level under the actual current ancestor prefix; each task skip SHALL use its direct owner's complete planned path. These records SHALL not create admitted passes or real attempts.

#### Scenario: Continue skips later controls
- **WHEN** c1 continues and c2 follows later in the pass
- **THEN** c2 and intervening suffix work are recorded skipped without judge calls and the next pass requires budget

#### Scenario: Break is local
- **WHEN** inner control breaks while its parent has remaining work
- **THEN** inner completes, the parent may continue, and no ancestor action is inferred

#### Scenario: Mid-pass attention is unresolved
- **WHEN** c maps a verdict to needs_attention
- **THEN** its prefix remains complete and its suffix stays pending without skip records until intervention

#### Scenario: Failure is not a verdict
- **WHEN** control backend fails or produces empty output
- **THEN** no mapped action is applied and the same-path control is eligible for explicit repair under retry guards

#### Scenario: Free-form request does not hold
- **WHEN** a task without control returns text asking for help or a verdict named needs_input
- **THEN** no mapped loop action is inferred

#### Scenario: Break skips child and grandchild invocations
- **WHEN** outer at iteration 2 breaks before child and its grandchild are admitted
- **THEN** child is skipped at outer=2/child=1 and grandchild at outer=2/child=1/grandchild=1, both entered false with iterations_started zero and no attempts

## REMOVED Requirements

### Requirement: Loop conditions evaluate settled verdicts
**Reason**: Terminal-only until_task/on_verdict is replaced by ordered task-local control barriers.
**Migration**: Use workflow version 2 and move the old on_verdict map to the directly owned task.control; preserve needs/caps and review early-exit result availability. Saved executions follow the explicit legacy recovery boundary.
