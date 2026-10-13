# Proposal

## Why

Devin agents need reproducible model, reasoning, speed and mode choices rather than inheriting opaque native session defaults. Machine proxy defaults must make account-backed execution usable without repeating environment options in every squad.

## What Changes

- **BREAKING**: require a nonblank explicit `options.model` for configured Devin agents; session-free model discovery without an agent remains available.
- Add optional `reasoning`, `speed` (default standard), and `mode` (default bypass). Choose high reasoning when offered, otherwise retain the advertised native default when reasoning is omitted.
- Keep `yolo` (default true) as a compatibility selector; resolve explicit mode/yolo conflicts before dispatch.
- Apply and verify controls for new and loaded conversations, with no prompt on unsupported selections.
- Preserve command/env/inherit_env and existing machine proxy_url/no_proxy support. Document and configure this machine's proxy after publishing.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `devin-backend`: required model and verified reasoning/speed/mode defaults and compatibility.

## Impact

Changes affect Devin adapter validation, ACP negotiation, config tests, examples and docs. Existing Devin YAML without a model must add one. Other backends and stored session identity semantics remain unchanged. No Fusion, sandbox, hooks, plugins, MCP, implicit fallback or arbitrary CLI arguments are added. Release remains v0.21.0 because the initial Devin PR has not yet been merged or published.
