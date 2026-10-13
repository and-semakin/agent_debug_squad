# Devin CLI backend

`backend: devin` runs Cognition's official **local** CLI through ACP v1. Install it using the [official instructions](https://docs.devin.ai/cli) and authenticate separately with `devin auth login`. Squad never runs installation, update or login commands. Local tools act in the Squad workspace; inference uses the native account and its billing/quota. This is not the cloud API or `acp --cloud`.

## Configuration

The agent accepts `command`, `model`, `yolo`, `env`, and `inherit_env`. It rejects reasoning, speed, sandbox, arbitrary arguments and cloud/permission-mode overrides. `model` must exactly match an offered ACP model value; aliases and fuzzy matching are not applied. Omission preserves the native default/saved model. Selection is checked before every prompt, including resumed conversations.

`yolo: true` selects ACP `bypass`; unexpected native approval requests still require an explicit decision. `yolo: false` requires native `normal` approval semantics and fails before prompting when unavailable. **The tested 3000.11.1 release offers Code (`accept-edits`), Ask, Plan and Bypass, but no Normal ACP mode. Consequently this release currently supports execution with `yolo: true` only.** Code automatically allows workspace edits and is deliberately not silently substituted for Normal. The example uses explicit `false` to make this limitation visible instead of silently opting into bypass. Neither setting certifies an OS sandbox.

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

Each operation uses a private mode-0600 semantic copy of the user config, retaining native account, proxy, permission and import policy while disabling auto-update and native subagents. Native files are not rewritten. Legacy embedded MCP configuration is rejected before launch because the CLI otherwise migrates it. Configured hooks, MCP and plugins are outside this initial supported profile. Known native and imported extension configuration paths are conservatively rejected, including disabled or empty files. Use a separately prepared compatible native environment; Squad does not delete or rewrite these settings to make a run pass. Organization-managed extensions are also outside the tested profile.

A new empty native session disappears when its process exits. `Init` therefore retains its process while Squad saves the allocated ID, then `Send` uses that same process for the first prompt. Preparation expires after 30 seconds and is closed on cancellation or pre-dispatch storage failure. A crash in this interval can leave a saved but unavailable ID; the next explicit turn fails and requires reset rather than silently replacing it.

After the first prompt, subsequent operations verify the exact saved ID against the canonical workspace and load it in a new process. History is excluded from output; startup instructions are included only before any historical user message. There is no latest-session lookup or automatic prompt replay. Workflow attempts/retries inherit Squad's existing independent runtime ownership. Reset preserves native history and defers replacement until checked work begins.

Run success requires `end_turn`, new assistant text, no pending permissions/tools, successful persistence and cleanup. A background tool still in progress fails the turn even if the model has answered. Cancellation sends native `session/cancel`, closes stdin, and reaps the owned process group and readers within a bounded cleanup budget. It does not reverse remote side effects or guarantee termination of malicious detached services after forced process death.

Only offered `once` and `reject` decisions are supported through the existing permission endpoint. `always`, unknown callbacks, authentication UI and questionnaires are unsupported. Native stderr and arbitrary metadata are not saved. Tool/progress events stream immediately; assistant text is assembled and scrubbed before being saved at completion or failure, so secrets split across wire chunks are not leaked by chunk logging. Partial output remains failed evidence.

## Model discovery

```sh
agent-debug-squad models --backend devin --workspace . --json
```

Discovery uses `models list --format json` without starting ACP or a conversation. It projects the observed `families[].variants[]` schema; unknown shapes are unsupported. Account catalog variants and ACP model options are different sets. Rows therefore report `selection.supported: false` with `requires_live_acp_model_option` and no fabricated YAML recipe. A manually configured exact ID still works when advertised and verified by the execution session (live-tested: `swe-2-high`). Thought-level/speed mappings are deferred. Hidden coverage, freshness, configuration and inference availability remain unknown unless independently established.

## Verification

Offline subprocess fixtures cover continuity, load replay, missing/foreign/locked sessions, persistence failure, permission decisions, cancellation, malformed/oversized streams, early EOF, stderr flooding, unrecognized callbacks, incomplete tools and isolated cleanup. Fixtures are synthetic and do not substitute for the live compatibility record in `openspec/changes/archive/2026-10-13-add-devin-acp-backend/design.md`.

The opt-in `SQUAD_DEVIN_LIVE=1 go test -run TestLiveLocalContinuity -v ./internal/adapters/devin` uses the operator's authenticated account and consumes quota. It checks session-free listing, two small inference turns, exact selection, continuation and unchanged native user configuration. Ordinary test runs skip it.
