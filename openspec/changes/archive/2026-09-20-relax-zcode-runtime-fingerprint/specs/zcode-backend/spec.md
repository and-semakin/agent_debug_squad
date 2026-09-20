## MODIFIED Requirements

### Requirement: Explicit compatible configuration
The system SHALL accept backend zcode with executable/runtime, provider/model/reasoning, and explicit environment options. It SHALL use Flash with low reasoning by default, decide installed runtime compatibility by probing the bundle for the host structures the bridge requires before loading it or using native account credentials, and fail clearly for unavailable or ambiguous credentials. Compatibility SHALL NOT depend on a pinned bundle fingerprint or an exact runtime version: any installed ZCode desktop 3.x bundle whose required structures are located unambiguously SHALL be treated as compatible. A bundle whose required structures are missing or ambiguous MUST NOT be loaded. Credentials MUST NOT appear in run artifacts or persisted state.

#### Scenario: Compatible signed-in account
- **WHEN** a run starts with an installed runtime whose required structures are located unambiguously and one available supported account
- **THEN** it starts an authenticated App Server conversation with the configured selection and inherited environment allowlist

#### Scenario: Desktop update within the supported line
- **WHEN** the installed ZCode desktop is updated to a newer 3.x bundle that keeps the required structures discoverable
- **THEN** runs continue to start without a new Squad release, a fingerprint update, or configuration changes

#### Scenario: Unsupported installation
- **WHEN** required runtime structures are missing or ambiguous, or the supported account is unavailable
- **THEN** the run fails with an actionable error before a model prompt is sent, the error includes the bundle fingerprint for reporting, and an unusable bundle is never loaded
