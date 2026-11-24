## MODIFIED Requirements

### Requirement: Human output and error envelopes have stable meaning

Without --json the command SHALL produce human-readable target blocks ordered as JSON, identifying backend/target/agents, status, completeness, diagnostics, provider/model/alias/display name, exact selection options, known reasoning/defaults, source retrieval/freshness and hidden policy. Unknown evidence and unsupported selections SHALL be explicit. Empty/failed targets SHALL remain visible beside successful rows. Output SHALL end with aggregate status and indicate that inference was not checked. Terminal control characters in all user/backend text SHALL be escaped; exact IDs/options SHALL NOT be silently truncated. Layout spacing SHALL NOT be a machine parsing contract.

In JSON mode exit-2 failures SHALL use exactly the top-level fields schema_version: 1, generated_at (RFC 3339 UTC), status: failed, results: [], and error containing code and message. Error code SHALL be invalid_arguments, invalid_configuration or no_targets, as appropriate; message SHALL be a safe diagnostic without raw option/configuration values. Backend-result envelopes SHALL omit error. Documentation SHALL explain that exit 1 covers both partial and failed, distinguishable by JSON status/results. Examples SHALL capture exit 0/1 for inspection instead of relying on an && success chain.

#### Scenario: Useful all-backend result has nonzero exit
- **WHEN** some real backends complete successfully and another registered backend fails discovery
- **THEN** aggregate status is partial with exit 1, successful rows remain visible in both output modes, and documented scripts inspect the report

#### Scenario: Human partial result is safe to display
- **WHEN** a source label or configured agent name contains terminal control characters and another target fails
- **THEN** human output escapes the controls, retains the failed target diagnostics and complete selection identifiers, and ends with the partial summary

#### Scenario: Invalid JSON invocation
- **WHEN** --json is present and mutually exclusive selectors or a malformed configuration is supplied
- **THEN** exit 2 returns one error envelope with invalid_arguments or invalid_configuration respectively, an empty results array and no raw input values

#### Scenario: Target count determines default timeout
- **WHEN** target deduplication yields five or seven targets with default B of 30s and no explicit --timeout
- **THEN** the overall discovery budget is respectively 75s or 110s, with queue time counted only against that overall budget

#### Scenario: Explicit shorter timeout wins
- **WHEN** --timeout is 10s and --backend-timeout is 30s
- **THEN** unfinished and queued targets time out at the overall deadline, completed results remain, and only bounded owned cleanup may continue

#### Scenario: Report identifies different hidden scopes
- **WHEN** otherwise equivalent Codex invocations omit or include --include-hidden
- **THEN** sources report requested/applied visible_only or include_hidden respectively, even if both enumerations are complete

#### Scenario: Coarse readiness failure is not guessed
- **WHEN** external OpenCode readiness reports service_unavailable without a typed cause
- **THEN** discovery reports error retaining the readiness code, while service_incompatible would map to unsupported and an expired caller deadline to timeout

#### Scenario: Readiness owns only one server
- **WHEN** managed OpenCode passes local installation checks during discovery
- **THEN** only the discovery-owned runtime performs startup/readiness, no admission preflight starts another server, and the runtime is closed once after its borrowers finish

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


### Requirement: Discovery works before workflow creation

The system SHALL provide `agent-debug-squad models` with `--all`, repeatable `--backend`, optional `--config`, `--workspace`, `--json`, `--include-hidden`, `--timeout`, and `--backend-timeout`. Without config or selectors it SHALL discover all real registered agent backends. `--all` SHALL include codex, cursor, kimi, opencode1, opencode2, and devin; fake SHALL require explicit selection or an existing configured fake agent. Judge SHALL NOT be treated as an agent backend. The command SHALL NOT require a squad file, workflow, running Squad server, agent startup prompt, or existing conversation. It SHALL NOT initialize an orchestrator, create/resume backend sessions, send prompts, invoke model tools, install/update software, log in, or deliberately mutate native auth/configuration. The first version SHALL NOT expose a REST discovery route.

#### Scenario: Author has only a workspace
- **WHEN** the author runs `models --all --json --workspace .` before writing YAML
- **THEN** one result for every real backend is produced without creating Squad state, conversations, or workflow executions

#### Scenario: Fake does not mask unavailable real backends
- **WHEN** every real backend fails discovery and fake was not requested
- **THEN** the command reports failure without adding a synthetic success

#### Scenario: Invalid selectors
- **WHEN** the caller supplies an unknown backend, both --all and --backend, or a nonpositive timeout
- **THEN** the command fails before probing with exit 2 and a safe structured error in JSON mode

## REMOVED Requirements

### Requirement: ZCode discovery fails honestly without a verified safe bridge
**Reason**: ZCode is no longer a registered backend.
**Migration**: Select a supported backend for discovery.

## ADDED Requirements

### Requirement: Removed ZCode discovery is rejected
Explicit zcode selectors SHALL fail with a deprecated-and-removed diagnostic and supported alternatives, without executing a probe. Default discovery and --all SHALL omit ZCode.

#### Scenario: Explicit removed selector
- **WHEN** models is called with --backend zcode
- **THEN** it returns an argument error with migration guidance and launches no ZCode process
