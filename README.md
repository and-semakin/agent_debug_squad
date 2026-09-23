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

Workflow-level settings: `confidence_threshold` (default `0.8`) — a judge verdict applies only when its confidence meets the threshold — and `on_uncertain` (default `needs_attention`):

- `needs_attention`: below-threshold confidence keeps the attempt in the `judging` state and moves the execution to `needs_attention`, exposing the full distribution (`uncertain_verdict:<task>:<n>` attention reason). Resolve it with a manual override or `resume` (which re-runs classification).
- `error`: below-threshold confidence fails the attempt with reason `uncertain_verdict`; existing failure policy and retries apply.

The former `hold` spelling of the waiting policy is no longer accepted: new YAML using `on_uncertain: hold` is rejected with guidance to use `needs_attention`, and a saved execution carrying `hold` fails to load with an actionable unsupported-policy error (no alias, fallback, or automatic migration).

Judge unavailability (transport errors after retries, call timeout) never fails the task: the execution holds in `needs_attention` with a `judge_unavailable` reason, and `resume` re-classifies the held attempt. Recovery treats mid-judging attempts the same way — their backend work is already committed, so a restart re-runs only the classification, never the agent.

A held judging attempt can be settled by hand without re-running the agent or the judge:

```sh
curl -sS -X POST http://127.0.0.1:8080/workflows/wf_000001/tasks/review_a/attempts/1/verdict \
  -H 'Content-Type: application/json' -d '{"request_id":"verdict-1","verdict":"review_passed"}'
```

The verdict must be one the task declares (`400` otherwise), and the request is idempotent per `request_id` — replaying the same verdict returns the recorded result, a different verdict for the same ID returns `409`. A judging attempt settles as succeeded with the verdict recorded as manually sourced. A *settled* attempt is also overridable when its verdict is load-bearing on a live loop hold (a condition task whose `needs_attention` action currently holds its loop): the override rewrites that verdict manually, the attempt stays settled, and the next reconcile re-derives the condition — clearing the hold and following the replacement action. Attempts from an iteration the loop has already advanced past remain immutable (`409`).

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
- A task may declare `verdicts` (at least two names, the reserved name `uncertain` is rejected); the workflow may set `confidence_threshold` (default `0.8`) and `on_uncertain` (`needs_attention` default, or `error`). Verdict tasks run an extra judging phase after their response is saved; see [Verdict Judge](#verdict-judge).
- The workflow may declare a `loops` map of bounded loops and label tasks with `loop:`; see [Bounded Loops](#bounded-loops). Loops may exit early on a verdict (see [Loop Conditions](#loop-conditions)) and nest to arbitrary depth via `parent` (see [Nested Loops](#nested-loops)).

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
- Restart recovers committed outcomes and artifacts, keeps paused executions paused, continues `cancelling` until resolved, and marks reserved/running attempts without committed outcomes as `interrupted`, stopping new dispatch until intervention. Unknown snapshot schema versions or damaged authoritative state fail closed. Workflow snapshot schema is versioned upgrade-only: older binaries cannot resume schema-2 (loop) or schema-3 (nested-loop) executions — stop the new server before rolling back and keep the artifacts; downgrade conversion is out of scope.
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

- A static loop (no condition fields) runs its body exactly `max_iterations` times; a conditioned loop may exit early through its condition (see [Loop Conditions](#loop-conditions)). Loops may also be nested to arbitrary depth with `parent` (see [Nested Loops](#nested-loops)). A task belongs to exactly one direct owner loop, and loops cannot share tasks or depend directly across unrelated loop boundaries (validation rejects these; sibling loops, ancestor/descendant handoff, and outside consumers are supported).
- Automatic work is bounded by construction: one dispatch per body task per iteration (body tasks × `max_iterations` initial attempts). Failures never trigger automatic retries. Explicit user/coordinator retries are excluded from that bound — there is no retry-count or elapsed-time guarantee, only the descendant guards below.
- An iteration advances when every body task has a settled current-iteration attempt that is acceptable under the ordinary dependency rules: `allowed_to_fail` and `min_successful_dependencies` compose per iteration exactly as in a flat graph, so tolerated failures don't block advance but re-run in the next iteration.
- Handoff is iteration-scoped: `needs` inside the loop resolve to the dependency's current-iteration committed attempt; dependencies from outside the loop resolve once the loop is done and consume only the final iteration. Prior iterations are immutable inputs — completed iterations are never re-executed.
- Carry-over: a body task dispatching in iteration k > 1 gets a previous-iteration outcomes section in its manifest and prompt — all body tasks' iteration-(k−1) committed attempts (states, verdicts, errors, result files), verified like every committed artifact before dispatch.
- The execution view exposes per-loop state (`name`, `iteration`, `max_iterations`, `state`), and every attempt view carries its `iteration` number.
- A non-tolerated body failure or a blocked body task puts the execution in a durable `needs_attention` hold with an actionable `loop_failure`/`loop_blocked` reason naming loop, task, and iteration. The hold survives restart, stops new dispatch and all loop advance, lets live work finish, and keeps outside consumers pending with a `waiting_loop` reason. Recovery is intervention: retry the eligible attempt or cancel.
- Retry inside a loop targets the latest failed/interrupted attempt of the current iteration and stays in that iteration; an eligible retry can reopen a finished loop at its final iteration. Same-loop descendants block a retry only when they already have an attempt in that iteration; outside descendants and loopless tasks keep the all-history guard. Reserving a retry recomputes dependent readiness, clearing exactly the loop holds it resolves.
- Resume is independent of retry/cancel and never dispatches loop work by itself: it revalidates restored artifacts and re-attempts held judge classifications (a completed resume can still return `200` with `needs_attention` while unresolved causes remain). Iteration counters survive restart; the advance is committed before any next-iteration backend work, so recovery never repeats, skips, or double-dispatches an iteration.
- Loop executions raise the snapshot schema: flat (nonnested) loops persist at version 2, nested executions at version 3. Compatibility is upgrade-only (schema 1 loads as loopless, unknown versions fail closed) and downgrade support is out of scope. See [Nested Loops](#nested-loops).

### Loop Conditions

A loop may decide its continuation from a verdict instead of running a fixed budget. Three optional fields turn a static loop into a conditioned one (a loop with none of them stays fully static):

```yaml
workflow:
  version: 1
  name: review-until-clean
  loops:
    polish:
      max_iterations: 3
      until_task: review            # the loop's single condition task
      on_verdict:                   # total map, one entry per declared verdict
        review_passed: break        # complete now at this iteration
        issues_found: continue      # re-arm, or hit the budget cap
        needs_human: needs_attention  # hold for intervention
      on_exhaustion: needs_attention  # or `succeed`; requires the pair above
  tasks:
    implement:
      agent: implementer
      loop: polish
      prompt: "Implement, then address the previous iteration's review."
    review:
      agent: reviewer
      loop: polish
      needs: [implement]
      verdicts:                       # until_task must declare verdicts
        review_passed: "No blocking defects remain."
        issues_found: "Concrete defects to fix."
        needs_human: "A decision only a human can make."
      prompt: "Review this iteration; pick exactly one verdict."
```

- `until_task` names the loop's condition task. It must be a body member, must declare `verdicts`, must be mandatory (`allowed_to_fail` omitted or `false`; other body tasks may stay optional reviewers), and must be the **unique body sink** — every other body task reaches it through same-loop `needs` edges (a singleton body qualifies). `on_verdict` must map each declared verdict exactly once, to `break`, `continue`, or `needs_attention`; missing keys, unknown verdict names, and bad actions are rejected with a corrective example. `until_task` and `on_verdict` pair up, and an explicit `on_exhaustion` requires that pair (a static loop that sets only `on_exhaustion` is rejected rather than ignored).
- **Evaluation point:** the condition is read only after the whole iteration settles acceptably, and it reads `until_task`'s latest committed current-iteration verdict. Loop failure/blocking holds, an interrupted attempt, a held/judging classification, and `on_uncertain: error` all take precedence and prevent evaluation — a failed condition attempt holds for retry and never supplies a continuation decision, and a synthetic `uncertain` value is never looked up in `on_verdict`.
- **Actions:** `break` completes the loop at the current iteration and releases outside consumers with that iteration's results; `continue` re-arms the next iteration, or at the effective cap triggers the exhaustion policy; `needs_attention` holds at the current iteration with a `loop_attention:<loop>:<iter>:<task>:<verdict>` reason offering override, stop, or cancel. A `needs_attention` action keeps priority even at the cap.
- **Exhaustion** (`continue` at the effective cap): `needs_attention` (default) holds with a `loop_exhausted:<loop>:<iter>` reason offering extend, stop, or cancel; `succeed` completes the loop like a `break`, preserving the recorded verdict and ordinary tolerated-failure accounting (it never forces workflow success or finalizes failure on its own).
- **Verdict names carry no built-in scheduling meaning.** `blocked`, `needs_input`, and any other declared name are ordinary verdicts; to wait for a human on a condition task, map its verdict to `needs_attention` explicitly. Non-condition tasks' verdicts stay observational and never hold execution by name, and a task without `verdicts` is never classified (free-form "please help" text creates no structured outcome). No automatic answer injection or successful-attempt rerun is provided ("answer and continue" is deferred).
- **Extend control:** `POST /workflows/{id}/loops/{name}/extend` with a positive `add_iterations` durably raises the loop's effective cap (`max_iterations` + granted extensions) — a manual, audited, idempotent action. The system itself never loops unboundedly. Identical requests replay without a second increase even after completion or restart; reusing a request ID for another loop or amount returns `409`; new requests against a done loop or a terminal/cancelling execution return `409`. Extending an exhausted loop clears its `loop_exhausted` cause on the next reconcile.
- **Stop control:** `POST /workflows/{id}/loops/{name}/stop` durably requests a manual break after the current iteration. Remaining body work finishes under normal dependency and pause rules, no next iteration begins, and outside consumers wait for acceptable settlement. Stop resolves a condition-action or exhaustion hold locally even while another loop is held, but it never cancels workers, unpauses, or waives failures, interruptions, or unresolved classification. Intent survives restart and same-iteration retries; `stop_requested` is exposed in loop views.
- **Views:** a conditioned loop view carries `until_task`, `effective_max_iterations` (declared plus extensions), the latest settled `last_condition_verdict`, and `stop_requested` when pending; static-loop views are unchanged. There is no automatic downgrade: an older binary must not be relied on to preserve condition semantics, extensions, or pending stop intent.

### Nested Loops

Loops nest to arbitrary depth. Add `parent` (the enclosing loop's name) to any loop; a task's `loop` always names its **direct owner**. `examples/workflow-nested-loop.yaml` is a runnable three-level graph (`implement → review → consolidate → test → accept`) following the authoring model below.

```yaml
workflow:
  version: 1
  name: nested-delivery
  loops:
    delivery_loop:            # root
      max_iterations: 2
      until_task: accept
      on_verdict: { accepted: break, reject: continue }
    test_loop:                # child of delivery_loop
      max_iterations: 2
      parent: delivery_loop
      until_task: test
      on_verdict: { passed: break, failed: continue }
    review_loop:              # child of test_loop
      max_iterations: 3
      parent: test_loop
      until_task: consolidate
      on_verdict: { ready: break, revise: continue }
  tasks:
    implement:   { agent: implementer, loop: review_loop }
    review:      { agent: reviewer, loop: review_loop, needs: [implement] }
    consolidate: { agent: consolidator, loop: review_loop, needs: [review], verdicts: { ready: "…", revise: "…" } }
    test:        { agent: tester, loop: test_loop, needs: [consolidate], verdicts: { passed: "…", failed: "…" } }
    accept:      { agent: acceptor, loop: delivery_loop, needs: [test], verdicts: { accepted: "…", reject: "…" } }
```

- **Forest and `parent`.** Loops form a forest via `parent`; a present `parent` must be a nonempty string naming a declared loop (no trimming or coercion of empty, whitespace, null, numbers, booleans, sequences, or maps), and only omission means no parent. Self-parenting, cycles, undeclared parents, empty subtrees, and shared tasks are rejected. `parent` participates in definition hashing when present.
- **Context identity is the complete path, not a local counter.** A nested invocation is identified by an ordered iteration path from the root loop to the direct owner, e.g. `[{"loop":"delivery_loop","iteration":1},{"loop":"test_loop","iteration":2},{"loop":"review_loop","iteration":1}]`. A repeated local counter never aliases another ancestor context. Schema-3 executions persist and expose `iteration_path` on every loop-owned loop state, attempt, view, and dependency/carry-over entry (root-owned tasks carry a one-entry path); `iteration` stays as the direct owner's counter and must agree with the path's last entry. Paths render as `name=N` joined by `/` (root first, no spaces) inside reason strings.
- **Subtree body vs direct members.** A loop's body is its whole task subtree; its direct members are only the tasks that name it. Advancing a loop runs its descendants again: **advance** increments the loop's counter, resets its direct tasks, and recursively creates fresh child invocations at iteration 1 (clearing child-local extensions and stop intent), while **complete** marks the invocation done at its existing path and preserves descendants. Dependency descendants are always distinguished from loop descendants.
- **Whole-subtree sinks.** Each conditioned loop must directly own its `until_task`, which must be mandatory and the **unique sink of the loop's whole subtree**: every other task in that subtree reaches the condition through `needs` edges. A nested condition cannot double as an ancestor's condition (rejected); an ancestor decision needs its own directly owned task, so a container-only loop is static.
- **Context-exact handoff.** When a consumer depends on a producer in another loop, resolution is exact to the context with no cross-context fallback: a same-owner producer resolves to the latest attempt at the exact current path; an ancestor-owner producer to the outcome at the consumer path truncated to that owner; a descendant producer to the **final** outcome under the consumer's current prefix once every intervening child invocation is done; a loop producer feeding a workflow-scope consumer resolves to the final outcome after the producer's root-ancestor invocation completes. Never falling back to another context is what keeps a negative verdict distinct from a backend failure: a `failed`/`reject` verdict is a deliberate `continue`, while a real task error takes the durable `needs_attention` hold path.
- **Final-only, whole-subtree carry-over.** Carry-over walks the owner then each ancestor nearest-first; each level whose local counter exceeds 1 summarizes that previous iteration's **entire subtree** (with `previous_iteration_path` alongside the existing `previous_iteration` array in schema 3). Hand-offs carry final outcomes only, never a transcript of every inner iteration; earlier attempts remain inspectable history and every referenced artifact is verified before dispatch.
- **Shared-enclosing-iteration retry guard.** The flat all-history descendant guard becomes a shared-enclosing-context guard for nested producers: a same-loop consumer blocks a retry only within the exact current path; an inner consumer of an outer producer blocks anywhere in that producer's outer iteration; a bridged sibling blocks within the common ancestor iteration; workflow-scope producers/consumers keep the whole-history rule. An eligible retry atomically reopens done owning/ancestor invocations at the same paths without resetting independent work, and never grants another iteration (a capped or stopped invocation completes again at the same path).
- **Controls bind to the invocation current at serialization (a race).** There is no compare-and-set parameter. If a parent advance commits before a newly issued stop serializes, that stop targets the *new* current iteration; replaying a control after an advance acknowledges the old action and does not apply it to the new invocation. Extensions belong to their accepted invocation, persist across that loop's local advances, and clear only when an ancestor creates a new invocation (root extensions therefore last the execution). Control audit records store the accepted target path and affected descendant paths.
- **Stop vs cancel.** A loop's **stop** completes the current iteration normally and propagates to every currently unfinished descendant invocation in one saved transition — never cancelling workers or waiving failures; even a child initialized at iteration 1 with no dispatched attempt finishes that iteration on stop. **Cancellation** is the operation for abandoning unstarted work. Propagated stop clears a child's condition-action or exhaustion hold once that iteration is otherwise acceptably settled, but not failure, interruption, or judging holds.
- **Invocation-local budgets.** Automatic attempts per leaf are bounded by the product of effective caps along its owner chain. Extensions raise an invocation's cap and clear only when an ancestor creates a new invocation, so a conservative bound uses the maximum cap ever granted to each named loop (a reset may have cleared a larger historical extension). Four nested levels with cap 5 permit up to 5⁴ = 625 initial attempts per leaf before retries — a conservative upper bound, not a remaining-work estimate or a spending limit, and no global cost cap is added. Explicit retries stay excluded, so indefinite external intervention is not a finite-execution guarantee.
- **Bridges cost an agent task and transfer completed invocations.** A direct edge between unrelated loop branches is rejected; the error names the least common enclosing scope where an explicit bridge task can transfer results. A bridge costs an agent task and transfers a *completed* invocation once — it is not a channel for iteration-by-iteration exchange between roots, and it stays subject to boundary-cycle checks.
- **Ordering is explicit in `needs`.** Setting `parent` adds no ordering edge and no workspace snapshot. A directly owned static-loop task with no `needs` on a child loop may run concurrently with it. `break` completes only its owning invocation; static ancestors still repeat their declared budgets.
- **Observation and schema.** Loop and attempt views carry paths, and loop views carry the `parent` name, so the forest is reconstructable from one snapshot; failure/blocking/attention/exhaustion/wait reasons render the complete path, and a wait reason names the actual root or intermediate completion barrier (not the producer's innermost loop). Flat (nonnested) executions keep their prior wire format, reason strings, and task counts byte-for-byte and stay at schema 2; nested executions use schema 3, which is upgrade-only.

See [examples/workflow-chain.yaml](examples/workflow-chain.yaml), [examples/workflow-review.yaml](examples/workflow-review.yaml), [examples/workflow-loop.yaml](examples/workflow-loop.yaml), [examples/workflow-loop-conditions.yaml](examples/workflow-loop-conditions.yaml), and [examples/workflow-nested-loop.yaml](examples/workflow-nested-loop.yaml) for runnable fake-backend graphs.

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
