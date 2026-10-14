# Design

## Context
See proposal.md. profile.go currently rejects every known external JSON path via Stat. Existing native settings must remain unchanged. Official import documentation describes per-tool default-true read_config_from switches; project/local settings override user settings.

## Goals / Non-Goals
Goals: bounded JSONC inspection, harmless metadata acceptance, effective import switches, safe source/key diagnostics.
Non-goals: launching extensions, sandboxing, rewriting user settings, changing authentication, session or process cleanup contracts.

## Decisions
- Retain the private semantic profile; inspect JSON with the existing bounded reader and HUJSON parser. Reject malformed relevant sources with a path but no parser excerpts.
- Resolve read_config_from per tool from user then ancestor project/local configs toward the workspace. Inspect all native sources regardless of import policy. Skip disabled external tool sources; preserve all native import policy in the launch profile.
- Check schema fields: mcpServers (standard), mcp (OpenCode), context_servers (Zed), hooks and enabledPlugins (Claude), requiredPlugins/optionalPlugins (native). OpenCode plugin entries are not documented as imported, so they do not block Devin by themselves. Standard disabled:true and OpenCode enabled:false servers do not block. Unknown nonempty field shapes fail conservatively.
- Check Claude project state only for the current workspace and its ancestors. Do not include arbitrary server/plugin names or values in errors; report static schema key paths.
- Empty extension collections pass. Native plugin directories still conservatively fail when nonempty; full plugin resolution is outside scope. Legacy native mcpServers remains rejected even when empty because startup migration can write files.
- Keep ancestor scanning and conservative channel scanning already used by this adapter. This is a startup compatibility guard, not an isolation boundary or comprehensive emulator of all future native extension loaders.

## Risks / Trade-offs
- Native import behavior can evolve → document sources and limitations, cover regression fixtures, preserve failure for uncertain extension shapes.
- Project configuration can re-enable imports → resolve overrides before external inspection and test both directions.
- Errors can leak secrets → report only known key names and quoted source paths, never configuration values or underlying parser/read errors.

## Migration Plan
No user migration required. Existing configs remain byte-identical. Deploy through the normal build; rollback restores the overly restrictive guard.

## References
- https://docs.devin.ai/cli/reference/configuration/read-config-from
- https://docs.devin.ai/cli/reference/configuration/global-vs-local
