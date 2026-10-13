## Why

Users cannot currently include Cognition's official local Devin CLI in a Squad or workflow. Its documented ACP interface and locally confirmed session-loading capability make an adapter feasible without replacing the shared local workspace with a cloud VM.

## What Changes

- Add a local-only `devin` backend using owned `devin acp` subprocesses, independent persisted conversations, streamed turn results, bounded cancellation and conservative recovery.
- Reuse existing installation admission, model catalog, run permissions, artifacts and workflow ownership contracts. Add Devin registration and machine settings rather than another preflight or runtime framework.
- Expose executable/model/environment selection and existing YOLO control; support native permission requests through the current endpoint. Defer reasoning/speed/Fusion overrides and sandbox promises until their concrete protocol behavior is verified.
- Discover account model choices through `devin models list --format json` only after its output and non-mutating lifecycle are verified; otherwise return explicit unsupported evidence without creating a session.
- Document official installation/authentication, billing boundaries, configuration imports, version evidence and release-blocking compatibility checks. This proposal is sufficiently grounded to plan implementation, but is not a claim of tested end-to-end operation.
- Exclude cloud relay/handoff, API clients named Devin, automatic login/install/update, native configuration writes, automatic prompt retries and unattended background work surviving a turn.

## Capabilities

### New Capabilities

- `devin-backend`: Local ACP execution, persistent session ownership, permissions, failure semantics and lifecycle boundaries.

### Modified Capabilities

- `backend-config`: Accept Devin machine settings and define its constrained environment and network mapping.
- `backend-installation`: Add Devin's effective executable prerequisites and official installation link without executing the CLI during preflight.
- `backend-model-catalog`: Include Devin in real-backend discovery and define safe account-catalog projection.

## Impact

Expected implementation areas: `internal/adapters/devin`, adapter/model-lister factories, `internal/config`, `internal/domain/backend_config.go`, `internal/preflight`, `internal/modeldiscovery`, and integration tests in orchestrator/workflow. Reuse `internal/procgroup`, `internal/modelprobe`, `promptfmt` and existing permission/result types. No new REST route, cloud service or mandatory third-party Go dependency is planned.

YAML gains a backend and machine section; `models --all` gains another result and may consequently report degradation when Devin is absent. Existing backend configuration and workflow schemas remain unchanged. Devin uses existing backend session IDs and run artifacts; authenticated compatibility checks must confirm that the existing persistence boundary is sufficient before shipping. Code, user configuration, installed tools and release tags are outside this planning change.


## Implementation findings (2026-10-13)

Small live tests were explicitly authorized and executed from fresh main. Empty sessions require bounded process retention across Init/persistence/first Send. The tested CLI does not advertise Normal ACP mode, so yolo:false fails before prompting rather than silently substituting Code. Native JSON catalog variants are informational where an exact ACP selection encoding is not established; discovery never starts a session to resolve them. See design.md for versioned observations, synthetic coverage and supported-profile limits.
