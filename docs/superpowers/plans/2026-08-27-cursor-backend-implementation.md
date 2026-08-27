# Cursor Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a stateful Cursor Agent CLI backend with model selection, read-only modes, YOLO support, streamed NDJSON artifacts, session resume, and explicitly scoped proxy/auth environment variables.

**Architecture:** Add a CLI adapter parallel to Codex. One Cursor subprocess handles one turn; the adapter parses Cursor stream JSON, stores its session id in existing agent state, and resumes it on later turns. Keep generic config, orchestrator, store, and REST contracts unchanged.

**Tech Stack:** Go 1.22, `os/exec`, NDJSON with `bufio.Scanner` and `encoding/json`, existing adapter and `RunSink` interfaces, shell-script test doubles.

---

## File Structure

- Create `internal/adapters/cursor/cursor.go`: lifecycle, argument/environment construction, subprocess streaming, and Cursor event parsing.
- Create `internal/adapters/cursor/cursor_test.go`: parser, arguments, environment, send, reset, and failure tests.
- Modify `internal/adapters/adapter.go`: register `backend: cursor`.
- Modify `README.md`: document Cursor setup, authentication, model/mode, proxy env, YOLO, and resume behavior.
- Modify `docs/code-review-squad.md`: describe using Cursor for a reviewer or implementer role.
- Modify the installed `agent-debug-squad` skill after implementation so future sessions know the backend contract.

### Task 1: Cursor Parser Tests

- [ ] Add failing tests for `system/init` session ids, successful terminal results, assistant delta fallback, Cursor error results, malformed JSON, missing terminal result, and large events.
- [ ] Run `go test ./internal/adapters/cursor` and confirm red.
- [ ] Implement the minimal parser and result builder.
- [ ] Re-run the package tests and confirm green.

### Task 2: Arguments And Environment Tests

- [ ] Add failing table-driven tests for the base print/stream/trust arguments.
- [ ] Cover `--model`, `--mode`, `--sandbox`, `--force`, and `--resume` ordering.
- [ ] Add environment tests proving ambient proxy/auth variables are excluded unless present in `inherit_env`, while explicit `env` entries override inherited values by process ordering.
- [ ] Implement argument and environment builders and make the tests green.

### Task 3: Adapter Lifecycle And Streaming

- [ ] Add shell-double tests for the first-turn startup wrapper and later-turn plain message.
- [ ] Test stdout/stderr streaming into `RunSink`.
- [ ] Test saving the Cursor session id and using it on resume.
- [ ] Test non-zero exit, malformed output, context cancellation, and oversized stream lines.
- [ ] Implement `Init`, `Send`, `Recover`, and `Reset` with `exec.CommandContext`.
- [ ] Run `go test ./internal/adapters/cursor`.

### Task 4: Factory And Integration

- [ ] Register `cursor.New(spec)` in `internal/adapters/adapter.go`.
- [ ] Add focused factory/orchestrator coverage proving a Cursor spec initializes rather than returning `unknown backend`.
- [ ] Run adapter and orchestrator tests.

### Task 5: Documentation

- [ ] Document `cursor-agent login`, API-key authentication, and model discovery.
- [ ] Document the absolute command path recommendation and the need to inherit `HOME` for browser-login credentials.
- [ ] Document `model`, `mode`, `sandbox`, `yolo`, `env`, and `inherit_env` with a proxy example that contains no real credentials.
- [ ] Explain that reviewers should use `mode: ask` plus `yolo: false`.
- [ ] Explain session resume/reset behavior and event artifacts.
- [ ] Update the local Agent Debug Squad skill with the new prerequisite and configuration contract.

### Task 6: Verification

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.
- [ ] Verify the pre-existing `go.sum` change was not overwritten or folded into feature commits.
- [ ] With the authenticated local CLI, run one read-only `composer-2.5` smoke turn through the actual Agent Debug Squad REST API and resume it for a second turn.
- [ ] Review the diff and commit the SDD docs, tests, implementation, and user documentation in coherent commits.
