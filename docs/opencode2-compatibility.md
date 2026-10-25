# OpenCode 1 and OpenCode 2

Squad exposes `opencode1` (OpenCode 1) and `opencode2` (OpenCode 2). The old
`opencode` identifier remains an alias for OpenCode 1, including saved runs and
workflow definitions. It never selects a protocol based on whichever binary
happens to be installed. `models --all` lists both generations; legacy model
selectors resolve to the canonical `opencode1` result.

Both upstream generations use the `opencode` executable name. Configure explicit
paths in `~/.agent-debug-squad/backends.yaml` when keeping both installations:

```yaml
opencode1:
  command: /absolute/path/to/opencode-v1
opencode2:
  command: /absolute/path/to/opencode-v2
  mode: managed
  snapshot: false
```

Declare only one of `opencode` and `opencode1`. The two generations have independent
runtime settings and processes. Native config locations are shared upstream;
use separate HOME/XDG config/data roots via machine `inherit_env` when necessary.
Squad does not install binaries, convert native configuration or migrate sessions
between generations. Existing v1 workflow identities remain unchanged.

Select v2 in squad or workflow agent configuration:

```yaml
backend: opencode2
options:
  model: provider/model
  agent: build
  yolo: false
```

Use IDs returned by `agent-debug-squad models --backend opencode2`. Model IDs
remain opaque, including slashes after the provider. Native variants are catalog
metadata; Squad does not yet expose a variant execution option.

## Feature parity

| Feature | OpenCode 2 behavior |
| --- | --- |
| Managed server | One authenticated, private loopback `serve` process per Squad; shared across agents and workflow attempts |
| External server | Explicit `mode: external`, `base_url`, optional per-agent `username`/`password`; no local executable required |
| Models and agents | Native selection at session creation and before each turn |
| Continuation | Saved session and canonical workspace verified; first-turn startup prompt retained |
| Streaming | Native `/api/event`; delivered input and assistant identity correlate each turn |
| Descendants | Verified parent links, nested activity, waiting phase and run-scoped permissions |
| Permission replies | `once` and `reject`, including optional messages; YOLO only auto-replies `once` |
| `always` | Rejected: v2 persists grants across the project, beyond the session scope of the Squad API |
| Questions/forms | No automatic answers, matching the previous adapter's question behavior |
| Cancellation/reset | Interrupt root and observed owned descendants, invalidate old approvals, preserve shared server |
| Model catalog | Native model/provider/default snapshot; access and connectivity remain unknown unless evidenced |
| Snapshots | Machine `snapshot` maps to native `snapshots`; last applicable document is verified before work |
| Environment/proxy | Baseline allowlist plus explicit inheritance, provider proxy settings, direct Squad HTTP, no redirects |

External servers must expose the same absolute workspace path. Configure native
`snapshots` explicitly to match the machine `snapshot` expectation, even when
false; missing or conflicting evidence fails before work. Squad does not patch
the server configuration. The v1-specific schema/TOML migration guard remains
limited to v1; v2 normalizes supported v1 settings in memory.

Events are live-only. A disconnected stream fails the turn and attempts bounded
interruption, retaining the session identity without resubmitting the prompt.
An interrupt acknowledgement does not prove arbitrary tool side effects stopped.
A session still executing cannot receive another Squad turn. A failed managed
startup or a dead owned server requires an explicit Squad restart.

## Verified contract

Verified against installed OpenCode **2.1.4** and upstream tag `v2.1.4`, commit
`68bc26b7da1990ce10ffecfbc1e09eb13aa5d824`:

- `serve --hostname 127.0.0.1 --port 0` announces `server listening on <url>`.
- `OPENCODE_SERVER_USERNAME` and `OPENCODE_SERVER_PASSWORD` enable Basic Auth;
  unauthenticated API calls return 401. The process is independent of the personal
  background service; Squad never calls service discovery/start/stop.
- `/api/info` supplies the version. `/api/config` returns configuration entries in
  ascending priority; `Config.latest(entries, "snapshots")` selects the last
  document that defines the value. Legacy `snapshot` normalizes to `snapshots`.
- Session creation returns `{data: Session.Info}` in 2.1.4, not the transfer object
  described by the newer online OpenAPI schema. Recovery reads `data.location`.
- Prompts return the admitted input ID; `session.inbox.delivered`,
  `session.step.started`, and `session.execution.*` identify turn progress.
  Final text is read by the exact observed assistant ID, avoiding pagination and
  historical-message ambiguity.
- Native permission replies use `decision`. `Permission.reply` calls
  `PermissionSaved.add` with a project ID for `always`; Squad therefore refuses it.
- Config startup normalizes in memory. The installed smoke test checks a
  schema-less native config remains byte-for-byte unchanged.

Protocol, lifecycle, permission, cancellation and catalog tests use local fixtures.
The opt-in installed-binary smoke creates isolated HOME/XDG directories and a
local fake OpenAI-compatible provider, checks creation/recovery/catalog and two
real OpenCode turns, then removes its isolated session and stops its server:

```sh
SQUAD_OPENCODE2_SMOKE=1 go test ./internal/adapters/opencode2 -run TestInstalledOpenCode2Smoke -count=1 -v
```

No paid provider inference or real account authentication is exercised by this
test. Live external deployments and future v2 minor releases may have different
contracts; unsupported responses fail explicitly.

Primary sources: [migration guide](https://opencode.ai/v2/docs/migrate-v1),
[client and live events](https://opencode.ai/v2/docs/build/client/),
[pinned permission implementation](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/core/src/permission.ts),
[pinned server handlers](https://github.com/anomalyco/opencode/tree/v2.1.4/packages/server/src/handlers).
