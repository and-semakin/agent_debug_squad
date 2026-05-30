# Agent Debug Squad Design

## Summary

Agent Debug Squad is a local Go service that lets a facilitator coordinate a small group of coding agents through a simple REST JSON API. The service hides backend-specific agent harness details behind adapters, persists each agent's logical session state, and writes each agent turn to files that other agents can be explicitly asked to read.

The first version is intentionally local and file-backed. It supports one squad session per server process, configured from YAML at startup, with asynchronous runs and per-agent locking.

## Goals

- Start one squad session from a YAML config.
- Define multiple named agents, each with its own backend and startup prompt.
- Expose a small REST API for inspecting agents and starting agent runs.
- Keep each agent logically long-lived across runs by preserving backend session identity and local state.
- Store final agent messages as text files that can be shared with other agents by the facilitator.
- Allow different agents to run in parallel while preventing concurrent runs for the same agent.
- Keep backend-specific details out of the REST API through a Go adapter interface.

## Non-Goals

- No multi-session server in v1. Run another process on another port for another experiment.
- No dynamic session creation through REST in v1. YAML is the only session creation path.
- No automatic group chat fan-out or agent-to-agent routing in v1. The facilitator explicitly tells agents which files to read.
- No database in v1. Durable state is stored on the filesystem under the configured workspace.
- No guaranteed live process per backend. A backend may use a per-turn headless invocation as long as it preserves logical session continuity.

## Architecture

The service is a single Go binary:

```text
agent-debug-squad serve --config squad.yaml
```

At startup it reads the YAML config, initializes one squad session, creates the storage tree if needed, initializes each agent adapter, and starts an HTTP server.

Core components:

- `HTTP API`: REST JSON endpoints for health, session, agents, runs, and transcript.
- `Orchestrator`: validates requests, owns run lifecycle, enforces per-agent locks, and delegates backend work to adapters.
- `AgentAdapter`: backend-specific interface implemented by OpenCode, Codex, and Kimi adapters.
- `StateStore`: file-backed persistence for config snapshots, agent state, run metadata, output files, logs, and transcript records.
- `RunWorker`: goroutine that executes one run, streams backend output, records completion or failure, and updates state.

The selected architectural shape is an adapter boundary: the API and orchestrator only know about common concepts such as agents, runs, statuses, and output paths. Completion detection, command invocation, JSONL parsing, and backend session recovery are adapter responsibilities.

## Configuration

The squad is configured only through YAML at server startup.

Example:

```yaml
session_name: debug-quorum
workspace_dir: /path/to/project
state_dir_name: .agent-debug-squad
host: 127.0.0.1
port: 8080

agents:
  - name: Reviewer
    backend: codex
    startup_prompt: |
      You review debugging hypotheses and look for missing evidence.
    options:
      command: codex
      model: openai/gpt-5.5
      inherit_env:
        - OPENAI_API_KEY
        - CODEX_HOME

  - name: Skeptic
    backend: opencode
    startup_prompt: |
      You challenge assumptions and propose alternative root causes.
    options:
      command: opencode
      model: anthropic/claude-sonnet-4.5

  - name: Implementer
    backend: kimi
    startup_prompt: |
      You focus on practical implementation and test strategy.
    options:
      command: kimi
      model: kimi-code/kimi-for-coding
```

Agent names are stable user-facing identifiers. Agents should address each other by these names and should not need to know which model or backend another agent uses.

## Storage Layout

All durable state is stored under:

```text
<workspace_dir>/.agent-debug-squad/sessions/<session_id>/
```

Files:

```text
config.json
agents/<agent_name>/state.json
runs/<run_id>/run.json
runs/<run_id>/<agent_name>.txt
runs/<run_id>/<agent_name>.stderr.log
transcript.jsonl
```

`config.json` is the normalized startup config snapshot.

`agents/<agent_name>/state.json` stores machine-readable agent state:

```json
{
  "name": "Reviewer",
  "backend": "codex",
  "startup_prompt": "...",
  "workspace_dir": "/path/to/project",
  "backend_session_id": "019e...",
  "status": "idle",
  "created_at": "2026-05-30T12:00:00Z",
  "last_run_id": "run_0007",
  "last_error": null
}
```

`runs/<run_id>/<agent_name>.txt` stores the final human-readable response for one run:

```text
Agent: Reviewer
Run: run_0007
Started: 2026-05-30T12:00:00Z
Completed: 2026-05-30T12:04:31Z

<final agent message>
```

`transcript.jsonl` stores structured events for API consumption:

```jsonl
{"type":"facilitator_message","run_id":"run_0007","to":"Reviewer","text":"Review this failure hypothesis..."}
{"type":"agent_result","run_id":"run_0007","agent":"Reviewer","output_path":".../Reviewer.txt","status":"completed"}
```

## REST API

The API controls the single configured session.

```text
GET  /health
GET  /session
GET  /agents
GET  /agents/{name}
POST /agents/{name}/runs
GET  /runs
GET  /runs/{run_id}
GET  /transcript
```

`POST /agents/{name}/runs` request:

```json
{
  "message": "Read runs/run_0006/Skeptic.txt and respond to the critique.",
  "metadata": {
    "reason": "cross-review"
  }
}
```

Default response is asynchronous:

```json
{
  "run_id": "run_0007",
  "agent": "Reviewer",
  "status": "queued",
  "output_path": null
}
```

The endpoint accepts optional wait query parameters:

```text
POST /agents/{name}/runs?wait=true&timeout_seconds=60
```

With `wait=true`, the server waits up to the timeout for completion and returns the latest run state. The run still continues after the HTTP request times out.

`GET /runs/{run_id}` response:

```json
{
  "run_id": "run_0007",
  "agent": "Reviewer",
  "status": "completed",
  "created_at": "2026-05-30T12:00:00Z",
  "started_at": "2026-05-30T12:00:01Z",
  "completed_at": "2026-05-30T12:04:31Z",
  "output_path": "/path/to/project/.agent-debug-squad/sessions/session_.../runs/run_0007/Reviewer.txt",
  "error": null
}
```

If a run is already active for an agent, a second `POST /agents/{name}/runs` returns `409 Conflict`.

## Run Lifecycle

Statuses:

- `queued`: accepted by the API but worker has not started.
- `running`: adapter is executing the turn.
- `completed`: adapter produced a final response and the output file was written.
- `failed`: adapter or process failed.
- `interrupted`: server restarted while the run was marked `queued` or `running`.

Parallelism rules:

- Runs for different agents may execute concurrently.
- Only one run may execute for a given agent at a time.
- v1 does not queue multiple runs for the same agent. It returns `409 Conflict` instead.

Lifecycle steps:

1. Validate agent exists and is not busy.
2. Allocate `run_id` and write initial run metadata.
3. Append facilitator message to `transcript.jsonl`.
4. Mark agent and run as `running`.
5. Call the selected adapter with the message and current agent state.
6. Capture final agent message, stderr/logs, backend session updates, and completion status.
7. Write `<agent_name>.txt`, update `run.json`, update agent `state.json`, and append result to `transcript.jsonl`.
8. Release the per-agent lock.

## Adapter Interface

The Go interface should hide backend-specific details:

```go
type AgentAdapter interface {
    Init(ctx context.Context, spec AgentSpec, state AgentState) (AgentState, error)
    Send(ctx context.Context, state AgentState, run RunRequest) (RunResult, AgentState, error)
    Recover(ctx context.Context, state AgentState) (AgentState, error)
}
```

`RunResult` contains:

- final assistant message;
- backend status;
- stderr/log text or paths;
- structured raw events when available;
- error details when failed.

### OpenCode Adapter

OpenCode is the strongest fit for a live backend. It supports `opencode serve`, which runs a headless HTTP server exposing OpenAPI endpoints and events. The adapter should prefer the HTTP server path, create or resume sessions through the API, and detect completion through OpenCode events or message completion responses.

### Codex Adapter

Codex should use non-interactive mode:

```text
codex exec --json ...
codex exec --json resume <SESSION_ID> ...
```

The adapter reads JSONL events from stdout/stderr as provided by Codex. A turn completes on `turn.completed` and fails on `turn.failed`. The final agent message is extracted from the completed agent message event or from the final stdout result when available.

The Codex adapter must support a configurable environment whitelist. The YAML `options.inherit_env` list names environment variables to copy from the service process into each `codex exec` child process. Variables not listed are not inherited except for the minimal process environment required by the operating system and command lookup. This is required for deployments that need explicit control over credentials, Codex home/config paths, proxies, or feature flags.

### Kimi Adapter

Kimi should use non-interactive prompt mode with structured output:

```text
kimi -p "..." --output-format stream-json
```

If a stable resume mechanism is available, the adapter stores and reuses the Kimi session id. If the stream does not expose an explicit turn-completed event, the adapter treats successful process exit as run completion and uses the last assistant message as the final response. Non-zero exit code means failure.

## Error Handling And Recovery

Run-level failures are recorded in `run.json`, `transcript.jsonl`, and stderr/log artifacts. The agent is marked `failed` only when the adapter cannot safely continue without manual intervention, such as missing backend credentials, missing executable, invalid backend session id, or unrecoverable protocol errors.

On server startup:

- Load `config.json` and agent `state.json` if present.
- Reinitialize adapters.
- Mark any `queued` or `running` runs from a previous process as `interrupted`.
- Recover backend sessions when the adapter supports it.
- Leave agents `idle` if their adapter state is usable, otherwise mark them `failed` with `last_error`.

## Security And Operational Boundaries

The service is local-first. Default host is `127.0.0.1`. No authentication is required for v1 if bound to localhost only. If binding to a non-loopback host is later supported, authentication must be added before exposing the API.

The service should not automatically transmit one agent's output to another agent. The facilitator decides which output files an agent should read and includes those file paths in the next prompt.

Backend approval, sandbox behavior, and backend child-process environment remain backend-specific and should be configured in YAML options. The service records what it invoked but does not bypass approvals or inherit broad environment variables unless explicitly configured.

## Testing Strategy

Unit tests:

- YAML config parsing and validation.
- StateStore path creation, atomic writes, JSONL append, and recovery.
- Orchestrator run lifecycle.
- Per-agent lock and `409 Conflict` behavior.
- Transcript events.
- REST handlers with fake adapters.
- Adapter parsers against sample OpenCode, Codex, and Kimi JSONL streams.
- Codex adapter environment inheritance, verifying only whitelisted variables are passed to child processes.

Integration tests:

- Run server with fake adapters and exercise the REST API end to end.
- Simulate restart with a `running` run and verify it becomes `interrupted`.

Real CLI smoke tests should be opt-in because they require installed tools, credentials, and potentially network/model access.

## Acceptance Criteria

- A user can start the service with `agent-debug-squad serve --config squad.yaml`.
- `GET /agents` returns the configured named agents.
- `POST /agents/{name}/runs` starts an async run and returns a `run_id`.
- `GET /runs/{run_id}` reports lifecycle status and final `output_path`.
- Final agent output is written to `runs/<run_id>/<agent_name>.txt`.
- `transcript.jsonl` contains facilitator messages and agent results.
- Concurrent runs for different agents are allowed.
- Concurrent runs for the same agent return `409 Conflict`.
- Restart recovery marks stale active runs as `interrupted`.
