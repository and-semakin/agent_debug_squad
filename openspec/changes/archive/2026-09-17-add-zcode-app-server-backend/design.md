## Context

See proposal.md for motivation. The installed desktop is ZCode 3.12.3 with runtime 0.16.5. Its App Server speaks bidirectional JSON lines, returns acceptance before turn completion, and delegates credentials and runtime preferences to its host. Runtime version alone does not identify the private host API.

## Goals / Non-Goals

Use existing AgentAdapter, RunProgress, and PermissionReplier interfaces. Keep Go in charge of run lifecycle and Node responsible only for bootstrap/authentication and protocol relay. Support the tested Z.AI individual Coding Plan account; do not invent subscription entitlements or promise billing discounts. Browser, interactive questionnaires, login/refresh, and detached work surviving a Squad turn are outside this change.

## Decisions

- Embed a small Node bridge and launch it with `node -e`; no installed helper files or npm dependencies. The bridge verifies the runtime SHA-256 before loading its native credential reader and registry in memory with CLI autorun disabled. It never modifies the installed bundle. A strict fingerprint is preferred to copying credential encryption code or guessing minified symbols after upgrades. Unknown builds fail with actionable compatibility errors.
- Derive bundled config from runtime location and personal config from HOME unless explicitly overridden. Check cached entitlement and require exactly one account credential for the supported provider. Native credential values remain in the bridge, are redacted from forwarded output, and never enter Go state, argv, configuration, or diagnostics. Only the matching model-request auth callback receives the credential; unsupported challenge flows fail explicitly.
- Start a private process per Send, resume the persisted session, subscribe before sending, and include provider/model/reasoning selection on every send. Apply build or yolo explicitly each turn; do not expose the misleading legacy plan/auto modes. Disable title generation. Startup prompt is included only for a new backend conversation. Recover preserves the session ID; reset clears it without deleting ZCode history.
- A concurrent reader dispatches responses to bounded RPC calls and queues events/callbacks for the run loop. EOF, malformed/oversized input, unknown callbacks, turn failure, and cancellation cannot count as success. A terminal event correlated through the submitted input ID and turn ID plus its assistant response text is required. Historical or foreign completion events cannot supply the next result.
- Use native YOLO mode. With YOLO off, publish tool permission callbacks through existing run progress. Reply once/always/reject using the backend's offered response objects; reject unsupported always responses. Requests and reply channels are scoped to one active Send and duplicate/stale replies fail. Unknown user-input callbacks fail explicitly rather than inventing answers.
- Poll session/subagents while running and combine with streamed events. Track only descendants of the owned session. Background children are bounded by the owning turn: close/stop owned runtime work on return, cancellation, or force reset. Use bounded graceful shutdown followed by process-group termination on supported macOS/Linux platforms.
- Preserve explicit inherit_env/env behavior. Document ZCODE_HTTP_PROXY, ZCODE_NO_PROXY, and ZCODE_AGENT_CA_CERT; do not implicitly pass the entire parent environment.

## Risks / Trade-offs

- Private runtime API changes → fingerprint guard and deterministic protocol tests; update support only after revalidation.
- Desktop and Squad can open the same database → do not edit database rows; document exclusive ownership of an active conversation and normal project import for visibility.
- Runtime mode also updates workspace preferences → document that YOLO selection is visible to ZCode for this workspace.
- Credential refresh/captcha may be required → explicit error instructs user to sign in through ZCode, with no silent fallback to another account/model.
- Starting a process each turn adds latency → buys isolated ownership and deterministic cleanup without adding a daemon lifecycle to AgentAdapter.

## Migration Plan

Additive configuration only. Existing backends and persisted states retain behavior. Publish a minor release after strict spec validation, formatting, vet, race tests, build, bounded Flash smoke tests, and green main CI. Rollback selects an earlier binary; ZCode conversation history remains in its own store.
