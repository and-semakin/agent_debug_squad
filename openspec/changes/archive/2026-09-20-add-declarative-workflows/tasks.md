## 1. Definition and validation

- [x] 1.1 Add workflow definition and execution/task/attempt types in `internal/domain` using the fields and states in design.md; verify JSON/YAML representations and defaults with focused tests.
- [x] 1.2 Extend `internal/config` with optional version-1 workflow parsing, positive concurrency/timeouts, and strict workflow/task fields; verify existing config fixtures still load and malformed workflow values/duplicate keys fail.
- [x] 1.3 Validate task/agent references, unique agent use, safe IDs, duplicate edges, cycles, and success thresholds; verify chains, diamonds, identical models under distinct names, and all rejection scenarios with table-driven tests.

## 2. Durable state and ownership

- [x] 2.1 Add exclusive session-directory ownership before startup mutation with appropriate macOS/Linux handling and explicit unsupported-platform behavior; verify a competing process cannot acquire ownership and process exit releases it.
- [x] 2.2 Add versioned atomic workflow snapshots, immutable resolved definitions, revisions, histories, and stable request/attempt IDs in `internal/store`; verify round trips, unsupported versions, corruption, and injected write/rename/sync failures without backend dispatch.
- [x] 2.3 Persist attempt prompts, input manifests, and response files with hashes and safe execution-relative paths; verify missing/changed outputs, large responses, and partial failed output never being promoted to successful input.
- [x] 2.4 Persist and recover submission/retry idempotency from authoritative snapshots; verify duplicate requests, changed definitions, and concurrent admission create only one execution/attempt.

## 3. Execution integration

- [x] 3.1 Introduce a workflow executor interface and orchestrator support for caller-reserved run IDs and workflow/task/attempt metadata; verify duplicate dispatch is rejected without a second adapter call and manual run numbering still works.
- [x] 3.2 Create workflow-owned agent runtimes with fresh backend sessions per attempt, without loading/resetting manual histories; verify isolation across identical models, tasks, executions, retries, and an existing manual session.
- [x] 3.3 Integrate owned runs with streaming artifacts, run queries and permission replies while rejecting manual turn/reset mutation of owned runtimes; verify current permission ownership and stale-reply tests plus new workflow ownership tests.
- [x] 3.4 Add workflow attempt timeout/cancellation and a reliable worker-stopped completion boundary; verify permission waits count toward timeout, confirmed cleanup yields failure, and unconfirmed cleanup yields interruption without freeing scheduling to continue.
- [x] 3.5 Separate authoritative workflow outcome publication from ordinary run projections and legacy startup interruption; verify an unsaved completion or stale projection cannot enable a dependent task.

## 4. Scheduler and result handoff

- [x] 4.1 Implement a serialized reconciliation loop with startup/event/periodic wake-ups and stable ready ordering; verify chain and diamond execution without coordinator follow-up, including duplicate and lost wake-ups.
- [x] 4.2 Implement `max_parallel` slot accounting through dispatch, permissions, completion, and cancellation; verify no oversubscription under simultaneous readiness/completion and no workspace-write serialization is introduced.
- [x] 4.3 Implement acceptable-dependency checks, `allowed_to_fail`, success thresholds, blocked propagation, and independent-branch continuation; verify every dependency-policy scenario in `declarative-workflows` including all reviewers failing and a mandatory failure despite enough successes.
- [x] 4.4 Build and persist ordered dependency manifests and exact prompts, referencing verified full response files; verify reverse completion order preserves input ordering and optional failures appear as errors rather than successful outputs.
- [x] 4.5 Implement task success/output checks and workflow final state calculation; verify missing-output failure, failed blocking branches, completed_with_errors, and fully successful graphs.

## 5. Recovery and controls

- [x] 5.1 Recover immutable definitions and committed outcomes, revalidate artifacts, and recompute pending readiness; use fault-injection tests at reservation/send/output/commit boundaries to verify no automatic replay of uncertain attempts.
- [x] 5.2 Implement pause/resume with storage/artifact revalidation and durable cancellation intent; verify completion races, pause across restart, byte-for-byte artifact repair, no successor after cancel, and observable incomplete cleanup.
- [x] 5.3 Implement explicit retry with expected-attempt checks, idempotency, fresh conversations, blocked-descendant reevaluation, and rejection after downstream consumption; verify paused retries do not dispatch and independent completed tasks do not rerun.
- [x] 5.4 Implement audited cleanup assertions for interrupted retry/cancellation, preserving known-live-worker guards; verify no confirmation means no retry/false cancellation completion and duplicate controls are safe.
- [x] 5.5 Integrate server shutdown and startup ordering: stop new scheduling, cancel/join owned workers within existing shutdown bounds, preserve uncertainty when cleanup is incomplete, then release ownership; verify restart never treats shutdown as a new workflow submission.

## 6. API and observation

- [x] 6.1 Add workflow create/list/get routes and their validation/status codes from design.md; verify explicit start, one active execution, historical queries, definition snapshots, and request replays using HTTP tests.
- [x] 6.2 Add pause/resume/cancel/retry routes and return current state; verify unknown IDs, invalid transitions, stale/conflicting controls, and persistence failures do not claim successful mutations.
- [x] 6.3 Add workflow long-polling, task blocking reasons, attempt/run identities, result paths, and permission progress; verify wait bounds, early intervention, terminal results, and continued execution after client disconnect.

## 7. Documentation and ordinary review workflow

- [x] 7.1 Add portable fake-backend chain and diamond/review examples with optional reviewers and `min_successful_dependencies: 1`; verify automated example loading and end-to-end execution without paid backends.
- [x] 7.2 Update `README.md` with configuration defaults, API examples, failure truth table, shared-workspace responsibility, result-file handoff, retry/recovery/rollback limits, and manual compatibility; verify examples match implemented routes and fields.
- [x] 7.3 Update repository `skills/agent-debug-squad/SKILL.md` to translate familiar quorum requests into reviewers plus verifier, preserve requested backend/model/tool constraints, submit once, handle intervention, and deliver the final report; verify by walking through the documented scenario and fake example, without installing or changing personal skills.
- [x] 7.4 Update `openspec/config.yaml` context to describe declarative execution alongside explicit manual turns; verify it agrees with the delivered behavior and does not claim filesystem isolation or exactly-once external effects.

## 8. Acceptance and handoff

- [x] 8.1 Run end-to-end fake workflows for a chain, diamond, optional failure, all reviewers failed, mandatory failure, timeout, pause/resume, cancellation, and retry; verify outcomes and manifests against both delta specs.
- [x] 8.2 Run race/fault coverage for simultaneous finishes, duplicate submission/notification, persistence errors, competing owners, and crash recovery; verify no duplicate dispatch, lost ready task, or unsaved-success handoff.
- [x] 8.3 Verify regression coverage for old manual configurations, parallel manual review, follow-up continuity, reset, artifacts, and existing OpenCode/ZCode permissions; record any environment-specific live-backend validation limits without claiming unrun tests passed.
- [x] 8.4 Run `gofmt` on changed Go files, `go vet ./...`, and `go test -race -count=1 ./...`; verify all complete successfully before code handoff.
- [x] 8.5 Run `openspec validate add-declarative-workflows --strict`, audit every requirement/scenario against implementation and tests, and reconcile these artifacts with actual behavior; verify all implementation checkboxes are accurate before any archive step.
- [x] 8.6 Deliver implementation summary and validation evidence, preserving the change for review; use `openspec-archive-change` only when finalizing completed work after the preceding checks, and do not create a release/tag without a separate request.
