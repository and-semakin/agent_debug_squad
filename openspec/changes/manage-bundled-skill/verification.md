# Implementation verification

Implementation date: 2026-10-15. Code adds the embedded payload package, `internal/skillmanager`, executable maintenance orchestration and CLI commands. No real user skill directories, installed binaries, external agent settings or existing sessions were changed during testing. The working tree's release version constants remain untouched.

## Requirement and scenario evidence

Every scenario under each requirement below is exercised by the listed fixtures or the stated code-boundary check. Host discovery remains limited to the documented filesystem contract, not a live model/login claim.

| Requirement | Evidence |
| --- | --- |
| Every binary carries its matching complete skill bundle | `TestEmbeddedSourceInventory`, `TestBundleIdentity`, `TestInvalidBundle`; isolated real CLI install; `scripts/verify-skill-builds.py` checks canonical bytes in darwin/linux amd64/arm64 CGO-free builds. |
| Installation selects explicit facilitator destinations | `TestOverrides`, `TestBadArgumentsAreReadOnly`, `TestFrozenDestinationAndLegacyCodexObservation`, `TestPlatformAndHumanEscaping`; resolver has no adapter/YAML/auth imports. |
| Selection minimizes managed duplicates without claiming host precedence | `TestFreshSharedInstallAndNoop`, `TestAmbiguousCopiesAndStatusFilter`, legacy observation fixture; known-root observation reports unknown effective selection. |
| Ownership requires explicit registration or adoption | `TestAdoptionAndUnownedCollision`, `TestMissingRegistryAndInvalidState`, `TestPathsAndForeignReceipt`, `TestInitialAdoptionRecovery`. |
| Synchronization reconciles the whole owned bundle conservatively | `TestReconcileLocalEditsAndDeletion`, `TestAdoptionAndUnownedCollision`, `TestUnsafeParentsBackupAndExternalEdits`, `TestReceiptRepairAndForeignOwner`, `TestFreshSharedInstallAndNoop`. |
| Mutations are serialized and crash-recoverable per destination | `TestCrashRecovery`, `TestInitialAdoptionRecovery`, `TestPostCrashEditConflict`, `TestLockAndJSON`, `TestMaintenanceLockReleasedAfterProcessDeath`, `TestAtomicUnsupportedLeavesLiveTree`, `TestPartialAndCancellation`, `TestUnwritableDestinationDoesNotBlockAnother`, `TestCancellationAfterPublicationFinishesCommit`, `TestRecoveryDoesNotObscureOtherCommittedTargets`. Native macOS and Linux CI passed, including filesystem transactions and recovery fixtures. |
| Paths and ownership inventories cannot escape their boundaries | `TestPathsAndForeignReceipt`, `TestStableSymlinkAncestorAndDestinationLink`, `TestUnsafeParentsBackupAndExternalEdits`, `TestUnsafeStagingAndStructuralCollision`; descriptor-relative no-follow helpers anchor filesystem access. |
| Status and results are honest and machine-readable | `TestLockAndJSON`, `TestMissingRegistryAndInvalidState`, `TestSkillCLIIsolatedHome`, `TestPlatformAndHumanEscaping`, `TestRecoveryDoesNotObscureOtherCommittedTargets`; status reads actual files without maintenance writes. |
| Explicit update also reconciles registered skills | `TestMaintenanceCurrentAndOfflineRepair`, `TestMaintenanceUncertainInstall`; no-registration path uses a read-only empty-registry result. |
| Reconciliation after replacement uses the new executable | `TestRealNewBinaryHandoff` builds and executes the new image with an old fixture bundle; `TestMaintenanceNewImageAndFailedHandoff`, `TestMaintenanceLock`, `TestSyncInstalledReportValidation`, `TestHandoffDeadlineAndCommittedRecoveryReport`. |
| Automatic serve synchronization respects existing opt-outs | `TestServeMaintenanceOptOutAndRestartFailure`, `TestServeSkillFailureStillReachesConfiguration`, `TestContinuationIsOneUseAndVersionBound`; private continuation is removed before backend environment capture. |
| Older and development builds require explicit authority | `TestAuthority`, `TestFreshSharedInstallAndNoop`, real dev CLI test; same-release mismatch cannot be bypassed by downgrade. |
| Update reporting separates executable and skill outcomes | `TestMaintenanceCurrentAndOfflineRepair`, `TestMaintenanceNewImageAndFailedHandoff`, `TestHandoffDeadlineAndCommittedRecoveryReport`, `TestSkillCLIIsolatedHome`; explicit update returns existing operational exit 2. |
| Skill maintenance does not alter ongoing agent work | `TestNonServeCommandsNeverMaintain`, `TestSkillMaintenanceLeavesServingSessionIntact`, existing workflow/oneshot/preflight suite; updater and manager do not import backend/session orchestration. |

## Checks

- Focused skill, update and real CLI tests passed repeatedly after their changes.
- Final `go vet ./...` and `go test -race -count=1 ./...` passed after all recovery/diagnostic regression tests.
- All four CGO-free cross-builds passed embedded-byte verification.
- `gofmt` and `git diff --check` passed.
- `openspec validate manage-bundled-skill --strict` passed.
- Native Linux/macOS tests and all four cross-build payload checks passed in [CI run 38684511977](https://github.com/and-semakin/agent_debug_squad/actions/runs/38684511977). The main quality job also passed on the minimum Go version. Platform tests and release builds use current stable Go to support the current macOS loader; the first minimum-Go macOS run failed before test execution with a missing LC_UUID load command.
- Release publication is gated on green CI for the final commit; downloaded archive checksums and the native released executable will be verified after publication.

## Operational limitations retained from the design

Atomic exchange must be supported by the destination filesystem. Skill-directory symlinks, plugin ownership, unsafe parents, ambiguous recovery, unowned collisions and mismatched release identity fail conservatively. No force flag bypasses these boundaries. Backups are retained without automatic pruning. Native Windows and automatic host context reload are not claimed. The full host-selector checklist and source evidence are in `docs/skill-management.md`.
