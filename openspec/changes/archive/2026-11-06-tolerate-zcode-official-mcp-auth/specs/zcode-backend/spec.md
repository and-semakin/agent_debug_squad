# Spec Delta

## ADDED Requirements

### Requirement: Optional official MCP authentication is nonfatal
The system SHALL answer `interaction/requestOfficialMcpAuthHeaders` requests with the native negative result `ok: false, reason: official_auth_unavailable`, without credentials or headers and without failing the conversation solely because of that request. This applies during startup and resumed turns, including MCP-server and plugin-command consumers. Other unsupported host requests SHALL retain explicit failure behavior.

#### Scenario: Optional auth during session creation
- **WHEN** ZCode requests official MCP auth while session creation is pending
- **THEN** it receives the correlated negative result promptly and an otherwise successful no-tools turn completes normally

#### Scenario: Optional auth on resume
- **WHEN** a resumed conversation requests official plugin-command auth
- **THEN** it receives the same negative result and the conversation retains its existing session identity

#### Scenario: Foreign or malformed auth context
- **WHEN** an official MCP auth request names a foreign workspace or malformed context
- **THEN** Squad supplies no credential material or authorization and responds only with auth unavailability

#### Scenario: Additive notification
- **WHEN** the official MCP method arrives without a request ID
- **THEN** Squad sends no response and does not grant authentication

#### Scenario: Unknown host request
- **WHEN** the runtime requests another unsupported interaction
- **THEN** Squad returns an explicit unsupported error and fails the run rather than silently granting or ignoring it
