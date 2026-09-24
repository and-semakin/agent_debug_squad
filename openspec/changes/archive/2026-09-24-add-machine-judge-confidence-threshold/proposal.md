## Why

A user who runs several workflows cannot change the confidence gate for all of them without editing each workflow file. A machine-level judge default lets one computer use a different gate while keeping shared workflow YAML portable.

## What Changes

- Accept `judge.confidence_threshold` as a numeric setting in the existing `~/.agent-debug-squad/backends.yaml` file, with the same `(0, 1]` range as a workflow threshold.
- Resolve the effective gate in this order: explicit workflow threshold, machine judge threshold, built-in 0.7. Continue recording the effective threshold on each verdict, including manual overrides.
- Apply the startup-loaded machine setting to future classifications of saved executions whose workflow omitted a threshold, including recovery and resume. Keep settled verdicts and definition hashes unchanged.
- Document the setting and its compatibility effect; configure this computer to use 0.6 without changing any shared workflow YAML.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `backend-config`: accept and validate a numeric judge threshold in the machine file.
- `verdict-judge`: use the machine default when the workflow omits its own threshold.

## Impact

The Go machine-config parser and domain settings gain a judge threshold field; the workflow manager consults that setting during classification and manual override. No HTTP or persisted schema changes. Invalid machine values fail startup. Existing installations without this key still use 0.7. Saved definitions keep their omitted field and hash; editing the machine file takes effect after server restart and can change the gate for unresolved classifications. Explicit workflow values, including 0.8, continue to win. No release version source is edited by hand.
