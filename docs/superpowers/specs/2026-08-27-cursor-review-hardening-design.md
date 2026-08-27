# Cursor Review Hardening Design

## Context

A three-turn Cursor review identified five possible correctness and security issues. This design validates them against HEAD `7c83a02` before changing behavior.

## Accepted Findings

### Successful-run continuity

`LastRunID` is the marker that tells a backend whether startup instructions have already been sent. Adapters currently update it themselves, and the orchestrator then overwrites it unconditionally after every outcome. A failed first run therefore suppresses startup instructions on retry, while a failed follow-up replaces the last successful marker.

The orchestrator will be the sole owner of normal run progression. Adapters may inspect `LastRunID`, but only reset operations may clear it. After a backend turn completes successfully, the orchestrator records the current run ID. A backend failure or forced interruption preserves the previous value. If artifact persistence fails after the backend completed, continuity still advances because the remote or CLI session may already contain that turn.

### Session API secret redaction

`GET /session` serializes the raw configured agent options. These can contain passwords, tokens, explicit environment assignments, or authenticated proxy URLs. Although the server is loopback-only, callers should not receive credential values as configuration metadata.

The API will return a cloned, redacted view. Sensitive option keys are replaced with `[REDACTED]`; explicit `env` values retain only their variable names; URL user-info is removed from other displayed string options. The in-memory runtime config remains unchanged.

The normalized `config.json` snapshot remains complete because the original design documents it as recovery state and removing values would make that snapshot unusable. Atomic store writes already create files with owner-only `0600`; tests will make that security property explicit. This is deliberate defense in depth without changing recovery semantics.

### Serialized transcript appends

Different agents may run concurrently and both facilitator and result events append to one `transcript.jsonl`. The store will serialize appends with a mutex, and transcript reads will share that lock so readers never observe an in-process partial append.

### Bounded run request bodies

`POST /agents/{name}/runs` currently decodes an unbounded body. The handler will cap the complete JSON body at 1 MiB with `http.MaxBytesReader`. Oversized requests return `413 Request Entity Too Large` and create no run.

## Rejected Finding

### Narrower OpenCode SSE filtering

HEAD `7c83a02` intentionally accepts session-scoped events after prompt submission. OpenCode emits useful run activity without message identifiers, and tests reproduce that protocol. Returning to message-only filtering would discard valid tool/session events and can prevent recognizing completion. No reproducible cross-run bug exists because Agent Debug Squad permits only one active run per named agent and ignores idle until activity has been observed. No OpenCode behavior changes are included.

## Testing

- Orchestrator tests cover failed first runs, failed follow-ups, and successful progression.
- Adapter tests assert successful `Send` no longer owns `LastRunID`.
- API tests assert secret redaction, no mutation of runtime config, and a 413 response without run creation.
- Store tests append many transcript events concurrently and verify every JSON event, and assert `config.json` is `0600`.
- Focused packages and the full Go test suite run under the race detector where practical.
