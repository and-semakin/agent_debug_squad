## Purpose

Coordinate persistent ZCode conversations through Squad while making runtime compatibility, permissions, and descendant activity observable.

## ADDED Requirements

### Requirement: Explicit compatible configuration
The system SHALL accept backend zcode with executable/runtime, provider/model/reasoning, and explicit environment options. It SHALL use Flash with low reasoning by default, validate supported runtime compatibility before using native account credentials, and fail clearly for unavailable or ambiguous credentials. Credentials MUST NOT appear in run artifacts or persisted state.

#### Scenario: Compatible signed-in account
- **WHEN** a run starts with a supported installed runtime and one available supported account
- **THEN** it starts an authenticated App Server conversation with the configured selection and inherited environment allowlist

#### Scenario: Unsupported installation
- **WHEN** the runtime fingerprint is unknown or the supported account is unavailable
- **THEN** the run fails with an actionable error before sending a model prompt

### Requirement: Persistent explicit turns
The system SHALL persist the backend session ID, resume it on subsequent turns and recovery, include startup instructions only in a new conversation, and reset to a new conversation without deleting old history. Acceptance alone SHALL NOT complete a run. Success SHALL require the owned turn's completion and new assistant response text; failure, protocol loss, and cancellation SHALL remain distinct from success.

#### Scenario: Second turn
- **WHEN** a second run uses an agent with an existing backend session
- **THEN** it resumes that session, passes the full model selection, and returns only the new assistant response

#### Scenario: Interrupted transport
- **WHEN** the process exits before an owned completion event
- **THEN** the run fails and retains any known session ID for recovery

#### Scenario: Reset
- **WHEN** an agent is reset
- **THEN** the next run creates a new session and sends startup instructions again

### Requirement: Permission control
Effective YOLO SHALL select the native yolo mode; false SHALL select build mode. Manual tool requests SHALL appear as pending permissions with waiting_for_permission phase and be resolvable through the existing run-scoped permission endpoint. Replies SHALL honor the offered backend decisions and SHALL NOT answer interactive questions or approve foreign, duplicate, or inactive requests.

#### Scenario: Manual tool approval
- **WHEN** YOLO is false and an owned tool requests approval
- **THEN** the coordinator sees its metadata and can send once, reject, or always if the backend offers it

#### Scenario: Stale approval
- **WHEN** a reply targets a resolved request or a completed/cancelled run
- **THEN** it is rejected without sending another backend decision

### Requirement: Observable bounded work
The system SHALL stream non-secret execution events, expose owned subagent status while running, and make cancellation/force reset stop the owned turn and clean up its processes within a bounded shutdown period. Background work SHALL NOT outlive its owning Squad turn by design. Unsupported host interactions SHALL fail explicitly rather than hang indefinitely.

#### Scenario: Child activity
- **WHEN** an owned session spawns a child
- **THEN** its child session ID and status appear in run progress independently of parent text output

#### Scenario: Cancellation with children
- **WHEN** the run is cancelled while a descendant or permission request is active
- **THEN** owned runtime work is stopped, stale replies are inactive, and the run does not report success

#### Scenario: Unsupported question
- **WHEN** ZCode requests an interactive questionnaire that Squad cannot represent
- **THEN** the run fails with an explicit unsupported-interaction error without fabricating an answer

### Requirement: Documented host boundaries
Documentation SHALL describe model selection and discovery, proxy variables, runtime/account support, workspace preference effects, desktop project import, and the absence of any promised billing discount.

#### Scenario: Configure a Flash agent
- **WHEN** a user follows the provided example
- **THEN** they can configure an authenticated Flash agent without placing a credential value in YAML
