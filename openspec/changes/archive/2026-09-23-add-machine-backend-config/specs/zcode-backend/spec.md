# zcode-backend Delta

## MODIFIED Requirements

### Requirement: Explicit compatible configuration

The system SHALL accept backend zcode with executable/runtime, provider/model/reasoning, and explicit environment options. It SHALL use Flash with low reasoning by default, decide installed runtime compatibility by probing the bundle for the host structures the bridge requires before loading it or using native account credentials, and fail clearly for unavailable or ambiguous credentials. Compatibility SHALL NOT depend on a pinned bundle fingerprint or an exact runtime version: any installed ZCode desktop 3.x bundle whose required structures are located unambiguously SHALL be treated as compatible. A bundle whose required structures are missing or ambiguous MUST NOT be loaded. Credentials MUST NOT appear in run artifacts or persisted state. The executable (`command`) and `runtime_path` MAY default from the machine backend configuration when the agent sets none, ZCode proxy settings from the machine backend configuration (`ZCODE_HTTP_PROXY`, `ZCODE_NO_PROXY`, `ZCODE_AGENT_CA_CERT`) SHALL be injected into the host process environment as defaults below explicit agent environment entries, and machine `inherit_env` SHALL union with the agent's own `options.inherit_env`, under the precedence rules of the backend-config capability.

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
