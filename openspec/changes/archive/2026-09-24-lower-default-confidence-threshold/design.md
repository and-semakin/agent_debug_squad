## Context

See proposal.md for motivation. `WorkflowDefinition` stores an omitted confidence threshold as zero with JSON `omitempty`; `EffectiveConfidenceThreshold` resolves the shared domain default at use. The scheduler classifies against the saved definition and records the threshold on each verdict. Definition hashing serializes the definition rather than its effective defaults.

## Goals / Non-Goals

**Goals:** Change the fallback in one place, preserving explicit configuration and persisted definition identity.

**Non-Goals:** Snapshot migration, retroactive verdict evaluation, new controls, altered recovery scheduling, or changes to adapters, cancellation, concurrency, and sessions.

## Decisions

Change only `DefaultConfidenceThreshold` to 0.7 in production code. Do not materialize defaults during config load or recovery: that alternative would change definition identity and replay compatibility. Keep the existing `confidence >= threshold` gate and uncertainty handling.

Regression tests will pin the literal default, cover 0.70/0.72/0.78/0.79 acceptance and 0.68 uncertainty, explicit 0.8 precedence, both uncertainty policies, and recovered classification. Existing golden hash coverage verifies unchanged omitted fields. Current specs and README will describe the changed fallback; historical design documents and explicit example thresholds remain intact.

## Risks / Trade-offs

- Lower-confidence verdicts may now continue automatically → explicit 0.8 preserves the previous gate.
- Persisted definitions do not pin the old implicit default → future classification of omitted-threshold executions, including recovery of judging attempts, uses 0.7. This is intentional behavioral compatibility impact despite stable hashes and schemas.
- Existing uncertain or settled outcomes are not simply recomputed from their old confidence → normal recovery/resume classification rules still apply; settled verdict records remain unchanged.

## Migration Plan

No snapshot migration or schema bump. Update the binary through the ordinary process; this task does not publish a release. Explicit settings in user-local workflow files remain untouched. Configure 0.8 explicitly for newly submitted definitions if the previous behavior is required; changing a YAML file does not replace a persisted execution's definition. Reverting the constant restores the old fallback for future classification but does not undo already settled outcomes.
