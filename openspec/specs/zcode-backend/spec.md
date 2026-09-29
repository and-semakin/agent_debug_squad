# ZCode Backend Specification

## Purpose

Coordinate persistent ZCode conversations through Squad while making runtime compatibility, permissions, and descendant activity observable.

## Requirements

### Requirement: Explicit compatible configuration
The system SHALL accept backend zcode with executable/runtime, provider/model/reasoning, and explicit environment options. It SHALL NOT accept a `plan_policy` option: a configuration that sets `plan_policy` to any value SHALL fail validation before preflight or run allocation with an error that names `plan_policy` as removed. The provider option SHALL be limited to `account:zai-individual-coding-plan` or omitted, and an omitted provider SHALL mean `account:zai-individual-coding-plan`. It SHALL NOT introduce a personal API provider path, a Start Plan or other account-routing option, direct/native integration option or separate backend. It SHALL use Flash with low reasoning by default, decide installed runtime compatibility by probing the bundle for the host structures the bridge requires before loading it or using native account credentials, and fail clearly for unavailable or ambiguous credentials. Compatibility SHALL NOT depend on a pinned bundle fingerprint or an exact runtime version: any installed ZCode desktop 3.x bundle whose required structures are located unambiguously SHALL pass the private-extraction compatibility gate, while execution SHALL additionally satisfy the runtime wire checks. A bundle whose required structures are missing or ambiguous MUST NOT be loaded. Credentials MUST NOT appear in run artifacts or persisted state. The executable (`command`) and `runtime_path` MAY default from the machine backend configuration when the agent sets none, ZCode proxy settings from the machine backend configuration (`ZCODE_HTTP_PROXY`, `ZCODE_NO_PROXY`, `ZCODE_AGENT_CA_CERT`) SHALL be injected into the host process environment as defaults below explicit agent environment entries, and machine `inherit_env` SHALL union with the agent's own `options.inherit_env`, under the precedence rules of the backend-config capability.

#### Scenario: Compatible signed-in account
- **WHEN** a run starts with an installed runtime whose required structures are located unambiguously and one available supported account
- **THEN** it starts an authenticated App Server conversation with the configured selection and inherited environment allowlist

#### Scenario: Desktop update within the supported line
- **WHEN** the installed ZCode desktop is updated to a newer 3.x bundle that keeps the required structures discoverable
- **THEN** runs continue to start without a new Squad release, a fingerprint update, or configuration changes

#### Scenario: Unsupported installation
- **WHEN** required runtime structures are missing or ambiguous, or the supported account is unavailable
- **THEN** the run fails with an actionable error before a model prompt is sent, the error includes the bundle fingerprint for reporting, and an unusable bundle is never loaded

#### Scenario: Machine-level defaults apply to ZCode
- **WHEN** the machine backend configuration sets `zcode.command`, `zcode.runtime_path`, and `zcode.proxy_url`, and the agent sets no `command`, `runtime_path`, or ZCode proxy environment entries
- **THEN** the host process runs with the machine-configured Node executable and runtime bundle, and its environment contains `ZCODE_HTTP_PROXY` from the machine configuration

#### Scenario: Explicit ZCode agent environment wins
- **WHEN** the machine backend configuration sets `zcode.proxy_url` and the agent defines `ZCODE_HTTP_PROXY` in `options.env`
- **THEN** the host process receives the agent's `ZCODE_HTTP_PROXY` value and the machine value is ignored

#### Scenario: Removed plan policy option is rejected
- **WHEN** YAML or direct adapter configuration sets `plan_policy` to any value, including `fixed`
- **THEN** validation fails before preflight or run allocation with an error naming `plan_policy` as a removed option, and no run starts

#### Scenario: Foreign provider is rejected
- **WHEN** a ZCode agent sets `provider` to any value other than `account:zai-individual-coding-plan`
- **THEN** validation fails before preflight or run allocation with an error stating the supported provider
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

### Requirement: Wire compatibility is established at the correct boundary
The system SHALL distinguish local installation, legacy private extraction and runtime wire compatibility. Runtime checks SHALL be bounded to one fifteen-second startup budget and SHALL NOT create/resume sessions, send prompts or invoke connectivity/model tools. The supported execution dialect SHALL be the inspected legacy session NDJSON contract; a desktop version number, runtime/capabilities independentPlanState flag, MCP protocolVersion or v4 hello type SHALL NOT negotiate or certify that contract. Missing required methods, unexpected success on verified invalid-parameter probes or invalid critical response fields SHALL fail as incompatible. Unknown additive fields SHALL NOT alone fail compatibility. Subsequent session and event responses SHALL still be validated; an initial check SHALL NOT imply future semantic correctness.

#### Scenario: Capability flag without required methods
- **WHEN** runtime/capabilities returns independentPlanState but a required session operation returns method-not-found
- **THEN** startup fails as incompatible before creating or resuming a conversation or sending a prompt

#### Scenario: Version label is misleading
- **WHEN** an installed runtime is labelled 3.14.3 but sends v4 frames where legacy session events are required
- **THEN** the adapter reports a dialect incompatibility rather than mixing event schemas or reporting success

#### Scenario: Additive source evolution
- **WHEN** a required response retains all valid critical fields and adds unknown optional fields
- **THEN** the adapter accepts the known contract without requiring a new bundle fingerprint

### Requirement: Host interactions remain responsive and owned
The system SHALL service reverse requests while any ordinary RPC is pending, including create/resume. Preference responses SHALL explicitly disable native search enhancements, memory and automatic question resolution. A bootstrap preference request SHALL be bound to the outstanding create/resume operation and reconciled with its returned session identity; it SHALL NOT authorize tool or credential access. Requests SHALL be correlated to the current process generation, workspace, root or positively owned descendant and turn where applicable. Unsupported requests SHALL receive a bounded explicit error. Input/output frames SHALL be bounded to 8 MiB and queues SHALL be bounded; overflow SHALL fail visibly rather than discard required control/completion events. RPCs and writes SHALL honor the earlier caller deadline and fifteen-second RPC bound.

#### Scenario: Preferences arrive before create returns
- **WHEN** the runtime waits for runtime preferences during session/create
- **THEN** Squad responds before waiting for the create result, reconciles the session identity and does not deadlock or use automatic-question fallback defaults

#### Scenario: Pending poll and cancellation
- **WHEN** child polling waits on a response and cancellation or an inbound callback arrives
- **THEN** response routing and shutdown remain responsive and do not wait behind the blocked ordinary request

#### Scenario: Foreign or late request
- **WHEN** a permission or auth request belongs to another workspace, historical descendant, old process generation or cancelled request
- **THEN** Squad sends no approval or credential material and rejects or discards the stale request explicitly

#### Scenario: Queue overflow
- **WHEN** a runtime exceeds the bounded event queue while the consumer is blocked
- **THEN** the run fails with an overflow diagnostic and owned cleanup occurs instead of silently dropping events

### Requirement: Account authentication is local and identity bound
The existing guarded host SHALL remain responsible for native credential/registry access. Individual execution SHALL preserve the existing unique-credential identity profile and default selection: exactly one Individual Coding Plan credential key resolvable at bootstrap, with a readable secret; an ambiguous or unreadable credential SHALL fail clearly before a session is created. Execution SHALL NOT require a Start JWT, cross-plan current-account resolution, a subscription-list read, or any other pre-dispatch network read against Z.AI origins; the reachability of `api.z.ai` or `zcode.z.ai` SHALL NOT gate run dispatch. Squad SHALL NOT switch account identity, copy its credential cipher, perform login/refresh, activate offers, redeem resets or rewrite desktop provider settings. Unsupported or expired authentication SHALL fail safely. Account overlays SHALL use the real native registry revision and mark the configured Individual provider entitled and current without fabricating a broader catalog. Overlay receipt SHALL NOT imply model access verification.

Auth requests/cancellations SHALL be scoped to generation/workspace/session/request and selected provider/model. Late results after cancellation SHALL be discarded. Secret material SHALL stay within the private host/runtime boundary; only safe evidence SHALL reach Go/artifacts. Raw provider configuration, billing responses, upstream stderr and arbitrary upstream error bodies SHALL NOT enter public diagnostics. Redaction SHALL cover every secret seen during process lifetime, including rotation.

#### Scenario: Offline account service
- **WHEN** the Z.AI account origins are unreachable but the installed runtime, local credential store, and unique Individual credential are available
- **THEN** the run dispatches normally and never performs a pre-dispatch read against `api.z.ai` or `zcode.z.ai`

#### Scenario: Ambiguous credential fails safely
- **WHEN** the local credential store resolves zero or multiple Individual Coding Plan credential keys
- **THEN** the run fails with an actionable sign-in error before a session is created, without consulting any other stored token

#### Scenario: Auth result arrives after cancellation
- **WHEN** cancellation occurs while the private credential lookup is pending
- **THEN** the late result is discarded and no credential response or secret-bearing diagnostic is emitted
### Requirement: Model selection requires selectable local evidence
Before dispatch, the system SHALL verify the exact configured provider/model/reasoning against the live selectable registry view of the installed runtime, after applying the account evidence that marks the configured Individual provider entitled and current. The verification SHALL be local: it SHALL NOT depend on network reads against Z.AI origins, subscription state, or billing data. A requested model that is absent or disabled, or a requested reasoning level the model does not offer, SHALL fail the run before any model request is sent, without substituting another model or reasoning level. An unreadable or ambiguous registry view SHALL fail closed before dispatch rather than dispatch on unverified availability. Selectability evidence SHALL NOT claim positive remaining quota, subscription freshness, or inference verification.

#### Scenario: Requested model is not selectable
- **WHEN** the live selectable registry view, after applying the account evidence, lacks the exact requested model or lists it disabled
- **THEN** the run fails before dispatch with an actionable error and no substitute selection is sent

#### Scenario: Requested reasoning is not selectable
- **WHEN** the requested model exists in the view but does not offer the requested reasoning level
- **THEN** the run fails before dispatch and no other reasoning level is substituted

#### Scenario: Registry view is unreadable
- **WHEN** the registry view cannot be read or decoded from the installed bundle
- **THEN** the run fails closed before dispatch instead of sending a model request on unverified availability
### Requirement: Turn recovery never replays prompts
The system SHALL correlate acceptance, started and terminal events with the submitted input and owned turn within the active generation, buffer valid early events within bounds, and ignore historical or foreign completions. A contradictory terminal sequence observed before finalization SHALL fail rather than produce multiple completions. Transport loss after a prompt may have been written or accepted SHALL preserve the known session ID and streamed evidence, return an execution-outcome-unknown diagnostic and SHALL NOT automatically resend the prompt or a permission decision. Subsequent explicit turns SHALL resume the saved session without repeating startup instructions. Reset SHALL retain old native history.

#### Scenario: Completion arrives before send acknowledgement
- **WHEN** a correctly correlated started/completed event sequence arrives before the accepted response
- **THEN** bounded buffering preserves it and the owned run completes once after acceptance is established

#### Scenario: Lost acknowledgement
- **WHEN** session/send may have reached the runtime and the connection closes before an authoritative result
- **THEN** Squad reports the unknown outcome, keeps the session ID and does not retry send even with the same inputId
### Requirement: Owned background work and cleanup are explicit
The system SHALL distinguish descendants, background tasks and parent turn completion. Resume ownership baselines SHALL cover nested descendants and all observed pages. Enumeration SHALL detect repeated/cyclic cursors and ancestry cycles and SHALL be bounded to 100 pages and 10,000 unique descendants per observation; incomplete ownership SHALL fail visibly and SHALL NOT authorize unknown child requests. Every final Squad-run terminal path SHALL cancel known owned work, close owned sessions and release the owned process group within a shared five-second cleanup deadline, including blocked writers and an already-exited group leader. A stop acknowledgement alone SHALL NOT establish cleanup completion. Only positively owned work/processes SHALL be targeted. Cleanup failure SHALL be reported and SHALL prevent clean success, while cancellation SHALL remain cancellation despite late completion. No guarantee SHALL be made for deliberately detached external processes outside known ownership and process-group controls.

#### Scenario: Historical grandchild on a later page
- **WHEN** resuming a session whose historical descendant is nested or appears on a later baseline page
- **THEN** that descendant is not claimed as newly owned by the new turn and its permission cannot be approved on that basis

#### Scenario: Parent completed but background bash is active
- **WHEN** an owned turn returns final text while owned background work is still active
- **THEN** Squad retains the text, cancels the owned work and completes cleanup before reporting clean success

#### Scenario: Child holds descriptors after leader exits
- **WHEN** the runtime leader exits while an owned process-group child holds pipes open
- **THEN** cleanup still terminates the owned group within its shared deadline without killing unrelated ZCode processes

#### Scenario: Traversal cannot prove ownership
- **WHEN** descendant traversal repeats cursors, exceeds limits or cannot establish lineage
- **THEN** the run reports incomplete ownership, sends no decision for unknown descendants and performs owned cleanup
