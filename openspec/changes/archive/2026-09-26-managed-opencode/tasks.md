## 1. Configuration

- [x] 1.1 Implement machine settings and migration/override validation; cover defaults, wrong types, external incompatibilities and safe diagnostics with config tests.
- [x] 1.2 Implement constrained child environment, proxy precedence/loopback exclusions and preserving JSONC inline snapshot merge and non-mutating config preflight; verify with unit tests.

## 2. Runtime

- [x] 2.1 Implement owned serve startup, authenticated readiness, reuse, cancellation and bounded teardown; test concurrent acquisition, launch failure, timeout and child death with helper processes.
- [x] 2.2 Verify effective configuration and saved sessions before work, redact sensitive output, and preserve direct HTTP/SSE behavior; test mismatch/missing config and no replay.
- [x] 2.3 Integrate shared runtime with ordinary/workflow adapter construction and CLI cleanup; verify reuse, per-agent model isolation and failure cleanup.

## 3. Delivery

- [x] 3.1 Update README and examples with migration, scope, environment and limitations; verify consistency with configuration tests.
- [x] 3.2 Run an isolated no-model smoke with installed OpenCode, verifying false/true effective config and unchanged project settings.
- [x] 3.3 Run gofmt, go vet ./..., go test -race -count=1 ./..., openspec validate managed-opencode --strict and review implementation against all scenarios.

## Verification record

- `go vet ./...`: passed.
- `go test -race -count=1 ./...`: passed across all packages, including permission API and workflow suites.
- `openspec validate managed-opencode --strict`: passed.
- `git diff --check` and gofmt on changed Go files: clean.
- `SQUAD_OPENCODE_SMOKE=1 go test ./internal/adapters/opencode -run TestInstalledOpenCodeSmoke -count=1 -v`: passed against installed OpenCode 1.18.30. Both snapshot values, preserved inline/project settings, unchanged config file, saved session recovery in a newly launched process, and shutdown verified; no prompts or model calls.
- Configuration scenarios: machine contract and process override tests cover migration, external incompatibilities (including empty declarations), secret-free validation, proxy precedence/inheritance, loopback exclusions and JSONC preservation.
- Ownership scenarios: process helper tests cover concurrent reuse, cancellation, missing executable, startup exit/timeout, bounded TERM/KILL, invalid announcements, resets, missing saved sessions and death after prompt acceptance without replay. The orchestrator integration test runs ordinary and workflow agents with different models on one child and checks artifacts for machine secrets.
- Effective-config scenarios: missing/malformed/mismatched configuration blocks work, configuration changes block the next prompt, external inspection never writes config, and the config guard rejects native rewrite inputs without editing them.
- Lifecycle review: orchestrator construction failure closes its runtime; the CLI defers Close before judge/workflow/server setup; workflow Init uses the cancellable worker context. No changes to workflow scheduling, thresholds, releases or personal settings.

Known boundaries remain as documented in design/README: classic v1 protocol, explicit migration for old base_url, config preflight for native schema insertion/legacy conversion, operator cleanup after uncatchable process death, no automatic replay or promise of exactly-once external effects, and no sandbox against arbitrary native plugins/tools or concurrent privileged config edits.

## 4. Review follow-up

- [x] 4.1 Reconcile external workspace, empty declarations, authentication, startup cancellation, baseline environment, redirects, JSONC semantics and readiness budgets across specs/design/README; verify strict OpenSpec validation.
- [x] 4.2 Add regression tests for external directory recovery, redirect rejection, managed/external authentication and terminal startup cancellation; align snapshot diagnostics and run gofmt, go vet ./... and go test -race -count=1 ./....

Review follow-up verified on 2026-09-26: all review items reconciled. External recovery is deliberately limited to the same filesystem/path contract; initiating-request cancellation remains terminal until explicit restart. Regression tests cover matching/symlink/foreign/missing external directories, authentication, redirects, pre-cancelled startup and cancellation during readiness. A synchronous cancellation check prevents the already-cancelled caller from racing process launch. Snapshot decode errors now include file/section/key consistently. `go vet ./...`, `go test -race -count=1 ./...`, strict OpenSpec validation, gofmt and `git diff --check` all passed. The no-model installed-CLI smoke above remains the earlier 1.18.30 verification; it was not rerun for this review-only contract update and cancellation guard.

Release integration on current `main`: the one-shot workflow path retains the shared runtime through initialization and closes it during teardown; the synced machine-settings spec preserves the newer judge confidence threshold contract. The full release checks are rerun on the merged source before tagging.
