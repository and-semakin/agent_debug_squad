## Context

See proposal.md. The OpenCode adapter already consumes /event SSE and submits prompt_async. YOLO currently only logs an unsupported warning. Progress tracks root/task sessions, while orchestrator Wait only wakes on terminal completion. Backend API observed during the incident exposes permission.asked / permission.replied and POST /permission/{requestID}/reply with once/always/reject.

## Goals / Non-Goals

Goals: add permission control without resetting sessions, changing global backend policy, or altering the mandatory adapter interface. Keep existing run status values.
Non-goals: permission handling for CLI backends, answering OpenCode questions, reconnecting broken SSE streams, and new global configuration flags.

## Decisions

- Use an optional run-scoped PermissionReplier adapter interface. The active OpenCode stream owns the permission registry, not persisted state. Orchestrator checks active run ownership; the adapter checks pending request ownership again. Resolved IDs are retained until the run ends to suppress duplicate events.
- Consume permission.asked/replied SSE for the root and known descendant sessions. Preserve metadata and tool identifiers. Unknown sessions are ignored, as are requests explicitly tied to a different known root turn. No global permission allow configuration is installed. Automatic replies use once, avoiding backend-wide or lasting policy changes.
- Serialize replies per active run, bound HTTP calls, tie them to both caller and run cancellation, and clear the registry on exit. A matching permission.replied event is authoritative when OpenCode ends the SSE stream before returning the reply HTTP response. Serialize tracker updates and deep-copy JSON metadata/progress to avoid races. Automatic replies execute separately from the SSE reader so a slow reply cannot hide cancellation or other requests.
- Add pending_permissions to progress and let waiting_for_permission override other active phases. Automatic requests carry auto_approving until the reply resolves; only manual waits or failed automatic replies wake callers. Record permission decision events in run events, including decision source and errors.
- Poll local progress in Wait at 100 ms while retaining its completion channel. This avoids changing ownership of channels closed by workers and supports waits registered before the active sink exists. It polls local state, never OpenCode.
- Return 400 for invalid bodies/replies, 404 for unknown runs/requests, 409 for inactive runs or unsupported backends, and 502 for backend reply failures. Successful replies return 200. Duplicate or already resolved requests are not silently approved again.

## Risks / Trade-offs

- SSE events are backend-version-specific → fixture-based tests plus a local installed-server smoke check; describe supported classic HTTP/SSE API.
- Auto approval is broader than historical OpenCode behavior → document that existing YOLO defaults now apply; yolo false preserves manual approval.
- Requests made before a tracked descendant is discovered cannot safely be attributed → ignore unknown sessions; no global permission polling/approval.
- A timed-out reply may have been accepted remotely → do not retry automatically; later replied events clear it, and manual retry errors remain explicit.
- Existing worktree contains OpenSpec setup edits → preserve and inspect them before release; do not include unrelated content or runtime artifacts.

## Migration Plan

No persisted-state migration is required: new fields are optional and older progress files remain readable. Restart squad on the new binary to apply behavior; active runs retain existing interrupted-on-restart semantics. Rollback restores the previous adapter behavior. Publish the next minor release after strict validation, required Go checks, green main CI, and archive verification.
