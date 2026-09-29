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

## Discover models before writing YAML

Use the installed backend catalogs to choose exact agent options before creating a squad:

```sh
agent-debug-squad models --all --workspace .
agent-debug-squad models --backend codex --backend cursor --json
agent-debug-squad models --config squad.yaml --json
```

Without selectors or a config, `models` queries the five real backends. With a config,
no selector means every distinct effective agent context; `--all` also adds missing
real backends. Repeated `--backend` filters select matching agents, falling back to
machine settings when that backend is absent. Equivalent reviewers share a query.
`fake` is included only when explicitly selected or configured; judge is excluded.
A conflicting `--workspace` and config workspace is an error. Machine settings and
environment restrictions are the same as execution.

Inspect useful partial reports even when the command exits nonzero:

```sh
status=0
agent-debug-squad models --all --json > models.json || status=$?
case "$status" in
  0|1) jq '{status, results: [.results[] | {backend, status, complete, models, diagnostics}]}' models.json ;;
  *) cat models.json; exit "$status" ;;
esac
```

Exit 0 means every target is complete (`ok` or `empty`); exit 1 covers both
`partial` and `failed`, distinguished by JSON `status` and `results`. Exit 2 is an
argument/config error; cancellation exits 130 with collected results. **`--all`
returns exit 1 while ZCode discovery is unsupported**, even if all other backends
succeed. Do not hide the report behind an `&&` success chain.

Copy `selection.backend` and exact `selection.options` from a supported returned
row. For example, **if the current catalog returns** `gpt-6-astra` for Codex and
lists `high` with `squad_option: reasoning`, those values can become:

```yaml
backend: codex
options:
  model: gpt-6-astra
  reasoning: high
```

This is an illustration, not a model lookup table. Preserve opaque IDs, including
Cursor bracketed parameters and Kimi aliases. Resolve multiple backend/provider
matches explicitly instead of silently choosing one. Kimi effort metadata and
OpenCode variants currently have no executable Squad option mapping. Unknown or
unlisted explicit YAML models remain structurally valid: discovery is not a run gate.

`--include-hidden` requests hidden records where supported; every source reports
requested/applied policy. `configured`, `connected`, source freshness and inference
access are separate evidence; `null` means unknown. Listing sends no inference,
creates no Squad/conversation state and never proves paid-model access. There is no
Squad catalog cache: run the command again to refresh. No REST route is provided.

At most three targets run concurrently. `--backend-timeout` defaults to 30s and
starts when a worker starts. Overall `--timeout` defaults to
`max(60s, ceil(targets/3) * (backend-timeout + 5s) + 5s)` (75s for five targets,
110s for seven); explicit shorter timeouts win. Cleanup has at most five additional
seconds. Limits are 8 MiB per frame/response, 32 MiB total source bytes, 100 pages,
and 10,000 rows; incomplete data is never labelled complete.

| Backend | Discovery interface | Selection / limitations |
| --- | --- | --- |
| Codex | Private App Server `model/list` | Native model ID; returned reasoning maps to `reasoning`; no thread or turn |
| Cursor | `--list-models` text | Opaque exact ID; verified header/rows/footer; format drift is explicit |
| Kimi | `provider list --json` | Configured alias; provider secrets and unknown fields are excluded |
| OpenCode | Classic `/provider` and `/config/providers` | Provider/model; managed owned runtime or configured external endpoint |
| ZCode | No verified read-only bootstrap | `unsupported`, `read_only_catalog_unavailable`; no session workaround |
| fake | Fixed synthetic `fake` record | Explicit test fixture; arbitrary manually configured models still work |

**Kimi compatibility:** explicit `options.model` is now passed unchanged as
`--model`. Stale values previously ignored can therefore fail at execution; choose
a returned configured alias or omit `model` to use Kimi's default. There is no
silent fallback. Discovery does not certify Kimi's broader run protocol.

See [compatibility evidence](docs/model-discovery.md) for observed versions, smoke
outcomes and the ZCode boundary. Observed versions are not guaranteed support ranges.

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
  proxy_url: ""                  # HTTP proxy for decision requests; empty falls back to the machine backend settings file, then environment proxy settings
  timeout_seconds: 30            # per-call timeout; transient failures are retried with backoff
```

The API key lives in a one-line file containing the bare token (no `Bearer` prefix, no quoting). The default location is in the user's home directory — outside the workspace — so credentials never land in a git repository. Startup requires the key file only when the configured workflow declares verdict tasks or a `judge:` section is present; otherwise the server starts and operates with no judge dependency.

Workflow-level settings: `confidence_threshold` (built-in default `0.7`, overridable by the machine judge setting below) — a judge verdict applies only when its confidence meets the threshold — and `on_uncertain` (default `needs_attention`):

- `needs_attention`: below-threshold confidence keeps the attempt in the `judging` state and moves the execution to `needs_attention`, exposing the full distribution (`uncertain_verdict:<task>:<n>` attention reason). Resolve it with a manual override or `resume` (which re-runs classification).
- `error`: below-threshold confidence fails the attempt with reason `uncertain_verdict`; existing failure policy and retries apply.

An explicit workflow `confidence_threshold` takes precedence over the machine judge default, which takes precedence over the built-in `0.7`. Omitted thresholds remain omitted in saved definitions, so definition hashes and replay identity are unchanged. Future classifications of those executions, including recovery or resume of judging attempts, use the machine value loaded at server startup when present; already settled verdicts keep their recorded outcomes and thresholds. Editing workflow YAML does not change an existing execution's saved definition.

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
| `opencode` | Owned `opencode serve` (default), or explicit external HTTP server | OpenCode session ID | `model`, `agent`, `timeout_seconds`, `yolo`; external `mode`, `base_url`, `username`, `password` |
| `zcode` | Private App Server process | ZCode session ID | `command`, `runtime_path`, `provider`, `model`, `reasoning`, `yolo` |
| `kimi` | CLI process | Kimi local session | `command`, `model`, optional `session_root` |
| `fake` | In process | Deterministic state | none |

`defaults.yolo` is `true` when omitted. Codex maps YOLO to its approval/sandbox bypass flags, Cursor maps it to `--force`, and OpenCode automatically replies `once` to permission requests for the active run and its tracked descendant sessions. Reviewer roles should explicitly use `yolo: false`; Cursor reviewers should additionally use a read-only `mode` such as `ask` or `plan`.

Executable and location options (`command`, `runtime_path`, `base_url`) and proxy/CA variables can default from the machine backend settings file below; explicit agent options always win.

Cursor model IDs depend on the account and current catalog. Verify them before use:

```sh
cursor-agent --list-models
```

### Backend Installation Preflight

Before Squad admits work it verifies local backend prerequisites, so missing executables fail with one actionable report instead of failing mid-attempt after the workspace changed. The gate runs for manual turns (selected agent only), workflow submission (every agent referenced by the task graph, including downstream, loop and allowed-to-fail tasks), recovery and resume (agents that can still execute), retry acceptance (only the retry target), every dispatch batch, and the one-shot `run` command. It never installs or updates tools, probes CLI versions, reads credentials, tests authentication or model access, or creates backend sessions.

Bare command names are resolved with the **effective child PATH**, not the server's PATH — an intentional fix over earlier behavior that looked the executable up in the server environment. Agents with no `env`/`inherit_env` options inherit the ambient environment; once environment options produce a nonempty child environment, PATH must be present in it (via `inherit_env: [PATH]` or an explicit entry) or an absolute `command` must be configured. PATH entries that are relative directories reject bare-name lookup (`invalid_search_path`); empty entries are ignored. Explicit paths never fall back to defaults or aliases, resolve without shell expansion, and relative paths resolve against the workspace. Symlinks are followed to regular executables with effective-user execute access, and known shebang interpreters are verified without executing wrapper contents.

A failed admission returns `503` with a structured report before any run or execution is created:

```json
{
  "error": "codex [not_found] (executable): the configured command was not found ...",
  "code": "backend_preflight_failed",
  "issues": [
    {
      "phase": "installation",
      "backend": "codex",
      "agents": ["reviewer"],
      "component": "executable",
      "code": "not_found",
      "restart_required": false,
      "message": "the configured command was not found in the effective child PATH; inspect the command and PATH settings and install the missing backend",
      "installation_links": [{"label": "Codex CLI installation", "url": "https://learn.chatgpt.com/docs/codex/cli"}]
    }
  ]
}
```

Messages never include command/path values, environment, proxy credentials or child output; reports name at most the built-in default command and the option source (agent, machine or built-in default). A rejected manual run or workflow submission consumes no run/execution identity and no request ID, so the same request can be resubmitted after repair.

OpenCode readiness is a separate phase after every local check passes: managed mode starts (or reuses) the owned server, external mode probes `GET /global/health` with redirects disabled. If the first managed startup fails or is cancelled, that runtime stays failed until Squad is explicitly restarted; the report carries `restart_required: true` with guidance. Local installation failures and external service errors never latch anything, and a successfully started shared server survives unrelated admission failures.

Official installation pages used in reports:

| Product | Link |
| --- | --- |
| Codex CLI | https://learn.chatgpt.com/docs/codex/cli |
| Cursor CLI | https://cursor.com/docs/cli/installation |
| Kimi CLI | https://www.kimi.com/code/docs/en/ |
| OpenCode | https://opencode.ai/docs/ |
| OpenCode server | https://opencode.ai/docs/server/ |
| ZCode desktop | https://zcode.z.ai/en/docs/install |
| Node.js (ZCode interpreter) | https://nodejs.org/en/download |

These pages document upstream OS availability; Squad releases target macOS and Linux (AMD64/ARM64). Native Windows source builds report `unsupported_platform` for local backend checks; WSL uses Linux semantics. ZCode's implicit runtime location stays macOS-specific; Linux users must configure `runtime_path` explicitly.

### ZCode App Server

See [examples/zcode-squad.yaml](examples/zcode-squad.yaml). Start it with:

```sh
agent-debug-squad serve --config examples/zcode-squad.yaml
```

This experimental adapter supports **ZCode desktop 3.x** installs and a signed-in **Z.AI individual Coding Plan** account. Install Node (tested with Node 26) and sign in through ZCode first. There is no pinned version list: before loading the runtime, the host bridge probes the bundle structurally for the anchors it needs (CLI autorun statement, native credential store, provider registry). Bundles that keep those anchors work without a Squad update; bundles where an anchor is missing or ambiguous fail closed before a prompt is sent, with an actionable error that includes the bundle SHA-256 for reporting. The host bridge loads the installed runtime's native credential reader in memory; it never edits the bundle or exports credentials into Squad state.

Options:

- `command`: Node executable, default `node`.
- `runtime_path`: bundle location, default `/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs`. Other locations must contain a compatible bundle and its companion resources.
- `provider`: `account:zai-individual-coding-plan` (default and the only accepted value). Every other provider ID fails validation before preflight.
- `model`: default `GLM-5.3-Flash`; `reasoning`: `low` (default), `high`, or `max`. Before dispatch the adapter verifies the exact provider/model/reasoning is selectable in the live registry view of the installed runtime; an unselectable request fails before any model request is sent, without substituting another model or reasoning level. The full selection is sent on every turn.
- `yolo`: inherits squad defaults. True selects native `yolo`; false explicitly selects `build`. This also updates ZCode's workspace permission preference. False is permission-controlled, **not a read-only sandbox**.
- `env` / `inherit_env`: the same explicit environment rules as other CLI adapters. Inherit `HOME` and `PATH` for the existing login and tools.

**Removed `plan_policy` option (breaking):** the `plan_policy` agent option (`fixed`/`start-first`), Start Plan routing, Start-to-Individual continuation, and the pre-dispatch active-subscription check were removed in v0.20.0 after the subscription gate broke fixed-mode runs whenever `api.z.ai` was unreachable. Configurations that still set `plan_policy` fail validation with an error naming the option; delete the line to migrate. Dispatch now requires only the resolvable Individual credential plus the local registry-view selectability check; no Z.AI network origin is read before a turn.

**Live compatibility (ZCode Desktop 3.14.3):** Individual Flash conversations have been tested on a real account.

**Session diagnostics (separate from `models`):** the current account catalog is captured from `session/create` or `session/resume` as a `zcode.models` record in `<agent>.diagnostics.jsonl` before each prompt. For example:

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

Kimi is the one exception: its child keeps the server's full ambient environment until the resolved spec carries `env` or `inherit_env` entries — agent options or machine backend settings — at which point kimi follows the same constrained rules as the other CLI backends. A machine file that sets a kimi proxy should also declare `kimi.inherit_env: [HOME, PATH]` so login and config keep working without touching squad YAML.

Keep credentials out of committed YAML. Prefer the machine backend settings file (below), environment variables, an OS credential store, or a private ignored launcher/config. The checked-in proxy URLs use the reserved `.example` domain and are non-functional placeholders.

Cursor browser authentication normally requires inheriting `HOME`; API-key authentication requires `CURSOR_API_KEY`. When Cursor uses `HTTP_PROXY` or `HTTPS_PROXY`, also set `NODE_USE_ENV_PROXY=1`. Inherit `NODE_EXTRA_CA_CERTS` if the proxy performs TLS inspection. The machine backend settings file sets all of these from `cursor.proxy_url` / `cursor.ca_cert_file`.

ZCode model traffic uses `ZCODE_HTTP_PROXY`, with exclusions in `ZCODE_NO_PROXY` and a custom CA file in `ZCODE_AGENT_CA_CERT`. Explicitly inherit these variables (as in the example), set non-secret values through `env`, or configure them once per machine via the `zcode` section of the machine backend settings file. Ordinary `HTTP_PROXY` / `HTTPS_PROXY` variables alone are not a substitute for `ZCODE_HTTP_PROXY`: the runtime treats model, web-fetch, and tool traffic differently. The bridge also honors `ZCODE_BUILTIN_PROVIDER_CONFIG_FILE` and `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE` when explicitly passed; normally it derives these paths from the installation and HOME. Custom `ZCODE_STORAGE_DIR` / `ZCODE_SESSION_DB_PATH` isolate conversations from the desktop's default history.

### Machine Backend Settings

Machine-specific backend settings — proxies, CA files, executable, runtime, and server locations — and the judge confidence default live in an optional `~/.agent-debug-squad/backends.yaml` (resolved against the server user's home directory, outside any workspace), so squad YAML stays free of machine details and remains shareable. A missing or empty file changes nothing. The file is loaded once at server startup; later edits apply after a restart. Unknown sections, unsupported keys, and invalid values fail startup with an error naming the file, section, key, and supported values. Proxy URLs may embed credentials: values are never logged or persisted, and startup reports only which backends have machine settings.

```yaml
codex:
  command: /opt/homebrew/bin/codex       # default executable when an agent sets none
  proxy_url: http://proxy.example:3128   # HTTP_PROXY / HTTPS_PROXY for the CLI
  no_proxy: localhost,127.0.0.1          # string or list; NO_PROXY, delivered verbatim
  inherit_env: [HOME, PATH]              # ambient vars always copied to the CLI (list or comma string)
cursor:
  proxy_url: http://proxy.example:3128   # standard vars plus NODE_USE_ENV_PROXY=1
  ca_cert_file: /etc/proxy-ca.pem        # NODE_EXTRA_CA_CERTS
  inherit_env: [HOME, PATH]
kimi:
  proxy_url: http://proxy.example:3128   # standard vars plus NODE_USE_ENV_PROXY=1
  inherit_env: [HOME, PATH]              # keeps login/config working under the constrained env
zcode:
  command: /opt/homebrew/bin/node        # Node executable default
  runtime_path: /Applications/ZCode.app/Contents/Resources/glm/zcode.cjs
  proxy_url: http://proxy.example:3128   # ZCODE_HTTP_PROXY only — never plain HTTP_PROXY
  no_proxy: localhost
  ca_cert_file: /etc/proxy-ca.pem        # ZCODE_AGENT_CA_CERT
opencode:
  mode: managed                       # default; Squad owns and reuses opencode serve
  command: opencode                    # executable path, not a shell command
  proxy_url: http://proxy.example:3128  # optional: OpenCode -> provider, not Squad -> OpenCode
  no_proxy: [localhost, 127.0.0.1, "::1"] # mandatory loopback entries are always added
  snapshot: false                      # default; true opts back in
  inherit_env: [OPENAI_API_KEY]          # optional explicit provider/config environment
judge:
  proxy_url: http://proxy.example:3128   # default for the OpenRouter judge; the session judge.proxy_url wins
  confidence_threshold: 0.6             # default for workflows without their own threshold; number in (0, 1]
```

`inherit_env` (rejected for `judge` and external OpenCode; see the managed OpenCode policy below) names ambient variables copied from the server process into every child process of that backend — values come from the server environment at dispatch time, so the file itself never carries secrets. It unions with the agent's own `options.inherit_env` (machine entries first, duplicates removed), an explicit agent `options.env` entry still wins, and naming a variable there suppresses a machine-injected value for it. Declaring `inherit_env: [HOME, PATH]` alongside a proxy is the recommended baseline for CLI backends — for `kimi` in particular, whose child switches to the constrained environment model as soon as any machine network settings or `inherit_env` apply.

`judge.confidence_threshold` must be an unquoted, finite number greater than 0 and at most 1. An explicit workflow threshold still wins. The machine default applies to future classifications of saved executions whose workflow omitted the field, including after recovery; it does not rewrite completed verdicts or enter the workflow hash. Restart the server after changing this file.

For Codex, Cursor, Kimi and ZCode, explicit agent `options` in squad YAML win over machine settings, which win over built-in defaults. A machine-injected variable is suppressed when the agent defines it in `options.env` or names it in `options.inherit_env`, so the child environment never carries duplicate keys. Machine settings apply to facilitator agents and to agents dispatched for recovered workflow executions alike, and never enter persisted workflow snapshots or run artifacts.

### Managed and external OpenCode

OpenCode defaults to **managed** mode. One Squad owns one reusable `opencode serve` process in its workspace; ordinary agents and workflow attempts share that server while retaining separate sessions and per-request models. The server binds `127.0.0.1`, disables mDNS, and uses a generated password. `--port 0` lets OpenCode bind an available port itself (v1 prefers 4096, otherwise an OS-selected port); Squad uses the child's announcement, never an independently running server. Startup allows 30 seconds for authenticated health readiness, followed by a separate configuration check bounded to 10 seconds or an earlier caller deadline.

Process settings belong only in `~/.agent-debug-squad/backends.yaml`. Agent `options.command`, `proxy_url`, `no_proxy`, `snapshot`, `env` and `inherit_env` are rejected. Nonempty agent `username`/`password` are rejected in managed mode because Squad generates authentication; they remain available in external mode (username defaults to `opencode`). Models, OpenCode agent selection, timeouts and YOLO remain per-agent. This prevents agents racing over shared process configuration. Different process settings require separate Squad processes.

The child inherits `HOME`, `PATH`, `TMPDIR`, `TMP`, `TEMP` and the four `XDG_*_HOME` config/data/cache/state paths. Other variables (provider API keys, `OPENCODE_CONFIG`, `OPENCODE_CONFIG_DIR`, `OPENCODE_CONFIG_CONTENT`, certificates, etc.) require machine `inherit_env`. Environment is captured when Squad creates its runtime. Ambient proxies are not inherited automatically. An explicit `proxy_url` overrides inherited proxies and sets `HTTP_PROXY`/`HTTPS_PROXY`; otherwise explicitly inherited proxy variables remain effective. No configured or inherited proxy means direct provider connections. Upper/lowercase `NO_PROXY` combines inherited exclusions, machine `no_proxy`, and `localhost,127.0.0.1,::1`. Squad's own HTTP transport always bypasses proxies, including in external mode.

Managed mode merges `snapshot` into inherited JSONC `OPENCODE_CONFIG_CONTENT`, preserving other property values and OpenCode's normal config merging. Comments and formatting are discarded only in the serialized child environment; source files and the parent environment are not rewritten. It never patches `/config` or writes user/project configuration to apply this setting. Before each prompt, session creation or saved-session recovery, Squad reads effective `/config` for the workspace and requires the requested boolean. Missing values, unavailable APIs and higher-priority administrator overrides fail closed. Existing snapshot data is not deleted.

**OpenCode 1.18.30 compatibility guard:** OpenCode itself adds `$schema` to config files that omit it, and migrates a legacy global `config` TOML. Managed startup inspects known config locations and refuses these files (or unreadable/invalid JSONC), leaving migration to the operator. Existing JSON/JSONC configs should contain `"$schema": "https://opencode.ai/config.json"`. This is not a filesystem sandbox: native package caches, plugins and agent tools still have their usual behavior. The smoke test covers classic v1 `serve` and HTTP/SSE on 1.18.30; this is not the v2 managed service API.

To retain an independently operated server, **add explicit external mode** to an existing endpoint configuration:

```yaml
opencode:
  mode: external
  base_url: http://127.0.0.1:4096
  snapshot: false  # read-only expectation; configure the server itself beforehand
```

**External workspace compatibility:** the server must see the same workspace at the same absolute path as Squad. Both modes send `x-opencode-directory`; saved sessions must report that directory or a symlink resolving locally to it. Remote servers with different roots/mount paths are unsupported, with no implicit mapping or fallback to server cwd. This is a migration restriction for existing external configurations. HTTP redirects are rejected in both modes to keep endpoint selection and authentication under Squad control; use the final, non-redirecting `base_url`.

Alternatively use `options.mode: external` with `options.base_url` on an individual agent; its endpoint overrides the machine endpoint. Existing `base_url` without external mode now fails with migration guidance instead of silently changing connection ownership. External mode rejects machine `command`, `proxy_url`, `no_proxy` and `inherit_env`, even if explicitly empty. The operator must apply provider networking to their own process. Squad verifies `snapshot` but never changes external configuration or stops that server. If snapshots are intentionally enabled externally, declare `snapshot: true` in machine settings.

Resetting an agent or completing a workflow attempt keeps the managed server alive. Squad cancellation and normal exit terminate only its owned process group, escalating from TERM to KILL after three seconds. Cancellation of the initiating request during first startup also leaves that runtime failed until an explicit Squad restart, even if no prompt was submitted. Later request cancellation after successful startup keeps the shared server running. Child failure fails operations; it never triggers automatic restart or prompt replay. Restart Squad explicitly to launch a fresh owned process and validate existing saved session IDs in the same workspace/data directory. Missing IDs or IDs from a different workspace fail instead of silently replacing conversations; restore the original data or explicitly choose a new `session_name`. After uncatchable SIGKILL or a machine crash, an orphan may need operator cleanup; a new Squad never adopts or kills that process. One-shot workflow execution closes the owned server after stopping scheduling and workers.

Raw child diagnostics are suppressed because they can contain secrets; startup errors report the failing stage. Known proxy URLs and credentials are redacted from adapter output and errors. No model calls are needed for the opt-in integration check:

```sh
SQUAD_OPENCODE_SMOKE=1 go test ./internal/adapters/opencode -run TestInstalledOpenCodeSmoke -count=1 -v
```


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
- A task may declare `verdicts` (at least two names, the reserved name `uncertain` is rejected); the workflow may set `confidence_threshold` (built-in default `0.7`, subject to the machine judge default) and `on_uncertain` (`needs_attention` default, or `error`). Verdict tasks run an extra judging phase after their response is saved; see [Verdict Judge](#verdict-judge).
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
- Restart recovers committed outcomes and artifacts, keeps paused executions paused, continues `cancelling` until resolved, and marks reserved/running attempts without committed outcomes as `interrupted`, stopping new dispatch until intervention. Unknown snapshot schema versions or damaged authoritative state fail closed. Version-2 workflows use schema 4. Legacy loop executions must be finished or cancelled with the previous binary before upgrade; terminal legacy history remains read-only. See [Loop Intervention and Recovery](#loop-intervention-and-recovery).
- A workflow-owned runtime rejects manual run/reset mutation with `409`; manual agents, follow-up continuity, run APIs, and permission replies keep their existing behavior, and workflow attempts are visible through the same run endpoints.

### Bounded Loops

New loops require `workflow.version: 2`. Loopless version-1 workflows retain their existing format and behavior. Each loop declares a positive `max_iterations`; each task's `loop` names its direct owner. A loop without control tasks runs exactly its bounded number of passes. See [workflow-loop.yaml](examples/workflow-loop.yaml).

```yaml
workflow:
  version: 2
  name: review-queue
  max_parallel: 2
  loops:
    queue: {max_iterations: 10}
  tasks:
    select:
      agent: selector
      loop: queue
      prompt: "Select the next MR, or report that the queue is empty."
      verdicts: {ready: "An MR is ready", empty: "No MRs remain", human: "A decision is required"}
      control: {ready: proceed, empty: break, human: needs_attention}
    review:
      agent: reviewer
      loop: queue
      needs: [select]
      prompt: "Review the selected MR."
    report:
      agent: reporter
      needs: [select]
      prompt: "Summarize the queue outcome."
```

A pass counts as soon as it is entered, even when the head selector immediately breaks or continues without reviewing anything. Root loops enter pass 1 at submission. A child has a planned path at local counter 1 but consumes no pass until admitted. Judge calls and explicit task retries do not increment pass counters. Automatic attempts per leaf are bounded by the effective caps of its enclosing invocations; manual extensions and retries remain explicit interventions, not an elapsed-time or spending guarantee.

Every loop-owned attempt and outcome carries its complete `iteration_path`, including flat loops (`queue=1`) and nested loops (`delivery=2/review=1`). Local counters alone do not identify historical outcomes. Results from earlier passes remain inspectable but do not replace missing results in the final pass.

### Task Controls

An optional task `control` maps **every declared verdict** to one of four actions. The control task must directly belong to a loop, declare at least two verdicts and be mandatory (`allowed_to_fail: false`, the default). `until_task` and loop-level `on_verdict` have been removed; configuration errors include migration guidance. Other tasks may declare observational verdicts without a control map.

| Action | Effect after the control verdict settles |
| --- | --- |
| `proceed` | Release the remaining phase of this pass. |
| `continue` | Skip the remaining phase, close this pass and request another bounded pass. |
| `break` | Skip the remaining phase and complete only the owning loop. |
| `needs_attention` | Hold at the control; keep the remaining phase pending for intervention. |

Controls can appear at the head, middle or tail, and a loop may have several ordered controls. In each loop's projected graph, every other direct task or immediate child-loop subtree must be before or after each control. An independent bypass branch is rejected. Child admission is a whole-subtree barrier: even an internal root without a raw dependency waits for every projected predecessor. An outer control waits for an entire preceding child invocation to finish.

Classification and scheduling stay distinct. Backend failure, timeout, missing response, unavailable judge and low confidence never select a control action. Confidence keeps the existing precedence: explicit workflow threshold, startup-loaded machine threshold, then `0.7`. The synthetic `uncertain` verdict is not a control-map key.

Normal completion of a pass requests another pass too. At the effective cap, a fixed-count loop succeeds; a controlled loop applies `on_exhaustion` (`needs_attention` by default, or `succeed`). **A controlled loop whose maps always say `proceed` therefore holds at the cap by default.** `needs_attention` selected by a control is a distinct hold and is not waived by exhaustion policy. Explicit `on_exhaustion` requires a directly owned control task.

Skipped work is an outcome, not a backend attempt: it has a full planned path, `loop_break` or `loop_continue` reason and a stable `lcd_N` decision ID, with no fabricated run, attempt, verdict or result. Skipping an unadmitted child recursively records its children and grandchildren with `entered: false` and `iterations_started: 0`. Existing prefix results and all historical skips are preserved. Skips alone do not fail the workflow, but a required dependency on a final skipped task blocks with `dependency_skipped:<producer>:<decision-id>`; neither `allowed_to_fail` nor a quorum threshold waives missing output. In the example, a report on `select` can run after an empty queue, while a report on `review` would block.

Loop failure or blocking holds unfinished loop work for intervention. Outside consumers wait for the actual child/root completion barrier. A required skipped output outside all loops can make the final workflow fail. Ordinary tolerated failures retain their existing accounting.

### Nested Loops

Loops form a forest through optional `parent`. A present parent must be a nonempty string naming a declared loop; cycles, empty subtrees and unrelated cross-branch dependencies are rejected. A container loop may have only children and no directly owned tasks. See [workflow-nested-loop.yaml](examples/workflow-nested-loop.yaml) for a three-level example.

The shared scope graph projects each immediate child subtree to one vertex and checks boundary cycles recursively. Route sibling handoffs through an explicit task in their common enclosing scope, or through a workflow-scope task for separate roots. Such bridges cost an agent task and transfer completed invocations; they are not a channel between simultaneous iterations.

Advancing a parent resets descendants to fresh planned pass-1 paths and clears their local extensions and stop intent. Completing a parent preserves the final child states. A child's `break` is local: fixed-count ancestors may still repeat. Every closed pass records its final child paths, so handoff follows exact historical contexts instead of searching for the latest successful descendant. `previous_iteration` and nearest-first `ancestor_previous_iterations` are explicitly historical summaries of the entire corresponding subtree, including skips. Every referenced successful result remains hash-verified before dispatch.

### Loop Intervention and Recovery

- **Extend:** `POST /workflows/{id}/loops/{name}/extend` takes `request_id` and positive `add_iterations`, raises only the current invocation's cap and rejects integer overflow. It can release an exhaustion hold but does not resurrect a skipped suffix. Done/skipped loops and terminal/cancelling executions reject new extensions. Replays acknowledge the originally accepted context, even after an ancestor advances.
- **Stop:** `POST /workflows/{id}/loops/{name}/stop` takes `request_id` and finishes the current pass without starting another. It propagates to all unfinished descendants, including planned children that must still run their first pass when admitted. At a held `needs_attention` decision it records effective `proceed` while preserving the mapped action and verdict evidence. At `continue` it preserves suffix skips and completes. A stop never cancels workers, unpauses, or waives failure, uncertainty or interruption.
- **Override:** only a current judging attempt or the current live held control decision can be overridden. A revision creates a new decision ID. Released or closed decisions cannot be rewritten, including attention decisions released by stop while paused. Accepted requests replay before lifecycle eligibility checks; new conflicts return `409`.
- **Retry:** only a current failed/interrupted real attempt can be retried, subject to existing cleanup and consumption guards. Skips cannot be retried. Projected child admission and committed controls consume their prefix, preventing a retry even if paused before suffix dispatch. An unfenced fixed-count loop can reopen at its final path without resetting descendants or granting a new pass.
- **Pause/cancel:** pause allows already-running outcomes and control decisions to commit, but prevents new admission, advance and dispatch. Resume does not waive semantic holds. Cancellation committed before classification wins; committed decisions and historical skips remain auditable.

All version-2 executions, including loopless graphs, persist **schema 4**. The authoritative commit includes a settled control verdict, decision, skips, close/exhaustion state and accepted request record. Advance consumes a closed pass in a separate atomic transition before any new work. Failed saves do not publish partial decisions. Recovery preserves committed decisions and skips, reclassifies unresolved judge work without repeating backend work, and requires intervention for interrupted attempts.

Views expose ordered `control_tasks`, decisions and evidence, effective actions, full paths, admitted/planned state, `iterations_started`, skip history, loop close history and unavailable-result reasons. Decision IDs are execution-local monotonic `lcd_N` identifiers; the decision record supplies the exact path/task/attempt/revision.

Before upgrading, finish or cancel every active legacy loop execution **using the previous binary**, then stop that server. Recognized schema-2/3 legacy loop executions are read-only history (`execution_supported: false`); any nonterminal legacy loop blocks new-server startup before state mutation or API availability. New mutations return `409`; accepted historical request replays remain read-only. Unknown schemas and corrupt records fail startup even when terminal. Loopless schemas 1/2 retain supported recovery.

Migrate configuration to version 2 and move each old `on_verdict` map onto the named task as `control`; use a **new submission request ID** because the definition identity changes. Do not edit snapshots or replay external effects to migrate an execution. Schema 4 cannot be rolled back into an older binary: stop the new server and retain its state and artifacts separately before rollback.

Runnable examples: [fixed count](examples/workflow-loop.yaml), [tail control](examples/workflow-loop-conditions.yaml), [nested controls](examples/workflow-nested-loop.yaml), and [queue with parallel reviews](examples/workflow-queue.yaml). Verdict examples require the configured judge, even with fake task backends.

## One-Shot Workflow Runs

`serve` is a long-lived service you submit work to explicitly. For batch use, the `run` command is the one-shot counterpart: one process owns exactly one workflow execution, preserves its results, and exits automatically when that execution settles.

```sh
agent-debug-squad run --config squad.yaml --request-id review-2026-09-25
```

Both `--config` and `--request-id` are required. The request ID is the durable identity of the execution:

- a new request ID creates exactly one execution and runs it;
- repeating the same request ID with the same resolved definition selects the original execution — recovery after a crash, or a replay of a finished one — without duplicating work;
- reusing the ID with a changed definition or agent configuration fails before any backend activity;
- another nonterminal execution in the same session state refuses startup, so two batch processes can never fight over one session.

While a nonterminal execution is owned, the process announces its control URL on stderr (including in quiet logging mode) and serves the normal workflow API scoped to that execution: pause/resume/cancel, eligible retries, verdict and loop controls, and permission replies keep working; new submissions, manual runs/resets, and controls of other executions return `409`. Intervention states (paused, needs attention, pending permission) keep the process alive with no overall timeout — a notice is printed when the state or its reasons change, and nothing is auto-approved, retried, resumed, or confirmed.

Once the execution durably reaches a terminal state the process seals it, drains the API, cancels and joins its owned workers under one shared 30-second budget, writes the summary, releases the session lock, and exits. Local child processes are killed through the process groups the run created; an externally managed OpenCode server is never stopped, and remote side effects are never claimed stopped. Safe replay of a terminal execution verifies its committed artifacts and exits without a listener, a judge, or new attempts.

Exit codes:

| Exit | Meaning |
| --- | --- |
| `0` | `succeeded` or `completed_with_errors` |
| `1` | `failed`, or a fatal runtime/persistence/reporting/cleanup error |
| `2` | invalid arguments or configuration, request/ownership conflict, port conflict, or other pre-execution startup failure |
| `3` | `cancelled` (no triggering signal) |
| `130` / `143` | SIGINT / SIGTERM accepted before terminal commitment initiated cancellation |

The final report is one JSON summary on stdout, also saved as `workflows/<execution_id>/run-summary.json`. It carries the request/execution identity, the last durable state and revision, the exit code and reason, the triggering signal if any, task counts, failed/blocked task details, recorded verdicts with their task/attempt/iteration-path identity, attention reasons, artifact paths, and the cleanup status with any outstanding run IDs. It is a derived, latest-invocation report: recovery and scheduling never read it, replay may replace it, and all existing artifacts stay untouched. Shell status and stderr take precedence over the report's recorded exit code when the summary file could not be persisted or stdout failed.

Batch users can replace the separate serve/submit/wait/stop sequence with `run` and keep their request ID for safe replay; `serve` behavior is unchanged, and recovery can also be done with an existing `serve` after a one-shot owner has exited.

See [examples/workflow-chain.yaml](examples/workflow-chain.yaml) for a runnable fake-backend config:

```sh
agent-debug-squad run --config examples/workflow-chain.yaml --request-id chain-example
```

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
workflows/<execution_id>/run-summary.json
```

The diagnostic artifact records safe adapter invocation metadata. Cursor diagnostics include the executable and effective CLI flags while omitting prompts, environment values, credentials, and backend session IDs. `run-summary.json` is the derived one-shot CLI report described above and is written only by `run`. The files are designed to be readable by people, scripts, and other agents. Add the configured state directory to the workspace's `.gitignore`; runtime transcripts may contain source code, prompts, or model output.

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
