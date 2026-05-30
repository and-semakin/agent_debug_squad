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

## Output Layout

State is written below:

```text
<workspace_dir>/<state_dir_name>/sessions/<session_id>/
  config.json
  transcript.jsonl
  agents/<agent_name>/state.json
  runs/<run_id>/run.json
  runs/<run_id>/<agent_name>.txt
  runs/<run_id>/<agent_name>.stderr.log
```

With `examples/squad.yaml`, that starts under `./.agent-debug-squad/sessions/<session_id>/`.

## Codex Environment Whitelist

Codex-backed agents can pass a constrained environment through `options.inherit_env`. Keep this list explicit and minimal:

```yaml
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review the proposed fix and identify risks.
    options:
      command: codex
      inherit_env:
        - OPENAI_API_KEY
        - CODEX_HOME
        - PATH
```
