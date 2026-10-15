## 1. Bundle and identity

- [x] 1.1 Add the embeddable package adjacent to the canonical skills tree and offline materialization; verify a built binary installs the full bundle without a checkout or network in an isolated home.
- [x] 1.2 Define sha256-v1 length-prefixed encoding and normalized executable metadata; verify golden vectors, path ordering, nested/hidden resources, renames/deletions, byte changes and cross-platform identity.
- [x] 1.3 Validate portable frontmatter/resource references and reject symlinks, special files, traversal, reserved names and case collisions; verify negative fixtures and preserve the existing model-discovery/reviewer-quorum guidance.

## 2. Target selection and ownership

- [x] 2.1 Implement explicit CLI argument parsing for install/status/sync and their JSON modes, exact-path semantics and authority flags; verify invalid selectors/flags exit 2 before writes and non-macOS/Linux mutations fail safely.
- [x] 2.2 Implement the documented host matrix and absolute environment overrides with separate current/legacy Kimi presets; verify table-driven isolated-home cases, unset/invalid overrides, frozen registrations and no inferred CODEX_HOME/KIMI_SHARE_DIR relocation.
- [x] 2.3 Implement batch target resolution, canonical deduplication, eligible shared-copy reuse and bounded user/project observations; verify four-host shared installation, Claude's separate root, ambiguous registrations, legacy conflicts and duplicate warnings without invoking host CLIs.
- [x] 2.4 Add schema-1 private registry and matching receipts with provenance, accepted release/hash authority and atomic registry writes; verify missing/corrupt/unknown schemas, foreign identities, registry loss and permissions without changing backend settings or workspace artifacts.
- [x] 2.5 Implement exact-path adoption with matching-name checks, full-tree verified backup and conservative ownership inventory; verify byte-identical manual copies still require adoption, unknown legacy resources survive and recognized plugin-controlled copies are rejected.

## 3. Reconciliation and filesystem integrity

- [x] 3.1 Implement actual-content comparison and whole-bundle reconciliation including additions, removals, executable bits, missing-tree repair and no-op provenance updates; verify old managed deletions, retained unknown files and unchanged payload mtimes.
- [x] 3.2 Implement required private archive backups and conflict policy; verify modified managed files, dev/downgrade replacement, missing receipt repair, backup permission/disk failures and unowned file/directory collisions never lose data.
- [x] 3.3 Implement canonical/no-follow path handling and overlap validation; verify destination/descendant symlinks, stable versus changed ancestor links, hard links, special files, traversing inventories, writable parents and management-root overlap.
- [x] 3.4 Implement registry locking and same-filesystem staging outside discovery roots; verify concurrent process contention, lock release after death, safe stage cleanup, detected external edits and no live-tree changes on staging failure.
- [ ] 3.5 Add CGO-free macOS/Linux atomic exchange and first-install no-replace helpers; verify native filesystem behavior on both OS families and safe failure when atomic exchange is unsupported, with no in-place fallback.
- [x] 3.6 Implement durable intent journals and registry/tree reconciliation; verify injected crashes before publish, after publish, before/after registry commit and before cleanup, including interrupted initial adoption and externally edited recovery conflicts.
- [x] 3.7 Implement per-destination partial success and cancellation boundaries; verify one unwritable target does not roll back or suppress another success, cancellation starts no further target, and post-publication cancellation remains recoverable.

## 4. Version authority and reporting

- [x] 4.1 Implement strict release normalization, accepted-version/hash floor, explicit downgrade and dev policies; verify two installed binaries cannot oscillate copies, equal-release hash mismatch fails, identical payload advances metadata and release restore after dev retains backups.
- [x] 4.2 Implement read-only hash-based status and stable diagnostic/result schema; verify current/outdated/missing/modified/conflict/pending-recovery and unregistered observations, sorted paths, no writes or child execution, and exit 0/1/2 semantics.
- [x] 4.3 Implement human and JSON reports with changed/reread fields, preserved files, backup paths and retry commands; verify one-envelope stdout, escaped terminal controls, no skill contents/environment leakage and honest no-op/metadata-only notifications.

## 5. Executable update integration

- [x] 5.1 Refactor self-update orchestration to separate binary outcomes from skill reconciliation without modifying version constants; verify latest-version repair, no registrations, network/download failures and ambiguous binary replacement never falsely report combined success.
- [x] 5.2 Add executable-update serialization and new-installed-image skill-sync handoff with a 60-second/caller deadline; verify two distinct fixture bundles prove the new image supplies content, lock order has no deadlock, and invalid child reports/timeouts/cancellation produce honest retryable results.
- [x] 5.3 Add update --json aggregation preserving update operational exit 2; verify binary-success/skill-partial-failure, current/current no-op, child-start failure and argument-error envelopes without mixed stdout.
- [x] 5.4 Integrate serve pre-start reconciliation and one-use re-exec continuation; verify opt-out flag/environment suppress both automatic phases, dev skips them, network failure still permits local repair, warning-only skill failure allows startup, and re-exec failure stops old-image startup.
- [x] 5.5 Add regression coverage showing run/models/version/status/preflight perform no implicit maintenance and separate updater processes do not reset active backend sessions; verify existing CLI/workflow tests retain their behavior.

## 6. Documentation and release verification

- [x] 6.1 Replace manual-copy README guidance with first installation, explicit host selection, matrix/source links, exact custom path, adoption/unknown-file policy and shared-root examples; verify all CLI examples against help and isolated fixtures.
- [x] 6.2 Document opt-outs, dev/downgrade authority, partial failures, backup/recovery, same-user/filesystem limits and reread/refresh semantics; verify a failed-path retry and intentional rollback walkthrough using fixture binaries only.
- [x] 6.3 Update the bundled skill to check status and handle repair/partial results within user authorization; verify it never infers installation consent from worker backends or claims to force context reload.
- [x] 6.4 Add release checks proving the canonical complete payload is embedded in all four existing CGO-free archives and reports matching provenance/hash; verify cross-builds and existing archive names without publishing or tagging a release.
- [x] 6.5 Deliver a host discovery compatibility checklist for the documented installed generations, isolated custom profiles and disabled/duplicate skills; verify with read-only evidence or isolated fixtures, record untested host-loading behavior explicitly, and do not modify real user skill directories as part of automated tests.

## 7. Final validation

- [x] 7.1 Run gofmt on every changed Go file and verify no formatting diff remains.
- [x] 7.2 Run go vet ./... and go test -race -count=1 ./...; record passing results and resolve failures before handoff.
- [ ] 7.3 Run openspec validate manage-bundled-skill --strict and map every requirement/scenario to implementation evidence, including native platform checks; verify artifacts and task checkboxes reflect actual completed work before any archive action.
