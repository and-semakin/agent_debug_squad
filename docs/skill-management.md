# Skill installation compatibility and verification

The installer delivers a portable directory with `name` and `description` frontmatter. It does not modify host settings, enable disabled skills, reload conversations, install cloud copies, or infer the facilitator from worker backends. Filesystem success does not certify that a host currently selects that copy.

## Evidence and host checklist

The initial read-only inventory on 2026-09-28 found Codex CLI 0.156.1, Claude Code 2.1.228, Cursor Agent 2026.09.23-86fc751, OpenCode 1.18.30, Kimi Code 2.1.1 and ZCode 3.14.3. These are evidence of installed generations, not an exact version allowlist or a claim that their live skill selectors were tested. No real user skill installation is changed by the automated test suite.

| Host | Evidence | Discovery check after an authorized installation |
| --- | --- | --- |
| Codex | [Official skills documentation](https://learn.chatgpt.com/docs/build-skills) documents `.agents/skills`, duplicate selectors and symlink support. Squad deliberately installs regular copies. | Inspect the skill selector and absolute path; check for legacy `.codex/skills` and project duplicates. Refresh/restart if required. |
| Claude Code | [Skills](https://code.claude.com/docs/en/skills) and [configuration directory](https://code.claude.com/docs/en/claude-directory) describe the personal root and CLAUDE_CONFIG_DIR. | Use the same profile environment as installation; verify the slash command and whether a plugin/project copy shadows it. |
| Cursor | [Skills](https://cursor.com/docs/skills) lists `.agents`, `.cursor` and compatible roots; cloud sync is separate. | Inspect local skill selection; do not infer cloud or SSH availability. |
| OpenCode | [Skills](https://opencode.ai/docs/skills/), [configuration](https://opencode.ai/docs/config/), and the [v1.18.30 XDG resolver](https://github.com/anomalyco/opencode/blob/3104c1428ec91f809e5ab86631300de41eb6952e/packages/core/src/global.ts). | Verify the effective config root and external-skill disabling settings; multiple roots can expose duplicates. |
| ZCode | [Official skills](https://zcode.z.ai/en/docs/skill) documents `.zcode/skills` and import options. | Refresh Settings → Skills and verify source path and enabled state. Existing imported symlinks are not silently adopted. |
| Current Kimi Code | [Current skills](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/skills.html) describes KIMI_CODE_HOME and `.agents`. | Confirm the 2.x product/profile and start a session that discovers the new skill. |
| Legacy kimi-cli | [Upstream skills](https://moonshotai.github.io/kimi-cli/en/customization/skills.html) describes version/config-sensitive brand and generic groups. | Select the legacy preset explicitly; inspect which configured group wins rather than relying on the executable name. |

For other hosts, including a host usable as a Squad worker, use an explicitly verified custom directory. No generic “backend means skill directory” mapping exists. Native Windows installation is not shipped; WSL is a Linux environment with its own home and filesystem. A custom path states where to deliver files, not how an unverified host discovers them.

## Recovery and trust boundaries

The registry schema and tree hash are versioned. `sha256-v1` hashes the domain `agent-debug-squad/skill/sha256-v1` followed by a NUL byte and sorted records: big-endian uint64 UTF-8 path length, path bytes, uint64 exact content length, one executable byte (0/1), and the raw 32-byte SHA-256 digest. Directory timestamps and modes are not payload identity. `skills/executable-files.json` preserves executable intent that `embed.FS` does not carry; source tests detect manifest drift.

Each managed tree has a receipt tied to the private registry; a receipt alone is not ownership. Do not reconstruct lost/corrupt registry state by guessing. Restore verified state or explicitly adopt a safe detached manual copy. Unknown registry schemas fail without replacement. When recovery reports conflicting external edits, retain the journal, live directory, private staging tree and backup archive for inspection; do not delete evidence just to force a retry.

Mutations use descriptor-relative no-follow access, canonical paths and advisory locks. Symlink descendants, hard links, special files, traversal, unsafe writable mutation parents, overlaps and plugin-controlled roots are rejected. This coordinates Squad writers and detects external edits; it does not defend against a hostile process running with the same user credentials. A directory exchange publishes the old or new tree at its pathname; a host reading multiple files across the exchange may still hold an old file descriptor. There is no global atomic transaction across the executable and several destinations.

Backups are private compressed archives rather than loose discoverable SKILL.md copies. Archive bytes and extracted file identities are verified before destructive replacement. Keep local customizations outside the managed payload when possible. A backup failure leaves the live tree unchanged. Successful destinations remain committed when another path fails.

## Repeatable verification

- `go test ./internal/skillmanager`: bundle/source identity, portable frontmatter, all resolver overrides, sharing/ambiguity, ownership/adoption, unknown files, backups, downgrade/dev, locking, filesystem boundaries, cancellation and crash recovery.
- `go test ./internal/selfupdate`: network-failure repair, new-image synchronization, partial/uncertain outcomes, serialization, structured child reports and a real compiled release-image handoff with a different old bundle.
- `go test ./cmd/agent-debug-squad`: real isolated-home CLI, JSON/exit codes, dev and automatic opt-outs, failed re-exec, and workflow subprocess regressions.
- CI runs native exchange/recovery tests on both Linux and macOS. Cross-build verification covers the four shipped OS/architecture combinations with CGO disabled. Actual host selector checks above remain a documented manual compatibility check, not an automated model session or login.
