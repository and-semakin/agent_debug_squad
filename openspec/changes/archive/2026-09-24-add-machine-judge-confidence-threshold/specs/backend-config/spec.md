## MODIFIED Requirements

### Requirement: Strict validation of the machine settings file

The file SHALL accept only the backend sections `codex`, `cursor`, `kimi`, `opencode`, `zcode`, and `judge`. Each section SHALL accept only its supported keys: `codex` and `kimi` accept `command`, `proxy_url`, `no_proxy`, and `inherit_env`; `cursor` accepts `command`, `proxy_url`, `no_proxy`, `ca_cert_file`, and `inherit_env`; `zcode` accepts `command`, `runtime_path`, `proxy_url`, `no_proxy`, `ca_cert_file`, and `inherit_env`; `opencode` accepts `base_url` only; `judge` accepts `proxy_url` and `confidence_threshold`. Startup SHALL fail with an actionable error naming the file, the offending section or key, and the reason when the file contains an unknown backend section, a key the backend does not support, a `proxy_url` or `base_url` that is not a parseable URL with an `http` or `https` scheme and host, a value of the wrong type (non-string scalars such as booleans, integers, or null; non-string list items), a present-but-blank value, or more than one YAML document. An explicit empty string SHALL be valid and mean "unset" for string settings; `judge.confidence_threshold` SHALL instead require an unquoted finite YAML number greater than 0 and at most 1. An empty file SHALL be valid and equivalent to a missing file.

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

#### Scenario: Invalid machine threshold fails startup

- **WHEN** `judge.confidence_threshold` is zero, negative, greater than one, null, quoted text, NaN, or infinity
- **THEN** startup fails with an error naming the file, `judge.confidence_threshold`, and its allowed numeric range

#### Scenario: Unsupported threshold placement fails startup

- **WHEN** `confidence_threshold` appears in a backend section other than `judge`
- **THEN** startup fails with an unsupported-key error for that section


## ADDED Requirements

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
