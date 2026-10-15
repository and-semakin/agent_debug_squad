# Design

## Context
profile.go creates a private semantic copy of native user configuration, then extensions.go scans native/imported/project sources and refuses unsupported features. The user chooses native behavior instead of these restrictions.

## Goals / Non-Goals
Remove compatibility refusals. Do not replace native import resolution, edit real settings, change ACP/model checks or add a bypass option.

## Decisions
Delete extensions.go and its refusal tests. Remove the legacy mcpServers guard. Keep bounded JSONC reading of the user config because it is needed to create a private launch copy; project/imported settings are not read by Squad. Retain auto_update=false and subagents_enabled=false in that copy. The unchanged ACP path propagates native startup failures through safe errors. Add tests proving extension config reaches the private copy unchanged, malformed external files do not cause Squad preflight errors, and native config cannot be copied when malformed.

## Risks / Trade-offs
Native hooks/MCP/plugins may run subprocesses, contact services or migrate settings. Owned process-group cleanup remains in place but cannot guarantee isolation or reversal of arbitrary extension effects. Document this native responsibility without imposing another opt-in. No settings or session migration is needed.
