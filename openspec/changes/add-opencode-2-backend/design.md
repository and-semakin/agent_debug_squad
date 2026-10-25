# Design

## Context

See proposal.md for motivation. The existing internal/adapters/opencode package implements classic HTTP/SSE, owns its process through Runtime, verifies effective config and directory, and implements AgentAdapter and PermissionReplier. The orchestrator stores one concrete OpenCode runtime and special-cases deferred initialization/readiness. Config, preflight links and catalog selectors also hard-code opencode. These integration points must become generation-aware; changing only the adapter factory is insufficient.

Observed locally on 2026-10-25: installed CLI reports v2.1.4; serve accepts hostname, port, cors, service and stdio, but does not list the v1 mdns flag. No inference or user-service mutation was performed during planning.

Primary evidence:
- https://opencode.ai/v2/docs/migrate-v1 — server/client contracts changed; both versions now use opencode and share config locations.
- https://opencode.ai/v2/docs/build/client/ — live-only events without replay; explicit location and separate service management.
- https://opencode.ai/v2/openapi.json — inspected server.info, session creation/prompt/interrupt, config, events and permissions.

The current online schema is not itself proof of the installed binary's full behavior. Capture versioned, sanitized fixtures against 2.1.4 before treating protocol assumptions as verified.

## Goals / Non-Goals

**Goals:** Preserve the existing Squad interface and ownership guarantees while isolating incompatible wire protocols. Make differences explicit and testable.

**Non-Goals:** No global service discovery/adoption, cross-major session conversion, native plugin migration, JavaScript sidecar, general form answering or automatic protocol selection.

## Decisions

### Separate protocol adapter and runtime

Add internal/adapters/opencode2 implementing AgentAdapter, PermissionReplier and model discovery. Keep v1 implementation in its existing package to minimize regressions; user-facing naming is independent of Go package names. Share small proven transport/process utilities only where semantics truly match. Reject a universal version-switching parser: it risks interpreting v1 events as v2 and broadens legacy regression scope.

Maintain a distinct v2 runtime owner in the orchestrator, with generation-aware deferred initialization and preflight grouping. Model discovery must construct only its own runtime, without accidentally invoking a second readiness owner. Aliases share v1 ownership. Run-scoped permission registries and cancellation contexts stay separate from shared server lifetime.

### Explicit identities and additive migration

Canonical public choices are opencode1 and opencode2. Accept opencode as a legacy v1 alias at configuration, selector and recovered-state boundaries. Preserve existing saved identity strings; use an internal generation resolver rather than rewriting workflow definitions or identity hashes. Equivalent alias targets deduplicate for discovery; all-mode emits canonical names. Reject duplicate opencode/opencode1 machine sections even when values happen to match. Separate command paths allow both installations; never install or upgrade either automatically.

Version probes are bounded and use execution-equivalent environments. V1 rejects v2, v2 rejects v1 before creating sessions. External mode uses the remote server version, never the local CLI. Error text identifies the needed backend/command choice without leaking credentials.

### Native v2 HTTP translation

Use Go net/http and explicit small wire types, not the v1 SDK path named /v2 (which still belongs to the old contract). The inspected API uses /api/info, /api/session, /api/session/{sessionID}/prompt, /interrupt, /api/event and /api/config. Session creation accepts location, agent and model; prompt accepts text and an optional message ID, rather than classic parts/model fields. Resolve agent/model changes through the native session operations before submission. Responses commonly wrap data; location scoping and pagination must follow actual route schemas.

Subscribe before submission and wait for the connected marker. Correlate the submitted message/inbox identity and session log/messages to completion and final text; do not equate a successful submission response or unrelated idle event with turn completion. Read the exact observed assistant message ID directly, avoiding historical-message pagination. Preserve raw attributable events through a redacting sink. Event loss terminates the run conservatively; do not replay uncertain prompts. Startup prompt injection remains first-turn-only.

### Private lifecycle, configuration and authentication

Use an owned serve process, never service ensure/start or a registered personal endpoint. Verify startup announcement, port-zero behavior and authentication against the installed release in an isolated HOME/XDG environment. Do not copy classic flags or assume OPENCODE_SERVER_PASSWORD has the same semantics. Select the native supported credential mechanism and keep ephemeral credentials in memory; external mode uses operator-provided supported credentials. Fail clearly if the chosen release cannot provide authenticated private ownership.

Retain constrained environment, direct Squad transport, redirect rejection, bounded startup, failed-start latch and process-group cleanup. Merge snapshot configuration only in the child environment using the verified native shape; read effective location-scoped config before work. If native snapshots are absent, disabled-equivalence requires evidence; explicit unsupported requests fail instead of being ignored. Audit native startup configuration migration in temporary fixture directories before allowing real config paths. Existing v1 guards remain unchanged.

### Permissions and descendants

Map native Permission.Request action/resources/save/source to Squad permission/patterns/always/tool metadata without fabricating missing fields. The v2 reply route is session-scoped and uses decision, with the same once/always/reject enum. Preserve the current five-second reply bound, serialized resolution, failure visibility and audit records. Verify native always scope; if broader than session-local, reject always with guidance to use once/reject rather than silently persisting broader authorization. Do not implement a global allow policy as YOLO.

Track root/child/nested-child identities from verified native parent relations. Recover pending permission visibility through bounded session-scoped listing where initial event ordering could otherwise lose requests, without adopting unrelated requests. Forms are visible as native activity but are not automatically answered, consistent with the existing adapter's question limitation.

## Risks / Trade-offs

- Experimental API and moving online documentation → record observed version and minimal sanitized fixtures; reject unsupported contracts with safe diagnostics.
- Shared native config locations and migrations → isolated smoke tests, no automatic file conversion, document separate config roots for simultaneous installations.
- Authentication, startup announcement, snapshot and persistent-permission semantics are not proven by CLI help/OpenAPI alone → an explicit compatibility-verification task precedes production adapter integration; unresolved guarantees block the affected behavior rather than being weakened.
- Live-only event loss → fail with uncertainty and preserve identity; never claim exactly-once remote effects.
- Alias changes can alter recovery hashes → preserve persisted raw values and test historical state/workflow recovery.

## Migration Plan

1. Add versioned names and the v1 alias without rewriting existing YAML or state.
2. Add v2 with separate machine settings and version-aware readiness, then document explicit v2 selection.
3. Verify coexistence, ordinary agents, workflow attempts, catalogs and restart recovery; retain v1 fixture coverage.
4. Rollback by restoring the previous binary and v1 configuration. V2-created sessions are not migrated to v1; keep their artifacts intact. The user subsequently authorized a release: choose the next minor version for this additive backend, wait for green main CI, publish an annotated tag, and verify the release archives and embedded version.

## Implementation evidence

The installed 2.1.4 smoke passed private authenticated port-zero startup, schema-less native config preservation, creation/recovery, catalog discovery and two turns using a local fake provider. No paid inference was used. Native config uses the last defined snapshots document, and the supported Basic Auth environment matches v1. Creation in the pinned release returns data directly as Session.Info, unlike the newer online transfer shape. Permission always saves project-wide rules and is rejected by Squad; once/reject preserve the intended authorization scope. See docs/opencode2-compatibility.md and protocol/REST fixtures for the verified mappings.
