## 1. Establish the supported native contract

- [x] 1.1 Produce a versioned compatibility record answering design section 8 questions 1–3 (session load/workspace/history, required client capabilities, exact mode/model controls, stop/error shapes); distinguish official schema, synthetic fixture and observed Devin evidence. Verify all required controls have concrete fixtures before enabling execution; unavailable Normal mode must fail before prompting and is not silently replaced by Code.
- [x] 1.2 Verify startup/update/config-import and migration behavior without modifying the user's files; document the supported non-mutating launch profile and fail-before-launch guard. Verify fixture profiles with unsafe migration/update behavior are rejected. Do not weaken the profile requirement to make a probe pass.
- [x] 1.3 Establish cancellation/background ownership for native subagents, exec jobs, hooks and MCP children, using official evidence or separately authorized disposable live tests. Verify escaping work is either disabled by a tested native control or the profile is marked unsupported; record the remaining forced-death/remote-effect boundary.
- [x] 1.4 Verify the model-list JSON schema, exact selectable IDs and session-free non-mutating lifecycle. Deliver sanitized versioned observations, keep unverified variant-to-ACP recipes unsupported, or document and test the explicit read_only_catalog_unavailable result; never use a session to fill this evidence gap.

## 2. Register configuration and installation support

- [x] 2.1 Register devin in adapter factories, backend validation, model selectors and machine backend names; add only the planned option keys and machine section. Verify unknown keys/wrong types fail and existing backend fixtures remain unchanged.
- [x] 2.2 Implement one effective environment builder shared by execution, preflight and discovery, including baseline variables, machine merge and explicit overrides. Verify no accidental nil/full-environment fallback, no implicit ambient token/fallback/proxy inheritance, and identical executable resolution under conflicting PATH/cwd choices.
- [x] 2.3 Add Devin CheckInstallation and its official installation link through existing preflight infrastructure. Verify missing/non-executable/relative command, unsupported launcher/platform, explicit-path precedence and shared-config deduplication; assert no CLI/version/auth/model/session process is invoked.

## 3. Implement the bounded ACP operation

- [x] 3.1 Implement owned stdio transport with bounded frames, serialized writes, response correlation and server-request handling. Verify malformed/oversized frames, early EOF, out-of-order responses, stderr flooding and unknown required callbacks terminate safely without leaking readers/processes.
- [x] 3.2 Implement checked fresh Init and explicit-ID load with absolute cwd and capability negotiation. Verify two agents plus two workflow attempts have distinct IDs, missing/wrong-workspace/locked sessions never fall back to new/continue, and reset creates nothing until the next checked turn.
- [x] 3.3 Make pre-Send session persistence failure stop dispatch in manual and workflow-owned paths. Inject storage failure after native session allocation and assert zero prompts, a reported storage failure and no damage to previous durable state; run existing orchestrator lifecycle tests for regressions.
- [x] 3.4 Implement history replay exclusion and first-conversation startup instructions. Verify history does not become current output and a failed first submitted turn does not cause duplicate startup on a later explicit manual turn.
- [x] 3.5 Apply verified advertised model/mode controls before every prompt, including resumed sessions. Verify explicit selection is advertised and is not silently ignored/fuzzy-rewritten/fallback-selected and unsupported reasoning/speed/sandbox options fail before work.
- [x] 3.6 Implement prompt completion and safe result/progress mapping. Test end_turn with fresh text, empty text, refusal, limits, foreign-session updates, historical/thought/tool text, clean premature exit and retryable channel failure; assert no automatic prompt replay and partial output remains failed evidence.

## 4. Connect permissions and cleanup

- [x] 4.1 Map native permission requests and offered once/reject choices into existing pending-permission progress and PermissionReplier. Verify concurrent requests, exact native option IDs, stale/duplicate/foreign replies, cancellation races and unsupported questionnaire/auth callbacks; never persist native grants.
- [x] 4.2 Implement native cancel followed by bounded owned process-group teardown for success, failure, force reset and shutdown. Test unresponsive cancellation, descendants holding pipes and an unrelated control process; assert at most five seconds of cleanup and no unrelated kill.
- [x] 4.3 Implement the verified background-work restrictions and cleanup-failure propagation. Verify a prompt ending with unsettled owned work cannot release a successful workflow dependency and unresolved cleanup remains observable.
- [x] 4.4 Add safe event/error projection before RunSink/logs and reject executable leading slash commands. Inject credential/proxy sentinels into native error/config messages and assert they never reach diagnostics/state; verify /handoff, /cloud, /login and /loop do not trigger native commands.

## 5. Integrate catalog and workflow behavior

- [x] 5.1 Implement ListModels using the verified native list path or explicit unsupported result from task 1.4. Test known/unknown/empty schemas, family versus variant IDs, incomplete output, unsupported reasoning mappings, typed auth/offline failures, limits, cancellation and cleanup using existing modelprobe conventions.
- [x] 5.2 Extend model discovery --all/default/config-target resolution for Devin. Verify deterministic result ordering, deduplication, effective-env equality, hidden/freshness uncertainty and exit 1 when another valid backend coexists with unavailable Devin; no session or inference may be invoked.
- [x] 5.3 Add orchestrator/workflow integration fixtures covering manual continuity, ephemeral agents, fresh retries, recovery after uncertain dispatch, preflight rejection before reservation and run-scoped ownership conflicts. Verify existing dependency manifests and saved artifact hashes remain authoritative.

## 6. Document, verify and hand off

- [x] 6.1 Update README and backend authoring guidance/examples with official product/install links, explicit options.yolo:false, machine versus agent settings, operator login, billing/quota caveats, local workspace semantics and supported version/profile. Verify examples parse without CLI installation and contain no real credentials or machine-specific paths.
- [x] 6.2 Record acceptance evidence against every new/modified scenario and each core compatibility gate. Use the user-authorized small account-backed test sessions in disposable workspaces; record unsupported modes/profiles and skipped platform checks honestly, and distinguish documented controls, native observations and synthetic tests.
- [x] 6.3 Run gofmt on changed Go files, then go vet ./... and go test -race -count=1 ./...; record results and fix material failures before code handoff.
- [x] 6.4 Run openspec validate add-devin-acp-backend --strict, reconcile artifacts with final implemented behavior and confirm no unresolved core gate remains before archive. Archive only when implementation is complete; do not create or push a release tag as part of this change.


Acceptance details and limits are recorded in design.md. Normal ACP permissions are intentionally unavailable on the tested release; yolo:false is tested as a pre-prompt failure. JSON model variants without verified ACP encodings are informational, never executable recipes. Existing shared orchestrator/workflow lifecycle tests are reused for attempt/retry/recovery ownership; native Linux inference and external hooks/MCP/plugins are outside this supported profile. All tasks are complete. Main specs were synchronized and this change was archived during v0.21.0 release preparation. Publishing remains a separate step.
