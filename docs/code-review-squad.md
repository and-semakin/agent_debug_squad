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

The launcher exports the proxy variables needed by Codex. The YAML config whitelists those variables through `options.inherit_env` for the Codex-backed agents.

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
