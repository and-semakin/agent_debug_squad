# Proposal: add-machine-backend-config

## Why

Machine-specific backend settings — above all HTTP(S) proxies for Codex, Cursor, Kimi, ZCode, and the OpenRouter judge — currently have no home of their own. They must be repeated as per-agent `options.env` / `options.inherit_env` entries (`HTTP_PROXY`/`HTTPS_PROXY`, `NODE_USE_ENV_PROXY` for Cursor, `ZCODE_HTTP_PROXY`/`ZCODE_NO_PROXY`/`ZCODE_AGENT_CA_CERT` for ZCode) or as `judge.proxy_url` inside squad YAML files that are otherwise shareable and committable. Every squad config therefore duplicates machine details, and moving a config between machines either breaks those backends or leaks machine-specific (sometimes secret-bearing) proxy URLs into committed files.

## What Changes

- Add an optional machine-level backend configuration file at `~/.agent-debug-squad/backends.yaml` (resolved against the user's home directory, outside any workspace). A missing file changes nothing.
- Support per-backend sections: `codex`, `cursor`, `kimi`, `opencode`, `zcode`, and `judge`.
- Network settings per CLI backend: `proxy_url`, `no_proxy`, and `ca_cert_file`, translated into the mechanism each backend actually honors:
  - `codex`, `kimi`: `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` in the child-process environment.
  - `cursor`: the same variables plus `NODE_USE_ENV_PROXY=1`, and `ca_cert_file` maps to `NODE_EXTRA_CA_CERTS`.
  - `zcode`: `ZCODE_HTTP_PROXY`, `ZCODE_NO_PROXY`, `ZCODE_AGENT_CA_CERT`.
  - `opencode`: no proxy settings (Squad connects to a loopback HTTP server); supports only a `base_url` default.
  - `judge`: `proxy_url` feeds the OpenRouter HTTP transport, as a default below the session YAML `judge.proxy_url`.
- Machine-level defaults for installation-specific locations: `command` (for `codex`, `cursor`, `kimi`, and the Node executable for `zcode`), `runtime_path` (`zcode`), and `base_url` (`opencode`).
- Machine-level `inherit_env` for the CLI backends (`codex`, `cursor`, `kimi`, `zcode`): a default allowlist of ambient variables copied from the server process into every child process, unioned with the agent's own `options.inherit_env`. One machine file can keep `HOME`/`PATH` flowing to CLI agents whose squad YAML does not enumerate them — in particular for `kimi`, whose child switches to the constrained environment model as soon as any machine network settings or `inherit_env` apply.
- Precedence everywhere: explicit agent `options` in squad YAML win over machine config, which wins over built-in adapter defaults. Explicit agent `env` entries and variables named in `inherit_env` suppress machine-injected values for the same variable.
- Strict validation at startup: unknown backend sections, unknown keys, or invalid values (for example an unparseable `proxy_url`) fail startup with an actionable error.
- Squad-semantics settings (models, `reasoning`, `yolo`, `mode`, `sandbox`, `agent`, credentials such as `username`/`password` and API keys) intentionally stay in squad YAML or credential files and are out of scope.

No CLI flag, HTTP API, or persisted-artifact format changes. The change is fully backwards compatible: without the file, behavior is identical to today.

## Capabilities

### New Capabilities

- `backend-config`: Machine-level per-backend settings file — discovery, schema, strict validation, precedence over built-in defaults and under explicit squad YAML options, and per-backend proxy/CA translation into child-process environments and the judge transport.

### Modified Capabilities

- `verdict-judge`: The judge configuration requirement gains a machine-level `judge.proxy_url` default that applies when the session YAML `judge` section does not set one; the session YAML value still wins.
- `zcode-backend`: The explicit-configuration requirement gains machine-level defaults for `command` and `runtime_path` and ZCode proxy environment variables, still overridable by explicit agent options.

## Impact

- `internal/config`: load and strictly validate `~/.agent-debug-squad/backends.yaml`; expose the resolved machine settings on the session configuration.
- `internal/domain`: new types for machine backend settings and their merge into `AgentSpec`.
- `internal/orchestrator`: apply machine defaults in the existing `agentSpecWithDefaults` path so facilitator turns and workflow-snapshot recovery share one resolution.
- `internal/adapters` (codex, cursor, kimi, zcode, opencode): consume merged `command`/`runtime_path`/`base_url` defaults and proxy-derived environment entries; explicit agent environment entries keep winning.
- `internal/judge`: resolve the effective judge proxy from session YAML first, then machine config.
- `cmd/agent-debug-squad`: plumb the home directory into config loading if not already available.
- Documentation: README "Environment And Secrets" and backend notes, plus an example snippet; `configs/` examples stay committable (no machine paths added).
- Compatibility: no changes to HTTP responses, CLI flags, or persisted state; squads without the machine file behave exactly as before.
