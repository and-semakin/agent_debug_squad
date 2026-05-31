# Agent Run Streaming And YOLO Defaults Design

## Summary

Agent Debug Squad should expose useful live visibility into agent runs and should run coding agents in non-interactive approval mode by default. The current implementation buffers Codex and Kimi stdout until the child process exits, then writes only the final answer and optional stderr. That makes long or stuck runs opaque.

This change adds streaming tee behavior for CLI-backed adapters and a configurable YOLO mode. During a run, stdout/stderr lines are appended to run artifacts and mirrored to the server log as they arrive. By default, Codex and Kimi runs use their respective approval-bypass flags unless an agent explicitly opts out.

## Goals

- Persist intermediate agent stdout/stderr while the agent is still running.
- Mirror intermediate output to the `agent-debug-squad` process logs with run and agent context.
- Keep final answer behavior unchanged: `<agent>.txt` remains the final human-readable response.
- Make YOLO mode the default for configured agents.
- Allow per-agent override to disable YOLO.
- Keep OpenCode behavior explicit: no fake YOLO behavior if the HTTP adapter cannot enforce it.

## Non-Goals

- No browser UI or live dashboard.
- No SSE/websocket streaming API in this iteration.
- No automatic cross-agent broadcast.
- No removal of existing final output, stderr, run, state, or transcript files.
- No attempt to reverse-engineer OpenCode permission behavior beyond the currently used HTTP API.

## Configuration

Add top-level defaults:

```yaml
defaults:
  yolo: true
```

If omitted, `defaults.yolo` is treated as `true`.

Allow per-agent override through options:

```yaml
agents:
  - name: Implementer
    backend: codex
    options:
      yolo: false
```

Effective value:

1. If `agent.options.yolo` is set, use it.
2. Else use `defaults.yolo`.
3. Else default to `true`.

The config loader currently supports string and list options. This feature should support YAML booleans for `defaults.yolo` and `options.yolo`. If preserving the existing option maps is simpler, normalize `options.yolo` into `StringOptions["yolo"]` with `"true"` or `"false"` and expose a helper that computes the effective value.

## Streaming Artifacts

For each run, create append-only files:

```text
runs/<run_id>/<agent_name>.events.jsonl
runs/<run_id>/<agent_name>.stderr.log
```

`<agent>.events.jsonl` stores each stdout line exactly as emitted by the backend process. For Codex this is already JSONL. For Kimi in `--output-format stream-json`, this is stream JSONL. Do not parse/rewrite these lines before storing them.

`<agent>.stderr.log` stores stderr lines exactly as emitted by the backend process.

Both files should be written during the run, not after completion. They are diagnostic artifacts. The final `<agent>.txt` file still contains only the final agent message after parsing.

## Server Logging

Every streamed line should also be logged by the server with enough context to correlate it:

```text
run=run_000012 agent=Critic stream=stdout <line>
run=run_000012 agent=Critic stream=stderr <line>
```

Use the standard logger initially. Avoid logging full prompts separately; only log backend stdout/stderr that would already be written to artifacts.

## Run Sink

Introduce a small streaming boundary used by adapters:

```go
type RunSink interface {
    StdoutLine(line string)
    StderrLine(line string)
}
```

The orchestrator creates a sink for each run and passes it into the adapter call. The sink is responsible for:

- appending stdout lines to `<agent>.events.jsonl`
- appending stderr lines to `<agent>.stderr.log`
- logging each line with run, agent, and stream
- preserving write errors so the orchestrator can mark the run failed if artifact writes fail

The adapter is responsible for calling the sink as soon as lines arrive.

## Adapter Changes

### Codex

Change `codex.Send` from `cmd.Output()` to `StdoutPipe` and `StderrPipe`.

Execution flow:

1. Build the existing `codex exec --json ...` args.
2. If effective YOLO is true, add `--dangerously-bypass-approvals-and-sandbox`.
3. Start the process.
4. Scan stdout and stderr concurrently.
5. For each stdout line, call `sink.StdoutLine(line)` and append it to an in-memory buffer for existing JSONL parsing.
6. For each stderr line, call `sink.StderrLine(line)` and append it to an in-memory buffer for existing error handling.
7. Wait for scanners and process exit.
8. Reuse existing parsing logic against the accumulated stdout/stderr.

### Kimi

Change `kimi.Send` from `cmd.Output()` to `StdoutPipe` and `StderrPipe`.

Execution flow:

1. Build the existing `kimi -p ... --output-format stream-json` args.
2. If effective YOLO is true, add `--yolo`.
3. Stream stdout/stderr through the sink and accumulated buffers.
4. Reuse existing stream-json parsing against accumulated stdout/stderr.

### OpenCode

The current OpenCode adapter uses HTTP request/response. It does not expose live stream lines through the adapter today.

For this iteration:

- Do not claim OpenCode YOLO is enabled.
- If effective YOLO is true for an OpenCode agent, log a warning once during init or send:

```text
agent=Critic backend=opencode yolo=true unsupported by opencode HTTP adapter
```

- Do not add OpenCode event artifact support in this iteration. Live streaming OpenCode events can be designed later against the OpenCode event API.

## Error Handling

- If writing to streaming artifacts fails, the sink records the error.
- The adapter should continue trying to complete the process if possible.
- After `Send` returns, orchestrator checks the sink error. If no adapter error occurred but sink failed, mark the run failed with the sink error.
- If stdout/stderr scanners fail, treat that as an adapter error.
- If process execution fails, preserve any streamed artifacts already written.

## Backward Compatibility

- Existing final output paths and transcript behavior remain unchanged.
- Existing clients using `/runs`, `/transcript`, and `<agent>.txt` continue to work.
- New artifact files are additive.
- Existing YAML without `defaults.yolo` runs in YOLO mode by default.
- Agents can opt out with `options.yolo: false`.

## Testing

Add focused tests:

- Config:
  - omitted `defaults.yolo` means true
  - top-level `defaults.yolo: false` is respected
  - `options.yolo` overrides default
- Run sink:
  - stdout lines append to `.events.jsonl`
  - stderr lines append to `.stderr.log`
  - server log receives run/agent/stream context
  - append errors are surfaced
- Codex adapter:
  - fake command prints stdout/stderr lines with small delays
  - sink receives lines before process completion
  - accumulated stdout still parses final answer
  - YOLO true adds `--dangerously-bypass-approvals-and-sandbox`
  - YOLO false omits the flag
- Kimi adapter:
  - fake command prints stream-json stdout and stderr
  - sink receives lines
  - accumulated stdout still parses final answer
  - YOLO true adds `--yolo`
  - YOLO false omits the flag
- OpenCode adapter:
  - yolo true produces an unsupported warning rather than silently pretending it is applied

## Operational Notes

During a run, operators can inspect:

```sh
tail -f .agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.events.jsonl
tail -f .agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.stderr.log
```

The server process log should show the same streamed lines with contextual prefixes. This makes long-running agent behavior visible even when the final run has not completed yet.
