## ADDED Requirements

### Requirement: Content-aware extension inspection
Devin profile preparation SHALL allow known configuration files containing only unrelated settings, metadata or empty extension collections. It SHALL reject active unsupported extensions with the source path and schema key, without exposing configuration values. Malformed or unreadable relevant sources SHALL fail with a source-specific diagnostic. Native legacy MCP migration guards SHALL remain enforced.

#### Scenario: Claude metadata exists
- **WHEN** ~/.claude.json contains metadata and project state without relevant MCP configuration
- **THEN** its existence does not block preparation and the file remains unchanged

#### Scenario: Imported extension is active
- **WHEN** an enabled import contains an active MCP server or unsupported Claude hook/plugin
- **THEN** preparation fails with the file path and relevant schema key, without emitting commands, credentials or custom names

#### Scenario: Disabled MCP server
- **WHEN** a known standard MCP entry has disabled true or an OpenCode entry has enabled false
- **THEN** that entry does not block preparation

#### Scenario: Malformed source
- **WHEN** a relevant external file contains invalid JSONC or is not a readable bounded regular file
- **THEN** preparation fails with its path and a classified error without raw content

### Requirement: Import-aware extension inspection
External extension checks SHALL respect effective read_config_from switches, defaulting to enabled and applying ancestor project and local overrides after user settings. Disabled sources SHALL not block preparation. Squad SHALL preserve native import policy and SHALL not disable imports automatically to make a run pass.

#### Scenario: Disabled external source
- **WHEN** effective read_config_from.claude is false and Claude configuration contains extensions
- **THEN** those external files do not block preparation and the disabled import policy is preserved

#### Scenario: Project re-enables import
- **WHEN** user configuration disables an import and a higher-priority project/local setting enables it
- **THEN** active extensions from that source are checked and rejected before launch
