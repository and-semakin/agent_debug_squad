# Implementation verification

Implementation baseline: `origin/main` at `2f09a36`; the planning rebase used `1c444a2`. A fresh fetch confirmed no newer main commit during final verification. The three affected main specifications are unchanged between those baselines.

## Requirement review

All delta requirements and their scenarios were reviewed against the implementation and regression suites. Tests group scenarios by shared invariants rather than providing one test per prose scenario.

| Contract | Implementation and regression evidence |
| --- | --- |
| Strict v2 definitions, removed fields, total control maps, barriers and recursive boundary cycles | `internal/config/workflow_controls.go`, shared typed `domain.ScopeGraph`; config control/nested tests cover positions, bypass/unordered controls, removed-field precedence, types and three-level cycles. Existing version-1 loopless hash fixtures remain unchanged. |
| Entered/planned paths, budgets, real attempts, decisions and skips | Domain schema-4 types; storage validation rejects inconsistent paths/caps, missing decisions/close summaries, incomplete suffixes and attempts in skipped contexts. `TestSchema4RejectsPartialControlCommits` and queue round trips exercise authoritative-state checks. |
| Read-only legacy compatibility and replay | Compatibility tests cover fixed/conditioned/nested active refusal without snapshot changes, terminal read-only stop/extend/override/retry replay, new mutation rejection, unknown terminal schema and version-boundary submission identity. Legacy loop runtime branches were removed. |
| Whole-child admission and local phase actions | Queue tests cover head break, hidden descendant roots, parallel reviews, tail continuation and final skips; middle-control tests cover continue/break/attention, prefix preservation and manual revisions. Existing fixed/nested loop tests retain bounded execution and local context behavior. |
| Atomic decision/close/advance | Judge and manual-override write failures preserve the prior authoritative outcome without publishing skips. Existing extension/stop fault tests remain. Advance fault and boundary-crash tests recover one pass exactly once; paused closed-pass recovery retains decisions and skips until resume. |
| Exhaustion and intervention | Existing and added tests cover all-proceed natural exhaustion, both policies, continue-at-cap, overflow, extension without suffix resurrection, graceful stop, stopped attention override conflict, failure/uncertainty preservation and pause/cancel ordering. |
| Exact handoff and historical carry-over | Recorded final child paths drive descendant selection. Queue tests ensure a final skip cannot select an older success. Nested carry-over and artifact-integrity tests retain owner/ancestor summaries and verify successful referenced files. Skip manifests omit run, attempt, verdict and result fields. |
| Failure accounting and retries | Required skipped output fails an outside report even for tolerated producers. Pending phases expose their control/path; nonrepairable skips offer cancel/new-definition guidance. Decision fences apply while paused. Existing retry suites cover current paths, confirmed interruption, ancestor reopening, shared contexts and inert replay after advance. |
| Observation and HTTP/CLI projection | API observation/wait serialization test preserves decision IDs, counts, paths and unavailable results. Existing HTTP control tests preserve statuses and request validation. One-shot summaries include skip history and control decisions without changing lifecycle. |
| Backend readiness and confidence | A regression test makes a future review backend unavailable after selection and verifies the new phase reserves no attempts until repaired. Existing full preflight suites, target retry/resume behavior, machine threshold tests and loopless identity fixtures remain. Skipped-but-rearmable agents remain in checks; permanently skipped subtrees are excluded. |
| Documentation and examples | README covers phase barriers, pass counting, all-proceed exhaustion, final skipped outputs, stop versus break, migration and rollback. Fixed, tail-control, nested and queue examples use version 2 and parse under the config suite. |

## Main-spec preservation

The reviewed merge modifies 16 requirements, adds 3, removes the obsolete loop-condition requirement and renames the lifecycle intervention requirement. All 17 unrelated requirements retain their existing text. Every existing scenario title in a modified requirement is preserved, including shipped backend preflight and threshold scenarios. The three resulting specifications contain 156, 49 and 199 scenarios respectively.

## Validation

Final command results are recorded below before archive. No release tag or published release is part of this change; release preparation is in `docs/releases/v0.18.0.md`.

- `gofmt -l` on all changed Go areas: clean; `git diff --check`: clean.
- `go vet ./...`: passed.
- `go test -race -count=1 ./...`: passed across all packages (workflow package 57.591 seconds).
- After adding the queue example to the explicit example list, `go test -race -count=1 ./internal/config`: passed.
- `openspec validate unified-loop-control-nodes --strict`: passed against the current main-spec baseline before sync.
- Native development build reports `agent-debug-squad dev (commit none, built unknown)`.
- `CGO_ENABLED=0` builds passed for darwin/amd64, darwin/arm64, linux/amd64 and linux/arm64; binary architectures were checked. These are development verification builds, not published release archives.
- Synced main specs: `openspec validate --specs --strict` passed all 9 specs; all three merged capabilities were compared with the reviewed merge before archive.
