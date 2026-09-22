# verdict-judge Delta

## MODIFIED Requirements

### Requirement: Held verdicts admit manual override
The system SHALL accept `POST /workflows/{id}/tasks/{task}/attempts/{attempt}/verdict` with a nonempty `request_id` and a `verdict` value declared by that task. For an attempt held in `judging`, the override SHALL record the verdict with a manual source and settle the attempt as succeeded with that verdict. A settled attempt whose recorded verdict is load-bearing on a live hold SHALL be overridable the same way: an attempt whose verdict currently holds a loop condition through a `needs_attention` action. The replacement verdict is recorded with a manual source on a new view of the attempt outcome, and downstream behavior — dependency evaluation and loop conditions included — follows the replacement verdict. Repeating an identical request SHALL return the recorded result without new effects; a different verdict for the same request ID SHALL return 409. Overrides against attempts not in `judging` and not holding as described SHALL return 409, undeclared verdict names SHALL return 400, and unknown execution, task, or attempt identifiers SHALL return 404. Prior iterations remain immutable: overriding a verdict from an iteration the loop has advanced past is not permitted.

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

### Requirement: Confidence threshold gates uncertain outcomes
Workflow definitions SHALL accept a `confidence_threshold` between 0 and 1 exclusive of zero, defaulting to 0.8. The judge's chosen verdict SHALL apply only when its confidence is greater than or equal to the threshold. Below the threshold the attempt receives the synthetic `uncertain` outcome, which MUST NOT be declarable as a verdict name. With `on_uncertain: needs_attention` (the default) the execution SHALL enter needs_attention with an uncertain-verdict reason, keep the attempt in `judging`, and expose the full probability distribution for inspection. With `on_uncertain: error` the attempt SHALL fail with reason `uncertain_verdict`, the distribution SHALL still be recorded, and existing failure policy and retry semantics apply.

#### Scenario: Confident verdict applies
- **WHEN** the judge returns `issues_found` with confidence 0.93 against a threshold of 0.8
- **THEN** the attempt settles with verdict `issues_found` and the recorded distribution

#### Scenario: Uncertain holds for inspection
- **WHEN** the judge returns confidence 0.45 and the workflow uses the default `on_uncertain`
- **THEN** the execution enters needs_attention, the attempt remains judging, and the view shows the distribution across all declared verdicts

#### Scenario: Uncertain as failure
- **WHEN** the judge returns confidence below the threshold and the workflow sets `on_uncertain: error`
- **THEN** the attempt fails with reason `uncertain_verdict`, its dependents follow existing failure policy, and the task is retryable through the existing retry API

#### Scenario: Threshold boundary
- **WHEN** the judge's confidence equals the configured threshold exactly
- **THEN** the chosen verdict applies


#### Scenario: Explicit attention policy
- **WHEN** confidence is below the threshold and on_uncertain is needs_attention
- **THEN** the execution enters needs_attention and the attempt stays judging, with the same manual override and reclassification paths as the default policy


#### Scenario: Override to continue at the cap
- **WHEN** a held condition verdict is overridden to continue at the effective cap
- **THEN** the loop applies on_exhaustion, holding under needs_attention or completing under succeed, and never creates an iteration beyond the cap

#### Scenario: Override resolves only its own cause
- **WHEN** a valid condition override resolves one loop's action hold while another loop still needs attention
- **THEN** the override is accepted and its own cause is rederived, unrelated reasons remain, and no new task dispatch or iteration advance occurs until all causes clear
