## Why

OpenCode currently requires an independently operated HTTP server; Squad cannot reliably set provider networking or disable expensive snapshots before work starts. A Squad-owned server makes these settings enforceable without changing user files or another process.

## What Changes

- **BREAKING**: Default to managed OpenCode; explicit existing `base_url` configurations require `mode: external`.
- **BREAKING**: External mode requires the same workspace path/filesystem view; saved session directories are checked in both modes. HTTP redirects are rejected; configure the final endpoint.
- Own and reuse one loopback `opencode serve` process per Squad in its fixed workspace, independent of agent model and workflow attempts.
- Add machine-level mode, command, proxy_url, no_proxy, inherit_env and snapshot settings. Reject per-agent process overrides and incompatible external settings.
- Apply snapshot=false by default through merged runtime config, and verify effective configuration before session operations and prompts. External mode verifies the operator's configuration without changing it.
- Preflight known configuration locations to reject schema-less or legacy files that OpenCode 1.18.30 would rewrite on load, without editing them.
- Bound readiness and teardown, preserve saved session identities, and never automatically restart/replay after child failure.

## Capabilities

### New Capabilities
- `opencode-runtime`: Process ownership, readiness, effective snapshot enforcement, recovery and lifecycle.

### Modified Capabilities
- `backend-config`: OpenCode configuration schema, proxy/environment policy and migration.

## Impact

Changes affect config loading, the OpenCode adapter, orchestrator adapter creation for manual/workflow agents, CLI teardown, documentation and tests. No REST or persisted-state schema changes; runtime credentials/settings stay out of persisted agent options. No release, personal config changes, workflow one-shot mode, judge threshold or graph changes.
