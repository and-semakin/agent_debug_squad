## Why

A loop can currently consult only one mandatory terminal `until_task`, after the entire iteration settles. Queue processing therefore needs separate initial/next selectors and cannot decide to stop or restart before unnecessary work; nested subtrees and saved executions make an informal change to that rule unsafe.

## What Changes

- **BREAKING**: Introduce workflow version 2 for loop definitions; replace loop-level `until_task`/`on_verdict` with an optional task-level `control` map from every declared verdict to `proceed`, `continue`, `break`, or `needs_attention`. Reject removed fields with migration examples. Version 1 remains accepted only for loopless new definitions.
- Permit zero, one, or multiple directly owned control tasks, positioned by `needs`. Controls form ordered phase barriers; reject incomparable work in the owning scope and gate entire child subtrees across those barriers.
- Apply control decisions at their task, before releasing its suffix. `proceed` releases that suffix; `continue` skips it and requests another bounded iteration; `break` skips it and completes the owner; `needs_attention` holds it. Natural body completion repeats until the cap. No controls means a fixed-count loop.
- Count every entered iteration against a positive cap, including an empty-queue check or early continue. Retain explicit audited extension, exhaustion policies, failure tolerance, full iteration identities, and conservative retries.
- Persist decision and explicit skip outcomes atomically; retain completed results and immutable history. Distinguish absent final-iteration results from earlier successes in dependencies and historical carry-over.
- **BREAKING**: Add snapshot schema 4 for version 2, including full paths for flat loops, decisions, invocation history, and skips. Older loop executions remain inspectable but cannot execute in the new engine: active recovery fails closed with instructions to finish/cancel using the previous binary. No automatic migration, replay under new semantics, or deletion of old artifacts.
- Keep existing pause/resume/cancel, verdict-override, loop stop, and loop extend routes. Specify how graceful stop interacts with a mid-iteration control barrier; it does not become immediate cancellation.

## Capabilities

### New Capabilities

None; these are changes to the existing workflow engine.

### Modified Capabilities

- `declarative-workflows`: versioned syntax, barrier validation and scheduling, local loop control, budgets, skipped outcomes, exact-context handoff.
- `workflow-lifecycle`: durable control transitions, schema compatibility, recovery, observation, retry guards, and intervention semantics.
- `verdict-judge`: apply control mappings after classification, preserve uncertainty/failure separation, and restrict manual override to the current live decision hold.

## Impact

Touches `internal/config`, `internal/domain`, `internal/workflow`, `internal/store`, API views/tests in `internal/api`, CLI presentation/tests as needed, README and loop examples. Backend adapter interfaces, manual sessions, judge provider protocol, and external services need no changes. HTTP consumers must tolerate schema-4 paths, skipped task states, decision metadata and unavailable-result reasons; existing routes remain stable.

The implementation excludes changes to current one-shot execution behavior, changes to the current confidence-threshold resolution rules, early quorum, labelled/nonlocal jumps, concurrent cancellation on break, arbitrary expressions, dynamic graphs, new cross-iteration input DSLs, and exactly-once external side effects.

Planning baseline: reconciled against local `main` at `1c444a2` on 2026-09-27. Existing backend preflight, managed-runtime and one-shot behavior is preserved. Implementation uses `origin/main` baseline `2f09a36` (2026-09-27); the three affected main specs had no intervening drift.
