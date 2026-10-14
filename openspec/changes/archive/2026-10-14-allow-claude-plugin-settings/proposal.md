# Proposal

## Why
Squad rejects enabledPlugins in Claude settings even though the supplied Devin 3000.11.3 ACP experiments did not observe automatic Claude registry import. Users must currently disable all Claude imports, losing rules and skills unnecessarily.

## What Changes
- Treat Claude enabledPlugins as unrelated settings, preserving native import policy.
- Retain active hook/MCP checks and native Devin plugin/migration restrictions.
- Add regression coverage and explain the evidence and its version-specific limits.

## Capabilities
### New Capabilities
None.
### Modified Capabilities
- `devin-backend`: allow Claude plugin enablement metadata without disabling imports.

## Impact
Small profile compatibility fix, documentation and tests. No YAML/API/persistence changes or user configuration edits.
