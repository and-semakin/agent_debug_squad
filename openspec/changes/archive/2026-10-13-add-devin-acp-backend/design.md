## Context

See `proposal.md` for motivation. Research date: 2026-09-28. No active OpenSpec changes existed at research start. Installation checks, model discovery and OpenCode ownership are already implemented; their main specs, not earlier discussions, are the integration baseline. ZCode already demonstrates run-scoped permission replies and owned subprocess cleanup, but its private JavaScript bridge is not appropriate for Devin.

### Evidence and provenance

| Evidence | Finding and boundary |
| --- | --- |
| [Official CLI quickstart](https://docs.devin.ai/cli) and [installer](https://cli.devin.ai/install.sh), retrieved 2026-09-28 | Official local executable is `devin`. macOS and Linux support ARM64/x86_64; Windows installers support both architectures, and WSL follows Linux. Installation options include Cognition's script and Homebrew cask `devin-cli`. Squad's first version remains macOS/Linux. The installer was read, never executed. |
| [CognitionAI/devin-cli](https://github.com/CognitionAI/devin-cli), main `bd4163ed29e934b898f185752182c91aacac20f7` | Public repository contains README and release automation, not agent implementation source. Do not substitute unofficial Python/npm API wrappers. No public implementation/package API was established. |
| [Official release 3000.11.3](https://github.com/CognitionAI/devin-cli/releases/tag/3000.11.3) and [stable changelog](https://docs.devin.ai/cli/changelog/stable) | GitHub publication is 2026-09-23; changelog labels the release September 22. Installed build is `3000.11.1 (cc4e349ca55e)`, dated September 21 in the changelog. This is the observed baseline, not a claim that all versions behave identically. |
| Local read-only `--version`, `--help`, `acp --help`, `models list --help` | ACP stdio, explicit resume, print mode and JSON model listing exist. `acp --model` and `DEVIN_MODEL` are supported in this build, contrary to older third-party claims. `--cloud` explicitly selects cloud relay; it is not the default. No documented stream-JSON print flag was found. |
| Isolated local ACP `initialize`, protocol version 1, empty client capabilities | Returned `loadSession:true`, image/embedded-context support, session list/delete/additional-directories and Cognition extensions. Reported agent identity is `affogato` / `Devin Agent` / `0.0.0-dev`; do not use that version as the installed release version or require the programmatic name to be `devin`. Only initialization was sent, then stdin closed and the process exited 0. A first probe closed stdin too early and got no response; this is not protocol incompatibility evidence. Temporary config disabled updates/imports and temporary XDG config/data/cache locations avoided the user's credentials/configuration. |
| [Official ACP editor integration](https://docs.devin.ai/cli/acp/zed) and [command reference](https://docs.devin.ai/cli/reference/commands) | JSON-RPC stdio is an intended integration surface, with native model selection and slash commands. No cloud API adapter is required. |
| [ACP initialization](https://agentclientprotocol.com/protocol/v1/initialization), [session setup](https://agentclientprotocol.com/protocol/v1/session-setup), [turns](https://agentclientprotocol.com/protocol/v1/prompt-turn), [configuration](https://agentclientprotocol.com/protocol/v1/session-config-options), [tool calls](https://agentclientprotocol.com/protocol/v1/tool-calls) | Define standard request/notification shapes. Protocol support is evidence of a contract, not proof that every optional feature works in this Devin build. |

At the 2026-09-28 planning stage, only help/version and unauthenticated initialization were executed. On 2026-10-13 the user authorized small live test sessions and implementation from fresh main. The compatibility record below supersedes the earlier untested-runtime boundary; no installation, login, cloud session or native configuration edits were performed.

### Additional official constraints

- [Models](https://docs.devin.ai/cli/models): aliases such as `opus` resolve dynamically; account/team controls constrain availability. Do not hardcode the website's model menu or a default. The changelog confirms session configuration controls for model/thinking/speed in 3000.11.1, but exact values were not retrieved.
- [Authentication](https://docs.devin.ai/cli/enterprise/devin-auth): credentials normally live in the CLI's XDG data scope. The command reference also documents `WINDSURF_API_KEY` for ACP. Squad should inherit this only explicitly; do not invent `DEVIN_API_TOKEN` support or use the cloud REST API token contract. Login remains an operator action.
- [Self-serve billing](https://docs.devin.ai/admin/billing/self-serve) and [usage](https://docs.devin.ai/admin/billing/usage): CLI usage shares plan quota and can consume on-demand credits; enterprise billing uses its agreement/ACU allocation. Enterprise-only wording on the enterprise authentication page is not a universal CLI restriction. Free-plan CLI entitlement and exact concurrency ceilings are not established here. Installation or catalog success does not prove entitlement, quota, model access or zero cost.
- [Configuration](https://docs.devin.ai/cli/reference/configuration/config-file) and [system policy](https://docs.devin.ai/cli/enterprise/system-config): system-mode proxies honor HTTP_PROXY/HTTPS_PROXY/ALL_PROXY and native PAC. Native manual/off proxy policy can change that; enterprise policy takes precedence. CLI imports other tools' configuration by default, can run configured hooks/MCP, and enables auto-update by default. These effects must be verified/guarded for the supported launch path; Squad must not rewrite native files to repair them.
- [Permissions](https://docs.devin.ai/cli/reference/permissions) and [sandbox](https://docs.devin.ai/cli/sandbox): permission modes and OS sandbox are separate. macOS uses Seatbelt and Linux uses bwrap/seccomp; the sandbox is a research preview and can have excluded commands. Global CLI flags do not prove an ACP mode or sandbox was applied.

## Goals / Non-Goals

**Goals:** Preserve explicit facilitator turns, fresh workflow attempts, native session identities, current output artifacts, bounded ownership and existing failure/retry semantics through a small ACP adapter. Fail before prompting when required controls cannot be applied.

**Non-Goals:** A general ACP host for arbitrary agents, cloud workspace synchronization, editor terminals/filesystem callbacks, arbitrary slash-command execution, reasoning/speed/Fusion configuration, new approval APIs, a persistent shared Devin daemon, or native Windows release support. No guarantee that local execution means offline inference or that killing a process reverses external effects.

## Decisions

### 1. Native ACP, private process per operation

Implement `AgentAdapter`, optional `PermissionReplier`, and `ModelLister` in `internal/adapters/devin`. Use a small bounded JSON-RPC client over newline-delimited stdio; match response IDs and session IDs, serialize writes, handle server requests while a prompt is pending, and continuously drain stderr. Reuse `procgroup` rather than introducing a shared server owner. A process belongs to one adapter operation; turns never multiplex across unrelated agents. No network listening socket or OpenCode-style global runtime is needed.

Advertise only implemented ACP capabilities: initially no host filesystem, terminal, authentication UI or elicitation. Devin's own local tools must work with that profile; if required callbacks appear, fail with a typed unsupported-interaction error rather than acknowledging work that was not performed. Verify this profile in the compatibility gate before release. Unknown notifications can be safely ignored unless they invalidate required state; unknown requests receive an appropriate JSON-RPC error and abort when progress requires them. Bound frames to 8 MiB and accumulated in-memory text/diagnostics; stream events to the sink instead of retaining an unbounded duplicate `RawEvents` array. Exceeding a limit is failure with partial artifacts.

Alternatives: `--print` is convenient but lacks a verified structured completion/stream contract; parsing ANSI output or private SQLite/transcripts is fragile. A wrapper such as acpx adds another runtime and interpretation layer. Cloud REST/ACP is a different backend with remote files and cancellation/billing implications; it must not be a transparent fallback.

### 2. Persist conversations, not processes

Use absolute `state.WorkspaceDir` as both child cwd and ACP `cwd`; pass no additional MCP servers (`mcpServers: []` does not claim to disable native configured servers). `Init` runs only after existing admission preflight: initialize, require protocol v1 plus session loading/listing, and create a fresh session if the state has no ID. Observed empty sessions disappear when their process exits, so retain that process across Init, durable state persistence and the first Send. An optional PreparationCloser releases it on every worker exit; cancellation and a 30-second expiry independently bound abandoned preparation. Later Send operations open a process and verify/list/load the exact ID. Existing IDs are never replaced on errors; a lost empty session requires explicit reset. No process remains between completed turns.

The orchestrator currently calls `saveRuntimeState` before `Send` without checking the error. Implementation must make the new-session persistence gate effective: do not send work if that save fails, for manual and owned paths. This is a focused integration fix, not a new workflow journal. A crash after session creation but before persistence can leave an unused native conversation; do not delete/search/adopt it by recency.

Loading replays historical `session/update` events. Consume these before the load response, exclude them from the new run's output, and detect whether a user prompt already exists. Include `promptfmt.WithStartupPrompt` only for an empty conversation; `LastRunID` alone is insufficient after a failed first turn. If the replay cannot distinguish an empty session, fail instead of duplicating startup instructions. A failed load, missing identity, session lock or mismatched workspace must never turn into `session/new` or `--continue`.

`Reset` clears identity and defers replacement to the next checked turn; it never deletes native history. `Recover` preserves identity and performs no inference; validation/load is deferred to the next checked explicit turn. A restored run with uncertain prior dispatch is not replayed. Workflow attempts and confirmed retries already get new adapter state in `orchestrator/owned.go`; preserve that ownership and the existing manual-mutation 409 boundary. No workflow schema bump is planned.

### 3. Completion belongs to the prompt response

After load/configuration complete, send one `session/prompt` containing text. Accumulate only owned live `agent_message_chunk` text; do not treat thoughts, tool stdout, history or another session as the final response. A matching response with `stopReason: end_turn`, nonempty new assistant text, no fatal protocol/storage failure and successful owned cleanup constitutes success. Refusal, token/turn limits, cancellation, JSON-RPC error, EOF before response, malformed frames and empty output are failures with distinct safe diagnostic codes. Exit zero or silence is never completion evidence.

ACP updates do not always carry a turn ID. One outstanding prompt per owned process and the load-completion boundary provide correlation; a mismatched session is a protocol failure. Preserve partial text/events as failed artifacts under the existing orchestrator contract. Do not assume a prompt completion means all asynchronous tools have stopped.

The changelog's structured retryable channel-closed error permits reconnecting, not resending an uncertain prompt. Do not retry prompts, switch models after refusal, or attach to a different conversation. Explicit subsequent manual turns and workflow retries retain their existing user-controlled semantics and cannot promise exactly-once external effects.

### 4. Small configuration surface

| Location | Initial keys/behavior |
| --- | --- |
| `backend: devin`, `options` | `command` (default `devin`), optional opaque `model`, `env`, `inherit_env`, existing `yolo` |
| Machine `devin` section | `command`, `proxy_url`, `no_proxy`, `inherit_env` |
| Deferred / rejected as unsupported Devin options | `reasoning`, `speed`, `provider`, `sandbox`, `permission_mode`, `cloud`, `base_url`, arbitrary process args and runtime bundle paths |

Use a baseline allowlist of HOME, PATH, TMPDIR, TMP, TEMP and XDG_CONFIG_HOME/XDG_DATA_HOME/XDG_CACHE_HOME/XDG_STATE_HOME, plus explicit inherited values and explicit env. Always materialize a non-nil environment; never inherit the full ambient environment by accident. Share this builder between execution, preflight and catalog. Apply existing machine/agent union and override semantics. Map machine networking to HTTP_PROXY/HTTPS_PROXY/NO_PROXY; do not add Node-specific variables or guess a CA override for the Rust executable. Omitted proxy values do not imply direct connections when native CLI PAC/manual/enterprise policy applies. Document that precedence instead of promising machine env defeats enterprise policy.

Native login credentials may be used by Devin from this environment, but Squad does not read/export them or call `authenticate`. Do not inherit ambient DEVIN_REFUSAL_FALLBACK. Explicit custom env remains an operator-controlled escape hatch and must not silently override required session controls. Select explicit model through the advertised session model control after every load, checking the returned effective value; launch `acp --model` alone does not prove a resumed session changed. Do not fuzzy-match model labels in Squad or fall back after rejection. No explicit model means the native saved/account default remains in effect.

### 5. Permissions and interaction boundaries

Use advertised ACP configuration controls (category `mode`) and returned values; prefer `session/set_config_option` over deprecated modes. Effective `options.yolo` selects verified native bypass semantics when true and requires normal approval semantics when false. The observed release offers no Normal ACP mode, so false fails before prompting. Code/accept-edits is not a silent substitute; any future change to that mapping requires a documented scope decision. The exact mode IDs must come from versioned fixtures and be offered by the live session; neither labels nor the REPL's `dangerous` argument are proof of an ACP value. Reject missing/ambiguous controls before a prompt. Retain admin deny rules; YOLO does not override them or imply sandboxing.

Map `session/request_permission` into existing pending permissions and `waiting_for_permission`; match the offered option ID/kind when replying. Permit once/reject decisions, and offer persistent grants only if an explicit supported policy is added later: the first version must not expose an 'always' operation that writes native configuration. Never grant solely to avoid a hang. In bypass mode, unexpected native permission requests still require an explicit reply rather than overriding a stricter policy. Invalidate all requests at completion/cancellation and reject stale/foreign/duplicate replies. Unsupported questions/auth/elicitation fail explicitly without fabricated answers.

Do not enable native sandbox from an unverified global flag. Respect existing enforced sandbox policy and errors; document that the first backend does not certify filesystem/network isolation. Do not submit cloud/handoff or account/configuration slash commands. Reject leading executable slash commands in input so `/handoff`, `/cloud`, `/login` or `/loop` are not mistaken for ordinary task text. This is a local launch contract, not a claim to prevent arbitrary user-authorized shell/MCP actions from contacting a cloud service.

### 6. Bound cancellation and background ownership

On context cancellation, send `session/cancel`, invalidate approvals and allow a short grace period (at most two seconds within a total five-second owned cleanup budget), then terminate/kill the owned process group, close pipes and join readers/reap the child. Use the same cleanup after normal completion; no idle ACP process persists. Do not kill by executable name, port or stale PID. Respect the enclosing one-shot shutdown budget.

Live foreground cancellation and background process shutdown were observed on the tested release (record below). The supported private launch profile disables native subagents with subagents_enabled:false; it rejects known hook/MCP/plugin configurations rather than dropping them. Native tool statuses must all be terminal before success. Background work that can escape cleanup must be disabled through a verified native control for the supported profile, or that profile remains unsupported. A failed/uncertain cleanup cannot be reported as successful completion. Process-group cleanup cannot guarantee rollback or termination of arbitrary remote effects, detached services, malicious code or forced parent death; preserve the existing workflow confirmation boundary after restart.

### 7. Reuse installation and catalog infrastructure

Add Devin to factories, backend normalization/registries, machine merge, `internal/modeldiscovery/targets.go` and official links in preflight. `CheckInstallation` resolves the same single executable under effective PATH/cwd; native binary installation does not require Node/Python. Follow existing five-second local checks, deduplication and secret-safe failures. Do not run `--version`, ACP, auth or models in installation checking. A same-name unofficial binary can pass executable presence checks; incompatible protocol fails later before work, with official-product guidance.

Catalog uses `devin models list --format json` in the effective execution context through `modelprobe`. First verify the actual JSON schema and that this command cannot update/migrate native configuration or install binaries in that profile. Native auto-update/import/migration behavior is a compatibility gate; if no safe path is verified, return `unsupported` / `read_only_catalog_unavailable`, never use an ACP session just to list models. Do not call `doctor` or dump raw config to diagnose it.

Project only allowlisted identifiers, display names and context sizes. The observed JSON variant IDs are not the same set as ACP model control values: swe-2-high works, swe-2-medium is rejected as a direct control value. Catalog rows therefore expose no executable recipe and carry selection.supported:false / requires_live_acp_model_option. Execution still accepts manually configured model IDs only when the live session advertises them and the setter confirms them; no suffix-derived thought-level mapping is attempted. Reasoning/speed metadata may be shown without executable mapping. Unknown availability, hidden policy, upstream freshness or pricing stays unknown. Reuse existing result statuses, bounds, redaction, concurrency and exit codes. A missing/unsupported Devin result can make `--all` partial; do not revise other backend behavior.

### 8. Verification gates, not invented APIs

These questions are release blockers for the corresponding capability; answer with official clarification or controlled versioned fixtures. Failure means unsupported behavior or a revised proposal, not a silent weaker contract:

1. Can current Devin create/load local sessions and execute its own tools with no advertised host fs/terminal/elicitation capabilities? Does load honor the original workspace, and can identity/workspace mismatch be detected without reading private databases?
2. What exact mode/model config IDs and values are returned by `session/new` and `session/load` on the supported release? Do setters affect loaded sessions without changing user/project defaults? Is the required normal/bypass policy observable?
3. What structured error fields distinguish auth-required, quota, refusal, lock contention and channel closure? What stop reasons and ordering occur for normal, cancelled and limited turns? Never invent numeric Devin-specific codes.
4. Does cancellation/process exit settle native foreground/background subagents, shell jobs, hooks and MCP children? If any detach, what documented control reliably disables that behavior for this profile?
5. What is the JSON model-list schema, and which ID is exactly selectable? Can listing/ACP startup avoid native auto-update/config migration while retaining the account context? If not, which configurations must fail before launch or report unsupported discovery?

Account-backed work was explicitly authorized by the 2026-10-13 request for small test sessions. The implementation probes used disposable workspaces and short prompts; no numeric spending ceiling was invented. Future opt-in tests consume the operator account quota. Offline fixtures remain synthetic, separately identified from observed evidence.

## Risks / Trade-offs

- [Closed implementation and evolving optional ACP controls] → Keep a compatibility record with observed CLI version/capabilities, use dynamic offered values, fail closed on incompatible changes, and never label synthetic fixtures as live evidence.
- [Native config imports, hooks, automatic updates and migrations] → Verify a launch profile that does not rewrite user/project configuration or install updates; guard unsafe configurations and provide operator guidance. Do not create a private replacement config that silently drops existing account/tool policy.
- [Empty session is not durable in Devin] → Retain a bounded preparation until the first prompt, with mandatory worker cleanup before dispatch failures return. A crash before first submission leaves a saved ID that may require explicit reset.
- [History replay may be large] → Bound frames and retained history metadata, discard historical text after classification, stream only new projected events, and fail rather than truncate successful results.
- [No public complete exit-code taxonomy] → ACP request response and stop reason drive run state; subprocess exit only supplies transport evidence. Version-specific error projection must be tested.
- [Parallel sessions share local files and account quota] → Keep existing workflow-author coordination and account limits; no worktree isolation, hidden model discount or unlimited concurrency promise.

## Migration Plan

Add the backend without rewriting existing squad/machine files. Users install/authenticate independently and opt in with `backend: devin`; examples should set `options.yolo: false` explicitly because Squad defaults to true. Add machine settings only on user request. Publish tested version/profile evidence and remaining unsupported features with the backend. Existing workflow/artifact schemas remain valid, and old binaries predictably reject the new backend. To roll back, stop owned runs, retain state/native histories and use a configuration without Devin; never relabel session IDs or delete history. Do not release until core session, permission, completion and cleanup gates pass; catalog can remain explicitly unsupported if its independent safety gate does not.


## Compatibility and acceptance record — 2026-10-13

Implementation baseline: fresh `origin/main` at `bff3244`, in a separate managed worktree. Native executable: official installed `devin 3000.11.1 (cc4e349ca55e)` on macOS ARM64. No release was published. Inference tests explicitly inherited the existing proxy variables; direct/no-proxy access was not established.

### Observed native behavior

- ACP initialization with protocolVersion 1 and empty clientCapabilities worked. Native read/write/exec tools did not require host filesystem or terminal RPCs. agentInfo reports affogato / Devin Agent / 0.0.0-dev; it is not an installed-version attestation.
- session/new returns a distinct session ID, but an empty session vanishes after process exit. The first version of the adapter caught this through its real smoke test; bounded preparation now preserves the creating process until after Squad persists the ID.
- After one user prompt, a new process can session/list and session/load the explicit ID. macOS list filtering requires canonical /private/var paths, not the /var alias. History precedes the load response. A concurrent load returned native -32015 (locked); there was no fallback.
- A first short prompt returned SQUAD_OK/end_turn, then a fresh process recalled it. Native tools wrote/read a disposable file. Code-mode exec paused at session/request_permission; an exact offered allow_once response let it complete. No persistent permission was selected.
- Advertised mode values were accept-edits (Code), ask, plan and bypass. Normal was absent. False is explicitly unsupported on this release, tested to fail before prompting; Code was not substituted. Bypass was set and verified on new and resumed sessions.
- model config control accepted swe-2-high. A configuration-only follow-up changed thought_level to medium and then selected swe-2-high again; the returned thought_level reset to high, confirming that this advertised selection is applied rather than leaving the prior thought setting behind. It rejected swe-2-medium with -32602 despite that ID appearing in the JSON catalog. Deprecated session/set_model returned -32601. A separate thought_level control offered medium/high/max. No execution mapping is inferred from names; catalog recipes remain unsupported.
- Native session/cancel interrupted a foreground sleep and returned cancelled, including terminal_exit evidence. A 100-ms exec timeout left a background sleep in_progress while the prompt returned end_turn. Closing native stdin stopped that process (the recorded PID was absent immediately after exit). Such a prompt is deliberately failed by Squad rather than accepted as a completed dependency.
- Session-free models list --format json succeeded through the same private profile. Its recognized shape is families[].variants[] with model_uid, label and optional max_context_tokens. Other metadata is dropped; hidden coverage, freshness and inference verification are not inferred.
- The final opt-in Go test passed: session-free catalog, two short real turns through Init/Send, exact model control and continuity. A SHA-256 comparison confirmed the native user config stayed unchanged. Earlier raw probes disabled imports; the final Go test retained import policy and used the guarded production profile.

Sanitized observed wire examples (identities replaced):

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"example-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"SQUAD_OK"}}}}
{"jsonrpc":"2.0","id":6,"result":{"stopReason":"end_turn"}}
{"families":[{"family_uid":"swe-2","variants":[{"model_uid":"swe-2-high","label":"SWE-2 High","max_context_tokens":262000}]}]}
```

### Supported profile and evidence boundaries

The private config is a semantic copy (0600 inside 0700 directory) with auto_update:false and subagents_enabled:false, both documented native controls. Existing account/proxy/permission/import values are retained. Legacy mcpServers, project lifecycle overrides, native/imported hook/MCP/plugin configuration paths are rejected before launch. This is intentionally conservative even for empty or disabled extension files. Administrator-managed extensions are outside the tested profile. Native Linux inference and arbitrary external extensions were not exercised; POSIX ownership and forced cleanup are covered by offline fixtures. Forced process teardown reports cleanup uncertainty instead of success. Detached malicious processes and remote effects are not reversible guarantees.

The official [permission reference](https://docs.devin.ai/cli/reference/permissions), [configuration reference](https://docs.devin.ai/cli/reference/configuration/config-file), [import reference](https://docs.devin.ai/cli/reference/configuration/read-config-from), and [hooks reference](https://docs.devin.ai/cli/extensibility/hooks/overview) establish documented controls and configuration locations. ACP's [error schema](https://agentclientprotocol.com/protocol/v1/schema) establishes -32000 authentication-required, -32002 resource-not-found and -32800 cancelled. Native -32015 was observed. All other RPC failures remain generic safe failures; no invented quota/retry code is used.

### Offline acceptance map

- Local compatible execution / unsafe commands / unsupported callbacks: devin TestProtocolFailures, TestSlashCommandsNeverReachPrompt, TestInstallationNeverExecutes and TestInstallationUsesEffectivePathAndOfficialLink.
- Conversation ownership / replay / lost or locked sessions / lazy reset: TestContinuityAndLazyReset, TestMissingAndForeignSessionsDoNotRecreate, TestFailedFirstTurnDoesNotDuplicateStartup and TestPreparationCancellationAndExpiry; existing orchestrator owned/retry/recovery tests verify shared workflow ownership behavior.
- Durable pre-dispatch identity: orchestrator TestSessionPersistenceFailureStopsDispatch injects storage failure after allocation in manual and owned paths, asserts zero Sends, preparation cleanup, reported failure and unchanged prior durable state.
- Required selection / approval: TestExactModelMustBeOffered, TestUnavailableNormalModeFailsBeforePrompt and TestPermissionDecisions exercise supported offered IDs, foreign/stale replies, once/reject and refusal of persistent grants. Duplicate/foreign permission callbacks are protocol failures.
- Completion, partial evidence and cleanup: TestProtocolFailures, TestStderrFloodAndCancellation, TestOwnedCleanupWithDescriptorHoldingChild and TestForcedTeardownReportsUncertainty cover malformed/oversized streams, wrong IDs, empty/refusal/limit/cancelled results, early EOF, unsettled tools, descriptor-holding children and an unrelated control process.
- Account/config/diagnostic boundaries: TestProfilePreservesPolicyWithoutMutation, TestUnsafeProfileRejected, TestChunkedSecretsNeverReachArtifacts and secret-bearing RPC fixtures. Assistant chunks are assembled before scrubbing; only activity markers stream until final/partial text is projected.
- Catalog/configuration/environment: TestModelCatalogProjection, TestCatalogLaunchCreatesNoSession, TestEnvironmentIsExplicitAndConsistent, config TestDevinOptionsValidation / TestDevinMachineDefaults / TestDevinExampleParses, and existing modeldiscovery aggregate tests with the added sixth real backend. Known empty/unknown/malformed shapes are distinguished; no session fallback or fabricated reasoning recipe is used.

This record distinguishes native observations, documented controls and synthetic fixtures. It does not claim all account failures or every possible native configuration were exercised.


Final validation: gofmt applied to changed Go files; `go vet ./...`, `go test -race -count=1 ./...`, the opt-in native continuity test, `git diff --check` and `openspec validate add-devin-acp-backend --strict` passed. The live test was repeated after increasing graceful exit allowance to two seconds; forced/nonzero teardown remains a failure. Implementation branch: `feat/devin-acp-backend` from bff3244. This records the initial implementation validation. Subsequent release preparation synchronizes main specs, archives the change, and commits the implementation; publishing remains a separate step.
