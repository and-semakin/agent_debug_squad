# Proposal

## Why

ZCode runs fail before session creation because Squad sends `basedOnZcodeBuiltinRevision` while the native account snapshot parser expects `basedOnZCodeBuiltinRevision`. The mismatch was reproduced with the installed 3.14.4 parser and independently confirmed by static inspection of the reported 3.15.3 bundle.

## What Changes

- Correct the native revision field in local registry account snapshots and App Server account overlays.
- Add executable host contract coverage that rejects the misspelled field and verifies both paths with a native-shaped parser/runtime fixture.
- Document the patch release and clarify that the unsupported read-only model catalog does not diagnose this failure.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `zcode-backend`: explicitly require the case-sensitive native account revision field for both registry and App Server account configuration.

## Impact

Changes are limited to the ZCode host bridge, its tests, OpenSpec artifacts and patch release notes. No CLI, YAML, public HTTP or persisted-state migration is required. Account identity, model selection policy, credential handling and fail-closed behavior remain in place. No model catalog implementation or broader runtime compatibility changes are included.
