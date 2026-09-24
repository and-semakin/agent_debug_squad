## MODIFIED Requirements

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
- **WHEN** a held condition verdict is overridden to continue at the effective cap
- **THEN** the loop applies on_exhaustion, holding under needs_attention or completing under succeed, and never creates an iteration beyond the cap

#### Scenario: Override resolves only its own cause
- **WHEN** a valid condition override resolves one loop's action hold while another loop still needs attention
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
