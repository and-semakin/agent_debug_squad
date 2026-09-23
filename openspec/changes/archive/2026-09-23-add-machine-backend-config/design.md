# Design: add-machine-backend-config

## Context

Today every backend setting reaches adapters through per-agent `options` in the squad session YAML (`internal/config` → `domain.AgentSpec.StringOptions`/`ListOptions`), and the judge through the `judge` section. CLI backends receive a constrained child environment built from `options.inherit_env` (named copies from the server process) and `options.env` (explicit `KEY=value` entries); proxies are wired by hand through those lists (`HTTP_PROXY`/`HTTPS_PROXY`, `NODE_USE_ENV_PROXY` for Cursor, `ZCODE_*` for ZCode), and the judge already has a transport-level `proxy_url`. The orchestrator funnels every dispatch — facilitator turns and workflow-owned runs, including recovery from persisted snapshots — through `agentSpecWithDefaults(cfg, spec)` right before `adapters.New(spec)`. Workflow executions persist their own immutable agent specs and re-normalize them via `config.NormalizeAgentOptions` on recovery, so a merge done only at initial `config.Load` would miss recovered runs.

## Goals / Non-Goals

**Goals:**

- One machine-local file holding settings that describe the machine, not the squad: proxies, CA files, executable/runtime/server locations.
- A single precedence rule applied identically by every backend and by recovery.
- Adapters keep consuming plain `AgentSpec` options; no adapter interface change.

**Non-Goals:**

- Moving squad semantics (models, `reasoning`, `yolo`, `mode`, `sandbox`, `agent`, prompts) or credentials into the machine file.
- Generic per-backend `env` maps in the machine file — they would re-create squad-YAML duplication and invite secrets; only typed, validated keys. `inherit_env` is deliberately not `env`: it names non-secret variables whose values still come from the server process at dispatch time, so it fits the typed-key model.
- Hot reload; a custom file-path flag or env override (tests use an isolated `HOME`); proxying the loopback OpenCode connection.
- `ca_cert_file` for `codex`/`kimi`: no documented, testable mechanism yet; deferred until verified (the key is rejected as unsupported, so adding it later is a compatible extension).

## Decisions

### D1: Load once at startup, resolve at dispatch, carry on `SessionConfig`

`internal/config` gains `LoadMachineBackends(homeDir string)` that reads `~/.agent-debug-squad/backends.yaml` (missing file → empty config, no warning) and strictly validates it. `cmd` attaches the result to `domain.SessionConfig` next to the squad config. The file is read once per server start; the merged settings are applied at dispatch time in `agentSpecWithDefaults`, which both dispatch paths already pass through — so facilitator agents, fresh workflow tasks, and recovered snapshot agents get identical treatment without touching `adapters.New`.

*Alternative rejected:* merging inside `config.Load` into `cfg.Agents` — invisible to workflow snapshots restored from persisted state, which deliberately keep their own specs. *Alternative rejected:* passing machine config into `adapters.New` — ripples through the adapter constructor and surfaces config typos as per-run errors instead of startup failures.

### D2: Merge by materializing into ordinary agent options, with suppression

Machine defaults for `command`, `runtime_path`, and `base_url` become `StringOptions` entries only when the agent sets none. Proxy/CA settings are translated into `KEY=value` entries prepended to `ListOptions["env"]`, after suppressing any machine-injected key that the agent itself defines in `options.env` or names in `options.inherit_env` — so the built child environment never contains duplicate keys and explicit agent choices always win. Adapters and `BuildEnv` stay unchanged.

### D3: One explicit translation table, keyed by backend

A single helper (in `internal/config`) owns the mapping and is unit-tested per backend:

| Backend | `proxy_url`/`no_proxy` → env | `ca_cert_file` → env | Other keys |
| --- | --- | --- | --- |
| `codex` | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | — | `command` |
| `kimi` | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, plus `NODE_USE_ENV_PROXY=1` (Node-based CLI) | — | `command` |
| `cursor` | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, plus `NODE_USE_ENV_PROXY=1` | `NODE_EXTRA_CA_CERTS` | `command` |
| `zcode` | `ZCODE_HTTP_PROXY`, `ZCODE_NO_PROXY` only — plain `HTTP_PROXY` is deliberately not set because the runtime routes model/web/tool traffic differently | `ZCODE_AGENT_CA_CERT` | `command` (Node executable), `runtime_path` |
| `opencode` | — | — | `base_url` |
| `judge` | transport-level proxy (no env vars) | — | — |

The table mirrors what README "Environment And Secrets" already documents for hand-configuration; the change automates it. New backends must add a row when they land.

`inherit_env` (`codex`, `cursor`, `kimi`, `zcode`) is not an env entry but a machine-level inheritance allowlist. It **unions** with the agent's own `options.inherit_env` (machine entries first, deduplicated, agent order preserved) rather than being replaced by it: the machine-level intent is "on this machine, this CLI always needs `HOME` and `PATH`", and replacement would silently drop that baseline whenever a squad lists one extra variable. *Alternative rejected:* agent-list-replaces-machine — forces every squad to repeat machine plumbing, defeating the feature's purpose. For `kimi`, any machine entry (`inherit_env` included) selects the constrained child environment, so a machine file that sets a kimi proxy should also declare `inherit_env: [HOME, PATH]` — one file, no per-squad edits. `opencode` and `judge` run no child process and reject the key.

### D4: Judge proxy falls back, session wins

`internal/judge.Setup` (or its caller) resolves the effective proxy: session YAML `judge.proxy_url` if nonempty, else machine `judge.proxy_url`, else environment behavior. No other judge field comes from the machine file in this change.

### D5: Strict structural validation, fail fast

Decoding requires exactly one YAML document (a decoder pass that must end in `EOF`, so content after `---` cannot silently escape validation) and an explicit per-section key allowlist, so unknown sections, unknown keys, and wrong structural types all fail at startup with the file path, section, key, and supported values in the message. Scalars are type-checked against their YAML tags: only `!!str` is accepted for string keys and list items, because `yaml.v3` would otherwise coerce booleans and integers into strings (`command: false` would become an executable named `false`). A present-but-blank value fails; an explicit empty string means "unset". `proxy_url` and `base_url` must parse with scheme `http`/`https` and a host — and validation errors never echo the value, because a mistyped proxy URL may embed credentials. Paths are not existence-checked: a missing executable already fails with an actionable exec error, and a wrong `runtime_path` fails in the ZCode bundle probing, which is the clearer diagnostic. Startup also fails when the user's home directory cannot be resolved: without it the settings file would be looked up relative to the process working directory — often the workspace — which D1 forbids.

### D5a: Full ambient inheritance on empty constrained env is accepted behavior

When an agent's resolved environment lists produce no entries — for example every `inherit_env` name is absent from the server environment — `BuildEnv` returns nil and the child inherits the server's complete ambient environment. This is deliberate: agents are trusted squad members, and if the server environment carries secrets, inheriting them is the intended default rather than a leak. Machine settings do not change this stance. Reviewers flagged it twice; the owner confirmed it as by-design (2026-09-23).

### D6: Machine settings are runtime environment, not snapshot content

Workflow snapshots keep persisting only the agent's raw options; machine values are never written into snapshots or run artifacts. A machine file change therefore applies to recovered executions after a restart — the same status as `PATH` today. This is a deliberate exception to snapshot immutability, which exists to preserve squad semantics (backend, model, options), not machine plumbing.

### D7: Secrets stay in the file, never in output

Proxy URLs may embed credentials, which is acceptable in a user-home file that is never committed (same trust level as the OpenRouter key file). Proxy URLs — valid or rejected — never reach logs, artifacts, state, or error text; errors name at most the file, section, and key. Startup logs at most the names of backends for which machine settings were applied. Executable, runtime, and server locations are operational provenance, not secrets: they may appear in invocation diagnostics and launch errors exactly as agent-configured values of the same kind already do.

## Risks / Trade-offs

- [Proxy env semantics are empirical per CLI] → the table encodes README-documented behavior only; per-backend unit tests pin each mapping so a regression or CLI change surfaces immediately.
- [Two configuration layers complicate debugging ("where did this value come from?")] → one precedence rule everywhere (agent > machine > built-in) and a startup log naming the backends with machine defaults; values stay out of logs.
- [Env duplicates could make child behavior OS-dependent] → suppression happens at merge time and a test asserts the built environment has no duplicate keys.
- [Strict validation may reject a typo'd-but-harmless file] → that is the point: a machine file is written rarely and errors naming the exact key are cheaper than silently ignored settings.

## Migration Plan

No migration: without the file, behavior is byte-for-byte today's. Rollback is deleting (or fixing) the file. Release notes document the new path and the translation table.
