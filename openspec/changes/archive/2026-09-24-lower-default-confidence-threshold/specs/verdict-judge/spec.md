## MODIFIED Requirements

### Requirement: Confidence threshold gates uncertain outcomes
Workflow definitions SHALL accept a `confidence_threshold` between 0 and 1 exclusive of zero, defaulting to 0.7. Explicit thresholds SHALL take precedence over the default. Saved definitions without a threshold SHALL use the current default for subsequent classifications, including recovery; already settled verdicts SHALL retain their recorded outcomes and thresholds. The judge's chosen verdict SHALL apply only when its confidence is greater than or equal to the threshold. Below the threshold the attempt receives the synthetic `uncertain` outcome, which MUST NOT be declarable as a verdict name. With `on_uncertain: needs_attention` (the default) the execution SHALL enter needs_attention with an uncertain-verdict reason, keep the attempt in `judging`, and expose the full probability distribution for inspection. With `on_uncertain: error` the attempt SHALL fail with reason `uncertain_verdict`, the distribution SHALL still be recorded, and existing failure policy and retry semantics apply.

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
- **WHEN** the workflow omits `confidence_threshold` and the judge returns confidence 0.70, 0.72, 0.78, or 0.79
- **THEN** the declared verdict applies and the recorded threshold is 0.7

#### Scenario: Below the new default remains uncertain
- **WHEN** the workflow omits `confidence_threshold` and the judge returns confidence 0.68
- **THEN** the outcome is uncertain and the existing `on_uncertain` policy applies

#### Scenario: Explicit previous threshold keeps its gate
- **WHEN** the workflow explicitly sets `confidence_threshold: 0.8` and the judge returns confidence 0.79
- **THEN** the outcome is uncertain under the configured policy

#### Scenario: Recovery uses the saved definition and current default
- **WHEN** a saved execution without a threshold recovers and reclassifies a judging attempt with confidence 0.72
- **THEN** the verdict applies with recorded threshold 0.7 without rerunning the agent

#### Scenario: Recovery preserves explicit thresholds and settled history
- **WHEN** an execution with an explicit threshold or already settled verdicts is recovered
- **THEN** subsequent classification uses the explicit threshold when present and settled verdicts retain their recorded outcomes and thresholds
