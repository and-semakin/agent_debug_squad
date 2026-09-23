# verdict-judge Delta

## MODIFIED Requirements

### Requirement: Judge configuration and startup gating

The system SHALL accept an optional `judge` configuration section with `provider`, `model`, `api_key_file`, `proxy_url`, and a decision timeout. The `openrouter` provider SHALL authenticate every decision request with the key read from the configured file, whose default location is `~/.agent-debug-squad/openrouter-api-key` in the user's home directory, outside the workspace. Decision requests SHALL be sent through the configured HTTP proxy when one is set; without one, standard environment proxy behavior applies. The effective proxy SHALL resolve in order: a `proxy_url` set in the session YAML `judge` section wins; when it is empty, a `proxy_url` set for `judge` in the machine backend configuration file applies; otherwise no explicit proxy is configured. The default model SHALL be `~typesafe/jev-latest`, overridable to an exact version. When the configured workflow contains tasks declaring verdicts, or a `judge` section is present, the key file MUST exist and be readable at startup; otherwise startup SHALL fail with an actionable error naming the expected path. Without verdict tasks and without a `judge` section, the system SHALL start and operate without any judge dependency.

#### Scenario: Missing key with verdict tasks

- **WHEN** the configured workflow declares a task with verdicts and the OpenRouter key file is absent
- **THEN** startup fails with an error naming the expected key file path, and no server starts

#### Scenario: No judge dependency without verdicts

- **WHEN** the configuration has no `judge` section and no task declares verdicts
- **THEN** the server starts and serves squads and workflows without requiring an OpenRouter key

#### Scenario: Key file override

- **WHEN** `api_key_file` points to a readable file in a nondefault location
- **THEN** the judge authenticates with that file's key and startup succeeds

#### Scenario: Proxy is applied to decision requests

- **WHEN** `proxy_url` is configured
- **THEN** decision requests are sent through that proxy

#### Scenario: Machine-level judge proxy default applies

- **WHEN** the session YAML `judge` section sets no `proxy_url` and the machine backend configuration sets `judge.proxy_url`
- **THEN** decision requests are sent through the machine-configured proxy

#### Scenario: Session judge proxy wins over the machine default

- **WHEN** the session YAML `judge` section sets `proxy_url` and the machine backend configuration also sets `judge.proxy_url` to a different URL
- **THEN** decision requests are sent through the session YAML proxy
