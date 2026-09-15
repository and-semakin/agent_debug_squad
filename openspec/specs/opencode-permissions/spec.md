# OpenCode Permissions Specification

## Purpose

Make OpenCode permission waits observable and resolvable through the squad API while honoring configured automatic approval.

## Requirements

### Requirement: Effective YOLO controls automatic approval
The system SHALL automatically reply once to permission requests from an active OpenCode run or its tracked descendant sessions when effective YOLO is true. It MUST NOT approve unrelated sessions, override explicit backend denials, or automatically answer questions. With YOLO false it SHALL leave requests for external resolution.

#### Scenario: Automatic approval
- **WHEN** an owned permission request arrives and effective YOLO is true, including inherited defaults
- **THEN** the backend receives one automatic once reply and the decision is recorded in run artifacts

#### Scenario: Isolation and duplicate delivery
- **WHEN** an unrelated request or duplicate delivery of an already handled request arrives
- **THEN** no additional automatic reply is sent

#### Scenario: Manual mode
- **WHEN** effective YOLO is false and an owned request arrives
- **THEN** no automatic reply is sent

### Requirement: Pending permissions are visible
Active run responses SHALL expose pending_permissions with request ID, backend session ID, permission, patterns, metadata, suggested always patterns, tool identifiers when present, and first observation time. Their phase SHALL be waiting_for_permission while requests remain; this takes precedence over waiting_for_subagent. A failed automatic reply SHALL leave the request visible with an error and SHALL NOT enter an unbounded retry loop.

#### Scenario: Several blocked tools
- **WHEN** three requests are pending and one is resolved
- **THEN** two remain visible and the phase remains waiting_for_permission

#### Scenario: Resolution and failure
- **WHEN** all requests are resolved
- **THEN** pending permissions clear and progress resumes its underlying running or waiting_for_subagent phase

#### Scenario: Automatic reply failure
- **WHEN** an automatic backend reply fails or times out
- **THEN** the pending request remains visible with auto_approve_error for coordinator intervention

### Requirement: Coordinator can reply through a run-scoped API
The system SHALL accept POST /runs/{run_id}/permissions/{request_id}/reply with reply once, always, or reject and optional message. It SHALL route only currently pending requests belonging to that active run, audit accepted decisions, and reject invalid replies, missing runs/requests, inactive runs, or unsupported backends without approving other requests. Backend failures SHALL be reported to the caller without claiming success.

#### Scenario: Accepted manual reply
- **WHEN** a coordinator submits a valid reply to an active pending request
- **THEN** the backend receives that reply, the API returns 200, and the resolved request is removed without starting a new run

#### Scenario: Invalid or stale reply
- **WHEN** a caller supplies an invalid reply, a foreign request, or a request from a completed/reset run
- **THEN** the API returns a 4xx response and does not send an approval

#### Scenario: Concurrent replies
- **WHEN** two callers attempt to resolve the same request concurrently
- **THEN** at most one successful backend reply is submitted through squad

### Requirement: Waiting callers can observe intervention
Both create-run and GET-run long polls SHALL return promptly, within one second of local publication under normal operation, when pending permissions require intervention. HTTP status and run lifecycle status SHALL retain existing semantics; a permission wait remains an active running run.

#### Scenario: Blocked long poll
- **WHEN** a manual request or automatic reply failure appears during a long poll
- **THEN** the caller receives the current pending details without waiting for the requested timeout

### Requirement: Permission control follows run lifecycle
Permission handling SHALL stop with run cancellation/completion, bound backend reply requests, and preserve force reset and session continuity. Restarted interrupted runs SHALL not expose actionable stale approvals.

#### Scenario: Reset during approval
- **WHEN** force reset cancels a run with a pending or in-flight approval
- **THEN** approval work is cancelled, the run becomes interrupted, and replies to the old run cannot affect the new session
