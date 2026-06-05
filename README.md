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

CLI-backed agents stream intermediate stdout into `.events.jsonl` and stderr into `.stderr.log` while the run is still active. The same lines are emitted to the server log with `run`, `agent`, and `stream` fields. YOLO mode defaults to `defaults.yolo: true`; Codex uses `--dangerously-bypass-approvals-and-sandbox`, while Kimi prompt mode ignores YOLO because Kimi 0.10.1 rejects `--prompt` combined with permission flags. An agent can opt out with `options.yolo: false`.

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
