# Code Review Squad

This project-local squad config starts a finite multi-agent code-review session.

## Start

Start Agent Debug Squad:

```sh
./scripts/start-code-review-squad.sh
```

The launcher also starts `opencode serve` for the `Critic` adapter if no OpenCode server is already listening on `127.0.0.1:4096`.

`opencode` is still used because the `Critic` agent is configured as OpenCode with model `zai-coding-plan/glm-5.1`; the local Agent Debug Squad server talks to OpenCode through its headless HTTP server.

The server listens on `127.0.0.1:8090` and writes artifacts under:

```text
.agent-review-artifacts/
```

The launcher does not export backend-specific proxy variables for the whole squad process. The YAML config sets those variables through `options.env` on the relevant CLI-backed agents only. `options.inherit_env` is reserved for explicitly selected process-local values such as `PATH`, `HOME`, `CODEX_HOME`, and `CURSOR_API_KEY`.

Agent state includes the configured `model` when an agent has one, so `GET /agents` and `sessions/<session_id>/agents/<agent_name>/state.json` show which model backs OpenCode-style agents.

During a run, intermediate CLI output is written as it arrives:

```text
.agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.events.jsonl
.agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.stderr.log
```

For OpenCode agents, `<agent>.events.jsonl` contains raw OpenCode `/event` payloads for the active session, including `session.next.*`, `message.*`, `session.error`, and `session.idle` events. The final `<agent>.txt` output is assembled from OpenCode message history after the session returns to idle.

The same lines are also emitted to the `agent-debug-squad` server log with `run`, `agent`, and `stream` fields.

`GET /runs` and `GET /runs/<run_id>` also expose a `progress` object. For Kimi, a root `Agent` tool call sets `progress.phase` to `waiting_for_subagent`. The adapter observes Kimi's local session tree and updates `progress.subagents` plus `progress.child_last_activity_at` as child `wire.jsonl` files change. When the root receives the `Agent` tool result, the phase returns to `running` and the observed child entries become `completed`.

YOLO mode is enabled by default through `defaults.yolo: true`. Codex uses `--dangerously-bypass-approvals-and-sandbox`; Cursor uses `--force`; Kimi prompt mode ignores YOLO because Kimi 0.10.1 rejects `--prompt` combined with permission flags, and OpenCode HTTP mode does not expose an equivalent permission bypass. Set `options.yolo: false` on an agent to opt out.

## Optional Cursor Role

A Cursor agent can replace or complement one of the discussion roles without changing the facilitator protocol:

```yaml
  - name: CursorCritic
    backend: cursor
    startup_prompt: |
      Review only. Do not change files or commit.
    options:
      command: cursor-agent
      model: composer-2.5
      mode: ask
      yolo: false
      env:
        - NODE_USE_ENV_PROXY=1
      inherit_env:
        - PATH
        - HOME
        - HTTP_PROXY
        - HTTPS_PROXY
        - NO_PROXY
```

`mode: ask` provides Cursor's read-only execution boundary. `HOME` is needed for a browser-authenticated CLI; use `CURSOR_API_KEY` instead for service authentication. `NODE_USE_ENV_PROXY=1` makes the Node-based CLI honor inherited HTTP(S) proxy variables. Cursor NDJSON events are written to the normal `.events.jsonl` artifact, and its session id is resumed between turns until the agent is reset.

## Agents

- `Facilitator`: Codex. Owns the session flow and final summary.
- `Implementer`: Codex. Edits code only when Facilitator gives a concrete implementation request.
- `Critic`: OpenCode with model `zai-coding-plan/glm-5.1`. Reads and critiques only.
- `Advocat`: Kimi. Reads and defends current decisions objectively.

## Scenario

The Facilitator prompt enforces:

- 3 improvement cycles.
- 3 discussion rounds per cycle.
- Each discussion round asks `Critic` and `Advocat` for one turn each.
- After each cycle, `Facilitator` sends one concrete task to `Implementer`.
- `Implementer` makes at most 3 implementation turns total.
- Final retro: one turn each from `Critic`, `Advocat`, and `Implementer`, plus a final Facilitator summary.

Kick off the session by sending the initial request to `Facilitator`:

```sh
curl -sS -X POST 'http://127.0.0.1:8090/agents/Facilitator/runs?wait=true&timeout_seconds=120' \
  -H 'Content-Type: application/json' \
  -d '{"message":"Начни сессию code-review для текущего проекта по своему стартовому сценарию."}'
```
