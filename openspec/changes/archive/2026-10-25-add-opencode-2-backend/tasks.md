# Tasks

## 1. Verify the release contract

- [x] 1.1 Capture sanitized OpenCode 2.1.4 fixtures for private serve startup, authentication, port-zero announcement, version/readiness and shutdown in isolated HOME/XDG directories; verify no personal service or native configuration is changed and document the tested contract.
- [x] 1.2 Verify effective snapshot shape/inspection, location identity, prompt correlation/completion, event envelopes, descendant relations and always permission scope against the installed binary or pinned release source; record exact mappings and unsupported cases in a compatibility document with fixture tests. Resolve any contract-changing finding through openspec-update-change before implementation proceeds.

## 2. Versioned configuration and legacy compatibility

- [x] 2.1 Add opencode1/opencode2 names, display labels, independent machine settings and the legacy alias; verify config tests cover duplicate sections, forbidden process overrides and generation isolation, and document migration examples.
- [x] 2.2 Add bounded major-version checks and correct installation links for managed and external modes; verify wrong-major, missing executable, inaccessible server and external-without-local-binary cases.
- [x] 2.3 Preserve legacy persisted backend strings and workflow identity; verify historical recovery fixtures and mixed alias/canonical selections without rewriting saved definitions.

## 3. Runtime ownership and safeguards

- [x] 3.1 Implement the v2 runtime and native authentication with constrained environment/proxy/redaction rules; verify concurrent single startup, deadline/cancellation cleanup, failed-start latch, child death, direct transport and redirect rejection.
- [x] 3.2 Implement effective snapshot/location checks and conservative saved-session recovery; verify missing/conflicting config, wrong directory, missing ID and supported snapshot values without native config writes.
- [x] 3.3 Wire generation-aware runtime ownership, deferred initialization and two-stage preflight into the orchestrator; verify mixed-generation agents/workflow attempts share only their respective runtime and personal/external servers survive shutdown. Document managed/external settings and failure recovery.

## 4. Turns, progress and permissions

- [x] 4.1 Implement native session creation, model/agent selection, first-turn startup prompt and subsequent turns; verify connected-before-submit ordering, current-turn correlation, paginated final output and stream errors with protocol fixtures.
- [x] 4.2 Translate descendant activity and permission events into existing progress fields; verify nested children, unrelated events, duplicate delivery, permission precedence and manual/automatic replies through REST integration tests.
- [x] 4.3 Implement bounded interrupt/reset and permission-worker shutdown; verify concurrent-agent isolation, stale approvals, timeouts and uncertain prompt responses without replay. Document any proven feature gaps and their supported alternatives.

## 5. Catalog and user documentation

- [x] 5.1 Add native v2 model/provider/default discovery using execution-equivalent runtime settings; verify exact model selection, partial pages, auth failures, hidden-policy uncertainty and cleanup without prompts or session creation.
- [x] 5.2 Update all-mode/filter selection and alias deduplication; verify JSON identities and legacy selectors, and update README/backend tables, help and configuration examples for both versions.
- [x] 5.3 Audit backend-specific skill registration and other opencode name switches; verify aliases resolve to the documented native skill root without duplicate copies and preserve existing registration provenance.

## 6. Integration and completion

- [x] 6.1 Run an opt-in isolated installed-v2 smoke test for lifecycle, session continuity and supported API operations, plus existing v1 regressions; record tested versions and any untested paid-inference paths honestly.
- [x] 6.2 Run gofmt on changed Go files, go vet ./... and go test -race -count=1 ./...; resolve failures and record outcomes.
- [x] 6.3 Run openspec validate add-opencode-2-backend --strict and check implementation against every requirement/scenario and the feature-parity document; keep task checkboxes consistent before archive.
- [x] 6.4 Archive through openspec-archive-change only after completion, fetch origin/main, integrate concurrent changes without force push, and commit/push verified work to main under repository policy; verify the pushed commit then wait for green CI and publish/verify the explicitly requested semantic-version release.

## Verification evidence

- 2026-10-25: gofmt clean; `go vet ./...` and `go test -race -count=1 ./...` passed.
- Installed OpenCode 2.1.4 isolated smoke passed with the race detector and a local fake provider (no paid inference).
- Strict change validation and all 11 main specification validations passed; feature parity and failure scenarios checked against implementation and fixture tests.
- Implementation commit `b824a48` pushed to main; CI 40215392912 passed on Linux and macOS.
- Annotated tag `v0.23.0` published; Release workflow 40215590744 passed. All four macOS/Linux AMD64/ARM64 archives matched checksums.txt; the downloaded macOS ARM64 executable reported 0.23.0 and commit b824a48.
- Main specifications synchronized and verified against every delta; change archived after release verification. Final archive bookkeeping is committed separately from the tagged code.
