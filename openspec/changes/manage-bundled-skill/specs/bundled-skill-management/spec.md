## Purpose

Provide explicit, portable installation and safe reconciliation of the operating skill bundled with Agent Debug Squad, with verifiable content identity and ownership across local agent hosts.

## ADDED Requirements

### Requirement: Every binary carries its matching complete skill bundle

Every supported release and development binary SHALL contain the complete canonical `skills/agent-debug-squad` payload, including nested and hidden resources, independently of a source checkout or network. The payload SHALL use portable name/description frontmatter, relative resource references, a description no longer than 1024 characters, and SKILL.md smaller than 100 KB. Installation SHALL NOT require host-specific frontmatter rewriting or embed machine paths. Existing model-discovery and reviewer-quorum guidance SHALL remain present.

Bundle identity SHALL be a deterministic versioned SHA-256 tree hash covering each relative path, exact bytes and normalized executable bit, independent of timestamps, ownership, build platform and program version. Program version, commit and build date SHALL be separate provenance. Build verification SHALL reject symlinks, special files, traversal paths, case-fold collisions and reserved management receipt names in the canonical payload.

#### Scenario: Offline installation outside a checkout
- **WHEN** a release binary is copied to a supported clean host without the repository or network
- **THEN** an explicit skill installation can materialize every bundled resource with the same bundle hash as the release on another supported architecture

#### Scenario: Resource changes affect identity
- **WHEN** a resource is renamed, deleted, added, edited or changes executable intent
- **THEN** the bundle hash changes; changing only build date or source file timestamps does not change it

#### Scenario: Nonportable source tree
- **WHEN** the source bundle contains a symlink or two relative names differing only by case
- **THEN** the build validation fails before distribution instead of silently omitting or overwriting a resource

### Requirement: Installation selects explicit facilitator destinations

The CLI SHALL provide `skill install` with one or more `--agent NAME` selectors OR one absolute `--path DIR`. DIR SHALL be the exact directory ending in `agent-debug-squad`, not its parent. No selector, unknown agents, relative/custom paths with traversal, conflicting selectors or extra operands SHALL exit 2 before mutations. Supported presets SHALL be `codex`, `claude-code`, `cursor`, `opencode`, `zcode`, `kimi` (current Kimi Code), and `kimi-cli` (legacy). Presets SHALL select the roots and supported overrides in the agent matrix below. Other local skill hosts SHALL use exact custom paths with explicitly unknown discovery compatibility.

| Preset | Default root | Supported override |
|---|---|---|
| codex | `~/.agents/skills` | None for this root; CODEX_HOME only informs legacy observations |
| claude-code | `~/.claude/skills` | CLAUDE_CONFIG_DIR + `/skills` |
| cursor | `~/.agents/skills` | None |
| opencode | `~/.config/opencode/skills` | XDG_CONFIG_HOME + `/opencode/skills`; OPENCODE_CONFIG_DIR + `/skills` preferred when set |
| zcode | `~/.zcode/skills` | None |
| kimi | `~/.kimi-code/skills` | KIMI_CODE_HOME + `/skills` |
| kimi-cli | `~/.kimi/skills` | None; KIMI_SHARE_DIR does not relocate skills |

Nonempty supported overrides SHALL be absolute and invalid overrides SHALL fail without fallback. Resolved destinations SHALL be frozen in the registration. Only local macOS/Linux installation SHALL be supported; unsupported platforms SHALL fail without writes. Backend YAML SHALL NOT imply installation selection. Commands SHALL NOT launch agents, send prompts, read authentication material, or enable host settings.

#### Scenario: Explicit host differs from backend list
- **WHEN** the caller installs for claude-code while the squad YAML uses Codex and OpenCode workers
- **THEN** only the selected Claude skill destination is considered for installation and no backend is started or configured

#### Scenario: Profile-specific roots
- **WHEN** the caller selects Claude Code, OpenCode or current Kimi with a valid corresponding root override
- **THEN** the exact overridden root is used and recorded, while later sync under a different environment continues using the recorded destination

#### Scenario: Misleading override
- **WHEN** CODEX_HOME or KIMI_SHARE_DIR is set and the respective preset is selected
- **THEN** the documented preferred root is not relocated by that variable, and no unsupported root is silently invented

#### Scenario: Custom project installation
- **WHEN** an explicit safe project path ending in agent-debug-squad is selected
- **THEN** only that path is registered and the command reports that agent discovery is the caller's responsibility

### Requirement: Selection minimizes managed duplicates without claiming host precedence

Preset resolution SHALL consider the selected hosts together in deterministic order. It SHALL first reuse exactly one eligible registered global destination, then reuse a selected shared preferred root for compatible selected hosts before creating another copy. Cursor SHALL allow reuse of registered user `.agents`, `.cursor`, `.claude` and `.codex` skill roots; OpenCode SHALL allow effective native roots and documented `.agents`/`.claude` user roots; current Kimi SHALL allow its native root and `.agents`; Codex and Claude SHALL use their effective documented roots, ZCode and legacy Kimi their native roots. More than one eligible registration SHALL report ambiguous_destination and require an exact path. Only explicitly selected host labels SHALL be recorded.

An unmanaged same-name copy in an eligible observed root SHALL block preset installation instead of causing an additional copy or automatic adoption. Bounded observation SHALL cover known user roots and known project roots between CWD and repository root, including the legacy Codex root. It SHALL report discovered duplicates with paths and unknown effective selection, never invent a universal precedence rule. No full-home scan or recursive plugin-cache scan SHALL be required. Explicit custom-path installation SHALL disclose known collisions but SHALL NOT modify those copies.

#### Scenario: Shared root serves four hosts
- **WHEN** Codex, Cursor, OpenCode and current Kimi are selected on a clean default layout
- **THEN** one managed `.agents/skills/agent-debug-squad` copy serves those four selections and no native duplicate is created

#### Scenario: Claude also selected
- **WHEN** Codex and Claude Code are selected on a clean default layout
- **THEN** separate documented roots are registered, and possible cross-host duplicate visibility is disclosed without deleting either required copy

#### Scenario: Ambiguous existing managed copies
- **WHEN** two registered roots eligible for Cursor already exist and install selects Cursor without an exact path
- **THEN** installation reports ambiguous_destination and does not choose by modification time or silently overwrite one

#### Scenario: Shadowing outside management
- **WHEN** status observes a same-name project copy beside a global managed copy
- **THEN** it reports duplicate_or_shadowed with effective_selection unknown and does not edit the project copy or claim which instructions the agent currently uses

### Requirement: Ownership requires explicit registration or adoption

A private schema-versioned per-user registry outside all skill discovery directories SHALL record canonical destinations, stable registration identities, selected hosts, managed relative file inventory/hashes, payload identity and build provenance. Each installation SHALL carry a matching reserved receipt. A receipt alone SHALL NOT grant ownership. Missing registry, malformed registry and unsupported schema SHALL be distinguishable; malformed or unsupported state SHALL fail closed without replacing it with empty state. Missing registry inspection SHALL be read-only.

An existing unregistered directory SHALL require `skill install --path DIR --adopt`, even when byte-identical. Adoption SHALL verify a regular SKILL.md with matching name and a safe tree, and create a verified private full-tree backup before changes. Only paths in the incoming bundle SHALL become managed; other legacy files SHALL stay unowned with diagnostics. Known plugin-controlled destinations SHALL be rejected even with adopt. Unregistered project/plugin copies SHALL never be automatically changed.

#### Scenario: Identical manual installation
- **WHEN** preset installation encounters an unmanaged byte-identical skill
- **THEN** it reports unmanaged_conflict and requires explicit path adoption without treating the hash as ownership evidence

#### Scenario: Adoption preserves legacy extras
- **WHEN** the user explicitly adopts a safe manual copy containing extra files
- **THEN** a verified backup is created, bundled paths become managed, and extras remain byte-for-byte unowned with their paths reported

#### Scenario: Registry lost but receipt remains
- **WHEN** registry is absent and an old receipt remains at a destination
- **THEN** status reports unregistered ownership and sync does not reconstruct or take over that destination automatically

#### Scenario: Unsupported future registry
- **WHEN** a binary reads a registry schema newer than it supports
- **THEN** it reports unsupported_registry without deleting state, changing destinations or downgrading the schema

#### Scenario: Plugin adoption requested
- **WHEN** an explicit adoption targets a recognized host plugin cache or marketplace-controlled directory
- **THEN** it fails with instructions to detach/export the copy first and makes no installation changes

### Requirement: Synchronization reconciles the whole owned bundle conservatively

`skill sync` SHALL work offline on all registered destinations, or only an existing registration selected by exact `--path`. It SHALL compare actual contents with both the previous managed inventory and the calling binary's bundle. It SHALL install added managed resources, replace changed managed resources, remove previously managed resources absent from the new bundle, and restore missing managed resources or an entirely missing registered tree. Unknown files SHALL be preserved and SHALL NOT become managed implicitly.

Before replacing/removing locally modified managed data or repairing a missing/invalid receipt in an existing owned directory, sync SHALL create and verify a full-tree backup archive outside known discovery roots. Backup failure SHALL leave that destination unchanged. A newly bundled path colliding with an unowned file or a structural file/directory conflict SHALL block that destination without moving/deleting the conflicting data. A foreign receipt identity or unsafe tree SHALL be ownership_conflict, not repair. When all owned files match, sync SHALL avoid rewriting payload files; updating provenance alone SHALL not be reported as a skill-content change.

#### Scenario: Removed obsolete managed resource
- **WHEN** the prior manifest owns references/old.md and the incoming bundle omits it
- **THEN** sync removes it while preserving an unrelated local notes.txt, and backs up old.md first if it was locally edited

#### Scenario: Missing skill with current executable
- **WHEN** a registered destination is absent and the binary already matches the installed release
- **THEN** explicit sync recreates the complete bundle and reports repaired with the SKILL.md path

#### Scenario: Added bundle file collides with local file
- **WHEN** a new bundled templates/example.yaml path already exists but was never managed
- **THEN** that destination reports unowned_collision, retains all old files and suggests resolving the conflict before retrying

#### Scenario: Edited managed instructions
- **WHEN** SKILL.md differs from its previous managed hash
- **THEN** a verified private backup preserves its old bytes before replacement, and the report names the backup and updated skill path

#### Scenario: Backup cannot be saved
- **WHEN** a required backup fails due to permissions or disk space
- **THEN** no live files or committed ownership records for that destination change and the report identifies backup_failed

#### Scenario: No-op payload
- **WHEN** all managed files and executable bits match the desired bundle and no ownership repair is needed
- **THEN** sync leaves payload file timestamps unchanged, reports changed=false and does not request a reread

### Requirement: Mutations are serialized and crash-recoverable per destination

Managing processes SHALL serialize registry mutations with an OS-released exclusive lock and report busy promptly on contention. Each destination SHALL publish an entire staged verified tree atomically with a matching receipt; unsupported atomic replacement or unsafe/cross-filesystem staging SHALL fail before modifying the live tree. Staged trees and archived backups SHALL remain outside known and registered discovery roots. The registry SHALL be atomically published with durable recovery evidence linking its generation to each destination replacement. No global atomicity across executable and destinations or snapshot across a host's separate reads SHALL be claimed.

Before later mutations, recovery SHALL reconcile pending transactions by identities and contents, complete proven published transactions, discard proven unpublished staging, and fail with recovery_conflict when state is ambiguous or externally edited. It SHALL never blindly replay over changed files. Cancellation SHALL stop before the next destination; cancellation after tree publication SHALL preserve enough durable state to finish/recover registry publication. Independent destination failures SHALL preserve successful outcomes and allow remaining destinations while uncancelled; corrupt registry is a global failure.

#### Scenario: Two writers
- **WHEN** one skill mutation holds the registry lock and another begins
- **THEN** the second reports busy without changes and a later retry can succeed after the first exits, including after a crash

#### Scenario: Crash after tree publication
- **WHEN** a crash occurs after the new tree becomes visible but before registry commit
- **THEN** status reports pending_recovery and the next mutation verifies the journal and new tree before committing the matching registry without reinstalling old content

#### Scenario: Post-crash external edit
- **WHEN** a pending transaction's destination matches neither verified old nor new state
- **THEN** recovery reports recovery_conflict, preserves evidence and does not overwrite the external edit

#### Scenario: Unsupported filesystem
- **WHEN** a destination filesystem cannot provide the required atomic publication or safe staging location
- **THEN** the destination remains unchanged and reports atomic_replace_unsupported or unsafe_staging without falling back to in-place copying

#### Scenario: One target fails and another succeeds
- **WHEN** one registered target is unwritable and another is valid
- **THEN** sync reports individual failure and success with aggregate partial, retains the successful commit and provides a retry for the failed path

### Requirement: Paths and ownership inventories cannot escape their boundaries

Mutations SHALL reject symlink destinations or descendants, hard-linked managed tree files, special files, invalid/traversing manifest paths, overlapping/nested destinations, and overlaps with management state or staging. Canonical ancestor identities SHALL be checked and no-follow directory-relative operations SHALL prevent redirection through changed components. Group/world-writable mutation parents SHALL be rejected. Stable symlinked ancestors SHALL resolve once to recorded canonical paths; destination symlinks SHALL never be followed or automatically converted to copies. Unknown files SHALL be subject to safe-type checks before staging, without reading outside the destination. Detected concurrent external edits SHALL abort before publication. The boundary SHALL explicitly exclude hostile processes with the same user credentials.

#### Scenario: Symlink import
- **WHEN** a ZCode imported skill or custom destination is a symlink to another skill tree
- **THEN** install/adopt/sync refuses to manage through the link and explains that a regular owned copy is required

#### Scenario: Traversal in stored inventory
- **WHEN** registry inventory contains ../outside or an absolute managed filename
- **THEN** validation fails without reading, deleting or rewriting that outside file

#### Scenario: Ancestor replaced during mutation
- **WHEN** the destination's canonical parent identity changes before publication
- **THEN** the operation fails without following the replacement into another tree

#### Scenario: Nested registrations
- **WHEN** a requested exact path overlaps an existing registered destination or management state root
- **THEN** it is rejected before registration or copying

### Requirement: Status and results are honest and machine-readable

`skill status` SHALL inspect actual hashes read-only and classify not_registered, current, outdated, missing, modified, conflict and pending_recovery states, without host execution or repair. Default status SHALL inspect registrations and bounded observations; selectors SHALL filter that scope. All skill commands SHALL accept `--json` and return one schema_version 1 envelope with program build/hash, aggregate status, sorted destination results, observations and optional global error. Destination results SHALL identify absolute paths, selected agents, before/after identity when known, state/action, stable diagnostic code, changed and reread_required, backup path, preserved unowned paths and actionable retry when relevant. Human output SHALL convey equivalent outcomes; JSON stdout SHALL contain no progress prose or raw child output.

Skill commands SHALL exit 0 for successful requested state including no-op and a no-registrations inspection, 1 for detected registered drift or operational/partial/conflict failure, and 2 for argument errors. An unregistered observation SHALL not masquerade as a managed success. A successful changed/repaired payload SHALL report program version and absolute SKILL.md path with a request to reread it; the command SHALL NOT claim to reload the agent's context. No payload contents, secrets, credential material or raw environment SHALL be emitted. Human terminal control characters SHALL be escaped.

#### Scenario: Structured partial result
- **WHEN** JSON sync repairs one destination and fails another
- **THEN** stdout contains one parseable envelope with aggregate partial, both destination results and exit 1

#### Scenario: Nothing installed
- **WHEN** status runs with no registry
- **THEN** it exits 0 with not_registered and installation guidance, creates no management files, and does not claim any skill was updated

#### Scenario: Reload notification
- **WHEN** a destination's skill payload changes successfully
- **THEN** the report names the program version and SKILL.md path and asks the facilitator to reread it, with refresh/restart guidance when needed and no context-reload guarantee
