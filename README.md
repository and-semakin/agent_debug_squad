# Agent Debug Squad

Agent Debug Squad is a small local HTTP service for coordinating a named set of debugging agents. It loads a squad configuration, initializes agent state, accepts run requests over HTTP, and persists session state and run artifacts under the configured workspace.

## Run Locally

Start the sample fake-backend squad:

```sh
go run ./cmd/agent-debug-squad serve --config examples/squad.yaml
```

The server listens on `127.0.0.1:8080` with the sample config.

List agents:

```sh
curl http://127.0.0.1:8080/agents
```

Submit a run to Reviewer:

```sh
curl -X POST http://127.0.0.1:8080/agents/Reviewer/runs \
  -H 'Content-Type: application/json' \
  -d '{"message":"Review this debugging hypothesis.","metadata":{"source":"local-smoke"}}'
```

Wait for a run to complete in the same request:

```sh
curl -X POST 'http://127.0.0.1:8080/agents/Reviewer/runs?wait=true&timeout_seconds=5' \
  -H 'Content-Type: application/json' \
  -d '{"message":"Review this debugging hypothesis."}'
```

Wait for an existing run without starting another one:

```sh
curl -X GET 'http://127.0.0.1:8080/runs/run_000001?wait=true&timeout_seconds=600'
```

If the wait timeout expires before the run finishes, the response is still `200 OK` with the latest `RunRecord`, and the run continues in the background.
When `timeout_seconds` is omitted, `POST /agents/{name}/runs?wait=true` waits up to 60 seconds and `GET /runs/{run_id}?wait=true` waits up to 30 seconds.

Reset an idle or failed agent so its next run starts in a fresh backend session:

```sh
curl -X POST http://127.0.0.1:8080/agents/Reviewer/reset
```

If an agent is stuck in a run, interrupt that run and reset the agent explicitly:

```sh
curl -X POST 'http://127.0.0.1:8080/agents/Reviewer/reset?force=true'
```

Reset keeps existing run artifacts and appends an `agent_reset` event to `transcript.jsonl`. The next run after reset includes the agent startup prompt again.

The reset response includes both audit fields and the full updated agent state:

```json
{
  "agent": "Reviewer",
  "previous_backend_session_id": "thread_old",
  "backend_session_id": "thread_new_or_empty",
  "status": "idle",
  "active_run": false,
  "force": false,
  "state": {
    "name": "Reviewer",
    "last_run_id": "",
    "status": "idle"
  }
}
```

If `POST /agents/{name}/reset` returns `404`, check that the running server binary is up to date and that the request is going to the expected host and port. Older `agent-debug-squad` server processes do not have the reset route; reinstall with `go install ./cmd/agent-debug-squad` and restart the server.

## Output Layout

State is written below:

```text
<workspace_dir>/<state_dir_name>/sessions/<session_id>/
  config.json
  transcript.jsonl
  agents/<agent_name>/state.json
  runs/<run_id>/run.json
  runs/<run_id>/<agent_name>.events.jsonl
  runs/<run_id>/<agent_name>.txt
  runs/<run_id>/<agent_name>.stderr.log
```

With `examples/squad.yaml`, that starts under `./.agent-debug-squad/sessions/<session_id>/`.

CLI-backed agents stream intermediate stdout into `.events.jsonl` and stderr into `.stderr.log` while the run is still active. OpenCode-backed agents stream raw OpenCode `/event` JSON into `.events.jsonl` while the run is active, then write the final assistant response to `<agent>.txt` after `session.idle`. The same lines are emitted to the server log with `run`, `agent`, and `stream` fields. YOLO mode defaults to `defaults.yolo: true`; Codex uses `--dangerously-bypass-approvals-and-sandbox`, Cursor uses `--force`, while Kimi prompt mode ignores YOLO because Kimi 0.10.1 rejects `--prompt` combined with permission flags and OpenCode HTTP mode does not expose an equivalent permission bypass. An agent can opt out with `options.yolo: false`.

## Codex Environment Whitelist

Codex-backed agents can pass a constrained environment through `options.inherit_env`. Keep this list explicit and minimal:

```yaml
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review the proposed fix and identify risks.
    options:
      command: codex
      model: gpt-5.5
      reasoning: medium
      inherit_env:
        - OPENAI_API_KEY
        - CODEX_HOME
        - PATH
```

`model` maps to Codex `--model`. `reasoning` maps to Codex `model_reasoning_effort`; omit either key to use the default from Codex configuration.

## Cursor Backend

Install and authenticate Cursor Agent CLI before starting a Cursor-backed squad:

```sh
curl https://cursor.com/install -fsS | bash
cursor-agent login
cursor-agent status
cursor-agent --list-models
```

Cursor agents support a specific model, stateful resume, enforced read-only modes, sandbox selection, and the same constrained child-process environment pattern as Codex:

```yaml
agents:
  - name: CursorCritic
    backend: cursor
    startup_prompt: Review the code and report evidence. Do not edit files.
    options:
      command: /Users/andrey/.local/bin/cursor-agent
      model: composer-2.5
      mode: ask
      sandbox: enabled
      yolo: false
      env:
        - HTTP_PROXY=http://proxy.example:3128
        - HTTPS_PROXY=http://proxy.example:3128
      inherit_env:
        - PATH
        - HOME
        - CURSOR_API_KEY
        - NO_PROXY
```

`HOME` lets the child process use credentials saved by `cursor-agent login`. Alternatively, inherit `CURSOR_API_KEY` from the service environment. For proxy access, either put explicit `KEY=value` entries in `options.env` or name existing variables in `options.inherit_env`; do not export backend-specific proxy settings globally from a squad launcher.

`model` maps to Cursor `--model`. `mode` maps to `--mode` (`ask` and `plan` are read-only Cursor modes), and `sandbox` maps to `--sandbox`. Agent Debug Squad passes `--force` when YOLO is enabled. Reviewer roles should therefore set both `mode: ask` and `yolo: false`.

The first successful Cursor event supplies a `session_id`, which is persisted as `backend_session_id`. Later turns use `--resume <session_id>`. `POST /agents/{name}/reset` clears this continuity so the next turn starts a new Cursor conversation and receives the startup prompt again.
