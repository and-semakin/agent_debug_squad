## MODIFIED Requirements

### Requirement: Account authentication is local and identity bound
The existing guarded host SHALL remain responsible for native credential/registry access. Individual execution SHALL preserve the existing unique-credential identity profile and default selection: exactly one Individual Coding Plan credential key resolvable at bootstrap, with a readable secret; an ambiguous or unreadable credential SHALL fail clearly before a session is created. Execution SHALL NOT require a Start JWT, cross-plan current-account resolution, a subscription-list read, or any other pre-dispatch network read against Z.AI origins; the reachability of `api.z.ai` or `zcode.z.ai` SHALL NOT gate run dispatch. Squad SHALL NOT switch account identity, copy its credential cipher, perform login/refresh, activate offers, redeem resets or rewrite desktop provider settings. Unsupported or expired authentication SHALL fail safely. Account overlays SHALL use the real native registry revision and mark the configured Individual provider entitled and current without fabricating a broader catalog. Both local registry account snapshots and App Server account overlays SHALL carry that revision in the case-sensitive native field `basedOnZCodeBuiltinRevision`; the configuration source field remains `zcodeBuiltinRevision`. Overlay receipt SHALL NOT imply model access verification.

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

#### Scenario: Native account revision contract
- **WHEN** a compatible runtime requires `basedOnZCodeBuiltinRevision` in account configuration
- **THEN** both registry verification and the App Server account overlay receive that exact field with the native revision, allowing valid configuration to pass without a missing-revision error

#### Scenario: Account revision contract cannot be satisfied
- **WHEN** the native parser rejects the account snapshot or the runtime rejects the account overlay
- **THEN** the run fails before session creation or prompt dispatch, without bypassing the account contract or exposing raw upstream errors from the registry read
