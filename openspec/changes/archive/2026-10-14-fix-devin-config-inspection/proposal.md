# Proposal

## Why
Devin refuses to start when ordinary Claude or OpenCode settings files exist, even if they contain no extensions. The generic error hides which file caused the failure.

## What Changes
- Inspect known extension fields instead of rejecting JSON files by existence.
- Respect effective native import switches, including project/local overrides.
- Identify the source path and schema key without exposing configuration values.
- Keep actual active extensions and legacy native migrations unsupported; preserve user files and policies.

## Capabilities

### New Capabilities
None.

### Modified Capabilities
- `devin-backend`: content-aware launch-profile inspection and actionable diagnostics.

## Impact
Devin profile preparation, regression tests and backend documentation. No new YAML options, HTTP schemas or persisted state; previously rejected harmless configurations become usable.
