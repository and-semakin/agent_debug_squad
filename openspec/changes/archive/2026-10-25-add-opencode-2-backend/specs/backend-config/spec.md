## MODIFIED Requirements

### Requirement: Strict validation of the machine settings file

The file SHALL accept only the backend sections `codex`, `cursor`, `kimi`, `opencode`, `opencode1`, `opencode2`, `zcode`, `devin`, and `judge`. Each section SHALL accept only its supported keys: `codex`, `kimi`, and `devin` accept `command`, `proxy_url`, `no_proxy`, and `inherit_env`; `cursor` accepts `command`, `proxy_url`, `no_proxy`, `ca_cert_file`, and `inherit_env`; `zcode` accepts `command`, `runtime_path`, `proxy_url`, `no_proxy`, `ca_cert_file`, and `inherit_env`; `opencode`, `opencode1`, and `opencode2` accept `mode`, `command`, `base_url`, `proxy_url`, `no_proxy`, `inherit_env`, and boolean `snapshot`; `judge` accepts `proxy_url` and `confidence_threshold`. Startup SHALL fail with an actionable error naming the file, the offending section or key, and the reason when the file contains an unknown backend section, a key the backend does not support, a `proxy_url` or `base_url` that is not a parseable URL with an `http` or `https` scheme and host, a value of the wrong type (non-string scalars for string-valued settings, non-string list items, or a non-boolean `opencode.snapshot`), a present-but-blank value, or more than one YAML document. An explicit empty string for a string-valued setting SHALL be syntactically valid and mean "unset"; mode-specific validation SHALL still reject forbidden key presence, including empty declarations. This exception does not permit an empty string for boolean `opencode.snapshot`. `judge.confidence_threshold` SHALL require an unquoted finite YAML number greater than 0 and at most 1. An empty file SHALL be valid and equivalent to a missing file.

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

## ADDED Requirements

### Requirement: Versioned OpenCode configuration identities
The system SHALL accept opencode1 and opencode2 as distinct backends and machine sections, display OpenCode 1 and OpenCode 2, and retain opencode as a v1 alias. Legacy saved state SHALL remain readable without identity rewriting. Declaring both opencode and opencode1 machine sections SHALL fail. V2 SHALL use independent settings and SHALL NOT inherit v1 credentials or session identities.

#### Scenario: Existing configuration
- **WHEN** an existing squad or saved workflow uses opencode
- **THEN** it retains v1 behavior and never selects v2 automatically

#### Scenario: Ambiguous machine settings
- **WHEN** both opencode and opencode1 sections are present
- **THEN** validation fails naming the conflicting sections without exposing values

#### Scenario: Both generations
- **WHEN** agents select opencode1 and opencode2
- **THEN** their machine settings and runtimes remain independent

### Requirement: OpenCode 2 settings parity
OpenCode 2 SHALL apply the existing OpenCode mode, process-setting precedence, inheritance, proxy, snapshot and privacy rules to its own settings. Both generations SHALL default command to opencode and reject a wrong major version before session creation. External mode SHALL validate the configured server without requiring a local executable.

#### Scenario: Upgraded default executable
- **WHEN** a v1 managed backend resolves a v2 executable
- **THEN** admission fails with instructions to select opencode2 or configure a v1 executable path

#### Scenario: External v2 server
- **WHEN** opencode2 selects external mode with no local executable
- **THEN** readiness depends on the configured v2 server, authentication and workspace checks
