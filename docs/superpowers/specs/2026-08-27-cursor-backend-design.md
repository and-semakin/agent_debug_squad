# Cursor Backend Design

## Summary

Agent Debug Squad should support Cursor Agent CLI as a stateful backend alongside Codex, Kimi, and OpenCode. Cursor exposes a headless subprocess interface with explicit model selection, NDJSON events, a stable session id, and session resume. Those capabilities map directly onto the existing `AgentAdapter` contract.

The backend will run one `cursor-agent --print` process per turn, persist the Cursor `session_id` in `AgentState.BackendSessionID`, and pass that id back through `--resume` on later turns. Cursor stdout and stderr will use the existing `RunSink` artifact pipeline.

## Goals

- Allow `backend: cursor` in YAML agent definitions.
- Run a specific Cursor model through `options.model`, including `composer-2.5`.
- Preserve one Cursor conversation across multiple Agent Debug Squad runs.
- Stream Cursor NDJSON events into the existing run event artifact.
- Store the terminal Cursor result as the agent's final text artifact.
- Support Cursor `ask` and `plan` modes for enforced read-only roles.
- Map Agent Debug Squad YOLO mode to Cursor `--force`.
- Pass proxy and authentication environment variables through the same explicit `env` and `inherit_env` allowlists used by Codex.
- Reset Cursor continuity through the existing agent reset API.

## Non-Goals

- No Cursor SDK Bridge or ACP integration in this iteration.
- No Cursor Cloud Agent or remote GitHub worktree support.
- No model catalog validation; Cursor CLI remains responsible for rejecting unavailable models.
- No automatic Cursor login or API key creation.
- No change to the generic REST API, run artifacts, or persisted state schema.
- No automatic selection of `ask` mode from an agent name or startup prompt.

## Configuration

```yaml
agents:
  - name: CursorCritic
    backend: cursor
    startup_prompt: |
      Review only. Do not edit files.
    options:
      command: /Users/andrey/.local/bin/cursor-agent
      model: composer-2.5
      mode: ask
      sandbox: enabled
      yolo: false
      env:
        - HTTPS_PROXY=http://proxy.example:3128
        - HTTP_PROXY=http://proxy.example:3128
      inherit_env:
        - PATH
        - HOME
        - CURSOR_API_KEY
```

Backend-specific string options:

- `command`: Cursor Agent executable; defaults to `cursor-agent`.
- `model`: value passed through `--model`.
- `mode`: optional Cursor execution mode. Supported values are passed through to Cursor, which currently accepts `ask` and `plan`.
- `sandbox`: optional Cursor sandbox mode, passed through `--sandbox`.

The generic `options.yolo` value maps to `--force`. Read-only agents should set both `mode: ask` and `yolo: false`; role text alone is not an enforcement boundary.

## Environment And Authentication

Cursor uses the same constrained child-process environment policy as Codex:

- `options.env` contains explicit `KEY=value` entries for this Cursor process only.
- `options.inherit_env` copies only named values from the server environment.
- Ambient proxy variables are not inherited unless named explicitly.
- `HOME` is required when using credentials saved by `cursor-agent login`.
- `CURSOR_API_KEY` can be inherited instead for API-key authentication.
- `PATH` should normally be inherited because Cursor can invoke workspace tools.

This prevents Cursor-specific proxy credentials from leaking into OpenCode, Kimi, or other child processes.

## Command Contract

The first run uses:

```text
cursor-agent --print --trust --output-format stream-json --stream-partial-output [--force] [--model MODEL] [--mode MODE] [--sandbox MODE] MESSAGE
```

Later runs add:

```text
--resume SESSION_ID
```

The adapter sets `cmd.Dir` to `AgentState.WorkspaceDir`. `--trust` prevents a non-interactive workspace trust prompt. `--stream-partial-output` provides live assistant deltas while the final `result` event remains the canonical final response.

The startup prompt wrapper is added only while `LastRunID` is empty, matching the Codex and OpenCode adapters. The orchestrator remains responsible for persisted run lifecycle.

## Stream Parsing

Cursor stream output is newline-delimited JSON. The adapter will:

- Preserve every non-empty line in `RunResult.RawEvents` and forward it to `RunSink` as it arrives.
- Read `session_id` from `system/init` and terminal `result` events.
- Treat `result/subtype=success` with `is_error != true` as completion.
- Use the terminal event's `result` string as `FinalMessage`.
- Accumulate assistant text deltas only as a fallback when a successful terminal result omits text.
- Treat `is_error: true`, a non-success terminal subtype, malformed JSON, a missing terminal result, or a non-zero process exit as a failed run.
- Ignore unknown event fields and event types for forward compatibility.

The scanner limit stays at 8 MiB per event, consistent with the other CLI adapters.

## State And Reset

- `Init` creates normal idle state and records the configured model.
- `Send` reads `LastRunID` for startup prompt gating and writes only Cursor-specific session continuity (`BackendSessionID`).
- A resumed run passes the saved session id through `--resume`.
- `Recover` returns the agent to idle without inventing a new Cursor session.
- `Reset` clears `BackendSessionID`, `LastRunID`, and the previous error while retaining the configured identity, startup prompt, workspace, and model.

## Error Handling

- Stderr is streamed and preserved independently from stdout.
- If Cursor exits before a terminal result, the adapter returns a stable incomplete-turn error.
- If Cursor reports an error event, its message is preferred over a generic process exit error.
- Context cancellation uses `exec.CommandContext`, matching the other CLI adapters.
- A scanner error kills the child promptly so the run cannot hang on an oversized event.

## Compatibility

Existing backends and existing YAML remain unchanged. Generic config parsing already accepts all required string, boolean YOLO, and list options. Persisted `AgentState` already has `Model` and `BackendSessionID`, so no storage migration is required.

## Testing

- Parser tests for success, session id, deltas, terminal errors, malformed JSON, incomplete output, and large events.
- Argument tests for model, mode, sandbox, force, resume, and stable ordering.
- Environment tests proving only explicit and inherited variables reach Cursor.
- Send tests for startup prompt behavior, session persistence, streaming, stderr, and command failures.
- Reset tests for continuity cleanup.
- Adapter factory coverage for `backend: cursor`.
- Full `go test ./...` regression run.
- One real read-only smoke run with authenticated `composer-2.5`, followed by a resumed turn, if credentials are available.
