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

Agent definitions accept an optional `ephemeral: true` flag declaring a one-shot lifecycle for [declarative workflows](#declarative-workflows): every workflow invocation of the agent — each task attempt, retry, and any future repeated visit — runs on a newly created runtime whose backend session starts empty, with no conversation or session state carried over from earlier invocations. The flag is captured with the execution at submission, is preserved across restarts, and participates in the definition identity: resubmitting the same `request_id` after changing `ephemeral` is a changed definition (`409`), not a replay. In this version the flag does not relax graph validation — one agent is still referenced by at most one task — and manual facilitator turns ignore it entirely, keeping normal session continuity.

See [examples/squad.yaml](examples/squad.yaml) for a self-contained fake squad, [examples/cursor-squad.yaml](examples/cursor-squad.yaml) for a read-only Cursor reviewer, and [configs/code-review-squad.yaml](configs/code-review-squad.yaml) for a larger facilitator/implementer/critic setup.

### Verdict Judge

Workflow tasks can declare a `verdicts` map (verdict name → optional human description). After such a task's attempt saves its response, an external judge model classifies the response into exactly one of the declared verdicts and the classification is recorded on the attempt — verdict, confidence, the full probability distribution, the model string, and the source (`judge` or `manual`). The raw decision response is stored as `tasks/<task>/attempts/<n>/decision.json` next to the attempt's artifacts; the response file itself is never modified. In this version verdicts are recorded metadata: they do not change scheduling, dependency evaluation, or the execution's final state.

The judge is configured through an optional top-level `judge:` section:

```yaml
judge:
  provider: openrouter           # the only provider in this version
  model: "~typesafe/jev-latest"  # routing alias; pin e.g. "typesafe/jev-1.13" for a fixed version
  api_key_file: ""               # default: ~/.agent-debug-squad/openrouter-api-key
  proxy_url: ""                  # HTTP proxy for decision requests; empty uses environment proxy settings
  timeout_seconds: 30            # per-call timeout; transient failures are retried with backoff
```

The API key lives in a one-line file containing the bare token (no `Bearer` prefix, no quoting). The default location is in the user's home directory — outside the workspace — so credentials never land in a git repository. Startup requires the key file only when the configured workflow declares verdict tasks or a `judge:` section is present; otherwise the server starts and operates with no judge dependency.

Workflow-level settings: `confidence_threshold` (default `0.8`) — a judge verdict applies only when its confidence meets the threshold — and `on_uncertain` (default `hold`):

- `hold`: below-threshold confidence keeps the attempt in the `judging` state and moves the execution to `needs_attention`, exposing the full distribution (`uncertain_verdict:<task>:<n>` attention reason). Resolve it with a manual override or `resume` (which re-runs classification).
- `error`: below-threshold confidence fails the attempt with reason `uncertain_verdict`; existing failure policy and retries apply.

Judge unavailability (transport errors after retries, call timeout) never fails the task: the execution holds in `needs_attention` with a `judge_unavailable` reason, and `resume` re-classifies the held attempt. Recovery treats mid-judging attempts the same way — their backend work is already committed, so a restart re-runs only the classification, never the agent.

A held judging attempt can be settled by hand without re-running the agent or the judge:

```sh
curl -sS -X POST http://127.0.0.1:8080/workflows/wf_000001/tasks/review_a/attempts/1/verdict \
  -H 'Content-Type: application/json' -d '{"request_id":"verdict-1","verdict":"review_passed"}'
```

The verdict must be one the task declares (`400` otherwise), the attempt must still be judging (`409` otherwise), and the request is idempotent per `request_id` — replaying the same verdict returns the recorded result, a different verdict for the same ID returns `409`. The attempt settles as succeeded with the verdict recorded as manually sourced.

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

This experimental adapter supports **ZCode desktop 3.x** installs and a signed-in **Z.AI individual Coding Plan** account. Install Node (tested with Node 26) and sign in through ZCode first. There is no pinned version list: before loading the runtime, the host bridge probes the bundle structurally for the anchors it needs (CLI autorun statement, native credential store, provider registry). Bundles that keep those anchors work without a Squad update; bundles where an anchor is missing or ambiguous fail closed before a prompt is sent, with an actionable error that includes the bundle SHA-256 for reporting. The host bridge loads the installed runtime's native credential reader in memory; it never edits the bundle or exports credentials into Squad state.

Options:

- `command`: Node executable, default `node`.
- `runtime_path`: bundle location, default `/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs`. Other locations must contain a compatible bundle and its companion resources.
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
POST /workflows
GET  /workflows
GET  /workflows/{execution_id}
POST /workflows/{execution_id}/pause
POST /workflows/{execution_id}/resume
POST /workflows/{execution_id}/cancel
POST /workflows/{execution_id}/tasks/{task_id}/retry
POST /workflows/{execution_id}/tasks/{task_id}/attempts/{attempt}/verdict
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

## Declarative Workflows

Besides explicit manual turns, Squad can execute a declarative task graph. The optional `workflow` section of the config defines the graph; serving the config never starts it. `POST /workflows` creates an execution of the configured definition, and the scheduler dispatches tasks automatically — parallel fan-out, all-dependency fan-in, and dependency-failure policies are all ordinary graph behavior:

```yaml
session_name: review
workspace_dir: .
defaults:
  yolo: false
agents:
  - name: reviewer_a
    backend: codex
    startup_prompt: "Review code independently. Do not edit files."
  - name: reviewer_b
    backend: codex
    startup_prompt: "Review code independently. Do not edit files."
  - name: verifier
    backend: opencode
    startup_prompt: "Verify findings against the code. Do not edit files."
workflow:
  version: 1
  name: review-and-verify
  max_parallel: 2
  task_timeout_seconds: 1800
  tasks:
    review_a:
      agent: reviewer_a
      prompt: "Review the requested diff; report evidence and severity."
      allowed_to_fail: true
    review_b:
      agent: reviewer_b
      prompt: "Review the requested diff; report evidence and severity."
      allowed_to_fail: true
    verify:
      agent: verifier
      needs: [review_a, review_b]
      min_successful_dependencies: 1
      prompt: "Validate findings, remove duplicates, align severity, and report missing reviews."
```

Fields and defaults:

- `version` (must be `1`), nonempty `name`, positive `max_parallel`, and a nonempty `tasks` map are required.
- Each task requires nonempty `agent` and `prompt`. `needs` defaults to `[]`, `allowed_to_fail` to `false`, and `min_successful_dependencies` to `0`.
- `task_timeout_seconds` defaults to `1800`; a task may override it with a positive `timeout_seconds`. The timeout counts wall time from dispatch, including permission and subagent waits, and `0` does not disable it.
- Each agent may be referenced by at most one task; different tasks may reuse the same backend and model by declaring distinct agents. Unknown fields, duplicate YAML keys, cycles, self- or repeated dependencies, unsafe identifiers, and out-of-range thresholds are rejected before any task runs.
- An agent definition may set `ephemeral: true` to declare a one-shot lifecycle: every invocation starts from a clean context. See [Configuration](#configuration); note the flag does not allow referencing one agent from multiple tasks in this version.
- A task may declare `verdicts` (at least two names, the reserved name `uncertain` is rejected); the workflow may set `confidence_threshold` (default `0.8`) and `on_uncertain` (`hold` default, or `error`). Verdict tasks run an extra judging phase after their response is saved; see [Verdict Judge](#verdict-judge).
- The workflow may declare a `loops` map of bounded loops and label tasks with `loop:`; see [Bounded Loops](#bounded-loops).

Start, observe, and control an execution:

```sh
curl -sS -X POST http://127.0.0.1:8080/workflows   -H 'Content-Type: application/json' -d '{"request_id":"review-2026-09-20"}'

curl -sS 'http://127.0.0.1:8080/workflows/wf_000001?wait=true&timeout_seconds=60'
curl -sS -X POST http://127.0.0.1:8080/workflows/wf_000001/pause
curl -sS -X POST http://127.0.0.1:8080/workflows/wf_000001/resume
curl -sS -X POST http://127.0.0.1:8080/workflows/wf_000001/cancel
curl -sS -X POST http://127.0.0.1:8080/workflows/wf_000001/tasks/review_a/retry   -H 'Content-Type: application/json' -d '{"request_id":"retry-1","expected_attempt":1}'
```

Repeating `POST /workflows` with the same `request_id` returns the original execution (`200`) without new work, including after restart; reusing the ID with a changed definition returns `409`. Only one nonterminal execution per server session is admitted in v1. `wait=true` long-polls until a terminal state or an intervention (pending permission, failed auto-approval, recovery uncertainty), within one second of local publication; `timeout_seconds` is an integer from 1 to 600 (default 30) and expiry returns the current state without cancelling anything.

Task states: `pending`, `ready`, `dispatching`, `running`, `succeeded`, `failed`, `interrupted`, `blocked`, `cancelled`. Attempt states additionally include the transient `judging` phase of verdict tasks (the response is saved, classification pending). Execution states: `running`, `paused`, `needs_attention`, `cancelling`, `cancelled`, `succeeded`, `completed_with_errors`, `failed`. When all work settles: any blocked task or non-tolerated failure makes the execution `failed`; otherwise tolerated failures produce `completed_with_errors`; otherwise `succeeded`.

Failure truth table for a task T with direct dependencies:

| Direct dependency outcome | T's dependency acceptable? | Counts toward threshold? |
| --- | --- | --- |
| `succeeded` | yes | yes |
| `failed`, dependency `allowed_to_fail: true` | yes (stays visible as failed) | no |
| `failed`, dependency mandatory | no | no |
| `blocked` / `cancelled` / `interrupted` | no | no |

A threshold never overrides an unacceptable dependency; a task whose dependencies settle with fewer than `min_successful_dependencies` successes is `blocked` with an explicit reason, and blocking propagates to its descendants. Independent branches continue. Tolerated failures remain failed and are never counted as successes. Interruption and cancellation are never waived by `allowed_to_fail`.

Every task attempt gets a fresh backend conversation owned by the execution (`wrun_…` run IDs); tasks never inherit manual chat history or another task's context. Dependency results are transferred explicitly: before dispatch the scheduler saves the exact prompt and an input manifest (sorted by dependency task ID, with task/agent/attempt/run identities, outcomes, errors, and result path/size/SHA-256 for successes) and instructs the agent to read the referenced response files. Successful responses are verified (existence, size, hash) before a consumer is dispatched; a missing or changed committed result holds the execution in `needs_attention` until repaired byte-for-byte or cancelled. Partial output from failed attempts is never presented as successful input. Responses live under the execution directory:

```text
workflows/<execution_id>/workflow.json          # authoritative snapshot
workflows/<execution_id>/events.jsonl           # control/audit log
workflows/<execution_id>/tasks/<task>/attempts/<n>/prompt.txt
workflows/<execution_id>/tasks/<task>/attempts/<n>/input-manifest.json
workflows/<execution_id>/tasks/<task>/attempts/<n>/response.txt
workflows/<execution_id>/tasks/<task>/attempts/<n>/decision.json   # raw judge decision, verdict tasks only
```

Shared workspace: tasks run concurrently in the same workspace. Squad does not detect file-write intent, serialize workspace access, or create worktrees — coordinating edits is the workflow author's responsibility, and "read only" instructions are agent instructions, not sandbox guarantees.

Retry and recovery limits:

- No retries are automatic. `retry` reserves a fresh attempt (fresh conversation, same instructions and manifest semantics) for a failed/interrupted task only while no transitive descendant has attempt reservations, and only with a new unique `request_id` plus the `expected_attempt` number. Identical retries replay; stale or conflicting ones return `409`. Once a tolerated failure has been consumed downstream, retry is rejected — start a new execution for a different consistent result set. Inside a loop, eligibility is iteration-scoped; see [Bounded Loops](#bounded-loops).
- Retrying an interrupted attempt, or finishing a cancellation after a crash, requires `confirm_previous_stopped: true` — the caller's assertion that prior backend work stopped, recorded for audit. Squad cannot verify external cleanup after a process crash, and a worker known to be active in the current process is never overridden.
- Restart recovers committed outcomes and artifacts, keeps paused executions paused, continues `cancelling` until resolved, and marks reserved/running attempts without committed outcomes as `interrupted`, stopping new dispatch until intervention. Unknown snapshot schema versions or damaged authoritative state fail closed. Workflow snapshot schema is versioned upgrade-only: older binaries cannot resume schema-2 (loop) executions — stop the new server before rolling back and keep the artifacts; downgrade conversion is out of scope.
- A workflow-owned runtime rejects manual run/reset mutation with `409`; manual agents, follow-up continuity, run APIs, and permission replies keep their existing behavior, and workflow attempts are visible through the same run endpoints.

### Bounded Loops

A workflow may declare one or more bounded loops. `loops` maps loop names to their mandatory positive `max_iterations`; tasks join a loop body with `loop:`:

```yaml
workflow:
  version: 1
  name: refine-loop
  loops:
    refine:
      max_iterations: 3
  tasks:
    implement:
      agent: implementer
      loop: refine
      prompt: "Implement the change. In later iterations, address the previous iteration's review feedback."
    review:
      agent: reviewer
      loop: refine
      needs: [implement]
      prompt: "Review this iteration's implementation; report remaining defects or state it is acceptable."
    report:
      agent: reporter
      needs: [review]
      prompt: "Summarize the final implementation and the last review for the user."
```

Semantics and limits of this stage:

- The body runs exactly `max_iterations` times — there is no condition-based exit and no nesting yet, and loops cannot share tasks or depend across loop boundaries (validation rejects all of these; sibling loops and outside consumers are supported).
- Automatic work is bounded by construction: one dispatch per body task per iteration (body tasks × `max_iterations` initial attempts). Failures never trigger automatic retries. Explicit user/coordinator retries are excluded from that bound — there is no retry-count or elapsed-time guarantee, only the descendant guards below.
- An iteration advances when every body task has a settled current-iteration attempt that is acceptable under the ordinary dependency rules: `allowed_to_fail` and `min_successful_dependencies` compose per iteration exactly as in a flat graph, so tolerated failures don't block advance but re-run in the next iteration.
- Handoff is iteration-scoped: `needs` inside the loop resolve to the dependency's current-iteration committed attempt; dependencies from outside the loop resolve once the loop is done and consume only the final iteration. Prior iterations are immutable inputs — completed iterations are never re-executed.
- Carry-over: a body task dispatching in iteration k > 1 gets a previous-iteration outcomes section in its manifest and prompt — all body tasks' iteration-(k−1) committed attempts (states, verdicts, errors, result files), verified like every committed artifact before dispatch.
- The execution view exposes per-loop state (`name`, `iteration`, `max_iterations`, `state`), and every attempt view carries its `iteration` number.
- A non-tolerated body failure or a blocked body task puts the execution in a durable `needs_attention` hold with an actionable `loop_failure`/`loop_blocked` reason naming loop, task, and iteration. The hold survives restart, stops new dispatch and all loop advance, lets live work finish, and keeps outside consumers pending with a `waiting_loop` reason. Recovery is intervention: retry the eligible attempt or cancel.
- Retry inside a loop targets the latest failed/interrupted attempt of the current iteration and stays in that iteration; an eligible retry can reopen a finished loop at its final iteration. Same-loop descendants block a retry only when they already have an attempt in that iteration; outside descendants and loopless tasks keep the all-history guard. Reserving a retry recomputes dependent readiness, clearing exactly the loop holds it resolves.
- Resume is independent of retry/cancel and never dispatches loop work by itself: it revalidates restored artifacts and re-attempts held judge classifications (a completed resume can still return `200` with `needs_attention` while unresolved causes remain). Iteration counters survive restart; the advance is committed before any next-iteration backend work, so recovery never repeats, skips, or double-dispatches an iteration.
- Loop executions raise the snapshot schema to version 2; compatibility is upgrade-only (schema 1 loads as loopless, unknown versions fail closed) and downgrade support is out of scope.

See [examples/workflow-chain.yaml](examples/workflow-chain.yaml), [examples/workflow-review.yaml](examples/workflow-review.yaml), and [examples/workflow-loop.yaml](examples/workflow-loop.yaml) for runnable fake-backend graphs.

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
workflows/<execution_id>/workflow.json
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
