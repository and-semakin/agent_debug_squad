## MODIFIED Requirements

### Requirement: Discovery works before workflow creation

The system SHALL provide `agent-debug-squad models` with `--all`, repeatable `--backend`, optional `--config`, `--workspace`, `--json`, `--include-hidden`, `--timeout`, and `--backend-timeout`. Without config or selectors it SHALL discover all real registered agent backends. `--all` SHALL include codex, cursor, kimi, opencode1, opencode2, zcode, and devin; fake SHALL require explicit selection or an existing configured fake agent. Judge SHALL NOT be treated as an agent backend. The command SHALL NOT require a squad file, workflow, running Squad server, agent startup prompt, or existing conversation. It SHALL NOT initialize an orchestrator, create/resume backend sessions, send prompts, invoke model tools, install/update software, log in, or deliberately mutate native auth/configuration. The first version SHALL NOT expose a REST discovery route.

#### Scenario: Author has only a workspace
- **WHEN** the author runs `models --all --json --workspace .` before writing YAML
- **THEN** one result for every real backend is produced without creating Squad state, conversations, or workflow executions

#### Scenario: Fake does not mask unavailable real backends
- **WHEN** every real backend fails discovery and fake was not requested
- **THEN** the command reports failure without adding a synthetic success

#### Scenario: Invalid selectors
- **WHEN** the caller supplies an unknown backend, both --all and --backend, or a nonpositive timeout
- **THEN** the command fails before probing with exit 2 and a safe structured error in JSON mode

## ADDED Requirements

### Requirement: Versioned OpenCode catalog discovery
OpenCode 1 SHALL retain classic discovery. OpenCode 2 SHALL query its native model/provider/default endpoints with execution-equivalent mode, environment, authentication and location. Discovery SHALL preserve exact selectable IDs and evidence without inference. The legacy opencode selector SHALL resolve to v1 without duplicate equivalent probes; all-mode results SHALL identify both generations explicitly.

#### Scenario: Catalog without conversations
- **WHEN** OpenCode 2 model discovery runs
- **THEN** it creates no conversation or prompt, closes only its owned runtime, and preserves unknown access as unknown

#### Scenario: Alias and canonical selectors
- **WHEN** opencode and opencode1 select equivalent effective targets
- **THEN** one v1 probe serves both selections and opencode2 remains a separate target

#### Scenario: Incomplete catalog
- **WHEN** a later page or provider query fails
- **THEN** available rows remain partial with a safe diagnostic and unsupported fields are not invented
