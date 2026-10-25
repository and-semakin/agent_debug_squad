# Proposal

## Why

The installed OpenCode 2.1.4 uses a different server contract from the existing classic OpenCode adapter. Explicit versioned backends are needed to support both generations without silently changing existing configurations or saved conversations.

## What Changes

- Introduce OpenCode 2 (`opencode2`) as a separate Go HTTP/event adapter with managed and external runtimes.
- Name the existing backend OpenCode 1 (`opencode1`), retaining `opencode` as a compatible v1 alias, including saved state and machine settings. Reject simultaneous legacy and explicit v1 machine sections instead of guessing precedence.
- Preserve session continuity, startup prompts, model/agent selection, streamed artifacts, cancellation/reset, descendant activity and run-scoped permissions as far as the verified v2 contract allows.
- Preserve constrained environments, proxy handling, snapshot expectations, authenticated owned runtimes and conservative failure handling. Never adopt the user's background service implicitly.
- Add version-aware installation/readiness and model discovery, documentation, migration examples and isolated real-binary smoke coverage.
- Do not migrate conversations, rewrite native configuration, port plugins or add general form answering. Publish a release after verification, as explicitly requested by the user.

## Capabilities

### New Capabilities

- `opencode2-backend`: OpenCode 2 execution, ownership, protocol translation, continuity, permissions and compatibility limits.

### Modified Capabilities
- `backend-installation`: Generation-aware readiness/version validation with separate bounded installation and service phases.

- `backend-config`: Explicit v1/v2 identities and independent machine settings with legacy compatibility.
- `backend-model-catalog`: Discover both generations without duplicate alias targets and use the native v2 catalog.
- `opencode-runtime`: Delimit classic v1 protocol requirements and preserve runtime ownership per generation.

## Impact

Touches internal/adapters, internal/config, internal/domain, internal/orchestrator, model discovery and CLI/help documentation. Existing REST run and permission shapes remain stable. Legacy persisted backend names remain readable and are never interpreted as v2. New configuration identifiers are additive. Default executable names cannot distinguish versions because both upstream versions use `opencode`; explicit command paths and major-version checks resolve this. No JavaScript runtime dependency is proposed.
