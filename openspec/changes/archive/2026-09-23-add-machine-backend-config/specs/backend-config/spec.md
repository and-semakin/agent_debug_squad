# backend-config Delta

## Purpose

Keeps machine-specific backend settings — per-backend proxies, CA certificates, and local executable, runtime, or server locations — in one optional file in the user's home directory, so squad YAML files stay free of machine details and remain shareable.

## ADDED Requirements

### Requirement: Machine backend settings file

The system SHALL read an optional backend settings file from `~/.agent-debug-squad/backends.yaml`, resolved against the server user's home directory and therefore outside any workspace. The file SHALL be loaded exactly once at server startup; later edits to it SHALL NOT affect a running server. When the file is absent, the system SHALL start and behave exactly as if no machine backend settings existed, with no warning.

#### Scenario: Missing file changes nothing

- **WHEN** the server starts and `~/.agent-debug-squad/backends.yaml` does not exist
- **THEN** startup succeeds and every backend resolves its settings from squad YAML agent options and built-in defaults exactly as before

#### Scenario: Machine file supplies an executable default

- **WHEN** the machine file sets `codex.command` to an absolute path and a squad YAML codex agent sets no `command` option
- **THEN** that codex agent's runs use the machine-configured executable

#### Scenario: Edits apply only after restart

- **WHEN** the machine file is modified while the server is running
- **THEN** already-running and newly started agent runs keep using the settings resolved at startup until the server is restarted

### Requirement: Strict validation of the machine settings file

The file SHALL accept only the backend sections `codex`, `cursor`, `kimi`, `opencode`, `zcode`, and `judge`. Each section SHALL accept only its supported keys: `codex` and `kimi` accept `command`, `proxy_url`, `no_proxy`, and `inherit_env`; `cursor` accepts `command`, `proxy_url`, `no_proxy`, `ca_cert_file`, and `inherit_env`; `zcode` accepts `command`, `runtime_path`, `proxy_url`, `no_proxy`, `ca_cert_file`, and `inherit_env`; `opencode` accepts `base_url` only; `judge` accepts `proxy_url` only. Startup SHALL fail with an actionable error naming the file, the offending section or key, and the reason when the file contains an unknown backend section, a key the backend does not support, a `proxy_url` or `base_url` that is not a parseable URL with an `http` or `https` scheme and host, a value of the wrong type (non-string scalars such as booleans, integers, or null; non-string list items), a present-but-blank value, or more than one YAML document. An explicit empty string SHALL be valid and mean "unset". An empty file SHALL be valid and equivalent to a missing file.

#### Scenario: Unknown backend section fails startup

- **WHEN** the machine file contains a `claude` section
- **THEN** startup fails with an error naming `~/.agent-debug-squad/backends.yaml`, the unknown section `claude`, and the list of supported sections

#### Scenario: Unsupported key fails startup

- **WHEN** the machine file sets `proxy_url` under `opencode`
- **THEN** startup fails with an error naming the `opencode` section, the unsupported key `proxy_url`, and the keys `opencode` actually supports

#### Scenario: Invalid proxy URL fails startup

- **WHEN** the machine file sets `cursor.proxy_url` to a value that is not a parseable `http`/`https` URL
- **THEN** startup fails with an error naming the file and key, and no server starts

#### Scenario: Non-string scalar fails startup

- **WHEN** the machine file sets `codex.command` to a boolean, integer, or null, or an `inherit_env` list contains a non-string item
- **THEN** startup fails with a wrong-type error naming the file, section, and key, so no coerced value ever reaches a child process

#### Scenario: Multiple YAML documents fail startup

- **WHEN** the machine file contains content after a `---` document separator
- **THEN** startup fails with a single-document error instead of silently ignoring the extra content

#### Scenario: Blank value fails startup while empty string means unset

- **WHEN** the machine file sets a key to a whitespace-only value
- **THEN** startup fails with a blank-value error naming the key; an explicit empty string stays valid and leaves the setting unset

#### Scenario: Empty file is valid

- **WHEN** the machine file exists but is empty
- **THEN** startup succeeds and behaves exactly as if the file were missing

### Requirement: Proxy and CA translation per backend

For CLI backends, machine network settings SHALL be delivered as environment entries on every backend child process, using the variables each backend honors: `codex` and `kimi` receive `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` derived from `proxy_url` and `no_proxy`, and `cursor` receives those variables plus `NODE_USE_ENV_PROXY=1` when `proxy_url` is set; `ca_cert_file` SHALL map to `NODE_EXTRA_CA_CERTS` for `cursor` and to `ZCODE_AGENT_CA_CERT` for `zcode`; `zcode` receives `ZCODE_HTTP_PROXY` from `proxy_url` and `ZCODE_NO_PROXY` from `no_proxy` and MUST NOT receive plain `HTTP_PROXY`/`HTTPS_PROXY` derived from machine settings, because the ZCode runtime routes its traffic types differently. `no_proxy` SHALL accept a comma-separated string or a list of strings and SHALL be delivered verbatim. `opencode` connections are loopback HTTP and SHALL NOT be proxied. Machine proxy variables SHALL apply in addition to the constrained child-process environment rules, not in place of them.

#### Scenario: Codex proxy becomes child environment variables

- **WHEN** the machine file sets `codex.proxy_url` and a codex agent runs
- **THEN** the codex child process environment contains `HTTP_PROXY` and `HTTPS_PROXY` set to that URL

#### Scenario: Cursor proxy enables Node environment proxying

- **WHEN** the machine file sets `cursor.proxy_url` and `cursor.ca_cert_file`
- **THEN** the cursor child process environment contains `HTTP_PROXY`, `HTTPS_PROXY`, `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS` pointing at the configured CA file

#### Scenario: ZCode proxy uses ZCode-specific variables

- **WHEN** the machine file sets `zcode.proxy_url` and `zcode.no_proxy`
- **THEN** the ZCode host process environment contains `ZCODE_HTTP_PROXY` and `ZCODE_NO_PROXY` with those values and no machine-derived `HTTP_PROXY` or `HTTPS_PROXY`

#### Scenario: OpenCode is never proxied

- **WHEN** the machine file configures any `opencode` key other than `base_url`
- **THEN** startup fails, because Squad connects to OpenCode over loopback and does not proxy it

### Requirement: Precedence of machine settings

Explicit agent options in squad YAML SHALL win over machine settings for the same key: an agent-specified `command`, `runtime_path`, or `base_url` SHALL be used unchanged, and the machine value SHALL apply only when the agent sets none. A machine-injected environment variable SHALL be suppressed for any variable the agent defines in `options.env` or names in `options.inherit_env`; those explicit choices SHALL reach the child process unchanged. Machine settings SHALL apply to agents dispatched for recovered workflow executions exactly as to freshly dispatched facilitator agents.

#### Scenario: Agent executable wins

- **WHEN** the machine file sets `kimi.command` and a squad YAML kimi agent sets `command` in its options
- **THEN** the agent runs its own configured executable and the machine value is ignored

#### Scenario: Explicit agent env wins over injected proxy

- **WHEN** the machine file sets `codex.proxy_url` and the codex agent defines `HTTPS_PROXY` in `options.env`
- **THEN** the child process receives the agent's `HTTPS_PROXY` value, not the machine-configured URL

#### Scenario: Inherit_env naming wins over injected proxy

- **WHEN** the machine file sets `cursor.proxy_url` and the cursor agent lists `HTTPS_PROXY` in `options.inherit_env`
- **THEN** the child process receives the server process's `HTTPS_PROXY` value when present, not the machine-configured URL

#### Scenario: Recovered workflow agents use machine settings

- **WHEN** a server restarts, the machine file sets `codex.command`, and a workflow execution recovers agents from its persisted snapshot whose specs set no `command`
- **THEN** the recovered codex agents run with the machine-configured executable

### Requirement: Machine inherit_env defaults

The machine settings file MAY declare `inherit_env` for `codex`, `cursor`, `kimi`, and `zcode` as a list of strings or a comma-separated string; `opencode` and `judge` MUST reject the key as unsupported. The declared variables SHALL be copied from the server process into every child process of that backend as defaults below agent options: they union with the agent's own `options.inherit_env` (machine entries first, duplicates removed, agent entries preserved in order), an explicit agent `options.env` entry for the same variable still wins, and naming a variable in the union suppresses machine-injected values for it. For `kimi`, machine `inherit_env` — alone or together with other machine entries — selects the constrained child environment with the union applied.

#### Scenario: Machine inherit_env complements a proxy for kimi

- **WHEN** the machine file sets `kimi.proxy_url` and `kimi.inherit_env: [HOME, PATH]` and the squad YAML kimi agent sets no environment options
- **THEN** the kimi child process environment contains `HOME` and `PATH` copied from the server process plus the machine proxy variables

#### Scenario: Machine and agent inherit lists union

- **WHEN** the machine file sets `codex.inherit_env: [HOME, PATH]` and a codex agent sets `inherit_env: [PATH, CODEX_HOME]`
- **THEN** the child process inherits `HOME`, `PATH`, and `CODEX_HOME`, with no duplicate inheritance

#### Scenario: Agent env entry beats an inherited variable

- **WHEN** the machine file sets `codex.inherit_env: [HOME]` and the codex agent defines `HOME=/other` in `options.env`
- **THEN** the child process receives `HOME=/other`

#### Scenario: inherit_env rejected without a child process

- **WHEN** the machine file sets `inherit_env` under `opencode` or `judge`
- **THEN** startup fails with an error naming the section, the unsupported key, and the keys that section supports

### Requirement: Machine settings values stay out of logs and artifacts

Proxy URLs from the machine settings file MUST NOT be written to server logs, run artifacts, persisted state, or validation errors, because they may embed credentials; diagnostics SHALL name at most the file, section, and key — never a value. Startup diagnostics SHALL report at most which backends have machine settings applied, without their values. Executable, runtime, and server locations (`command`, `runtime_path`, `base_url`) are operational provenance rather than secrets: they MAY appear in existing invocation diagnostics and launch errors, exactly as agent-configured values of the same kind already do.

#### Scenario: Proxy URL never appears in diagnostics

- **WHEN** the machine file sets `codex.proxy_url` containing userinfo, or a credentialed `proxy_url` fails validation
- **THEN** neither the URL nor any credential part appears in any error, log, run artifact, or persisted state; diagnostics name at most the file, section, key, and the backends for which machine settings were applied

#### Scenario: Executable path is operational provenance

- **WHEN** the machine file sets `cursor.command` and a cursor agent run is recorded
- **THEN** the existing invocation diagnostic may name the resolved executable, as it already does for agent-configured commands
