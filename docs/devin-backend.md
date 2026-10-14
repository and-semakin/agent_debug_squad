# Devin CLI backend

`backend: devin` runs Cognition's official **local** CLI through ACP v1. Install it using the [official instructions](https://docs.devin.ai/cli) and authenticate separately with `devin auth login`. Squad never runs installation, update or login commands. Local tools act in the Squad workspace; inference uses the native account and its billing/quota. This is not the cloud API or `acp --cloud`.

## Configuration

Every configured agent must provide a nonblank exact ACP `model` ID. The agent accepts `command`, `model`, `reasoning`, `speed`, `mode`, `yolo`, `env`, and `inherit_env`. It rejects sandbox, arbitrary arguments and cloud overrides. Model aliases and catalog variant suffixes are not rewritten. Selection is checked before every prompt, including resumed conversations.

| Option | Default and behavior |
| --- | --- |
| `model` | Required exact advertised ACP model value; live-tested `swe-2-high` and `gpt-6-sol-medium` |
| `reasoning` | Prefer `high` when offered; otherwise use the advertised current level. Without a reasoning selector, omission is allowed but explicit reasoning fails |
| `speed` | `standard`; `fast` requires an advertised speed selector. Models without it accept only standard |
| `mode` | `bypass`; supported names are `normal`, `accept-edits`, `ask`, `plan`, `smart`, `bypass`, subject to native availability |
| `yolo` | Defaults to true under Squad defaults. Without mode, true selects bypass and false selects normal |
| `command` | `devin`, resolved from the explicit child PATH |
| `env`, `inherit_env` | Explicit environment values and inheritance, described below |

An explicit `mode` wins over an implicit/inherited yolo default. If both are explicit, `yolo: true` requires bypass and `yolo: false` requires a non-bypass mode. Contradictory choices fail configuration. Existing squad-wide `defaults.yolo` remains effective when mode is absent. The tested CLI offers no Normal ACP mode, so `yolo: false` without mode fails before prompting. Use explicit `mode: accept-edits` for Code (automatic workspace edits), or ask/plan for those native behaviors. Neither modes nor yolo certify an OS sandbox. Unexpected native permission requests still require an explicit decision.

Model selection runs first; each subsequent control uses refreshed model-specific choices. After applying reasoning, speed and mode, the adapter verifies all selected values together. Unsupported, ignored or reset controls prevent prompt submission. Missing reasoning/speed controls never cause suffix-derived model selection or silent Fast fallback.

Machine-specific configuration belongs in `~/.agent-debug-squad/backends.yaml`:

```yaml
devin:
  command: /opt/bin/devin
  proxy_url: http://127.0.0.1:7890
  no_proxy: localhost,127.0.0.1
```

Or explicitly inherit the operator's proxy variables:

```yaml
devin:
  inherit_env: [HTTP_PROXY, HTTPS_PROXY, ALL_PROXY, NO_PROXY]
```

Proxy values become child environment defaults, with existing agent overrides taking precedence. This does not override native manual/off proxy settings or enterprise policy. Tests succeeded with explicitly inherited proxies; direct connectivity was not established. No real proxy addresses or credentials belong in committed YAML.

The environment always includes only available HOME, PATH, TMPDIR/TMP/TEMP and XDG config/data/cache/state roots plus explicit inheritance/entries. Ambient credentials, proxy settings and refusal-fallback controls are not implicitly inherited. `DEVIN_MODEL`, `DEVIN_PERMISSION_MODE`, `DEVIN_SANDBOX`, and `DEVIN_REFUSAL_FALLBACK` overrides are rejected to preserve the checked contract.

## Lifecycle and supported profile

Compatibility was checked on macOS with `devin 3000.11.1 (cc4e349ca55e)` on 2026-10-13. Native installation preflight only inspects the executable; successful preflight does not establish login, model access or protocol support. Runtime negotiation checks ACP v1, explicit load and list support. Linux uses the same POSIX ownership implementation; native Linux inference was not tested.

Each operation uses a private mode-0600 semantic copy of the user config, retaining native account, proxy, permission and import policy while disabling auto-update and native subagents. Native files are not rewritten. Legacy embedded MCP configuration is rejected before launch because the CLI otherwise migrates it. Configured hooks, MCP and plugins are outside this initial supported profile. Known JSON configuration files are inspected by content: unrelated metadata, empty collections and explicitly disabled MCP entries do not block launch. External sources are checked only when their effective `read_config_from` switch is enabled (the native default); project and local overrides take precedence over user settings. Errors identify the source path and schema key without printing values. Invalid or unreadable relevant files fail inspection. Nonempty native plugin directories remain unsupported.

For example, a metadata-only `~/.claude.json` is allowed. Active Claude hooks/plugins or imported MCP servers are rejected. OpenCode's own `plugin` list is not a documented Devin import and does not block launch by itself; its active `mcp` entries do. To intentionally opt out of Claude and OpenCode imports, merge the following into your native Devin configuration (user `~/.config/devin/config.json` or project `.devin/config.local.json`):

```json
{"read_config_from": {"claude": false, "opencode": false}}
```

This also disables other imports from those tools, such as Claude rules and skills. Squad preserves the policy you choose and does not disable imports or rewrite settings automatically. See the official [import reference](https://docs.devin.ai/cli/reference/configuration/read-config-from) and [precedence reference](https://docs.devin.ai/cli/reference/configuration/global-vs-local). These startup checks cover known local schemas, not every future extension loader or remote policy. Organization-managed extensions are also outside the tested profile.

A new empty native session disappears when its process exits. `Init` therefore retains its process while Squad saves the allocated ID, then `Send` uses that same process for the first prompt. Preparation expires after 30 seconds and is closed on cancellation or pre-dispatch storage failure. A crash in this interval can leave a saved but unavailable ID; the next explicit turn fails and requires reset rather than silently replacing it.

After the first prompt, subsequent operations verify the exact saved ID against the canonical workspace and load it in a new process. History is excluded from output; startup instructions are included only before any historical user message. There is no latest-session lookup or automatic prompt replay. Workflow attempts/retries inherit Squad's existing independent runtime ownership. Reset preserves native history and defers replacement until checked work begins.

Run success requires `end_turn`, new assistant text, no pending permissions/tools, successful persistence and cleanup. A background tool still in progress fails the turn even if the model has answered. Cancellation sends native `session/cancel`, closes stdin, and reaps the owned process group and readers within a bounded cleanup budget. It does not reverse remote side effects or guarantee termination of malicious detached services after forced process death.

Only offered `once` and `reject` decisions are supported through the existing permission endpoint. `always`, unknown callbacks, authentication UI and questionnaires are unsupported. Native stderr and arbitrary metadata are not saved. Tool/progress events stream immediately; assistant text is assembled and scrubbed before being saved at completion or failure, so secrets split across wire chunks are not leaked by chunk logging. Partial output remains failed evidence.

## Model discovery

```sh
agent-debug-squad models --backend devin --workspace . --json
```

Discovery uses `models list --format json` without starting ACP or a conversation. It projects the observed `families[].variants[]` schema; unknown shapes are unsupported. Account catalog variants and ACP model options are different sets. Rows therefore report `selection.supported: false` with `requires_live_acp_model_option` and no fabricated YAML recipe. A manually configured exact ID still works when advertised and verified by the execution session (live-tested: `swe-2-high`). Execution applies reasoning and speed through live ACP controls; catalog variant IDs still do not supply verified executable recipes. Hidden coverage, freshness, configuration and inference availability remain unknown unless independently established.

## Verification

Offline subprocess fixtures cover continuity, load replay, missing/foreign/locked sessions, persistence failure, permission decisions, cancellation, malformed/oversized streams, early EOF, stderr flooding, unrecognized callbacks, incomplete tools and isolated cleanup. Fixtures are synthetic and do not substitute for the live compatibility record in `openspec/changes/archive/2026-10-13-add-devin-acp-backend/design.md`.

The opt-in `SQUAD_DEVIN_LIVE=1 go test -run TestLiveLocalContinuity -v ./internal/adapters/devin` uses the operator's authenticated account and consumes quota. It checks session-free listing, two small inference turns, exact selection, continuation and unchanged native user configuration. Ordinary test runs skip it.
