# backend-config Specification

## Purpose
Keeps machine-specific backend settings and the judge confidence default in one optional file in the user's home directory, so squad YAML files stay free of machine details and remain shareable.

## Requirements

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

The file SHALL accept only the backend sections `codex`, `cursor`, `kimi`, `opencode`, `zcode`, `devin`, and `judge`. Each section SHALL accept only its supported keys: `codex`, `kimi`, and `devin` accept `command`, `proxy_url`, `no_proxy`, and `inherit_env`; `cursor` accepts `command`, `proxy_url`, `no_proxy`, `ca_cert_file`, and `inherit_env`; `zcode` accepts `command`, `runtime_path`, `proxy_url`, `no_proxy`, `ca_cert_file`, and `inherit_env`; `opencode` accepts `mode`, `command`, `base_url`, `proxy_url`, `no_proxy`, `inherit_env`, and boolean `snapshot`; `judge` accepts `proxy_url` and `confidence_threshold`. Startup SHALL fail with an actionable error naming the file, the offending section or key, and the reason when the file contains an unknown backend section, a key the backend does not support, a `proxy_url` or `base_url` that is not a parseable URL with an `http` or `https` scheme and host, a value of the wrong type (non-string scalars for string-valued settings, non-string list items, or a non-boolean `opencode.snapshot`), a present-but-blank value, or more than one YAML document. An explicit empty string for a string-valued setting SHALL be syntactically valid and mean "unset"; mode-specific validation SHALL still reject forbidden key presence, including empty declarations. This exception does not permit an empty string for boolean `opencode.snapshot`. `judge.confidence_threshold` SHALL require an unquoted finite YAML number greater than 0 and at most 1. An empty file SHALL be valid and equivalent to a missing file.

#### Scenario: Unknown backend section fails startup

- **WHEN** the machine file contains a `claude` section
- **THEN** startup fails with an error naming `~/.agent-debug-squad/backends.yaml`, the unknown section `claude`, and the list of supported sections

#### Scenario: Unsupported key fails startup

- **WHEN** the machine file sets `ca_cert_file` under `opencode`
- **THEN** startup fails with an error naming the `opencode` section, the unsupported key `ca_cert_file`, and the keys `opencode` actually supports

#### Scenario: Invalid proxy URL fails startup

- **WHEN** the machine file sets `cursor.proxy_url` to a value that is not a parseable `http`/`https` URL
- **THEN** startup fails with an error naming the file and key, and no server starts

#### Scenario: Non-string scalar fails startup

- **WHEN** the machine file sets `codex.command` to a boolean, integer, or null, or an `inherit_env` list contains a non-string item
- **THEN** startup fails with a wrong-type error naming the file, section, and key, so no coerced value ever reaches a child process

#### Scenario: Snapshot requires a boolean

- **WHEN** the machine file sets `opencode.snapshot: "false"` or `opencode.snapshot: ""`
- **THEN** startup fails with a wrong-type error naming the file, section and snapshot key

#### Scenario: Multiple YAML documents fail startup

- **WHEN** the machine file contains content after a `---` document separator
- **THEN** startup fails with a single-document error instead of silently ignoring the extra content

#### Scenario: Blank value fails startup while empty string means unset

- **WHEN** the machine file sets a key to a whitespace-only value
- **THEN** startup fails with a blank-value error naming the key; an explicit empty string for a string-valued setting passes type validation and leaves its value unset, subject to mode-specific restrictions on key presence

#### Scenario: Empty file is valid

- **WHEN** the machine file exists but is empty
- **THEN** startup succeeds and behaves exactly as if the file were missing

#### Scenario: Invalid machine threshold fails startup

- **WHEN** `judge.confidence_threshold` is zero, negative, greater than one, null, quoted text, NaN, or infinity
- **THEN** startup fails with an error naming the file, `judge.confidence_threshold`, and its allowed numeric range

#### Scenario: Unsupported threshold placement fails startup

- **WHEN** `confidence_threshold` appears in a backend section other than `judge`
- **THEN** startup fails with an unsupported-key error for that section

### Requirement: Machine judge confidence default

The machine file SHALL accept `judge.confidence_threshold` as a computer-wide default for verdict classifications. The server SHALL read it once at startup and SHALL apply it only when the saved workflow definition omits its own `confidence_threshold`. When the machine value is absent, the built-in default SHALL remain 0.7. The machine value SHALL stay outside workflow definition identity and saved snapshots.

#### Scenario: Machine default applies to omitted workflow threshold

- **WHEN** the machine file sets `judge.confidence_threshold: 0.6` and the workflow omits `confidence_threshold`
- **THEN** the effective gate is 0.6 without changing the workflow definition hash

#### Scenario: Workflow threshold wins

- **WHEN** the machine file sets `judge.confidence_threshold: 0.6` and the workflow sets `confidence_threshold: 0.8`
- **THEN** the effective gate is 0.8

#### Scenario: Machine default is loaded at startup

- **WHEN** the machine file changes from 0.6 to another valid value while the server is running
- **THEN** classifications continue using 0.6 until the server restarts

### Requirement: Proxy and CA translation per backend

For CLI backends, machine network settings SHALL be delivered as environment entries on every backend child process, using the variables each backend honors: `codex`, `kimi`, and `devin` receive `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` derived from `proxy_url` and `no_proxy`, and `cursor` receives those variables plus `NODE_USE_ENV_PROXY=1` when `proxy_url` is set; `ca_cert_file` SHALL map to `NODE_EXTRA_CA_CERTS` for `cursor` and to `ZCODE_AGENT_CA_CERT` for `zcode`; `zcode` receives `ZCODE_HTTP_PROXY` from `proxy_url` and `ZCODE_NO_PROXY` from `no_proxy` and MUST NOT receive plain `HTTP_PROXY`/`HTTPS_PROXY` derived from machine settings, because the ZCode runtime routes its traffic types differently. `no_proxy` SHALL accept a comma-separated string or a list of strings and SHALL be delivered verbatim except for OpenCode, which adds mandatory loopback exclusions. Managed OpenCode SHALL receive HTTP_PROXY/HTTPS_PROXY from proxy_url and merged NO_PROXY; the Squad-to-OpenCode HTTP transport SHALL NOT use a proxy. Explicit machine proxy_url wins over inherited proxies; otherwise only explicitly inherited proxy variables apply. Machine proxy variables SHALL apply in addition to the constrained child-process environment rules, not in place of them.

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

- **WHEN** managed OpenCode has a configured proxy and custom no_proxy entries
- **THEN** its process receives that proxy and the union of inherited/custom exclusions and localhost, 127.0.0.1, ::1; Squad HTTP bypasses the proxy

### Requirement: Machine inherit_env defaults

The machine settings file MAY declare `inherit_env` for `codex`, `cursor`, `kimi`, `zcode`, `devin`, and managed `opencode` as a list of strings or a comma-separated string; `judge` MUST reject the key as unsupported. For OpenCode, inheritance is machine-only, frozen for its owned process, and machine proxy_url wins over inherited proxies. For other CLI backends, the declared variables SHALL be copied from the server process into every child process of that backend as defaults below agent options: they union with the agent's own `options.inherit_env` (machine entries first, duplicates removed, agent entries preserved in order), an explicit agent `options.env` entry for the same variable still wins, and naming a variable in the union suppresses machine-injected values for it. For `kimi`, machine `inherit_env` — alone or together with other machine entries — selects the constrained child environment with the union applied.

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

- **WHEN** the machine file sets `inherit_env` under external `opencode` or `judge`
- **THEN** startup fails with an error naming the section, the unsupported key, and the keys that section supports

### Requirement: Precedence of machine settings

OpenCode process settings SHALL be machine-only; only external mode/base_url and per-agent request settings are permitted in squad YAML. Explicit agent options for other backends in squad YAML SHALL win over machine settings for the same key: an agent-specified `command`, `runtime_path`, or `base_url` SHALL be used unchanged, and the machine value SHALL apply only when the agent sets none. A machine-injected environment variable SHALL be suppressed for any variable the agent defines in `options.env` or names in `options.inherit_env`; those explicit choices SHALL reach the child process unchanged. Machine settings SHALL apply to agents dispatched for recovered workflow executions exactly as to freshly dispatched facilitator agents.

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

### Requirement: Machine settings values stay out of logs and artifacts

Proxy URLs from the machine settings file MUST NOT be written to server logs, run artifacts, persisted state, or validation errors, because they may embed credentials; diagnostics SHALL name at most the file, section, and key — never a value. Startup diagnostics SHALL report at most which backends have machine settings applied, without their values. Executable, runtime, and server locations (`command`, `runtime_path`, `base_url`) are operational provenance rather than secrets: they MAY appear in existing invocation diagnostics and launch errors, exactly as agent-configured values of the same kind already do.

#### Scenario: Proxy URL never appears in diagnostics

- **WHEN** the machine file sets `codex.proxy_url` containing userinfo, or a credentialed `proxy_url` fails validation
- **THEN** neither the URL nor any credential part appears in any error, log, run artifact, or persisted state; diagnostics name at most the file, section, key, and the backends for which machine settings were applied

#### Scenario: Executable path is operational provenance

- **WHEN** the machine file sets `cursor.command` and a cursor agent run is recorded
- **THEN** the existing invocation diagnostic may name the resolved executable, as it already does for agent-configured commands

### Requirement: OpenCode mode and migration validation
OpenCode mode SHALL default to managed, with command defaulting to opencode and snapshot to false. Only managed/external modes SHALL be valid. Managed mode with any explicit base_url SHALL fail with instructions to select external. External mode SHALL require base_url and reject process settings command, proxy_url, no_proxy and inherit_env, including explicitly empty declarations. Snapshot in external mode SHALL be a read-only expectation. Squad YAML SHALL reject process overrides command, proxy_url, no_proxy, snapshot, env and inherit_env rather than silently ignoring them. Managed mode SHALL reject nonempty per-agent username or password because Squad owns server authentication; external mode SHALL accept these credentials for HTTP Basic authentication.

#### Scenario: Existing endpoint needs explicit migration
- **WHEN** an old configuration specifies base_url without external mode
- **THEN** startup fails with actionable migration guidance without connecting or launching

#### Scenario: External process overrides fail
- **WHEN** external mode includes a machine proxy_url or a squad agent declares snapshot
- **THEN** configuration fails without modifying a server or disclosing values

#### Scenario: Default managed configuration
- **WHEN** no OpenCode machine settings exist
- **THEN** OpenCode agents use managed mode and require effective snapshot=false

#### Scenario: Managed authentication cannot be overridden
- **WHEN** a managed OpenCode agent specifies nonempty username or password
- **THEN** construction fails before launching or connecting, without disclosing credentials

#### Scenario: External authentication remains per agent
- **WHEN** an external OpenCode agent supplies username and password
- **THEN** requests use those Basic credentials; an omitted username defaults to opencode

### Requirement: Devin uses a constrained consistent environment
Devin SHALL use available HOME, PATH, TMPDIR, TMP, TEMP, XDG_CONFIG_HOME, XDG_DATA_HOME, XDG_CACHE_HOME and XDG_STATE_HOME baseline variables plus explicit machine/agent inheritance and explicit env entries. Explicit env SHALL take precedence; machine inheritance SHALL union under existing CLI rules. The effective environment SHALL always be explicit, including when empty, and identical across execution, installation and model discovery. Other ambient variables, including credential, proxy and model-fallback variables, SHALL NOT be inherited implicitly. Machine proxy_url/no_proxy SHALL map to HTTP_PROXY/HTTPS_PROXY/NO_PROXY below explicit agent choices; this mapping SHALL NOT claim to override native manual/off proxy settings, PAC or enterprise policy. No native configuration file SHALL be rewritten to impose machine defaults.

#### Scenario: Ambient credential is not opted in
- **WHEN** the server has WINDSURF_API_KEY and DEVIN_REFUSAL_FALLBACK but neither is explicitly inherited
- **THEN** neither enters the Devin child environment, while available baseline HOME and PATH remain available

#### Scenario: Empty environment cannot expand
- **WHEN** no baseline variable exists and no env entry is configured
- **THEN** the child receives an explicit empty environment rather than the full ambient environment

#### Scenario: Machine executable and agent override
- **WHEN** devin.command is set in backends.yaml and the agent explicitly selects another command
- **THEN** preflight, discovery and execution all use the agent command without fallback

#### Scenario: Native proxy policy wins
- **WHEN** machine proxy_url is mapped to environment variables but Devin's native policy selects a different proxy strategy
- **THEN** Squad leaves native policy intact and documentation does not promise the environment mapping overrides it
