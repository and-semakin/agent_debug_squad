## Why

The released executable can update itself while its manually copied operating skill stays stale. A facilitator may then follow instructions for a different program version, and there is no reliable ownership record that makes replacing an existing skill safe.

## What Changes

- Embed the complete portable `skills/agent-debug-squad` package in every binary, with deterministic content identity and the existing build version metadata.
- Add explicit `skill install`, read-only `skill status`, and offline `skill sync` commands with agent presets and exact custom destinations. Register only installations explicitly selected by the caller; backend membership is not installation consent.
- Support local Codex, Claude Code, Cursor, OpenCode, ZCode and current Kimi Code, with a distinct legacy Kimi preset. Resolve documented directories and overrides, reuse eligible registered copies, and diagnose duplicates without rewriting unrelated copies.
- Maintain a private per-user registry, per-file ownership hashes, backups and recoverable per-destination transactions. Preserve unknown files, back up changed managed files before replacement, and require explicit adoption of manual copies.
- Synchronize registered skills using the newly installed executable after self-update; repair missing/stale files even when no binary upgrade is available. Report binary and destination outcomes separately, including the instruction to reread the updated skill.
- Preserve `serve --no-auto-update` and its environment equivalent as a complete automatic-mutation opt-out. Explicit offline sync remains available. Development and older binaries cannot silently replace a newer managed package.

## Capabilities

### New Capabilities

- `bundled-skill-management`: Portable bundle identity, explicit target selection and adoption, managed ownership, transactional reconciliation, status and diagnostics.
- `bundled-skill-update-integration`: Explicit update and automatic serve integration, new-executable handoff, repair, opt-outs and version authority.

### Modified Capabilities

None. Backend preflight, backend configuration, model discovery and workflow semantics remain unchanged. In particular, `run` does not update or re-execute itself, and the existing skill's model-discovery and reviewer-quorum instructions remain requirements.

## Impact

Implementation will touch `cmd/agent-debug-squad`, `internal/selfupdate`, a new internal skill-management package and an embeddable package adjacent to the canonical `skills/` tree, CLI tests, README and release/build checks. It adds private user state under `~/.agent-debug-squad/skill-management/`; it does not change squad YAML, backend settings, HTTP responses or workspace run artifacts. No external package manager, marketplace publishing, backend session, model request, agent configuration rewrite or remote installation is introduced. Distribution remains the existing macOS/Linux binary archives; native Windows and universal plugin lifecycle management are outside this change. This proposal contains planning artifacts only.
