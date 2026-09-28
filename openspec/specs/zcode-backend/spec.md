# ZCode Backend Specification

## Purpose

Coordinate persistent ZCode conversations through Squad while making runtime compatibility, permissions, and descendant activity observable.

## Requirements

### Requirement: Explicit compatible configuration
The system SHALL accept backend zcode with executable/runtime, provider/model/reasoning, and explicit environment options. It SHALL accept plan_policy fixed (default) or start-first; fixed SHALL preserve existing provider/default behavior, while start-first SHALL allow only Z.AI Start/Individual account routing for the requested model under the account-plan policy requirements below. It SHALL NOT introduce a personal API provider path, direct/native integration option or separate backend. It SHALL use Flash with low reasoning by default, decide installed runtime compatibility by probing the bundle for the host structures the bridge requires before loading it or using native account credentials, and fail clearly for unavailable or ambiguous credentials. Compatibility SHALL NOT depend on a pinned bundle fingerprint or an exact runtime version: any installed ZCode desktop 3.x bundle whose required structures are located unambiguously SHALL pass the private-extraction compatibility gate, while execution SHALL additionally satisfy the runtime wire checks. A bundle whose required structures are missing or ambiguous MUST NOT be loaded. Credentials MUST NOT appear in run artifacts or persisted state. The executable (`command`) and `runtime_path` MAY default from the machine backend configuration when the agent sets none, ZCode proxy settings from the machine backend configuration (`ZCODE_HTTP_PROXY`, `ZCODE_NO_PROXY`, `ZCODE_AGENT_CA_CERT`) SHALL be injected into the host process environment as defaults below explicit agent environment entries, and machine `inherit_env` SHALL union with the agent's own `options.inherit_env`, under the precedence rules of the backend-config capability.

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

### Requirement: Account authentication is identity bound
The existing guarded host SHALL remain responsible for native credential/registry access. Fixed Individual execution SHALL preserve the existing unique-credential identity profile and default selection, but SHALL require fresh active Individual entitlement and exact selectable model/reasoning evidence before constructing an available overlay. Cached availability plus a readable key SHALL no longer suffice. Fixed SHALL NOT require Start JWT or cross-plan current-account resolution; both policies SHALL require the runtime wire checks. Start-first SHALL require verified current Z.AI account identity shared by the selected Start JWT and Individual plan credential, not merely the presence of matching key names. Squad SHALL NOT switch account identity, copy its credential cipher, perform login/refresh, activate offers, redeem resets or rewrite desktop provider settings. Unsupported or expired authentication SHALL fail safely. Account overlays SHALL use the real native registry revision and observed entitlement/model restrictions rather than fabricating availability. Overlay receipt SHALL NOT imply model access verification.

Auth requests/cancellations SHALL be scoped to generation/workspace/session/request and selected provider/model. Late results after cancellation SHALL be discarded. Secret material SHALL stay within the private host/runtime boundary; only safe evidence SHALL reach Go/artifacts. Raw provider configuration, billing responses, upstream stderr and arbitrary upstream error bodies SHALL NOT enter public diagnostics. Redaction SHALL cover every secret seen during process lifetime, including rotation.

#### Scenario: Expired Start token
- **WHEN** the current Start token is rejected or cannot be resolved safely
- **THEN** start-first reports auth-required without switching to another account, silently charging Individual or attempting login refresh

#### Scenario: Identity changes during balance lookup
- **WHEN** current account identity changes between eligibility lookup and auth response
- **THEN** the result is invalidated and no cross-account credential or entitlement is applied

#### Scenario: Auth result arrives after cancellation
- **WHEN** cancellation occurs while the private credential lookup is pending
- **THEN** the late result is discarded and no credential response or secret-bearing diagnostic is emitted

### Requirement: Start verification limitations are explicit
The adapter SHALL report unsupported Start CAPTCHA verification as a failed turn without automatically spending Individual quota. Documentation SHALL distinguish successful balance eligibility from successful model execution and SHALL identify Start inference and real quota-exhaustion continuation as unverified until independently demonstrated. The default fixed Individual mode SHALL remain available without Start verification.

#### Scenario: Start requires Desktop CAPTCHA
- **WHEN** an eligible Start model request requires CAPTCHA verification unsupported by the host
- **THEN** the turn fails with a verification requirement, retains the conversation, and does not dispatch an Individual continuation

### Requirement: Turn recovery never implies prompt replay
The system SHALL correlate acceptance, started and terminal events with the submitted input and owned turn within the active generation, buffer valid early events within bounds, and ignore historical or foreign completions. A contradictory terminal sequence observed before finalization SHALL fail rather than produce multiple completions. Transport loss after a prompt may have been written or accepted SHALL preserve the known session ID and streamed evidence, return an execution-outcome-unknown diagnostic and SHALL NOT automatically resend the prompt or a permission decision. Subsequent explicit turns SHALL resume the saved session without repeating startup instructions. Switching plans for a new turn SHALL preserve the existing session and full requested model/reasoning selection without repeating startup instructions. Reset SHALL retain old native history.

#### Scenario: Completion arrives before send acknowledgement
- **WHEN** a correctly correlated started/completed event sequence arrives before the accepted response
- **THEN** bounded buffering preserves it and the owned run completes once after acceptance is established

#### Scenario: Lost acknowledgement
- **WHEN** session/send may have reached the runtime and the connection closes before an authoritative result
- **THEN** Squad reports the unknown outcome, keeps the session ID and does not retry send even with the same inputId

#### Scenario: New turn uses another account plan
- **WHEN** start-first chooses Individual after a previous completed Start turn
- **THEN** the next explicit input uses the same session and same requested model/reasoning through Individual, with no startup prompt replay

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

### Requirement: Model-specific Start-first routing
With plan_policy start-first, the backend SHALL obtain fresh account-bound eligibility before each new Squad turn and SHALL prefer Start only when the exact requested model has active, applicable, spendable allowance. Confirmed model absence, expiry or exhaustion SHALL select Individual only if it supports that same requested model and reasoning. It SHALL NOT replace the requested model, treat a display percentage as authoritative spendability, or infer a full catalog from billing data. Fixed SHALL keep its configured plan without automatic routing.

Eligibility SHALL distinguish remaining, available and reserved amounts, associate buckets with active plan/entitlement instances, respect periods and server time, and reject ambiguous ownership, unsupported units or invalid/missing evidence as unknown. A bounded read SHALL use the native account endpoint/auth context and actual app version, without inference, account mutation or unverified redirects. Private credentials and raw billing data SHALL never be persisted or logged. Temporary reservation pressure, admission busy, auth failure, rate limit, network failure and malformed responses SHALL NOT be treated as exhausted quota. Unknown state SHALL fail safely without silently using Individual. Decisions SHALL include safe effective-provider/reason/timestamp evidence. New promotions or quota replenishment SHALL restore Start priority on subsequent turns.

#### Scenario: Flash has promotional allowance
- **WHEN** Flash is requested and current Start evidence shows an active applicable bucket with positive available quota
- **THEN** the turn uses Flash through Start rather than Individual

#### Scenario: Requested model absent from Start
- **WHEN** GLM-5.3 is requested, current complete eligibility excludes it from Start, and Individual supports it
- **THEN** GLM-5.3 uses Individual without substituting Flash

#### Scenario: Confirmed exhausted allowance
- **WHEN** Start quota for the requested model is confirmed exhausted and Individual supports that selection
- **THEN** the next turn uses Individual in the same conversation

#### Scenario: Reservation is not exhaustion
- **WHEN** Start has positive remaining quota but no available quota because of active reservations
- **THEN** the backend performs only bounded rechecking or reports temporary busy, rather than assuming the plan is exhausted

#### Scenario: Balance cannot be established
- **WHEN** balance lookup fails or returns ambiguous/missing evidence
- **THEN** start-first reports the uncertainty without sending inference through Individual

#### Scenario: A new offer becomes active
- **WHEN** a previous turn used Individual but the next fresh lookup finds active Start allowance for that model
- **THEN** Start regains priority without editing configuration or activating an offer itself

### Requirement: Exhaustion during a turn does not replay external effects
With start-first, an owned Start native turn ending in a confirmed quota-exhaustion failure SHALL trigger at most one automatic continuation through Individual for the same account, model and reasoning when that selection is available. The continuation SHALL be a new native turn in the same backend session and logical Squad run, with a distinct input ID and a short instruction to continue existing work. It SHALL NOT resend the original task or startup instructions, change a running model step, or substitute credentials against the wrong provider endpoint. Fixed-plan execution SHALL NOT perform this fallback.

A verified structured exhaustion classification and known terminal turn outcome SHALL be required. Generic rate limits, admission busy, authentication errors, network errors, unknown transport outcomes or unrecognized text SHALL NOT trigger the transition. Owned old-attempt background work SHALL be stopped before continuation, old permissions/auth replies SHALL become inactive, and late old-turn events SHALL NOT complete the new attempt. If cleanup or Individual eligibility is uncertain, the run SHALL fail with preserved session/artifacts rather than dispatch overlapping work.

Both attempts SHALL share the original run deadline/cancellation. After eligibility and old-attempt cleanup, continuation dispatch SHALL require at least five seconds remaining on a finite original deadline, checked immediately before send; otherwise it SHALL fail with continuation_budget_insufficient without dispatch. A context without a deadline SHALL still honor cancellation. At most one Start-to-Individual transition SHALL occur per Squad run; Individual failure SHALL terminate without another automatic continuation. Final success SHALL require the continuation's owned terminal success; the failed Start attempt and transition SHALL remain visible as safe intermediate diagnostics with partial artifacts retained. This guarantees no adapter-generated task replay, not exactly-once behavior of model-chosen tool actions.

#### Scenario: Quota ends after a file edit
- **WHEN** an owned Start turn has edited a file and ends with confirmed quota exhaustion, and Individual supports that same selection
- **THEN** Squad retains the session and work, stops remaining owned old-attempt activity, and sends one new continuation input through Individual without repeating the original task

#### Scenario: Temporary admission busy
- **WHEN** Start returns a concurrency/admission-busy error such as the source-defined 3010 case
- **THEN** that error alone does not trigger Individual selection or classify the allowance as exhausted

#### Scenario: Connection lost without authoritative failure
- **WHEN** the transport disappears before the native turn outcome is known
- **THEN** no automatic continuation is sent and the existing outcome-unknown behavior preserves session/evidence

#### Scenario: Individual continuation fails
- **WHEN** the one automatic Individual continuation also fails
- **THEN** Squad reports failure with both attempts' evidence and sends no further automatic input

#### Scenario: Cancellation during transition
- **WHEN** cancellation, force reset or the original deadline occurs after Start failure but before continuation dispatch
- **THEN** no continuation is sent and owned final cleanup occurs under the existing bounds

#### Scenario: Late completion from the failed attempt
- **WHEN** an old Start turn event or permission reply arrives after the Individual continuation is bound
- **THEN** it cannot complete the run, authorize a tool or provide authentication for the new turn

### Requirement: Plan policy configuration is validated before admission
Policy type/enum and provider combination SHALL be validated during YAML load before preflight or run allocation, and equivalently for direct adapter construction. Omitted plan_policy SHALL mean fixed, preserving the Individual default. Fixed SHALL retain the existing Individual-only provider restriction. Start-first SHALL accept provider omission or exactly account:zai-start-plan or account:zai-individual-coding-plan; either explicit ID SHALL identify the permitted routing family rather than pin a plan. Other provider IDs SHALL fail. plan_policy SHALL be agent-only; the machine backend schema SHALL NOT accept it.

#### Scenario: Existing configuration omits policy
- **WHEN** a ZCode agent omits plan_policy and provider
- **THEN** it uses fixed Individual selection with the existing model/reasoning defaults and the tightened account/wire safety gates

#### Scenario: Explicit Start provider in fixed mode
- **WHEN** fixed is configured with provider account:zai-start-plan
- **THEN** YAML loading and direct adapter construction reject the combination before runtime launch or run admission

#### Scenario: Foreign provider in start-first
- **WHEN** YAML sets start-first with a provider outside the two allowed Z.AI account IDs
- **THEN** loading fails before preflight, runtime launch or run allocation

#### Scenario: Start-first provider is omitted
- **WHEN** an agent selects start-first without provider
- **THEN** it routes within the Z.AI Start/Individual pair for the requested model instead of pinning the default Individual provider

### Requirement: Individual eligibility has explicit evidence
Selecting Individual SHALL require a credential resolved under the policy's identity rule, fresh active subscription evidence from the native-origin subscription-list GET, and the exact provider/model/reasoning in the live selectable registry view after applying that account evidence. Active subscription SHALL follow the inspected upstream Coding-product rule with status VALID and inCurrentPeriod true. A malformed relevant record or unreadable registry SHALL mean unknown; a valid complete list without an active Coding subscription SHALL mean unavailable. Raw builtin membership, key presence or missing disabledReason alone SHALL NOT establish eligibility. This evidence SHALL NOT claim positive model-specific remaining quota or inference verification. Individual evidence SHALL be fetched only when Individual is selected, so unavailable Individual SHALL NOT block an otherwise valid Start turn.

#### Scenario: Fixed previously trusted cache only
- **WHEN** fixed has a readable key and an old available cache but no verifiable active Individual entitlement
- **THEN** it now fails safely before send instead of fabricating entitled/current availability, with documented migration guidance

#### Scenario: Individual subscription service is unavailable
- **WHEN** fixed or start-first selects Individual and its subscription-list read cannot establish fresh entitlement within the decision budget
- **THEN** dispatch fails with routing_individual_unknown even if a credential, cached availability and a healthy inference endpoint exist, without extending the evidence TTL or using stale eligibility

#### Scenario: Available Start does not require Individual service
- **WHEN** start-first establishes valid exact-model Start eligibility while the Individual subscription service is unavailable
- **THEN** the turn uses Start without calling or waiting for the Individual subscription endpoint

#### Scenario: Fixed runtime lost a required wire method
- **WHEN** a structurally discoverable fixed runtime lacks session/cancelBackgroundTask or another required method
- **THEN** execution startup rejects compatibility before model work although local installation may have passed

#### Scenario: Subscription exists but requested model does not
- **WHEN** Individual is active but the live selectable registry lacks the exact requested model or reasoning
- **THEN** Individual is not selected and no substitute selection is sent

#### Scenario: Neither plan supports the model
- **WHEN** complete current evidence excludes the requested model from both plans
- **THEN** routing returns routing_model_unavailable without a model request

#### Scenario: Missing cross-plan identity in fixed mode
- **WHEN** fixed satisfies its unique-credential, Individual entitlement/model and wire gates but lacks Start JWT or cross-plan identity evidence
- **THEN** fixed remains usable without reading Start billing or requiring start-first identity resolution

### Requirement: Eligibility freshness and waits have numeric bounds
Each eligibility decision SHALL have a fifteen-second total budget including queueing, reads, registry projection and waits, bounded by the caller's earlier deadline. Each HTTP attempt SHALL have at most five seconds and each endpoint at most two attempts per decision. Only Start balance GET and Individual subscription-list GET SHALL receive automatic read retries: at most one after 250 milliseconds for transport errors or HTTP 502/503/504. Authentication errors, other 4xx/429, malformed payloads, unknown business errors and cancellation SHALL NOT be retried. No generic protocol RPC retry SHALL be added.

Reservation busy SHALL permit at most one fresh balance recheck after one second within the same endpoint attempt cap and decision budget; unresolved pressure SHALL return routing_temporary_busy. Sharing SHALL be process-local and require matching account/credential generation, endpoint, environment and registry revision. Reuse/join SHALL require a request age no greater than one second from request start; a reused response SHALL still satisfy that age at dispatch and SHALL NOT cross identity/config/expiry boundaries. A slow response is usable on receipt by its initiating decision after expiry/identity revalidation, but a joiner with over-age evidence SHALL obtain fresh evidence within its existing budget or fail unknown. No persistent or cross-squad cache SHALL be added. Expiry invalidation SHALL affect the next selection, without background polling or mid-turn plan switching. Fresh quota-failure evaluation SHALL bypass pre-failure cached evidence.

#### Scenario: Balance service transiently fails
- **WHEN** Start balance GET returns 503 and its one retry also fails
- **THEN** start-first stops with routing_balance_unknown within the decision budget, even when Individual inference could work

#### Scenario: Reservation remains occupied
- **WHEN** the first response and the single one-second recheck both report reservation pressure
- **THEN** routing returns temporary busy with at most two Start requests and no Individual inference

#### Scenario: Shared evidence ages out
- **WHEN** a joining decision receives a response more than one second after its request started
- **THEN** it does not dispatch from that stale shared result and either obtains new evidence within its remaining budget or fails unknown

#### Scenario: Expiry passes during active work
- **WHEN** an entitlement expires while a native turn is running
- **THEN** the adapter neither polls nor changes the running selection solely because of the time boundary, and the next selection invalidates expired evidence

#### Scenario: Too little time to continue
- **WHEN** only four seconds remain after quota-failure eligibility and cleanup
- **THEN** no continuation is sent and continuation_budget_insufficient is reported with preserved session/artifacts

### Requirement: Routing diagnostics and explicit retries are distinct
Routing diagnostics SHALL expose only requested_model, chosen effective_provider when any, plan_policy, reason_code, observed_at in UTC RFC3339 when evidence exists, known evidence_age_ms, and applicable attempt/input IDs. Codes SHALL distinguish routing_start_available, routing_start_model_absent, routing_start_expired, routing_start_exhausted, routing_temporary_busy, routing_balance_unknown, routing_individual_unknown, routing_individual_unavailable, routing_model_unavailable, routing_auth_required, routing_continuation_started, continuation_budget_insufficient and routing_continuation_failed. Absent evidence SHALL NOT receive an invented timestamp. Existing wire/transport/cleanup diagnostics SHALL remain separate.

Documentation SHALL explain that no automatic adapter resend does not forbid an explicit manual resubmission or workflow retry. Workflow retries SHALL preserve their existing fresh-conversation contract and may repeat effects. Quota continuation SHALL remain within the current workflow attempt/session and its cancellation/control boundaries, not allocate a new workflow retry or loop iteration.

#### Scenario: Unknown evidence has no false freshness
- **WHEN** a balance request fails without usable evidence
- **THEN** diagnostics report routing_balance_unknown without an invented observed_at, effective provider or raw account/billing values

#### Scenario: User explicitly retries unknown outcome
- **WHEN** a user explicitly requests a workflow retry after an execution-outcome-unknown failure
- **THEN** the existing workflow retry rules apply with a fresh conversation, and documentation does not promise exactly-once external effects
