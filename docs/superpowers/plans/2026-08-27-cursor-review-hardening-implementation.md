# Cursor Review Hardening Implementation Plan

**Goal:** Correct confirmed lifecycle, API exposure, persistence concurrency, and request-size findings without regressing OpenCode session event handling.

**Architecture:** Keep backend session details in adapters while moving successful-run bookkeeping to the orchestrator. Add presentation-only config sanitization at the API boundary, retain the complete owner-readable recovery snapshot, serialize the store's shared transcript file, and bound JSON input at the HTTP boundary.

## Task 1: Establish successful-run ownership

1. Add failing orchestrator tests for a failed first run and a failed follow-up.
2. Add/update adapter tests proving `Send` preserves the incoming `LastRunID`.
3. Remove normal-run `LastRunID` assignments from all adapters.
4. Update the orchestrator only on a successful backend outcome; preserve the prior value on failure/interruption.
5. Run adapter and orchestrator tests, then commit.

## Task 2: Protect config presentation and persistence

1. Add a failing API test containing password/token/env/authenticated-URL options.
2. Implement a deep cloned, redacted session response without mutating runtime configuration.
3. Add a store test that asserts the complete persisted config remains available only with mode `0600`.
4. Run API and store tests, then commit.

## Task 3: Serialize transcript access

1. Add a concurrent append test that reads and validates every event.
2. Protect transcript append/read operations with a store mutex.
3. Run store tests with the race detector, then commit.

## Task 4: Bound create-run input

1. Add a failing API test for a request larger than 1 MiB and assert no run is created.
2. Apply `http.MaxBytesReader` and map `MaxBytesError` to HTTP 413.
3. Run API tests, then commit.

## Task 5: Validate the integrated change

1. Run `gofmt` on modified Go files.
2. Run focused package tests.
3. Run `go test ./...` and `go test -race ./...`.
4. Confirm the worktree contains only scoped changes and summarize accepted/rejected findings.
