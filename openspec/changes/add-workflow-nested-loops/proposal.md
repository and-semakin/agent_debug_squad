## Why

The current workflow engine can repeat a review-and-fix graph, but cannot repeat that entire loop together with a subsequent test task. Nested loops let authors express this workflow while preserving bounded execution, fresh agent conversations, durable history, and explicit intervention.

## What Changes

- Add optional `parent` to loop definitions. A task's `loop` names its direct owner; ancestor loops contain its work transitively. Nesting has no fixed depth limit.
- In nested executions, identify every loop iteration, including root iterations, by its complete root-to-owner `iteration_path`, not a pair of local counters. A child loop invocation belongs to exactly one parent iteration.
- Permit dependencies within a loop and across ancestor/descendant boundaries. A consumer above a producer waits for the intervening loop invocations to finish. Direct edges between unrelated branches remain invalid; authors can use an explicit task in the common enclosing scope, including workflow scope for separate roots.
- Treat completion and repetition as distinct transitions. Completion preserves the final subtree. Repetition increments only the repeating loop and starts new child invocations, atomically with descendant task reset.
- Give each condition task one direct loop owner. Validate dependency deadlocks at every scope, and require all tasks in a conditioned loop's subtree to reach its directly owned condition task.
- Carry forward the final outcomes of the previous iteration at each enclosing level. Resolve every reference using its complete iteration context.
- Scope retries to the current context and to downstream reservations in the same shared enclosing iteration. Old outer iterations do not block current repairs; consumed results and historical contexts remain immutable.
- Propagate ancestor stops through unfinished descendants. Nested extensions and stop intent belong to one invocation; accepted control replays never affect a later invocation.
- Preserve complete contexts across recovery and expose them in views, manifests, and control audit records. Nested executions use snapshot schema 3 so existing binaries reject them instead of treating them as flat workflows.

The target scenario remains an inner `implement -> review -> consolidate` loop followed by an outer `test` condition. A negative test verdict repeats the outer loop and passes its result plus the final outcomes of the previous inner invocation to the next implementer; intermediate inner attempts remain history. Agent/backend failures still require intervention; a negative semantic verdict is not a failed agent attempt.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `declarative-workflows`: loop-tree validation, iteration identity, bounded subtree execution, scoped dependencies and carry-over, and directly owned conditions.
- `workflow-lifecycle`: context-aware retry consistency, nested recovery and snapshot compatibility, canonical path-bearing reason strings, and invocation-scoped stop/extend controls.
- `verdict-judge`: complete-path override eligibility, rejection for done loops, and unchanged replay semantics.

## Impact

- `internal/domain`: optional parent/path fields, nested manifest context, control audit context, schema selection.
- `internal/config`: strict parent parsing, tree validation, recursive boundary checks, condition ownership and subtree sink validation.
- `internal/workflow`: path-based attempt selection, transitions, input construction, retry guards, condition/override checks, controls, and recovery.
- `internal/store` and API views: schema-3 validation and complete nested identities; existing endpoint paths and request bodies remain unchanged.
- README and examples: runnable nested review/test example and explanation of context, handoffs, budgets, and intervention.

Definitions without `parent` keep their existing identity, scheduling semantics, and serialized public shapes; newly saved nonnested executions continue using schema 2. The new binary reads supported schema-1/2 state and nested schema-3 state. Downgrading nested state is unsupported. No new dependencies, automatic retries, answer-text injection, workspace isolation, or verdict-classifier changes are included.
