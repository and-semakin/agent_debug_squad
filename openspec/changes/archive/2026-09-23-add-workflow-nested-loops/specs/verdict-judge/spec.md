# verdict-judge Delta

## MODIFIED Requirements

### Requirement: Held verdicts admit manual override
The system SHALL accept `POST /workflows/{id}/tasks/{task}/attempts/{attempt}/verdict` with a nonempty `request_id` and a `verdict` value declared by that task. For an attempt held in `judging`, the override SHALL record the verdict with a manual source and settle the attempt as succeeded with that verdict. A settled attempt whose recorded verdict is load-bearing on a live hold SHALL be overridable the same way: an attempt whose verdict currently holds a loop condition through a `needs_attention` action. The replacement verdict is recorded with a manual source on a new view of the attempt outcome, and downstream behavior — dependency evaluation and loop conditions included — follows the replacement verdict. Repeating an identical request SHALL return the recorded result without new effects; a different verdict for the same request ID SHALL return 409. Overrides against attempts not in `judging` and not holding as described SHALL return 409, undeclared verdict names SHALL return 400, and unknown execution, task, or attempt identifiers SHALL return 404. For a loop-owned attempt, a new override SHALL additionally require the complete iteration path to match the current loop and every ancestor iteration. Matching a local counter alone SHALL NOT make an old invocation current. A settled condition attempt in a done owning invocation SHALL return 409 even if that invocation's final path is still current: it has no live condition-action hold. Override SHALL NOT reopen a done invocation or any ancestor. Existing judging eligibility and direct ownership of a loop's condition task SHALL remain required where applicable. Prior iteration contexts SHALL remain immutable. Identical accepted requests SHALL replay before current-context and done-loop eligibility checks, returning their existing result without changing history or applying the old verdict to a new invocation.

#### Scenario: Human resolves an uncertain hold
- **WHEN** an attempt is held on an uncertain verdict and the facilitator posts a declared verdict for it
- **THEN** the attempt settles as succeeded with that verdict recorded as manually sourced, and the execution resumes scheduling

#### Scenario: Idempotent override
- **WHEN** the same override request ID and verdict are posted twice
- **THEN** the second call returns the recorded result without side effects

#### Scenario: Override rejected for settled attempts
- **WHEN** the addressed attempt has already settled and its verdict holds nothing
- **THEN** the request returns 409 and changes nothing

#### Scenario: Ordinary verdict name does not permit override
- **WHEN** a non-condition attempt has settled with a declared verdict named blocked or needs_input
- **THEN** the name alone does not make the attempt overridable, and a new override request returns 409

#### Scenario: Override redirects a held condition
- **WHEN** a condition task's verdict mapped to needs_attention is overridden to continue below the effective cap and no other attention reasons remain
- **THEN** the condition hold clears and the loop re-arms under the replacement verdict

#### Scenario: History stays immutable
- **WHEN** an override targets an attempt whose iteration the loop has advanced past
- **THEN** the request returns 409 and no recorded verdict or iteration counter changes

#### Scenario: Historical nested override is rejected
- **WHEN** a new override targets outer=1/inner=1 while the task is now at outer=2/inner=1
- **THEN** the endpoint returns 409 despite equal local counters and preserves both history and current holds

#### Scenario: Done invocation cannot be reopened by override
- **WHEN** a new override addresses a settled condition attempt in a done loop whose final path is still current
- **THEN** the endpoint returns 409 without reopening that loop or its ancestors

#### Scenario: Accepted override replays after completion or ancestor advance
- **WHEN** an accepted override is repeated identically after its loop completed or its ancestor advanced
- **THEN** the existing result is returned without changing a historical verdict, creating work, or applying the action to a new invocation
