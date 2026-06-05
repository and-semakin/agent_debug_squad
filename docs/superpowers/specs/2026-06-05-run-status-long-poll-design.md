# Run Status Long Poll Design

## Context

`POST /agents/{name}/runs?wait=true&timeout_seconds=N` can start a run and hold the HTTP request until the run reaches a terminal state or the wait timeout expires. When that wait timeout expires, the agent run continues in the background and the API returns the latest `RunRecord`.

Clients that receive a still-running response need a matching way to wait again without creating a second run. Today `GET /runs/{run_id}` returns the current `RunRecord` immediately and does not support long polling.

## Goal

Extend `GET /runs/{run_id}` with optional long-polling arguments:

```http
GET /runs/{run_id}?wait=true&timeout_seconds=600
```

By default the endpoint remains non-blocking and returns the current run state immediately.

## API Contract

`GET /runs/{run_id}` without `wait=true` keeps its current behavior:

- `200 OK` with the current `RunRecord` when the run exists.
- `404 Not Found` for an unknown run id.
- `400 Bad Request` for unsafe run id path values.
- `500 Internal Server Error` for unexpected store or orchestration errors.

`GET /runs/{run_id}?wait=true` waits for the existing run to reach a terminal state. If `timeout_seconds` is omitted, the endpoint waits for 30 seconds. If `timeout_seconds` is present, it must be a positive integer number of seconds.

Normal long-poll outcomes all return `200 OK` with a `RunRecord`:

- If the run is already terminal, return immediately.
- If the run reaches a terminal state before the timeout, return the terminal `RunRecord`.
- If the timeout expires first, return the latest non-terminal `RunRecord`, usually `queued` or `running`.

The wait timeout does not stop, cancel, interrupt, or reset the agent. The active run continues in the background.

Invalid `timeout_seconds` returns `400 Bad Request` and does not affect the run.

## Architecture

The implementation should keep the long-polling logic centralized in the existing orchestrator:

- The HTTP handler for `GET /runs/{run_id}` parses `wait` and `timeout_seconds`.
- With no `wait=true`, it continues calling `orchestrator.Run`.
- With `wait=true`, it calls `orchestrator.Wait(ctx, runID, timeout)`.
- `orchestrator.ErrWaitTimeout` is treated as a normal status-read outcome and still returns `200 OK` with the latest run state.

This mirrors the existing create-run wait behavior while using a different default timeout for status polling: 30 seconds for `GET /runs/{run_id}?wait=true`, while preserving the current create-run wait default.

## Request Cancellation

If the HTTP request context is canceled while waiting, the handler returns an error response using the same general semantics as the existing create-run wait path. Request cancellation does not cancel the agent run; the run continues under the orchestrator execution context unless explicitly interrupted through reset.

## Testing

Add focused API tests for:

- Existing `GET /runs/{run_id}` without wait still returns immediately with `200 OK`.
- `GET /runs/{run_id}?wait=true` returns a completed `RunRecord` when the run finishes within the wait window.
- `GET /runs/{run_id}?wait=true&timeout_seconds=1` against a delayed run returns `200 OK` with the latest non-terminal state and the run can still complete afterward.
- Invalid `timeout_seconds` values return `400 Bad Request`.
- Missing run ids with `wait=true` still return `404 Not Found`.

Where practical, verify the 30-second GET wait default through a small helper-level test rather than a slow wall-clock test.

## Documentation

Update README usage examples to show:

```sh
curl -X GET 'http://127.0.0.1:8080/runs/run_000001?wait=true&timeout_seconds=600'
```

Document that timeout only limits the HTTP wait and does not stop the agent run.
