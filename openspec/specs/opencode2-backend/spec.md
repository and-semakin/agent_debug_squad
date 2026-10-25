# opencode2-backend Specification

## Purpose

Integrate the OpenCode 2 server protocol while preserving Squad session ownership, observable progress and controlled execution.

## Requirements

### Requirement: Managed and external runtime ownership
OpenCode 2 SHALL support managed and external modes. Managed mode SHALL launch an authenticated loopback-only private server, await bounded readiness, share it across agents and reap only owned resources. Failed initial startup SHALL latch until Squad restart. Successful startup SHALL survive individual run completion or cancellation. External mode SHALL never start or stop a server.

#### Scenario: Concurrent admission and startup failure
- **WHEN** concurrent agents encounter a failed first managed startup
- **THEN** all fail without duplicate launches or automatic retries and receive restart guidance

#### Scenario: Personal background service
- **WHEN** a user-managed OpenCode service already exists
- **THEN** managed Squad starts a distinct owned instance and shutdown leaves the personal service intact

### Requirement: Configuration and transport safeguards
V2 SHALL constrain child environments and redact credentials under existing runtime privacy rules. Squad HTTP SHALL bypass proxies and reject redirects. Snapshot expectations SHALL default false and be verified for the selected location before creation, recovery and prompts; unavailable or conflicting settings SHALL fail closed. Native user/project config SHALL NOT be rewritten by Squad to enforce settings.

#### Scenario: Unsupported snapshot inspection
- **WHEN** the server cannot establish the effective requested snapshot behavior
- **THEN** work fails before submission with a compatibility diagnostic rather than silently ignoring the setting

### Requirement: Version and workspace verification
V2 SHALL verify major-version compatibility, authenticated access and the explicit workspace before creating work. Recovery SHALL require a saved v2 identity and a matching canonical local workspace. Missing sessions, incompatible servers or different roots SHALL fail without creating replacement conversations or replaying work.

#### Scenario: Cross-generation recovery
- **WHEN** a v1 identity is presented as a v2 continuation
- **THEN** recovery rejects it without importing or replaying the conversation

### Requirement: Multi-turn execution and correlated results
V2 SHALL preserve startup prompts, explicit native agent/model selection, timeouts and session continuity. It SHALL subscribe before submission, stream attributable events and return only the current turn's assistant output after confirmed completion. Manual turns SHALL reuse sessions; fresh workflow attempts SHALL use separate sessions. Accepted or uncertain prompts SHALL NOT be automatically resubmitted.

#### Scenario: Two turns with unrelated events
- **WHEN** a second turn executes while older or unrelated session events arrive
- **THEN** only the second turn determines completion and final text, and the startup prompt is not duplicated

#### Scenario: Event stream fails after submission
- **WHEN** the live stream disconnects before confirmed completion
- **THEN** the run reports failure or uncertainty without replaying its prompt or claiming success

### Requirement: Descendant progress visibility
V2 SHALL track verified child and nested-child relationships for the active run, publish subagents and child activity, and represent blocking child work as waiting_for_subagent. Unrelated sessions SHALL NOT update the run. Permission waits SHALL take precedence over child waits.

#### Scenario: Nested child activity
- **WHEN** a verified nested child emits progress
- **THEN** its activity and parent run liveness advance without attributing unrelated sessions

### Requirement: Permission parity
V2 SHALL implement the existing opencode-permissions contract through its native API, including once, always, reject, optional messages, audit records, manual replies and bounded automatic once approval. Only owned requests SHALL be actionable. Native questions/forms SHALL NOT be automatically answered. If native always has broader scope than session-local approval, Squad SHALL reject it with an actionable diagnostic.

#### Scenario: Manual permission failure
- **WHEN** an automatic once reply fails
- **THEN** the pending request remains visible for manual intervention without an unbounded retry loop

#### Scenario: Different persistent permission scope
- **WHEN** native always would persist permission beyond the intended session
- **THEN** Squad sends no broader approval and explains that once or reject remains available

### Requirement: Cancellation and reset isolation
Cancellation SHALL interrupt only the owned active session and verified owned descendants with bounded cleanup. Reset SHALL invalidate pending approvals and defer creation of the replacement conversation. It SHALL preserve shared runtimes and other agents. Failed interruption SHALL NOT be represented as proof that remote side effects stopped.

#### Scenario: Reset one concurrent agent
- **WHEN** one of two running agents is force-reset
- **THEN** its old approvals become inactive, its run is interrupted and the other agent continues
