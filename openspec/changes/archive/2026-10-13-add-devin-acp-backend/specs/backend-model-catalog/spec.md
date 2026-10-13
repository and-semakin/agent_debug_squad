## MODIFIED Requirements

### Requirement: Discovery works before workflow creation

The system SHALL provide `agent-debug-squad models` with `--all`, repeatable `--backend`, optional `--config`, `--workspace`, `--json`, `--include-hidden`, `--timeout`, and `--backend-timeout`. Without config or selectors it SHALL discover all real registered agent backends. `--all` SHALL include codex, cursor, kimi, opencode, zcode, and devin; fake SHALL require explicit selection or an existing configured fake agent. Judge SHALL NOT be treated as an agent backend. The command SHALL NOT require a squad file, workflow, running Squad server, agent startup prompt, or existing conversation. It SHALL NOT initialize an orchestrator, create/resume backend sessions, send prompts, invoke model tools, install/update software, log in, or deliberately mutate native auth/configuration. The first version SHALL NOT expose a REST discovery route.

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

### Requirement: Devin catalog is session-free and evidence-based
Devin discovery SHALL use the official models list --format json interface only for a verified session-free launch profile that does not install updates or mutate native auth/configuration. It SHALL NOT initialize or load a session to obtain model choices. An unverified safe profile SHALL return unsupported with read_only_catalog_unavailable. A supported profile SHALL project only allowlisted metadata from a recognized versioned schema, preserving exact selectable model IDs separately from families and aliases. Only verified execution encodings SHALL map to options.model; reasoning and speed SHALL remain informational until execution supports them. Unknown JSON shape SHALL be unsupported, not empty success. An explicit recognized empty catalog SHALL be empty. Account access, inference, hidden coverage, freshness and pricing SHALL not be guessed. Existing aggregate statuses, bounds, redaction and exit semantics SHALL apply.

#### Scenario: Catalog without a conversation
- **WHEN** a verified supported model-list invocation returns account model choices
- **THEN** the result reports those choices with inference_verification not_checked and no native conversation or prompt is created

#### Scenario: Unknown schema
- **WHEN** a successful model-list command returns unrecognized JSON
- **THEN** discovery reports unsupported without exposing raw output or inventing an empty catalog

#### Scenario: No verified non-mutating list path
- **WHEN** listing may trigger native updates or configuration migration and no safe profile has been verified
- **THEN** discovery returns read_only_catalog_unavailable without launching an agent as a fallback

#### Scenario: Reasoning metadata lacks execution mapping
- **WHEN** the catalog supplies thinking levels but this backend cannot apply them
- **THEN** they remain metadata and no options.reasoning selection is advertised


#### Scenario: Catalog variants are not direct ACP choices
- **WHEN** the native list contains variant IDs whose execution encoding cannot be established without a conversation
- **THEN** rows remain informational with unsupported selections and no options.model recipe, while manually configured execution IDs are checked against the live ACP control before prompting
