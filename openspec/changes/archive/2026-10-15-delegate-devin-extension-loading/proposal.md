# Proposal

## Why
Squad's extension/configuration compatibility guards reject native Devin setups before the CLI can decide whether they work. The operator explicitly requests delegating these decisions to Devin.

## What Changes
- Remove extension, imported configuration, project lifecycle and legacy MCP preflight refusals.
- Preserve native settings in the private user config; let Devin load extensions and report startup errors.
- Keep bounded private config copying, auto-update/subagent launch defaults, ACP/model verification and owned process cleanup.
- Document that native loading and migration may have side effects; Squad does not promise extension isolation.

## Capabilities
### New Capabilities
None.
### Modified Capabilities
- `devin-backend`: native extension loading replaces compatibility preflight.

## Impact
Devin launch preparation, regression tests and docs. No new options or state migration. Existing native hooks/MCP/plugins may now execute.
