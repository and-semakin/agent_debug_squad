# Tasks: add-verdict-judge

## 1. Judge package and OpenRouter adapter

- [x] 1.1 Create `internal/judge` with the `Judge` interface, `Request`/`Decision` types (choice answer, confidence, full distribution, raw provider response), and a nil-safe no-judge placeholder. Verify the package compiles and a unit test constructs a `Request` for all three decision types (`choice`, `noul`, `score`).
- [x] 1.2 Implement the OpenRouter provider over `POST /api/alpha/decisions`: bearer auth from the key file, typed request structs for choice/noul/score with `state`/`instructions`/`criteria`, typed answer parsing, per-call timeout, and bounded retry with backoff for 429/5xx/transport errors. Verify with `httptest` tests covering all three decision types, the auth header, retry-then-succeed, and retry exhaustion surfacing a transport error.
- [x] 1.3 Add HTTP proxy support: a dedicated `http.Client` whose transport uses `judge.proxy_url` when set, otherwise standard environment behavior. Verify with a test asserting decision requests are routed through a stub proxy when configured.

## 2. Domain, configuration, and validation

- [x] 2.1 Add `Verdicts map[string]string` to `WorkflowTaskDefinition` and `ConfidenceThreshold`/`OnUncertain` to `WorkflowDefinition` (JSON `omitempty`), plus `rawWorkflow`/`rawWorkflowTask` fields and copying in `config.Load`. Verify config tests: verdict maps parse with and without descriptions, omitted settings default (0.8 / `hold`), and unknown fields in the workflow subtree still fail.
- [x] 2.2 Extend `ValidateWorkflowDefinition`: reject empty maps, unsafe or duplicated verdict names, the reserved name `uncertain`, fewer than two verdicts, thresholds outside (0, 1], and `on_uncertain` values other than hold/error. Verify validation tests for each rejection and acceptance of a valid map.
- [x] 2.3 Assert definition-hash stability: definitions without verdict settings produce golden hashes identical to pre-change binaries, and changing `verdicts`, `confidence_threshold`, or `on_uncertain` changes the hash (replay identity).
- [x] 2.4 Add the `judge:` config section (`provider`, `model` default `~typesafe/jev-latest`, `api_key_file`, `proxy_url`, `timeout_seconds` default 30) with parsing tests, and startup gating in `cmd/agent-debug-squad`: when the workflow has verdict tasks or a `judge:` section exists, a missing/unreadable or blank key file (default `~/.agent-debug-squad/openrouter-api-key` in the user's home directory) fails startup with an error naming the path. Verify with config and wiring tests covering both gating branches and the no-judge path.

## 3. Judging phase in the workflow manager

- [x] 3.1 Add the `judging` attempt state (excluded from `Committed()`, task-state mapping treats it as running) and `AttemptVerdict` persistence on `WorkflowAttempt` (`Value`, `Confidence`, `Probabilities`, `Model`, `Threshold`, `Source`, `Truncated`, `JudgedAt`). Verify a snapshot round-trip test retains verdict fields.
- [x] 3.2 In `handleCompletion`, route successful attempts of verdict tasks into `judging` instead of `succeeded`, persist, and hand classification to a background worker that calls the judge outside the manager lock and posts a judgement event through the completions channel. Verify with a fake judge that a verdict task's dependents are not dispatched until settlement and that non-verdict tasks settle unchanged.
- [x] 3.3 Implement judgement settlement: confidence ≥ threshold → `succeeded` with the verdict recorded and the raw decision persisted as `tasks/<task>/attempts/<n>/decision.json`; below threshold with `on_uncertain: error` → `failed` with reason `uncertain_verdict`; hold cases keep the attempt `judging` with attention reasons `uncertain_verdict:<task>:<n>` / `judge_unavailable` moving the execution to needs_attention. Verify manager tests for each settlement path, including the threshold-equal boundary.
- [x] 3.4 Implement judge input construction: state map with task/agent identities, task prompt, final response; criteria from the verdict map; deterministic head+tail truncation of the response beyond 64 KiB with the `[...truncated N bytes...]` marker and the `Truncated` flag persisted. Verify the saved response file's bytes, size, and hash are unchanged after classification.
- [x] 3.5 Handle cancellation and timeouts around judging: cancelling an execution closes `judging` attempts as cancelled; `enforceTimeoutsLocked` does not apply to judging attempts (no live run). Verify manager tests for both.

## 4. Recovery

- [x] 4.1 On recovery, re-dispatch classification for snapshot attempts in `judging` instead of interrupting them, while `queued`/`dispatching`/`running` attempts keep the existing interruption rule. Verify a recovery test: crash during judging re-classifies without re-running the agent and the execution continues.

## 5. API surface

- [x] 5.1 Expose verdict data in workflow views: `judging` attempt state, verdict name/confidence/distribution/model/source, and judge-related attention reasons. Verify API view tests for judging, settled, and held attempts.
- [x] 5.2 Implement `POST /workflows/{id}/tasks/{task}/attempts/{n}/verdict` as a `verdict_override` control event with `request_id` idempotency: settles a `judging` attempt as succeeded with `Source: manual`; 409 for settled attempts or conflicting replays, 400 for undeclared verdict names, 404 for unknown identifiers. Verify API tests for each outcome including idempotent replay.
- [x] 5.3 Extend `resume` to re-classify attempts still in `judging` once their hold reasons are resolved. Verify a manager test: held on judge unavailability → resume after recovery of the fake judge → classification completes and scheduling continues.

## 6. Documentation and examples

- [x] 6.1 Document the judge in README.md: the `judge:` section (key file default location, proxy, model pinning), task-level `verdicts`, `confidence_threshold`/`on_uncertain`, the judging lifecycle, holds and the manual override endpoint.
- [x] 6.2 Add verdicts to an existing workflow example (e.g. the review verifier task) with a short comment explaining the intent, and confirm the example validates.

## 7. Final checks

- [x] 7.1 Run `gofmt` on changed Go files, `go vet ./...`, and `go test -race -count=1 ./...`; all green.
- [x] 7.2 Run `openspec validate add-verdict-judge --strict` and walk every delta scenario against the implementation and tests before archiving.
