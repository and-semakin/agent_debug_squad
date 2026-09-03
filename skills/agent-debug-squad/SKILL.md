---
name: agent-debug-squad
description: Configure, start, facilitate, monitor, and close local Agent Debug Squad multi-agent debugging and code-review sessions through YAML, the CLI, REST API, and persisted run artifacts.
---

# Agent Debug Squad

Use Agent Debug Squad as a local REST-controlled coordinator for named coding agents. One server process owns one YAML-configured session and stores agent state, transcripts, and run artifacts inside the target workspace.

## Core Workflow

1. Inspect the repository and choose distinct roles. Keep reviewers read-only; only implementer roles should edit code.
2. Create or adapt a project-local YAML config. Store runtime state in an ignored directory.
3. Check that `agent-debug-squad` and every configured backend command are installed and authenticated.
4. Start one server process with `agent-debug-squad serve --config <config.yaml>`.
5. Drive named agents through the REST API. Give agents explicit artifact paths when asking them to respond to another agent.
6. Verify implementation turns with repository tests and Git state.
7. Collect a short retro, report changes and remaining risks, then stop only processes started for this session.

Do not assume that agent output is broadcast to other agents. Each agent has its own backend session. Parallel runs are allowed for different agents; a second run for an already-busy agent returns `409 Conflict`.

## Configuration

Use a portable config shape such as:

```yaml
session_name: local-review
workspace_dir: .
state_dir_name: .agent-debug-squad
host: 127.0.0.1
port: 8080
log_level: info
defaults:
  yolo: false
agents:
  - name: Reviewer
    backend: cursor
    startup_prompt: |
      Review the repository and report evidence. Do not edit files.
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

`log_level` defaults to `info`: lifecycle only. Use `debug` for stderr plus safe adapter invocation diagnostics, or `trace` only when full backend stdout/stderr mirroring is needed. Full streams remain in run artifacts at every level.

Backends:

- `codex`: CLI adapter with `command`, optional `model`, `reasoning`, `yolo`, `env`, and `inherit_env`.
- `cursor`: Cursor Agent CLI adapter with `command`, optional `model`, read-only `mode`, `sandbox`, `yolo`, `env`, and `inherit_env`.
- `opencode`: HTTP adapter with `base_url`, optional `model`, and `timeout_seconds`.
- `kimi`: CLI adapter with `command` and optional model settings.
- `fake`: deterministic in-process adapter for smoke tests.

Keep credentials out of YAML and skill files. Use `options.inherit_env` for explicitly selected variables already present in the server environment. Use `options.env` only for non-secret values or values supplied through a private, ignored config. Never commit proxy URLs containing user information.

For Cursor browser login, inherit `HOME`; for API-key authentication, inherit `CURSOR_API_KEY`. When inheriting `HTTP_PROXY` or `HTTPS_PROXY`, set `NODE_USE_ENV_PROXY=1`. Inherit `NODE_EXTRA_CA_CERTS` if a TLS-inspecting proxy requires it. Verify account-specific model IDs with `cursor-agent --list-models` rather than guessing from display names.

## Start And Verify

Check only the backends present in the config:

```sh
command -v agent-debug-squad
command -v codex
command -v cursor-agent
cursor-agent status
command -v opencode
command -v kimi
```

OpenCode-backed agents require a reachable `opencode serve` endpoint. Start other prerequisite services before the squad, then run:

```sh
agent-debug-squad serve --config configs/squad.yaml
curl -sS http://127.0.0.1:8080/health
curl -sS http://127.0.0.1:8080/agents
```

Treat the service as local-only. It restricts its listener to loopback hosts because the API has no authentication.

## Drive Runs

Send a turn and wait for up to ten minutes:

```sh
curl -sS -X POST \
  'http://127.0.0.1:8080/agents/Reviewer/runs?wait=true&timeout_seconds=600' \
  -H 'Content-Type: application/json' \
  -d '{"message":"Review the current code. Return Findings, Evidence, and one Improvement Request."}'
```

If the HTTP wait expires while the run is still active, wait on that run instead of creating a duplicate:

```sh
curl -sS 'http://127.0.0.1:8080/runs/run_000001?wait=true&timeout_seconds=600'
```

The wait timeout does not cancel or reset the backend run. For immediate status, use `GET /runs` or `GET /runs/{run_id}`. Active run records include a `progress` object, and every backend stdout/stderr line advances the live `progress.last_activity_at`. For Kimi and OpenCode, inspect `progress.phase`, `progress.child_last_activity_at`, and `progress.subagents` before deciding that a quiet parent run is stuck. `waiting_for_subagent` with advancing child activity means the nested agent is still working. Kimi derives this signal from its local session tree; OpenCode derives it from parent-linked sessions and `task` tool metadata in the global event stream.

Use artifact files as the cross-agent channel:

```text
Read /absolute/workspace/.agent-debug-squad/sessions/<session_id>/runs/<run_id>/Reviewer.txt.
Respond to its concrete findings. Do not edit files.
```

For a bounded review, ask a critic for findings, ask an advocate to filter overreach, synthesize one concrete implementation request, send it to the implementer, and verify the result. Stop the loop when further discussion produces no meaningful risk or useful change.

## Reset One Agent

Reset an idle or failed agent to clear backend continuity while preserving artifacts:

```sh
curl -sS -X POST http://127.0.0.1:8080/agents/Reviewer/reset
```

A normal reset returns `409 Conflict` while the agent is busy. Force-reset only when abandoning a stuck run:

```sh
curl -sS -X POST 'http://127.0.0.1:8080/agents/Reviewer/reset?force=true'
```

Force reset marks the active run `interrupted`, clears the backend session ID, and keeps existing artifacts.

## Artifacts And Completion

Read results from:

```text
<workspace>/<state_dir>/sessions/<session_id>/
  transcript.jsonl
  agents/<agent>/state.json
  runs/<run_id>/run.json
  runs/<run_id>/<agent>.events.jsonl
  runs/<run_id>/<agent>.txt
  runs/<run_id>/<agent>.stderr.log
  runs/<run_id>/<agent>.diagnostics.jsonl
```

Cursor diagnostics record the executable and effective CLI flags while omitting prompts, environment values, credentials, and backend session IDs.

Before finishing:

- verify relevant tests and the working tree;
- report what was discussed, what changed, checks run, commits, and remaining risks;
- stop only the local processes started for this session;
- leave runtime artifacts ignored and do not commit transcripts that may contain source, prompts, or model output.
