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

Supported backends are Codex CLI, Cursor Agent CLI, OpenCode, Kimi CLI, ZCode App Server, and a deterministic fake backend for local smoke tests.

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
| `opencode` | Local HTTP server | OpenCode session ID | `base_url`, `model`, `timeout_seconds`, `yolo` |
| `zcode` | Private App Server process | ZCode session ID | `command`, `runtime_path`, `provider`, `model`, `reasoning`, `yolo` |
| `kimi` | CLI process | Kimi local session | `command`, `model`, optional `session_root` |
| `fake` | In process | Deterministic state | none |

`defaults.yolo` is `true` when omitted. Codex maps YOLO to its approval/sandbox bypass flags, Cursor maps it to `--force`, and OpenCode automatically replies `once` to permission requests for the active run and its tracked descendant sessions. Reviewer roles should explicitly use `yolo: false`; Cursor reviewers should additionally use a read-only `mode` such as `ask` or `plan`.

Cursor model IDs depend on the account and current catalog. Verify them before use:

```sh
cursor-agent --list-models
```

### ZCode App Server

See [examples/zcode-squad.yaml](examples/zcode-squad.yaml). Start it with:

```sh
agent-debug-squad serve --config examples/zcode-squad.yaml
```

This experimental adapter supports the tested **ZCode desktop 3.12.3 / runtime 0.16.5** bundle (SHA-256 `da61b0663336a65f7cce3dec223678794ccaa58158e304fc0d97b695434a8f01`) and a signed-in **Z.AI individual Coding Plan** account. Install Node (tested with Node 26) and sign in through ZCode first. Runtime versions are not sufficient compatibility identifiers: unknown bundle fingerprints fail before a prompt is sent. Updating ZCode may require an adapter update. The host bridge loads the installed runtime's native credential reader in memory; it never edits the bundle or exports credentials into Squad state.

Options:

- `command`: Node executable, default `node`.
- `runtime_path`: bundle location, default `/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs`. Other locations must contain the same supported bundle and its companion resources.
- `provider`: currently only `account:zai-individual-coding-plan`.
- `model`: default `GLM-5.3-Flash`; `reasoning`: `low` (default), `high`, or `max`. The full selection is sent on every turn.
- `yolo`: inherits squad defaults. True selects native `yolo`; false explicitly selects `build`. This also updates ZCode's workspace permission preference. False is permission-controlled, **not a read-only sandbox**.
- `env` / `inherit_env`: the same explicit environment rules as other CLI adapters. Inherit `HOME` and `PATH` for the existing login and tools.

**Model discovery:** the current account catalog is captured from `session/create` or `session/resume` as a `zcode.models` record in `<agent>.diagnostics.jsonl` before each prompt. For example:

```sh
jq 'select(.type == "zcode.models") | .model.available[] | {ref, reasoning}' \
  .agent-debug-squad/sessions/<session_id>/runs/<run_id>/ZCodeFlash.diagnostics.jsonl
```

The adapter owns one App Server process per turn and resumes the persisted ZCode session on the next turn. Reset starts a new conversation without deleting the previous history. A run completes only after its matching turn completes, not when the server accepts the input. Stream events are written to the normal run artifacts; direct and nested subagents appear in `progress.subagents`. Historical children from earlier turns are excluded. Child timestamps reflect observed status changes, rather than token-level activity. Background children are stopped with their owning Squad turn; they are not detached jobs.

With YOLO off, tool requests appear in `pending_permissions` and use the same `/runs/{run_id}/permissions/{request_id}/reply` endpoint described below. `always` uses only the backend's offered project rule; it can affect later ZCode work in that project. Duplicate/stale replies are rejected. Native YOLO does not override an explicit denial or answer questions. Browser integration, interactive questionnaires, official-MCP authentication, CAPTCHA and login refresh are not implemented; unsupported interactions fail explicitly. Resolve account challenges in ZCode before retrying.

The standard ZCode session database is shared with the desktop. Open/import the same workspace in ZCode to make its conversations discoverable there; Squad does not write the sidebar index directly. Do not operate the same active conversation concurrently from both hosts. This adapter does not request special off-peak dispatch or promise free tokens, subscription bonuses, or different billing from ordinary account usage.

## Environment And Secrets

CLI-backed agents receive a constrained environment rather than the server's complete ambient environment:

- `options.env` sets explicit `KEY=value` entries on one agent process;
- `options.inherit_env` copies only the named variables from the server process;
- later explicit entries override inherited values.

Keep credentials out of committed YAML. Prefer environment variables, an OS credential store, or a private ignored launcher/config. The checked-in proxy URLs use the reserved `.example` domain and are non-functional placeholders.

Cursor browser authentication normally requires inheriting `HOME`; API-key authentication requires `CURSOR_API_KEY`. When Cursor uses `HTTP_PROXY` or `HTTPS_PROXY`, also set `NODE_USE_ENV_PROXY=1`. Inherit `NODE_EXTRA_CA_CERTS` if the proxy performs TLS inspection.

ZCode model traffic uses `ZCODE_HTTP_PROXY`, with exclusions in `ZCODE_NO_PROXY` and a custom CA file in `ZCODE_AGENT_CA_CERT`. Explicitly inherit these variables (as in the example), or set non-secret values through `env`. Ordinary `HTTP_PROXY` / `HTTPS_PROXY` variables alone are not a substitute for `ZCODE_HTTP_PROXY`: the runtime treats model, web-fetch, and tool traffic differently. The bridge also honors `ZCODE_BUILTIN_PROVIDER_CONFIG_FILE` and `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE` when explicitly passed; normally it derives these paths from the installation and HOME. Custom `ZCODE_STORAGE_DIR` / `ZCODE_SESSION_DB_PATH` isolate conversations from the desktop's default history.

## HTTP API

The main endpoints are:

```text
GET  /health
GET  /agents
GET  /runs
GET  /runs/{run_id}
POST /agents/{agent}/runs
POST /agents/{agent}/reset
POST /runs/{run_id}/permissions/{request_id}/reply
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

The same fields are returned by `GET /runs`, `GET /runs/{run_id}`, and timed-out long polls. Every backend stdout/stderr line advances the live `last_activity_at`; disk persistence is rate-limited while the in-memory API view remains current. Kimi runs enter `waiting_for_subagent` when the root stream calls the `Agent` tool and return to `running` when that tool result arrives. While the root stream is waiting, the Kimi adapter watches the matching local Kimi session's `state.json` and child `wire.jsonl` files so `child_last_activity_at` continues to advance. OpenCode runs do the same for `task` tool calls by following child-session relationships in the global event stream. Child and nested-child events advance both `child_last_activity_at` and the parent run's `last_activity_at`, and each observed OpenCode session appears in `subagents`. This visibility is a liveness signal for facilitators; the service does not automatically force-reset a quiet run.

Reset an idle agent so its next turn starts a fresh backend session:

```sh
curl -X POST http://127.0.0.1:8080/agents/Reviewer/reset
```

Force-reset a stuck agent and mark its active run as interrupted:

```sh
curl -X POST 'http://127.0.0.1:8080/agents/Reviewer/reset?force=true'
```

The service deliberately accepts only loopback hosts because the v1 API has no authentication. Do not expose it directly to a network.


### OpenCode permissions

OpenCode uses its classic HTTP/SSE permission API. Effective `yolo: true` (the default) automatically replies `once` to owned permission requests. It does not change global OpenCode configuration or override explicit backend denials. Set `options.yolo: false` for coordinator-controlled replies. Questions are separate and are not automatically answered.

While any permissions are pending, progress includes:

```json
{
  "status": "running",
  "progress": {
    "phase": "waiting_for_permission",
    "pending_permissions": [{
      "id": "per_example",
      "session_id": "ses_example",
      "permission": "external_directory",
      "patterns": ["/review/*"],
      "metadata": {"filepath": "/review/candidates.md"},
      "always": ["/review/*"],
      "asked_at": "2026-09-15T10:00:00Z"
    }]
  }
}
```

`tool` identifiers are included when supplied by OpenCode. `auto_approving: true` means the adapter is sending an automatic reply. If it fails, `auto_approve_error` contains the failure and the request remains available for a coordinator reply; automatic failures are not retried indefinitely. Requests for unrelated or undiscovered sessions are never approved.

Both create-run and GET-run long polls return early when a manual request or automatic reply failure needs intervention. A pending request does not complete or cancel the run. Respond using its current run and request IDs:

```sh
curl -sS -X POST http://127.0.0.1:8090/runs/run_000001/permissions/per_example/reply \
  -H 'Content-Type: application/json' \
  -d '{"reply":"once","message":"Read the review artifacts"}'
```

Replies are `once`, `always`, or `reject`; `message` is optional. `always` approves OpenCode's suggested patterns for that backend session. Success returns `200` with `{"ok":true}`; invalid input returns `400`, unknown runs/requests `404`, inactive runs or unsupported backends `409`, and backend failures `502`. Concurrent replies are serialized and resolved requests cannot be approved again. Backend calls time out after five seconds and are cancelled with the run. Reset/restart invalidates old actionable requests.

Permission reply attempts are recorded as `squad.permission.reply` events in the run's `.events.jsonl`, with request/session IDs, reply, source (`yolo` or `coordinator`), timestamp, and any error. Waiting for permission takes precedence over waiting for subagents until all requests resolve.

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

### OpenSpec

New features, behavior changes, and substantial refactors use [OpenSpec](https://openspec.dev/).
The repository uses the `spec-driven` schema and Codex skills in `.agents/skills/`.
Project context and artifact rules live in [openspec/config.yaml](openspec/config.yaml);
workflow expectations are in [AGENTS.md](AGENTS.md).

Install the CLI (Node.js 20.19.0 or newer; setup tested with OpenSpec 1.13.0):

```sh
npm install -g @fission-ai/openspec@1.13.0
openspec --version
```

Open a new Codex session after installing or updating project skills. In Codex:

1. Use `$openspec-explore` to investigate an idea when needed.
2. Use `$openspec-propose <description>` to create a proposal, design, spec deltas, and tasks.
3. Review the artifacts, then use `$openspec-apply-change <name>` to implement the tasks.
4. If the scope changes, use `$openspec-update-change <name>` to revise the artifacts.
5. Run the checks below and `openspec validate <name> --strict`; check implementation against requirements and scenarios.
6. Use `$openspec-archive-change <name>` to sync spec deltas and archive completed work.

`openspec/changes/<name>/` holds active work, `openspec/specs/` holds the resulting
capability specs, and `openspec/changes/archive/` preserves completed changes.
Specs start empty and grow with changes; check existing behavior against the code
when first specifying it. Commit artifacts alongside the implementation.
Small fixes that restore specified behavior, typos, and formatting-only edits can
be made directly.

Useful terminal commands:

```sh
openspec list
openspec list --specs
openspec validate --all --strict
```

To regenerate the Codex integration, run `openspec init --tools codex --profile core`.
For another coding tool, run `openspec init --help` and select its tool ID with
`--tools`. After upgrading the CLI, run `openspec update` and review the generated diff.

### Checks and build

Format changed Go files with `gofmt`, then run static checks and the complete test suite:

```sh
go vet ./...
go test -race -count=1 ./...
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
