## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Turn recovery never replays prompts
The system SHALL correlate acceptance, started and terminal events with the submitted input and owned turn within the active generation, buffer valid early events within bounds, and ignore historical or foreign completions. A contradictory terminal sequence observed before finalization SHALL fail rather than produce multiple completions. Transport loss after a prompt may have been written or accepted SHALL preserve the known session ID and streamed evidence, return an execution-outcome-unknown diagnostic and SHALL NOT automatically resend the prompt or a permission decision. Subsequent explicit turns SHALL resume the saved session without repeating startup instructions. Reset SHALL retain old native history.

#### Scenario: Completion arrives before send acknowledgement
- **WHEN** a correctly correlated started/completed event sequence arrives before the accepted response
- **THEN** bounded buffering preserves it and the owned run completes once after acceptance is established

#### Scenario: Lost acknowledgement
- **WHEN** session/send may have reached the runtime and the connection closes before an authoritative result
- **THEN** Squad reports the unknown outcome, keeps the session ID and does not retry send even with the same inputId

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

## REMOVED Requirements

### Requirement: Account authentication is identity bound
**Reason**: The requirement mixed two things: the local credential/overlay boundary (kept) and the plan-routing-era gates — the fresh Individual entitlement read, Start JWT cross-plan identity, and balance-lookup identity tracking (removed with Start Plan support). It is replaced by "Account authentication is local and identity bound", which keeps the local boundary and drops the network-bound gates.
**Migration**: None for the kept scenarios; the "Expired Start token" and "Identity changes during balance lookup" scenarios have no referent because no Start token or balance lookup exists.

### Requirement: Turn recovery never implies prompt replay
**Reason**: Replaced by "Turn recovery never replays prompts": identical behavior except the plan-switching sentence and its scenario, which exist only for start-first plan transitions. OpenSpec 1.13 cannot drop individual scenarios from a MODIFIED requirement, so the requirement is reissued under a new name.
**Migration**: None; the "New turn uses another account plan" scenario has no referent because runs no longer switch account plans.

### Requirement: Start verification limitations are explicit
**Reason**: Start Plan support is removed; there is no Start CAPTCHA path, no Start inference, and no balance eligibility to document, so the requirement has no referent.
**Migration**: Configurations that relied on start-first must remove `plan_policy`; unsupported provider verification challenges now fail like any other unsupported provider interaction under "Account authentication is identity bound".

### Requirement: Model-specific Start-first routing
**Reason**: Start Plan routing is removed; the backend executes Individual only and no longer reads account-bound eligibility before turns.
**Migration**: Omit `plan_policy` and `provider`; runs use the Individual Coding Plan provider with the requested model and reasoning.

### Requirement: Exhaustion during a turn does not replay external effects
**Reason**: The Start-to-Individual quota-exhaustion continuation existed only for start-first runs and is removed together with the policy that admitted it.
**Migration**: Quota-exhaustion failures of an owned turn now terminate the run as an ordinary failure with preserved session and artifacts; the facilitator can issue a new explicit turn.

### Requirement: Plan policy configuration is validated before admission
**Reason**: There is no plan policy to validate; the option itself is rejected by the modified "Explicit compatible configuration" requirement, and the provider restriction moved there.
**Migration**: Remove `plan_policy` from agent options; set `provider` only to `account:zai-individual-coding-plan` or omit it.

### Requirement: Individual eligibility has explicit evidence
**Reason**: The requirement bundled the removed network-bound evidence (the fresh subscription-list GET under the inspected Coding-product rule, plan-scoped fetching) with the retained local selectability check (exact provider/model/reasoning in the live registry view). The subscription half is the pre-dispatch network dependency that broke fixed-mode runs in v0.19.0 and is removed; the selectability half is reissued as "Model selection requires selectable local evidence".
**Migration**: The unique-credential identity rule stays in "Account authentication is local and identity bound"; an unselectable model or reasoning level still fails before dispatch exactly as before, minus the subscription prerequisite and the plan framing.

### Requirement: Eligibility freshness and waits have numeric bounds
**Reason**: These bounds governed the removed network eligibility reads (Start balance and subscription-list retries, busy rechecks, cross-decision evidence sharing); with no eligibility network reads there is nothing to bound.
**Migration**: None; the retained registry-view projection runs as an ordinary bounded RPC, and ordinary RPC and wire budgets ("Host interactions remain responsive and owned", "Wire compatibility is established at the correct boundary") continue to apply.

### Requirement: Routing diagnostics and explicit retries are distinct
**Reason**: With plan routing removed there are no routing decisions to report; `zcode.routing` records and their reason codes cease to exist.
**Migration**: Consumers of run diagnostics should stop expecting `zcode.routing` records; wire/transport/cleanup diagnostics and the `zcode.models` record are unchanged.
