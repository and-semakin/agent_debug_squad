# Design

## Context
The guard in extensions.go conflates Claude plugin-format compatibility with importing Claude installed_plugins.json/ enabledPlugins. Supplied macOS ACP evidence for Devin 3000.11.3 shows exact SWE-2 completion with enabled gopls-lsp and a synthetic plugin, no observed plugin components, and a working ordinary Claude skill control gated by read_config_from.claude.

## Goals / Non-Goals
Allow unrelated Claude plugin metadata while preserving rules/skills imports. Do not implement plugin execution, change native settings, or alter process ownership/session lifecycle.

## Decisions
Remove enabledPlugins from checked Claude fields and its dead special handling. Check hooks/MCP exactly as before. Change precedence tests to use actual hooks rather than plugin metadata. Test user and project Claude files with imports enabled, unchanged source/settings, and coexisting real blockers. Document evidence limits rather than promising that every future Devin version ignores Claude registries.

## Risks / Trade-offs
Native imports may evolve; revisit on concrete evidence of registry loading. The supplied experiments lack system file/process tracing and are not universal proof. Retaining the unsupported metadata guard would impose known false rejections and broad opt-outs today. Native Devin plugins and hook/MCP restrictions remain independently covered.

## Migration Plan
Upgrade the Squad binary. No settings migration is required. Users who disabled imports only for enabledPlugins can review and remove that override; active hooks/MCP still require separate handling.
