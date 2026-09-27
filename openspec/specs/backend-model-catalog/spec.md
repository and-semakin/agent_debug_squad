# Backend Model Catalog

## Purpose

Provide attributable backend model choices before a squad or workflow is created, preserving exact configuration values and uncertainty about account access without sending inference requests.

## Requirements

### Requirement: Discovery works before workflow creation

The system SHALL provide `agent-debug-squad models` with `--all`, repeatable `--backend`, optional `--config`, `--workspace`, `--json`, `--include-hidden`, `--timeout`, and `--backend-timeout`. Without config or selectors it SHALL discover all real registered agent backends. `--all` SHALL include codex, cursor, kimi, opencode, and zcode; fake SHALL require explicit selection or an existing configured fake agent. Judge SHALL NOT be treated as an agent backend. The command SHALL NOT require a squad file, workflow, running Squad server, agent startup prompt, or existing conversation. It SHALL NOT initialize an orchestrator, create/resume backend sessions, send prompts, invoke model tools, install/update software, log in, or deliberately mutate native auth/configuration. The first version SHALL NOT expose a REST discovery route.

#### Scenario: Author has only a workspace
- **WHEN** the author runs `models --all --json --workspace .` before writing YAML
- **THEN** one result for every real backend is produced without creating Squad state, conversations, or workflow executions

#### Scenario: Fake does not mask unavailable real backends
- **WHEN** every real backend fails discovery and fake was not requested
- **THEN** the command reports failure without adding a synthetic success

#### Scenario: Invalid selectors
- **WHEN** the caller supplies an unknown backend, both --all and --backend, or a nonpositive timeout
- **THEN** the command fails before probing with exit 2 and a safe structured error in JSON mode

### Requirement: Effective configuration is shared with execution

Discovery SHALL resolve the same backend mode, executable/runtime, auth/configuration context, constrained environment, proxy/CA policy, and workspace as execution of an equivalent agent. Explicit agent options SHALL override machine defaults under existing precedence. The machine file and ambient environment SHALL each be captured once per invocation; no settings SHALL be written back. A config-free invocation SHALL use cwd unless --workspace is set. With --config, the config workspace SHALL be used, and a conflicting explicit workspace SHALL be rejected.

With config and no selector, discovery SHALL cover every distinct effective agent configuration. With --all it SHALL additionally cover machine/default targets for real backends absent from config. A backend filter SHALL select all matching configured targets, or the machine/default target when absent. Equivalent configurations SHALL be queried once and retain every affected agent name; differing accounts, endpoints, workspace or effective environments SHALL NOT be merged. Model/reasoning choices and agent names alone SHALL NOT force duplicate queries. Machine settings and private deduplication keys SHALL NOT enter persisted workflow snapshots or public results. Public target IDs SHALL be machine:<backend> or agents:<backend>:<names>, using UTF-8-byte-sorted original agent names, each individually percent-encoded with uppercase hex and only RFC 3986 unreserved bytes literal, then joined with commas; the original sorted agents array SHALL also be retained.

#### Scenario: Machine proxy and explicit agent override
- **WHEN** two configured agents differ in effective command or environment after machine defaults merge
- **THEN** discovery queries each effective context separately with execution's precedence and does not expose their environment values

#### Scenario: Equivalent reviewers
- **WHEN** reviewers differ only in role, prompt and chosen model under the same backend configuration
- **THEN** discovery queries that context once and reports both reviewer names

#### Scenario: Missing inherited authentication context
- **WHEN** execution's environment policy omits the HOME or credential variable needed by a backend
- **THEN** discovery preserves that policy and reports the resulting failure rather than silently inheriting the full shell environment

### Requirement: Catalog evidence and selections are explicit

JSON SHALL have schema_version 1, generated_at in RFC 3339 UTC with a Z suffix, aggregate status and deterministically ordered per-target results. Every result SHALL contain backend, target identity, agent references, status, completeness, source records, models, diagnostics and inference_verification set to not_checked. Source records SHALL identify operation, scope, retrieval time, completeness, upstream freshness and observed backend version when known. All timestamps SHALL use RFC 3339 UTC with a Z suffix. Each source SHALL expose hidden_policy with requested visible_only/include_hidden and applied visible_only/include_hidden/not_supported/unknown; --include-hidden SHALL NOT imply that an unsupported or unknown source actually included hidden models. Completeness SHALL be relative to that reported policy and scope. Known catalog, configured membership, connected evidence, disabled/hidden state and verified inference SHALL NOT be conflated. Absent evidence SHALL remain unknown rather than false or inferred success.

Rows SHALL preserve exact native model IDs and provider IDs where supplied, display names only when returned, aliases distinct from native IDs, and source references. Rows SHALL expose an allowlisted exact Squad selection plus whether the execution adapter supports that encoding. Returned reasoning choices/defaults and other known parameters SHALL retain their native names; an executable Squad option mapping SHALL only be claimed when it is implemented. Informational modalities/context windows SHALL NOT become fabricated options. No real model list, default, pricing, or capability SHALL be hardcoded or guessed from a model name.

#### Scenario: Astra from an account catalog
- **WHEN** a Codex source returns model gpt-6-astra with a display name and supported reasoning efforts
- **THEN** the row preserves those returned values and exposes backend codex with exact options.model and the supported options.reasoning mapping without performing semantic matching

#### Scenario: One model through multiple providers
- **WHEN** the same model family appears through two backends or two providers
- **THEN** all separately selectable alternatives remain visible and none is silently preferred

#### Scenario: Metadata without execution support
- **WHEN** Kimi returns effort values or OpenCode returns variants that Squad does not forward
- **THEN** the catalog may report those values as metadata but contains no claimed Squad option mapping for them

#### Scenario: Listing is not access verification
- **WHEN** a model is listed by a connected provider
- **THEN** configured/connected evidence is reported only as supplied, inference_verification remains not_checked, and no paid test request is sent

### Requirement: Codex discovery is a bounded account-context protocol

Codex discovery SHALL use a private short-lived App Server under the effective executable/environment/workspace, initialize its protocol and enumerate model/list pages. It SHALL NOT substitute the OpenAI API model list. It SHALL follow cursors to completion, default to visible models, support --include-hidden, preserve model versus catalog ID differences, and report partial data when later pages fail or repeat cursors. Missing metadata SHALL remain unknown. It SHALL NOT start/resume a thread or send a turn, and SHALL dispose of its owned process.

#### Scenario: Pagination and hidden models
- **WHEN** three pages are returned and the caller requests hidden models
- **THEN** all three pages are consumed with hidden inclusion enabled and each returned hidden flag is retained

#### Scenario: Later page fails
- **WHEN** the first page succeeds and the second page times out or repeats a cursor
- **THEN** the first page remains in a partial result with complete false and an explanatory safe diagnostic

### Requirement: Cursor listing preserves opaque IDs

Cursor discovery SHALL use its supported model-list command without agent prompts. A documented/tested text representation SHALL be supported when no verified structured format exists. IDs SHALL remain opaque, including parameterized IDs. Reasoning settings SHALL NOT be inferred from suffixes. An unrecognized successful output SHALL be unsupported; recognizable records mixed with malformed records SHALL be partial. Empty SHALL require recognized explicit empty-list output, not merely blank stdout.

#### Scenario: JSON flag still yields text
- **WHEN** the installed CLI offers --list-models but its output-format flag does not affect listing
- **THEN** discovery handles the verified text grammar and never starts a print-mode agent to obtain JSON

#### Scenario: Format drift
- **WHEN** the CLI exits zero but returns an unknown format
- **THEN** the result is unsupported rather than an empty successful catalog

### Requirement: OpenCode discovery is bound to the effective server

OpenCode discovery SHALL use the shared effective managed/external mode and runtime lifecycle. Discovery SHALL invoke only local installation checks before its own single runtime readiness/startup path; it SHALL NOT invoke the two-stage workflow/orchestrator admission preflight or create a second runtime owner for readiness. External mode SHALL query only the configured server with its transport/auth and workspace scope; local CLI installation SHALL NOT be required or used as fallback. Managed mode SHALL acquire the shared owned runtime with the execution proxy/environment/snapshot policy and close only its command-owned runtime. Discovery SHALL use the classic provider and config/providers representations, retaining known models, configured membership, defaults and connected provider evidence independently. It SHALL NOT expose provider credentials/options, create sessions, send prompts, PATCH configuration, or combine incompatible v1/v2 schemas. Partial endpoint coverage SHALL be explicit.

#### Scenario: External server has different models from local CLI
- **WHEN** the configured external endpoint and local opencode command expose different providers
- **THEN** only the endpoint's catalog is returned and no local command is launched

#### Scenario: Managed discovery without workflow
- **WHEN** managed OpenCode is selected before a squad exists
- **THEN** discovery uses the shared runtime startup/readiness plan, creates no session, and cleans up its owned runtime after listing

#### Scenario: Provider endpoint partially supported
- **WHEN** provider returns usable records but config/providers is unsupported
- **THEN** the known records and connected evidence remain available with partial status and configured membership unknown

### Requirement: Kimi configuration is projected safely and aliases are executable

Kimi discovery SHALL read provider list --json only when supported and extract allowlisted model metadata. Raw provider configuration, credentials and unknown fields SHALL NOT reach output, diagnostics, caches, or artifacts. Each configured alias SHALL remain distinct from its provider/native model ID and map to options.model. An explicit options.model, including a stale or placeholder value previously ignored, SHALL be forwarded unchanged to Kimi's --model argument during execution; omission SHALL retain the CLI's existing default. Documentation SHALL explain this compatibility change and instruct users to replace stale values with a configured alias or omit options.model; Squad SHALL NOT silently rewrite the configuration or fall back to another model after rejection. Unsupported list interfaces SHALL produce unsupported, without invoking login or migration. Listing SHALL NOT certify the compatibility of unrelated Kimi execution protocols.

#### Scenario: Provider output contains secrets
- **WHEN** list output contains providers with apiKey/oauth alongside model records
- **THEN** only permitted model fields and provider identifiers are returned and no secret or raw config appears at any log level

#### Scenario: Two aliases target one native model
- **WHEN** two configured aliases reference the same provider/model
- **THEN** both rows remain selectable and execution passes the chosen alias unchanged rather than substituting the native ID

### Requirement: ZCode discovery fails honestly without a verified safe bridge

ZCode discovery SHALL use only an independently verified session-free, configuration-read-only bridge path with structural compatibility checks before runtime loading or native credential access. An internal tool display named list_models SHALL NOT be treated as a public API. Discovery SHALL NOT create/resume a session, execute the ListModels agent tool, refresh login, mutate account configuration, or use connectivity inference probes. Missing, ambiguous or unsafe structures SHALL yield unsupported with a safe reason rather than a fabricated empty catalog. Until safe initialization is verified, read_only_catalog_unavailable SHALL be an accepted explicit unsupported outcome. Static provider files or old conversation diagnostics SHALL NOT silently substitute for the effective catalog. Verified metadata SHALL retain truncation and disabled state; choices unsupported by Squad's execution adapter SHALL be labelled accordingly.

#### Scenario: Installed runtime only exposes conversation-based listing
- **WHEN** a runtime has list_models display code but no verified session-free read-only bridge path
- **THEN** discovery returns unsupported and creates no conversation or credential/configuration mutation

#### Scenario: Compatible verified registry path
- **WHEN** a structurally compatible bridge has passed read-only lifecycle verification and returns registry records
- **THEN** discovery returns only allowed metadata, preserves truncation, and marks unsupported execution selections instead of promising they can run

### Requirement: Fake discovery is explicitly synthetic

The fake backend SHALL return one deterministic synthetic fake model record with source scope synthetic, no provider/auth/reasoning claims and no filesystem, network or session effects. This fixture SHALL NOT restrict arbitrary explicitly configured fake model values or appear implicitly in an all-real-backend query.

#### Scenario: Offline deterministic test
- **WHEN** models --backend fake --json is executed with no backend tools installed
- **THEN** it returns a complete synthetic record and exit 0 without backend filesystem/network access or session allocation

### Requirement: Failure aggregation and resource bounds are observable

Each target SHALL distinguish ok, empty, partial, not_installed, not_configured, auth_required, offline, unsupported, timeout, cancelled and error. Missing local prerequisites SHALL reuse installation-check evidence; ready/not_required SHALL NOT imply auth or model access. No target failure SHALL discard completed results from other targets. Aggregate status SHALL be ok only for all complete ok/empty targets, partial when useful results coexist with degradation, and failed when no usable listing result exists. A complete empty catalog counts as usable evidence.

Installation/readiness failures SHALL map using typed evidence and preserve safe phase/code/restart_required diagnostics: not_found to not_installed; unsupported_launcher/runtime/platform and service_incompatible to unsupported; not_executable, not_readable, invalid_search_path, check_failed, start_failed, restart_required and snapshot-policy mismatch to error; expired contexts/timed_out to timeout; cancelled contexts/cancelled to cancelled. Confirmed transport failure SHALL map to offline and explicit authentication-required evidence SHALL map to auth_required. Coarse service_unavailable with no typed cause and ambiguous non-2xx responses SHALL map to error, not guessed offline/auth_required. Caller context SHALL be checked before coarse codes. Failures after usable rows SHALL yield partial with the original cause diagnostic; latched managed failures SHALL NOT trigger automatic restart.

Missing required discovery context SHALL be not_configured; a supported empty configured catalog SHALL be empty. Unknown connectivity/auth evidence SHALL NOT itself be classified as offline/auth_required. Failure statuses SHALL have complete false. Selecting no targets from configuration SHALL produce exit 2 rather than vacuous success.

The command SHALL run at most three target operations concurrently. With N positive deduplicated targets and B equal to --backend-timeout (default 30s), omitted --timeout SHALL resolve to max(60s, ceil(N/3) * (B + 5s) + 5s); checked-arithmetic overflow SHALL fail with exit 2 before probing. The overall discovery budget SHALL start after target resolution immediately before queuing and include queue time. The target budget SHALL begin only when its worker starts and cover installation/startup/all pages/parsing. An explicit --timeout SHALL replace the computed budget even when shorter than B; the earlier deadline SHALL win without automatic extension. Earlier caller cancellation SHALL be honored. Configuration parsing SHALL precede this discovery budget. Cleanup SHALL hold the worker slot until completion. Unfinished/unqueued targets SHALL be marked on timeout/cancellation. Owned cleanup SHALL have at most five additional seconds. Individual frames/responses SHALL be bounded to 8 MiB, total source bytes to 32 MiB and enumeration to 100 pages/10,000 rows per target; hitting limits SHALL never produce complete success. Only owned processes/runtimes SHALL be stopped, and cleanup failure SHALL be reported.

JSON mode SHALL emit exactly one result document, including backend failures. Complete success SHALL exit 0; any backend degradation SHALL exit 1; invalid CLI/config SHALL exit 2; caller cancellation SHALL exit 130 with collected JSON when writable. Results SHALL be ordered deterministically independent of completion order. Duplicate identities SHALL merge provenance; conflicting metadata SHALL cause partial status and shall not be resolved by arrival order.

#### Scenario: One backend fails authentication
- **WHEN** Codex succeeds and Cursor returns a recognized authentication error
- **THEN** both results appear, Cursor is auth_required, aggregate status is partial and exit status is 1

#### Scenario: Cancellation preserves progress
- **WHEN** cancellation arrives after one target finishes while others run or wait
- **THEN** the completed result remains, unfinished targets become cancelled, owned probes stop within cleanup bounds and no external server is stopped

#### Scenario: Truly empty source
- **WHEN** a recognized source successfully returns a complete zero-record catalog
- **THEN** its status is empty with complete true, distinct from missing installation, malformed output and unknown availability

#### Scenario: Resource cap preserves valid rows
- **WHEN** a source exceeds its page or byte budget after valid records have been decoded
- **THEN** those records remain in a partial result with a limit diagnostic and complete false

### Requirement: Freshness and secret boundaries survive all output paths

The first version SHALL use no persistent or cross-invocation Squad catalog cache; each invocation SHALL query its selected sources again. Source-owned cache state/timestamp/staleness SHALL be preserved when supplied, otherwise upstream freshness SHALL be unknown. Retrieval time SHALL NOT imply upstream freshness or access. A source failure SHALL NOT silently fall back to stale data.

Only allowlisted normalized fields SHALL be output or logged. Raw configuration, source stdout/stderr, protocol bodies, environment values, auth headers, credentials, proxy URLs and secret-derived target keys SHALL NOT appear even at trace level. Errors SHALL use safe codes/templates and metadata SHALL be bounded and sanitized for known credential leakage and terminal controls. Model values SHALL be passed as argument data without shell interpretation.

#### Scenario: Fresh retrieval of an upstream cache
- **WHEN** a source responds now using an explicitly stale upstream snapshot
- **THEN** retrieved_at is current while freshness remains stale with the original timestamp when supplied

#### Scenario: Secret in backend failure text
- **WHEN** a subprocess or HTTP error echoes a token or credentialed proxy URL
- **THEN** the catalog and stderr expose only sanitized fixed diagnostics and no raw error body

### Requirement: Discovery guides authoring without becoming an execution gate

Repository documentation and the authoring skill SHALL instruct callers to discover models before writing workflow agent selections, inspect partial/error results, use exact returned YAML mappings and preserve requested backends/models. Natural-language matching SHALL remain the calling agent's responsibility. Multiple backend/provider matches SHALL require an explicit caller/user choice rather than a hidden preference. An absent catalog entry, unsupported discovery interface or failed discovery SHALL NOT independently reject an explicitly configured model during YAML validation or add mandatory model/auth discovery to each run.

#### Scenario: Explicit model absent from incomplete listing
- **WHEN** YAML contains an explicit model not present in a partial catalog
- **THEN** structural validation continues under existing rules and does not reject it solely because of catalog absence

#### Scenario: Ambiguous user request
- **WHEN** the caller finds multiple backend/provider candidates for a natural-language model request
- **THEN** the documented workflow exposes those candidates for an explicit choice and writes no silently selected alternative

### Requirement: Human output and error envelopes have stable meaning

Without --json the command SHALL produce human-readable target blocks ordered as JSON, identifying backend/target/agents, status, completeness, diagnostics, provider/model/alias/display name, exact selection options, known reasoning/defaults, source retrieval/freshness and hidden policy. Unknown evidence and unsupported selections SHALL be explicit. Empty/failed targets SHALL remain visible beside successful rows. Output SHALL end with aggregate status and indicate that inference was not checked. Terminal control characters in all user/backend text SHALL be escaped; exact IDs/options SHALL NOT be silently truncated. Layout spacing SHALL NOT be a machine parsing contract.

In JSON mode exit-2 failures SHALL use exactly the top-level fields schema_version: 1, generated_at (RFC 3339 UTC), status: failed, results: [], and error containing code and message. Error code SHALL be invalid_arguments, invalid_configuration or no_targets, as appropriate; message SHALL be a safe diagnostic without raw option/configuration values. Backend-result envelopes SHALL omit error. Documentation SHALL explain that exit 1 covers both partial and failed, distinguishable by JSON status/results, and that --all returns exit 1 while ZCode is unsupported even when all other targets succeed. Examples SHALL capture exit 0/1 for inspection instead of relying on an && success chain.

#### Scenario: Useful all-backend result has nonzero exit
- **WHEN** four real backends complete successfully and ZCode discovery is unsupported
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
