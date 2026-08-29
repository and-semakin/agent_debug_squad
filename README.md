# Agent Debug Squad

[![CI](https://github.com/and-semakin/agent_debug_squad/actions/workflows/ci.yml/badge.svg)](https://github.com/and-semakin/agent_debug_squad/actions/workflows/ci.yml)

Agent Debug Squad is a local, REST-controlled coordinator for long-lived coding-agent sessions. It gives a facilitator a small common API for starting turns, preserving backend sessions, collecting streamed artifacts, and resetting individual agents without losing the rest of the review.

The project is intentionally lightweight: one Go process owns one YAML-configured squad and stores all state as readable files inside the target workspace.

## Why It Exists

Coding-agent CLIs expose different flags, session formats, and streaming protocols. Agent Debug Squad normalizes the lifecycle around a few operations:

- configure named agents with distinct roles and models;
- run different agents independently or in parallel;
- resume each backend's conversation across turns;
- stream stdout and backend events into inspectable artifacts;
- interrupt and reset one stuck agent without restarting the squad;
- let an external facilitator coordinate review, critique, and implementation rounds through HTTP.

Supported backends are Codex CLI, Cursor Agent CLI, OpenCode, Kimi CLI, and a deterministic fake backend for local smoke tests.

## Quick Start

Requirements:

- Go 1.22 or newer;
- the CLI or service required by every backend in your chosen YAML config.

Install the current release from GitHub. For example, on an Apple Silicon Mac:

```sh
mkdir -p "$HOME/.local/bin"
curl -fsSL https://github.com/and-semakin/agent_debug_squad/releases/latest/download/agent-debug-squad_darwin_arm64.tar.gz \
  | tar -xz -C "$HOME/.local/bin"
"$HOME/.local/bin/agent-debug-squad" version
```

Release archives are also available for macOS AMD64 and Linux AMD64/ARM64. Ensure `$HOME/.local/bin` is on `PATH`. A binary installed with `go install` is a development build and intentionally does not self-update because it has no release version embedded in it.

Or run the checkout directly with the fake-backend example, which requires no external AI service:

```sh
git clone https://github.com/and-semakin/agent_debug_squad.git
cd agent_debug_squad
go run ./cmd/agent-debug-squad serve --config examples/squad.yaml
```

The example listens only on `127.0.0.1:8080`. In another terminal:

```sh
curl http://127.0.0.1:8080/agents

curl -X POST 'http://127.0.0.1:8080/agents/Reviewer/runs?wait=true&timeout_seconds=30' \
  -H 'Content-Type: application/json' \
  -d '{"message":"Review this debugging hypothesis."}'
```

## Updates

Release builds check the latest stable GitHub Release before `serve` starts. If a newer semantic version exists for the current platform, the CLI downloads its archive, verifies it against the published SHA-256 checksums, atomically replaces the current executable, and restarts with the same arguments and environment. Network and update errors are logged but do not prevent the server from starting.

Run the same check explicitly without starting a server:

```sh
agent-debug-squad update
```

Disable the startup check for one invocation with `--no-auto-update`, or for an environment with `AGENT_DEBUG_SQUAD_NO_AUTO_UPDATE=1`:

```sh
agent-debug-squad serve --config squad.yaml --no-auto-update
```

Automatic replacement is supported on macOS and Linux. Development builds report version `dev` and skip update checks.

## Configuration

A squad is a YAML file with session storage, a loopback address, and one or more named agents:

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
      Review the repository. Report concrete findings and do not edit files.
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

`log_level` accepts `quiet`, `info`, `debug`, or `trace` and defaults to `info`. `info` logs run lifecycle transitions without mirroring backend event streams, `debug` additionally logs stderr and safe adapter diagnostics, and `trace` logs complete stdout/stderr streams. All levels continue to preserve the full streams in run artifacts.

See [examples/squad.yaml](examples/squad.yaml) for a self-contained fake squad, [examples/cursor-squad.yaml](examples/cursor-squad.yaml) for a read-only Cursor reviewer, and [configs/code-review-squad.yaml](configs/code-review-squad.yaml) for a larger facilitator/implementer/critic setup.

### Backend Notes

| Backend | Connection | Session continuity | Important options |
| --- | --- | --- | --- |
| `codex` | CLI process | Codex resume ID | `command`, `model`, `reasoning`, `yolo` |
| `cursor` | Cursor Agent CLI | Cursor `session_id` | `command`, `model`, `mode`, `sandbox`, `yolo` |
| `opencode` | Local HTTP server | OpenCode session ID | `base_url`, `model`, `timeout_seconds` |
| `kimi` | CLI process | Kimi local session | `command`, `model`, optional `session_root` |
| `fake` | In process | Deterministic state | none |

`defaults.yolo` is `true` when omitted. Codex maps YOLO to its approval/sandbox bypass flags, and Cursor maps it to `--force`. Reviewer roles should explicitly use `yolo: false`; Cursor reviewers should additionally use a read-only `mode` such as `ask` or `plan`.

Cursor model IDs depend on the account and current catalog. Verify them before use:

```sh
cursor-agent --list-models
```

## Environment And Secrets

CLI-backed agents receive a constrained environment rather than the server's complete ambient environment:

- `options.env` sets explicit `KEY=value` entries on one agent process;
- `options.inherit_env` copies only the named variables from the server process;
- later explicit entries override inherited values.

Keep credentials out of committed YAML. Prefer environment variables, an OS credential store, or a private ignored launcher/config. The checked-in proxy URLs use the reserved `.example` domain and are non-functional placeholders.

Cursor browser authentication normally requires inheriting `HOME`; API-key authentication requires `CURSOR_API_KEY`. When Cursor uses `HTTP_PROXY` or `HTTPS_PROXY`, also set `NODE_USE_ENV_PROXY=1`. Inherit `NODE_EXTRA_CA_CERTS` if the proxy performs TLS inspection.

## HTTP API

The main endpoints are:

```text
GET  /health
GET  /agents
GET  /runs
GET  /runs/{run_id}
POST /agents/{agent}/runs
POST /agents/{agent}/reset
GET  /transcript
```

Append `?wait=true&timeout_seconds=N` when creating a run or reading one by ID to long-poll for progress. A wait timeout does not cancel the run; it returns the latest `RunRecord`. Starting a second run for an already-busy agent returns `409 Conflict`.

Active run records include observable progress:

```json
{
  "status": "running",
  "progress": {
    "phase": "waiting_for_subagent",
    "last_activity_at": "2026-08-27T14:07:48Z",
    "child_last_activity_at": "2026-08-27T14:07:48Z",
    "subagents": [
      {
        "id": "agent-0",
        "parent_id": "main",
        "status": "running",
        "last_activity_at": "2026-08-27T14:07:48Z"
      }
    ]
  }
}
```

The same fields are returned by `GET /runs`, `GET /runs/{run_id}`, and timed-out long polls. Every backend stdout/stderr line advances the live `last_activity_at`; disk persistence is rate-limited while the in-memory API view remains current. Kimi runs enter `waiting_for_subagent` when the root stream calls the `Agent` tool and return to `running` when that tool result arrives. While the root stream is waiting, the Kimi adapter watches the matching local Kimi session's `state.json` and child `wire.jsonl` files so `child_last_activity_at` continues to advance. This visibility is a liveness signal for facilitators; the service does not automatically force-reset a quiet run.

Reset an idle agent so its next turn starts a fresh backend session:

```sh
curl -X POST http://127.0.0.1:8080/agents/Reviewer/reset
```

Force-reset a stuck agent and mark its active run as interrupted:

```sh
curl -X POST 'http://127.0.0.1:8080/agents/Reviewer/reset?force=true'
```

The service deliberately accepts only loopback hosts because the v1 API has no authentication. Do not expose it directly to a network.

## Artifacts

Each session is stored below `<workspace_dir>/<state_dir_name>/sessions/<session_id>/`:

```text
config.json
transcript.jsonl
agents/<agent_name>/state.json
runs/<run_id>/run.json
runs/<run_id>/<agent_name>.events.jsonl
runs/<run_id>/<agent_name>.txt
runs/<run_id>/<agent_name>.stderr.log
runs/<run_id>/<agent_name>.diagnostics.jsonl
```

The diagnostic artifact records safe adapter invocation metadata. Cursor diagnostics include the executable and effective CLI flags while omitting prompts, environment values, credentials, and backend session IDs. The files are designed to be readable by people, scripts, and other agents. Add the configured state directory to the workspace's `.gitignore`; runtime transcripts may contain source code, prompts, or model output.

## Codex Skill

A portable Codex skill for operating Agent Debug Squad is included at [skills/agent-debug-squad/SKILL.md](skills/agent-debug-squad/SKILL.md). Copy the `agent-debug-squad` directory into your Codex skills directory if you want it available outside this repository. Keep machine-specific paths and credentials in local configuration rather than editing the tracked copy.

## Development

Run the complete test suite:

```sh
go test ./...
```

Build the CLI:

```sh
go build ./cmd/agent-debug-squad
```

The design records and implementation plans under [docs/superpowers](docs/superpowers) document the project's SDD history. [docs/code-review-squad.md](docs/code-review-squad.md) describes the larger example workflow.

## Current Scope

Agent Debug Squad is a local developer tool, not a hosted multi-tenant service. It does not provide API authentication, distributed scheduling, a browser UI, or automatic agent-to-agent broadcast. A facilitator must send explicit turns, and agents exchange longer results through the persisted artifact files.

## License

Agent Debug Squad is released under the [BSD 3-Clause License](LICENSE).
