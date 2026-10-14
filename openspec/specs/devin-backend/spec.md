# devin-backend Specification

## Purpose

Coordinate Cognition's official local Devin CLI through explicit ACP turns while preserving independent conversations, local workspace access and conservative lifecycle guarantees.

## Requirements

### Requirement: Local compatible Devin execution
The system SHALL accept backend `devin` for the official local CLI on supported macOS/Linux hosts. It SHALL use Squad's absolute workspace as the child and session working directory and require a compatible ACP protocol with session loading before submitting work. It SHALL NOT select cloud relay, adopt a cloud API client, silently fall back to print mode, or claim local installation checking proves protocol compatibility. Unsupported required capabilities SHALL fail with safe official-product guidance. Leading executable slash commands SHALL be rejected as unsupported input rather than invoking cloud, account, configuration or autonomous-loop commands.

#### Scenario: Official local session
- **WHEN** a compatible Devin backend starts a checked turn
- **THEN** its session uses the same local workspace and readable dependency artifact paths as Squad without creating a cloud workspace

#### Scenario: Unofficial executable with the same name
- **WHEN** an executable named devin exists but cannot establish the required ACP contract
- **THEN** work fails before any prompt and no alternate API or executable is tried

#### Scenario: Cloud command in task input
- **WHEN** a submitted message starts with an executable `/handoff` or `/cloud` command
- **THEN** the adapter rejects it without passing the command to Devin

### Requirement: Persisted independent conversations
Manual turns SHALL preserve a native session identity across operations and restarts. A newly allocated identity SHALL be durably saved before its first prompt; persistence failure SHALL prevent prompt submission. Workflow attempts and explicit workflow retries SHALL use fresh identities distinct from manual and other attempt conversations. Startup instructions SHALL be included only in an empty conversation. Historical load output SHALL NOT be presented as new response text. Missing, locked, wrong-workspace or unloadable saved sessions SHALL fail without replacement or recency-based adoption. Reset SHALL preserve old history and defer replacement until a checked turn.

#### Scenario: Subsequent manual turn
- **WHEN** a second turn loads the saved session and history is replayed
- **THEN** the ID remains unchanged and only the new turn's assistant text appears in its result

#### Scenario: Initial identity cannot be saved
- **WHEN** native session creation succeeds but Squad cannot persist its identity
- **THEN** no prompt is sent and the operation reports storage failure

#### Scenario: Retry after interrupted workflow attempt
- **WHEN** the caller explicitly confirms a permitted workflow retry
- **THEN** the retry gets a fresh conversation and the interrupted attempt is never automatically replayed

#### Scenario: First turn failed after submission
- **WHEN** an existing conversation contains a submitted user turn but no successful LastRunID
- **THEN** a later explicit manual turn does not duplicate startup instructions merely because LastRunID is empty

#### Scenario: Reset is lazy
- **WHEN** an idle Devin agent is reset
- **THEN** its current identity is cleared, native history remains intact and no replacement conversation is created until checked work begins

### Requirement: Owned turn completion is explicit
Success SHALL require the matching prompt response indicating normal end of turn, new nonempty assistant text, and completed owned cleanup. The system SHALL stream bounded owned events and preserve partial artifacts on failure. Historical text, thoughts, tool output, unrelated sessions, process exit zero and inactivity SHALL NOT establish successful completion. Refusal, token/turn limits, cancellation, transport loss, malformed data, unsupported required interactions and storage errors SHALL remain failures with distinguishable safe diagnostics. Uncertain prompts SHALL never be automatically resent, including after an error marked retryable.

#### Scenario: Process exits cleanly too soon
- **WHEN** Devin exits zero after partial text without the matching prompt completion
- **THEN** the run fails and retains the partial artifacts without releasing a successful dependency result

#### Scenario: Completion with a limit
- **WHEN** the prompt response reports a token limit despite useful output
- **THEN** the run is failed with a limit diagnostic rather than completed

#### Scenario: Retryable channel closure
- **WHEN** a transport error marked retryable follows prompt submission
- **THEN** the adapter reports uncertainty without resending the prompt or starting a replacement conversation

### Requirement: Verified selection and permission controls
Configured Devin agents SHALL require a nonblank model. Model, reasoning, speed and mode SHALL be applied and verified on new and resumed sessions before prompting. Defaults SHALL be high reasoning when offered (otherwise the advertised native default), standard speed, and bypass mode with yolo true compatibility. Unsupported explicit choices or conflicting mode/yolo SHALL fail before prompting. Permission replies SHALL remain run-scoped offered once/reject decisions, without persistent grants.

Selections SHALL NOT override administrator restrictions. Sandbox, cloud and arbitrary CLI arguments SHALL remain unsupported.

Native permission requests SHALL be visible through the existing run-scoped permission interface with only supported offered decisions. The initial backend SHALL expose no persistent grant that mutates native configuration. Foreign, duplicate, inactive and stale replies SHALL be rejected. Questions or authentication interactions that Squad cannot represent SHALL fail explicitly without fabricated responses. YOLO SHALL NOT imply OS sandboxing or permission to override unexpected native approval requests.

#### Scenario: Resumed session uses another model
- **WHEN** explicit options.model differs from a loaded session's saved model
- **THEN** the requested selection is applied and verified before prompting, or the operation fails without fallback

#### Scenario: Manual approval
- **WHEN** a normal-mode owned tool requires permission
- **THEN** progress exposes waiting_for_permission and an offered once or reject reply is translated to the exact native option

#### Scenario: Approval arrives after cancellation
- **WHEN** the run is cancelled before a pending reply arrives
- **THEN** the reply is rejected and cannot authorize another run or native session

#### Scenario: Required mode cannot be established
- **WHEN** the installed CLI does not offer an identifiable supported normal or bypass control requested by the configuration
- **THEN** no prompt is submitted and the error explains the unsupported capability

#### Scenario: Missing model
- **WHEN** a configured Devin agent omits model or supplies a blank or non-string model
- **THEN** configuration fails before any native session; standalone model discovery still works without model

#### Scenario: Default controls
- **WHEN** only model is configured
- **THEN** the adapter selects and verifies that model, chooses high reasoning if offered or the offered native default, standard speed, and bypass mode

#### Scenario: Model-specific controls
- **WHEN** selecting a model changes the offered reasoning or speed controls
- **THEN** subsequent choices use the returned controls and the final response still confirms all requested controls before any prompt

#### Scenario: Model lacks optional controls
- **WHEN** a model has no reasoning or speed selector
- **THEN** omitted reasoning and standard speed are valid, but explicit reasoning or fast speed fails before prompting

#### Scenario: Explicit mode and compatibility alias
- **WHEN** mode is explicit and yolo is omitted
- **THEN** mode wins over the implicit or inherited yolo default
- **WHEN** both are explicit
- **THEN** true is compatible only with bypass and false only with non-bypass modes; contradictory pairs fail configuration

#### Scenario: Legacy yolo false
- **WHEN** yolo is false and mode is omitted
- **THEN** normal mode is required without substituting accept-edits, and absence of normal fails before prompting

#### Scenario: Later control resets an earlier one
- **WHEN** a setter resets an already selected model, reasoning, speed or mode
- **THEN** final verification rejects the turn without sending its prompt

### Requirement: Bounded owned work and honest cleanup
Cancellation, force reset, startup failure and normal turn teardown SHALL close and reap owned processes/readers within a five-second cleanup budget subject to the enclosing shutdown budget. Cancellation SHALL request native cancellation when possible before terminating the owned process group. Independent Devin processes and sessions SHALL remain untouched. Background work SHALL NOT intentionally survive its turn; any native feature that cannot be settled or disabled through a verified supported profile SHALL be unsupported. Cleanup failure or uncertainty SHALL prevent successful completion. The system SHALL NOT claim that stopping a local process reverses or proves cessation of arbitrary remote side effects.

#### Scenario: Descriptor-holding child
- **WHEN** an owned shell descendant holds output pipes after the main process exits
- **THEN** cleanup terminates the owned group, closes and joins readers and finishes within its budget or reports cleanup failure

#### Scenario: Unrelated interactive session
- **WHEN** Squad cancels its Devin operation while a separate user Devin session is active
- **THEN** only Squad's owned resources are stopped

#### Scenario: Background completion is uncertain
- **WHEN** a native prompt ends while owned background work cannot be settled or accounted for
- **THEN** the run does not report success or promise all work has stopped

### Requirement: Native account and diagnostic boundaries
Squad SHALL NOT log in, install/update the CLI, rewrite native user/project configuration or auto-switch models to repair failures. It SHALL use only a verified launch profile compatible with those boundaries and fail safely for known unsafe startup mutation behavior. Authentication and entitlement SHALL remain separate from executable availability. Credentials, proxy secrets and raw authentication/configuration messages SHALL NOT reach logs, saved diagnostics or model output envelopes. Documentation SHALL distinguish local tools from remote inference, plan/quota consumption, configuration imports, unsupported sandbox guarantees and the tested release/profile.

#### Scenario: Authentication is required
- **WHEN** a compatible installed CLI reports an explicit authentication requirement
- **THEN** the run fails with operator login guidance without opening a browser, starting authentication or persisting credential text

#### Scenario: Startup would migrate native configuration
- **WHEN** the selected launch profile is known to rewrite native configuration and no verified non-mutating launch is available
- **THEN** the backend refuses that profile with migration guidance rather than modifying the files

#### Scenario: Secret-bearing backend error
- **WHEN** backend diagnostics include a configured credential or credentialed proxy value
- **THEN** persisted diagnostics and public errors contain only safe classified information

### Requirement: Empty native preparation is bounded
When the native CLI does not retain an empty session after process exit, Init SHALL retain its creating process only until the first checked Send, cancellation, explicit worker cleanup or a 30-second preparation expiry. A pre-dispatch storage failure SHALL release the retained preparation. An expired or crash-lost identity SHALL NOT be silently recreated.

#### Scenario: Empty process must survive the persistence gate
- **WHEN** Squad persists the newly allocated ID successfully and starts the first Send
- **THEN** Send uses the prepared native process, and all later completed-turn processes are closed

#### Scenario: Preparation is abandoned
- **WHEN** persistence fails, the operation is cancelled or preparation expires before Send
- **THEN** the native process and private config are released without submitting a prompt

### Requirement: Content-aware extension inspection
Devin profile preparation SHALL allow known configuration files containing only unrelated settings, metadata or empty extension collections. It SHALL reject active unsupported extensions with the source path and schema key, without exposing configuration values. Malformed or unreadable relevant sources SHALL fail with a source-specific diagnostic. Native legacy MCP migration guards SHALL remain enforced.

#### Scenario: Claude metadata exists
- **WHEN** ~/.claude.json contains metadata and project state without relevant MCP configuration
- **THEN** its existence does not block preparation and the file remains unchanged

#### Scenario: Imported extension is active
- **WHEN** an enabled import contains an active MCP server or unsupported Claude hook
- **THEN** preparation fails with the file path and relevant schema key, without emitting commands, credentials or custom names

#### Scenario: Disabled MCP server
- **WHEN** a known standard MCP entry has disabled true or an OpenCode entry has enabled false
- **THEN** that entry does not block preparation

#### Scenario: Malformed source
- **WHEN** a relevant external file contains invalid JSONC or is not a readable bounded regular file
- **THEN** preparation fails with its path and a classified error without raw content

#### Scenario: Claude plugin metadata does not establish imported extensions
- **WHEN** user or project Claude settings contain enabledPlugins entries with imports enabled
- **THEN** those entries alone do not block preparation, import policy and source files remain unchanged, and coexisting active hooks or MCP are still rejected

### Requirement: Import-aware extension inspection
External extension checks SHALL respect effective read_config_from switches, defaulting to enabled and applying ancestor project and local overrides after user settings. Disabled sources SHALL not block preparation. Squad SHALL preserve native import policy and SHALL not disable imports automatically to make a run pass.

#### Scenario: Disabled external source
- **WHEN** effective read_config_from.claude is false and Claude configuration contains extensions
- **THEN** those external files do not block preparation and the disabled import policy is preserved

#### Scenario: Project re-enables import
- **WHEN** user configuration disables an import and a higher-priority project/local setting enables it
- **THEN** active extensions from that source are checked and rejected before launch
