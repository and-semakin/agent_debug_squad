# Proposal

## Why

ZCode Desktop 3.17.2 requests optional official MCP authentication during ordinary session startup. Squad currently treats this callback as an unsupported fatal interaction, so even a no-tools ping fails before an assistant reply.

## What Changes

- Reply to official MCP/plugin-command authentication callbacks with the native negative result `official_auth_unavailable` instead of terminating the run.
- Preserve the unsupported authenticated-MCP boundary: do not provide headers, credentials, login, or refresh.
- Cover callbacks during creation and resumed turns, retain fail-fast behavior for unknown interactions, and verify live ping/pong on the installed runtime.
- Document the compatibility behavior and publish a verified patch release.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `zcode-backend`: optional official MCP auth unavailability is nonfatal to ordinary conversations.

## Impact

The Go reverse-request dispatcher, adapter regression tests, and ZCode documentation change. No CLI, YAML, HTTP, persisted-state migration, or new dependency is needed. Runtime native MCP behavior after a negative auth result remains owned by ZCode.
