## MODIFIED Requirements

Scenario titles inherited from the current main spec are retained as stable regression identifiers for OpenSpec replacement validation. In their updated scenario bodies, control means the version-2 task.control model; condition refers only to explicitly described legacy syntax/history.

### Requirement: Confidence threshold gates uncertain outcomes
Workflow definitions SHALL accept a `confidence_threshold` between 0 and 1 exclusive of zero, defaulting to 0.7 when no machine judge default exists. An explicit workflow threshold SHALL take precedence over a machine judge threshold, which SHALL take precedence over 0.7. Saved definitions without a threshold SHALL use the startup-loaded machine threshold, or 0.7 when absent, for subsequent classifications, including recovery; already settled verdicts SHALL retain their recorded outcomes and thresholds. The judge's chosen verdict SHALL apply only when its confidence is greater than or equal to the threshold. Below the threshold the attempt receives the synthetic `uncertain` outcome, which MUST NOT be declarable as a verdict name. With `on_uncertain: needs_attention` (the default) the execution SHALL enter needs_attention with an uncertain-verdict reason, keep the attempt in `judging`, and expose the full probability distribution for inspection. With `on_uncertain: error` the attempt SHALL fail with reason `uncertain_verdict`, the distribution SHALL still be recorded, and existing failure policy and retry semantics apply.

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
- **WHEN** a held control verdict is overridden to continue at the effective cap
- **THEN** the loop applies on_exhaustion, holding under needs_attention or completing under succeed, and never creates an iteration beyond the cap

#### Scenario: Override resolves only its own cause
- **WHEN** a valid control override resolves one loop's action hold while another loop still needs attention
- **THEN** the override is accepted and its own cause is rederived, unrelated reasons remain, and no new task dispatch or iteration advance occurs until all causes clear

#### Scenario: Default threshold accepts moderate confidence
- **WHEN** the workflow and machine config omit `confidence_threshold` and the judge returns confidence 0.70, 0.72, 0.78, or 0.79
- **THEN** the declared verdict applies and the recorded threshold is 0.7

#### Scenario: Below the new default remains uncertain
- **WHEN** the workflow and machine config omit `confidence_threshold` and the judge returns confidence 0.68
- **THEN** the outcome is uncertain and the existing `on_uncertain` policy applies

#### Scenario: Explicit previous threshold keeps its gate
- **WHEN** the workflow explicitly sets `confidence_threshold: 0.8` and the judge returns confidence 0.79
- **THEN** the outcome is uncertain under the configured policy

#### Scenario: Recovery uses the saved definition and current default
- **WHEN** a saved execution without a threshold recovers on a machine without a judge threshold and reclassifies a judging attempt with confidence 0.72
- **THEN** the verdict applies with recorded threshold 0.7 without rerunning the agent

#### Scenario: Recovery preserves explicit thresholds and settled history
- **WHEN** an execution with an explicit threshold or already settled verdicts is recovered
- **THEN** subsequent classification uses the explicit threshold when present and settled verdicts retain their recorded outcomes and thresholds

#### Scenario: Machine default governs classification and auditing
- **WHEN** the machine threshold is 0.6, the workflow omits its threshold, and the judge returns confidence 0.65
- **THEN** the declared verdict applies and its recorded threshold is 0.6

#### Scenario: Machine threshold boundary and uncertainty
- **WHEN** the machine threshold is 0.6, the workflow omits its threshold, and the judge returns confidence 0.6 or 0.59
- **THEN** 0.6 applies the declared verdict and 0.59 follows the configured `on_uncertain` policy

#### Scenario: Explicit workflow threshold still governs
- **WHEN** the machine threshold is 0.6, the workflow threshold is 0.8, and the judge returns confidence 0.7
- **THEN** the result is uncertain under the workflow threshold and the recorded threshold is 0.8

#### Scenario: Restart reclassifies with the current machine default
- **WHEN** an execution saved without a threshold has an unresolved judging attempt, the machine file sets 0.6, and the server restarts
- **THEN** reclassification uses 0.6 without rerunning the agent; settled verdicts keep their recorded outcomes and thresholds

#### Scenario: Manual override records the effective threshold
- **WHEN** a held judging attempt is manually overridden while a machine threshold of 0.6 applies and the workflow has no explicit threshold
- **THEN** the override records threshold 0.6

### Requirement: Held verdicts admit manual override
The system SHALL accept `POST /workflows/{id}/tasks/{task}/attempts/{attempt}/verdict` with a nonempty `request_id` and a `verdict` value declared by that task. For an attempt held in `judging`, the override SHALL record the verdict with a manual source and settle the attempt as succeeded with that verdict. A settled attempt whose recorded verdict is load-bearing on a live hold SHALL be overridable the same way: an attempt whose verdict currently holds a control barrier through a `needs_attention` action. The replacement verdict is recorded with a manual source on a new view of the attempt outcome, and downstream behavior — dependency evaluation and control barriers included — follows the replacement verdict. Repeating an identical request SHALL return the recorded result without new effects; a different verdict for the same request ID SHALL return 409. Overrides against attempts not in `judging` and not holding as described SHALL return 409, undeclared verdict names SHALL return 400, and unknown execution, task, or attempt identifiers SHALL return 404. For a loop-owned attempt, a new override SHALL additionally require the complete iteration path to match the current loop and every ancestor iteration. Matching a local counter alone SHALL NOT make an old invocation current. A settled control attempt in a done owning invocation SHALL return 409 even if that invocation's final path is still current: it has no live control-action hold. Override SHALL NOT reopen a done invocation or any ancestor. Existing judging eligibility and direct ownership of a loop's control task SHALL remain required where applicable. Prior iteration contexts SHALL remain immutable. Identical accepted requests SHALL replay before current-context and done-loop eligibility checks, returning their existing result without changing history or applying the old verdict to a new invocation.

For version-2 executions a successfully resolved control SHALL atomically publish its verdict and mapped decision/hold before releasing any suffix. An eligible override of a live needs_attention decision SHALL preserve original judge evidence and append a manual outcome revision with its new mapped decision. proceed SHALL release the current suffix, continue SHALL skip and request another bounded pass, and break SHALL skip and complete the direct owner. An override to another attention-mapped verdict SHALL retain a live hold. The existing stop-intent effective-action rules SHALL apply. Other attention causes and pause SHALL continue to gate dispatch/advance.

A settled proceed, continue or break decision SHALL NOT be overridden, even if paused or its suffix has no attempts yet. A superseded, historical, control-skipped, or done/skipped-owner context SHALL NOT become eligible by matching a local counter. Legacy read-only executions SHALL reject mutating overrides under the version compatibility contract. Override requests and their decision/skips/replay record SHALL commit atomically; save failure SHALL leave the original hold/evidence authoritative. Late judge callbacks after a manual resolution SHALL NOT replace it. No endpoint SHALL accept a raw loop action in place of a declared verdict.

Eligibility SHALL require an unresolved judging attempt or a decision whose current status is held on mapped needs_attention. Any released or closed decision SHALL reject a new override with 409 regardless of its mapped action. In particular, stop-resolved mapped needs_attention with effective proceed SHALL no longer be a live hold and SHALL not be overridden, even while paused or before suffix dispatch. Identical accepted replay SHALL remain read-only under the version compatibility rule.

#### Scenario: Human resolves an uncertain hold
- **WHEN** an attempt is held on an uncertain verdict and the facilitator posts a declared verdict for it
- **THEN** the attempt settles as succeeded with that verdict recorded as manually sourced, and scheduling follows its declared mapping and all remaining global gates

#### Scenario: Idempotent override
- **WHEN** the same override request ID and verdict are posted twice
- **THEN** the second call returns the recorded result without side effects

#### Scenario: Override rejected for settled attempts
- **WHEN** the addressed attempt has already settled and its verdict holds nothing
- **THEN** the request returns 409 and changes nothing

#### Scenario: Ordinary verdict name does not permit override
- **WHEN** a non-control attempt has settled with a declared verdict named blocked or needs_input
- **THEN** the name alone does not make the attempt overridable, and a new override request returns 409

#### Scenario: Override redirects a held control
- **WHEN** a control task's verdict mapped to needs_attention is overridden to continue below the effective cap and no other attention reasons remain
- **THEN** the control hold clears and the current suffix is skipped and the loop requests a bounded next pass under the replacement verdict

#### Scenario: History stays immutable
- **WHEN** an override targets an attempt whose iteration the loop has advanced past
- **THEN** the request returns 409 and no recorded verdict or iteration counter changes

#### Scenario: Historical nested override is rejected
- **WHEN** a new override targets outer=1/inner=1 while the task is now at outer=2/inner=1
- **THEN** the endpoint returns 409 despite equal local counters and preserves both history and current holds

#### Scenario: Done invocation cannot be reopened by override
- **WHEN** a new override addresses a settled control attempt in a done loop whose final path is still current
- **THEN** the endpoint returns 409 without reopening that loop or its ancestors

#### Scenario: Accepted override replays after completion or ancestor advance
- **WHEN** an accepted override is repeated identically after its loop completed or its ancestor advanced
- **THEN** the existing result is returned without changing a historical verdict, creating work, or applying the action to a new invocation

#### Scenario: Override to proceed retains current pass
- **WHEN** a middle control with a live attention hold is overridden to a declared proceed-mapped verdict
- **THEN** the hold clears, the current suffix becomes eligible under global gates, and the iteration counter is unchanged

#### Scenario: Override to break skips atomically
- **WHEN** a live control hold is overridden to a break-mapped verdict
- **THEN** manual evidence, decision, suffix skips and owner completion commit together

#### Scenario: Settled proceed cannot be rewritten
- **WHEN** a control has committed proceed while paused and its suffix has not run
- **THEN** a new override returns 409 without changing its verdict or reopening the decision

#### Scenario: Old callback cannot undo manual choice
- **WHEN** a judge result arrives after an accepted manual override
- **THEN** the manual resolution remains authoritative and no second control action applies

#### Scenario: Override redirects a held condition
- **WHEN** a live control attention verdict is overridden to a declared continue-mapped verdict below cap without stop intent
- **THEN** the suffix is skipped, that hold clears and a bounded next pass can begin when all global gates allow

#### Scenario: Stop-resolved attention cannot be overridden
- **WHEN** stop resolves a control attention hold to effective proceed and a new override arrives before its suffix starts
- **THEN** the endpoint returns 409 because the decision is released, preserving its original mapped needs_attention verdict and stop evidence

#### Scenario: Control override applies cap and global gates
- **WHEN** a held control is overridden to a declared continue-mapped verdict at the cap while another attention cause remains
- **THEN** its suffix is skipped and exhaustion is applied atomically; no extra pass or dispatch bypasses the cap or unrelated hold
