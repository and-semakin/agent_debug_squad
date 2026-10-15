## Purpose

Keep registered operating skills aligned with the executable after explicit or automatic updates, while preserving version authority, startup opt-outs and accurate partial-failure reporting.

## ADDED Requirements

### Requirement: Explicit update also reconciles registered skills

`agent-debug-squad update` SHALL retain release checking and checksum-verified binary installation and SHALL additionally reconcile registered skills. When no newer binary exists, it SHALL still attempt offline repair with the current bundle. A release-check or pre-replacement download failure SHALL still permit local skill reconciliation using the unchanged binary, while the binary error remains visible and the overall command fails. An ambiguous executable replacement outcome SHALL NOT authorize old-payload synchronization; it SHALL report uncertainty and an explicit recovery/retry command. No registrations SHALL be reported as not_registered with install guidance, not as updated. Update SHALL NOT discover-and-enroll skill installations or select hosts from backend YAML.

#### Scenario: Latest binary and stale instructions
- **WHEN** release checking finds no newer binary but a registered skill is missing or outdated
- **THEN** update repairs that skill and reports binary unchanged separately from skill repaired/updated

#### Scenario: Network unavailable
- **WHEN** the release check fails and the current binary's bundle can repair a registered destination
- **THEN** update attempts and reports the local repair while retaining the release-check failure and nonzero exit

#### Scenario: First update before installation
- **WHEN** the binary updates successfully but no skill destinations are registered
- **THEN** the report identifies the new program version, explicitly says no managed skills were synchronized, and suggests explicit skill install

#### Scenario: Replacement result uncertain
- **WHEN** executable replacement cannot establish which image is installed
- **THEN** update reports binary uncertainty and skipped skill synchronization instead of writing instructions from an unverified image

### Requirement: Reconciliation after replacement uses the new executable

After binary replacement, only the newly installed executable SHALL supply the bundle and perform skill synchronization. The parent SHALL invoke the resolved installed executable with a 60-second deadline or the earlier caller deadline, capture its structured report and verify the expected release identity before accepting success. It SHALL never fall back to its old embedded bundle after handoff failure. Competing executable updates SHALL be serialized through replacement and synchronization handoff, with a consistent executable-before-registry lock order; the registry lock SHALL not be held across downloads or child execution.

Child launch, timeout, incompatible output or synchronization failure SHALL leave an already installed new executable in place, report the separate skill failure/uncertainty, preserve successful destination commits and give a retry using the installed executable. Automatic rollback of the executable SHALL NOT occur solely because skill sync failed. Cancellation SHALL terminate/join the handoff child and retain recoverable skill transaction state without starting backend work.

#### Scenario: Old and new bundles differ
- **WHEN** release A replaces itself with release B and their bundles differ
- **THEN** destinations receive B's bundle and the accepted child report identifies B rather than A's in-memory version

#### Scenario: Child cannot run
- **WHEN** replacement succeeds but the new executable cannot start its synchronization command
- **THEN** update reports binary installed, skills failed/unknown and an explicit retry, without synchronizing A's payload or claiming combined success

#### Scenario: Concurrent updates
- **WHEN** two processes attempt to replace the same executable concurrently
- **THEN** only one owns replacement and its handoff at a time, and the other reports busy without racing skill authority

#### Scenario: Handoff cancellation
- **WHEN** the caller cancels during new-binary sync
- **THEN** completed destination commits remain reported, unfinished targets are cancelled/unknown, the child is joined, and later explicit sync can reconcile any pending journal

### Requirement: Automatic serve synchronization respects existing opt-outs

For a release build, eligible `serve` startup SHALL perform update/reconciliation before configuration-driven backend startup. Without binary replacement it SHALL reconcile registrations even when release checking finds no update or fails. After replacement it SHALL synchronize using the new executable and re-exec once, preventing a duplicate startup update cycle through a one-use internal continuation mechanism rather than persisting the public opt-out.

`serve --no-auto-update` and `AGENT_DEBUG_SQUAD_NO_AUTO_UPDATE=1` SHALL disable both automatic network update work and automatic skill writes. They SHALL NOT disable explicit update or skill sync. `update --no-auto-update` SHALL remain an invalid invocation. Automatic skill failures SHALL warn with paths/retry instructions and allow normal service startup; they SHALL NOT become backend preflight failures. Failure to re-exec after a successful replacement SHALL fail startup instead of continuing to serve from the old process. Development serve SHALL perform neither automatic update nor automatic skill sync.

#### Scenario: Disabled automation
- **WHEN** serve is started with its opt-out flag or environment value 1
- **THEN** it neither checks releases nor mutates registered skills, while a later explicit skill sync remains usable

#### Scenario: Automatic partial failure
- **WHEN** one skill destination cannot be updated during eligible serve startup
- **THEN** startup emits the individual failure and retry command, preserves successful targets, and proceeds to existing configuration/startup behavior

#### Scenario: New serve image starts once
- **WHEN** serve replaces its binary and successfully hands off sync
- **THEN** it re-execs the installed image once without an immediate duplicate update cycle, and a future independent serve invocation remains eligible for automatic updates

#### Scenario: Restart fails
- **WHEN** serve installs a newer binary but cannot re-exec it
- **THEN** it reports binary and skill outcomes and exits before listening or launching backends from the old process

### Requirement: Older and development builds require explicit authority

Skill state SHALL retain separate bundle identity, installed-from/last-verified-with provenance and greatest accepted release version with its associated bundle hash. Release comparison SHALL use normalized strict semantic versions without modifying embedded version constants. A newer release with identical payload SHALL update authority/provenance without rewriting files or requesting reread. An older release SHALL NOT automatically mutate or repair destinations with a higher accepted release, even if hashes match. Explicit install or sync with `--allow-downgrade` SHALL permit the selected older release, require backup when replacing a different payload, and reset authority for that destination. A same-release/different-bundle conflict SHALL fail as release_identity_conflict and SHALL NOT be bypassed by the downgrade flag.

Development install/sync SHALL require `--allow-dev`; dev SHALL never acquire release ordering from build date or commit text. Dev writes SHALL record dev plus hash and retain any prior release authority floor, with backup before replacing different existing payload. A release at or above that floor SHALL be able to restore its bundle, preserving changed dev content in a backup. `--allow-dev` and `--allow-downgrade` SHALL be mutually exclusive and only meaningful for matching build kinds; no broad force flag SHALL override ownership, path or unowned-file conflicts. Development `update` SHALL remain unavailable and direct callers to explicit skill sync. Status SHALL always be usable without mutation.

#### Scenario: Two release binaries share destinations
- **WHEN** v2.0.0 has synchronized a destination and v1.9.0 subsequently starts serve or invokes ordinary skill sync
- **THEN** the older binary reports newer_managed_release and does not rewrite or repair that destination

#### Scenario: Explicit intentional downgrade
- **WHEN** v1.9.0 explicitly syncs a selected registered path with --allow-downgrade after v2.0.0
- **THEN** it backs up differing contents, installs its full bundle and records v1.9.0 as that destination's new release authority

#### Scenario: Same content across versions
- **WHEN** v2.0.1 and v2.0.0 have identical bundles and the newer binary syncs
- **THEN** authority advances to v2.0.1, payload timestamps remain unchanged, changed is false and no reread notification is emitted

#### Scenario: Rebuilt release conflicts
- **WHEN** the registry records a release version and a binary with the same version carries a different bundle hash
- **THEN** reconciliation reports release_identity_conflict without replacing the tree, including with --allow-downgrade

#### Scenario: Development fixture
- **WHEN** a dev build installs to a safe temporary custom path with --allow-dev
- **THEN** it records dev/hash provenance without inventing a semantic version; without the flag it refuses mutation

#### Scenario: Return from development bundle
- **WHEN** a release at or above the retained authority floor syncs a destination last written by an explicitly authorized dev build
- **THEN** it backs up differing dev contents and restores the release bundle without treating dev as a newer release

### Requirement: Update reporting separates executable and skill outcomes

Update SHALL report the executable's before/after version and outcome separately from every skill destination, including unchanged, updated, repaired, failed, skipped or unknown results. `update --json` SHALL emit one schema_version 1 envelope compatible with skill command result fields, with a binary section and aggregate status. An explicit update SHALL exit 0 only when its binary work and requested reconciliation succeeded, including no-op; it SHALL preserve existing exit 2 for operational errors and use 2 for partial skill failure as well. JSON argument failures SHALL emit one error envelope when --json is requested. Logs/progress SHALL not corrupt JSON stdout.

Changed destinations SHALL include absolute SKILL.md paths, applicable backup paths and a request to reread instructions. Metadata-only/no-op results SHALL not claim a skill changed. A successful binary update plus failed skill synchronization SHALL explicitly state that the executable remains installed and give `skill sync` retry guidance. Reporting SHALL never promise to refresh an existing agent's loaded context.

#### Scenario: Binary succeeds and one destination fails
- **WHEN** explicit update installs a new release, synchronizes one target and fails another
- **THEN** it exits 2 with aggregate partial, binary installed, both destination outcomes and a retry for the failed destination

#### Scenario: Already current everywhere
- **WHEN** no newer binary exists and all managed content is current
- **THEN** update exits 0, reports unchanged/current, and does not say the skill was updated or request rereading

#### Scenario: JSON validation error
- **WHEN** update --json is combined with an unsupported flag
- **THEN** it exits 2 with a single argument-error envelope and no binary or skill mutation

### Requirement: Skill maintenance does not alter ongoing agent work

Automatic synchronization SHALL be restricted to eligible pre-start serve handling. `run`, `models`, `version`, status and backend preflight SHALL NOT implicitly update skills or binaries. Updating from another process SHALL NOT reset, restart, send instructions to or otherwise change running Squad/backend sessions. Existing saved workflows, manual session continuity, machine backend configuration and HTTP contracts SHALL remain unchanged. Skill-host installation SHALL NOT require a supported Squad backend of the same name.

#### Scenario: One-shot execution
- **WHEN** run executes with registered stale skill files
- **THEN** workflow handling proceeds under its existing contract without skill writes, self-update or re-execution

#### Scenario: Existing long-lived session
- **WHEN** another process updates the executable and registered skill while a Squad session is running
- **THEN** the running process and backend session identities remain intact and only the update caller receives the reread notification
