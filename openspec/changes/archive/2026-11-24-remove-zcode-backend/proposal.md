# Proposal

## Why

The ZCode backend depends on private Desktop internals that repeatedly change: registry bootstrap, host callbacks, and most recently credential cipher access from a separate Node process. Desktop login can remain valid while Squad fails before inference; maintaining this integration is no longer justified.

## What Changes

- **BREAKING**: Deprecate and remove the executable ZCode backend and private JavaScript bridge.
- Reject ZCode agents and machine settings with migration guidance; remove it from model discovery and installation support.
- Remove the runnable example and development probe, update documentation and the bundled skill, and preserve historical run artifacts.
- Retain ZCode as a skill installation host: hosting a facilitator skill does not require a ZCode execution backend.
- Publish v0.24.0 after validation and CI.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `zcode-backend`: replace execution contracts with explicit removal and migration behavior.
- `backend-config`: reject the removed machine section and remove its defaults/environment mapping.
- `backend-model-catalog`: exclude ZCode from discovery and reject explicit selectors.
- `backend-installation`: remove ZCode prerequisites and probes.

## Impact

Adapter factory, configuration validation, machine settings, discovery targets, installation metadata, tests, README, examples and bundled skill. Old ZCode configurations must be migrated to a supported backend with new native sessions; stored artifacts are not deleted or converted. No replacement desktop automation or authentication integration is introduced.
