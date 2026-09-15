## Why

OpenCode runs can silently wait for permission while squad reports running, even with YOLO enabled. Facilitators currently have to discover and call backend-specific endpoints to unblock a review.

## What Changes

- Honor effective OpenCode YOLO by replying once to pending permission requests belonging to the active run's session or tracked descendants; audit decisions.
- Expose pending requests and waiting_for_permission progress, including automatic reply failures, through existing run responses.
- Add a run-scoped permission reply endpoint and wake long-poll callers when intervention is needed.
- Preserve existing run statuses, session continuity, timeout/cancellation behavior and other backends. No global OpenCode configuration changes or question-answer automation.

## Capabilities

### New Capabilities

- `opencode-permissions`: Automatic permission approval, visible pending requests, and coordinator replies.

### Modified Capabilities

None; the main spec inventory is currently empty.

## Impact

Changes touch domain progress, the OpenCode HTTP/SSE adapter, orchestrator wait/reply handling, API, tests and documentation. YAML and CLI retain their existing shape; JSON gains optional permission fields and a progress phase. Existing YOLO defaults now take effect for OpenCode. A new additive HTTP endpoint supports manual replies. No new dependencies.
