## MODIFIED Requirements

### Requirement: Content-aware extension inspection
Devin profile preparation SHALL allow known configuration files containing only unrelated settings, metadata or empty extension collections. It SHALL reject active unsupported extensions with the source path and schema key, without exposing configuration values. Malformed or unreadable relevant sources SHALL fail with a source-specific diagnostic. Native legacy MCP migration guards SHALL remain enforced.

#### Scenario: Claude metadata exists
- **WHEN** ~/.claude.json contains metadata and project state without relevant MCP configuration
- **THEN** its existence does not block preparation and the file remains unchanged

#### Scenario: Imported extension is active
- **WHEN** an enabled import contains an active MCP server or unsupported Claude hook
- **THEN** preparation fails with the file path and relevant schema key, without emitting commands, credentials or custom names

#### Scenario: Disabled MCP server
- **WHEN** a known standard MCP entry has disabled true or an OpenCode entry has enabled false
- **THEN** that entry does not block preparation

#### Scenario: Malformed source
- **WHEN** a relevant external file contains invalid JSONC or is not a readable bounded regular file
- **THEN** preparation fails with its path and a classified error without raw content

#### Scenario: Claude plugin metadata does not establish imported extensions
- **WHEN** user or project Claude settings contain enabledPlugins entries with imports enabled
- **THEN** those entries alone do not block preparation, import policy and source files remain unchanged, and coexisting active hooks or MCP are still rejected

