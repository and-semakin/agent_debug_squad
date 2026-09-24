## Why

The default confidence gate stops workflows for judge scores that should be sufficient for routine continuation. Lowering it from 0.8 to 0.7 accepts scores such as 0.78, 0.79, and 0.72 while retaining intervention at 0.68.

## What Changes

- Set the workflow confidence threshold default to 0.7, preserving explicit overrides, validation, inclusive comparison, and uncertainty policies.
- Document the effect on persisted executions and retain definition hash compatibility.
- Update current specifications, documentation, and regression coverage. No structured verdicts, task-specific thresholds, lifecycle redesign, or release publication.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `declarative-workflows`: change the omitted confidence threshold default while retaining definition identity.
- `verdict-judge`: apply the 0.7 default during classification, including recovered judging attempts.

## Impact

The shared domain constant changes; existing config parsing, scheduler, API shapes, and persistence schemas remain intact. Omitted thresholds retain their zero/omitted representation and hashes but resolve to 0.7 in future classifications, including recovery. Explicit 0.8 remains 0.8. Already settled verdicts and recorded thresholds are not retroactively rewritten. Users requiring the previous gate should explicitly configure 0.8 for new definitions; existing saved definitions retain their own settings. No user-local workflow files will be edited.
