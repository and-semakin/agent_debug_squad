# Model discovery compatibility evidence

The `models` command is a read-only catalog operation independent of agent
initialization. Backend listing is not an authentication, entitlement or inference
test. Compatibility is based on interface shape, not a promised version range.

## Observed interfaces (2026-09-27)

| Installed source | Observed result |
| --- | --- |
| Codex CLI 0.156.1 | `initialize`, `initialized`, paginated `model/list`; successful no-thread catalog |
| Cursor 2026.09.23-86fc751 | Text header `Available models`, `id - name` rows, optional `(default)` and a `Tip: use --model ...` footer; no verified JSON-list representation |
| Kimi 2.1.1 | `provider list --json`; configured alias map and camelCase metadata; successful projection |
| OpenCode 1.18.30 | Managed classic server; GET health/config/provider/config-providers without session/prompt/config-write routes; usable partial catalog after unsafe metadata was excluded |
| ZCode installed runtime family | Structural registry match; isolated bootstrap attempted a persistent write; safe catalog initialization remains unverified |

Smoke checks used temporary workspaces and a temporary machine/config context.
Codex, Cursor and Kimi received explicitly selected native HOME/PATH; managed
OpenCode used an isolated HOME. Only versions, statuses, counts and safe diagnostic
codes were inspected; live catalogs/credentials are not committed. Existing machine
settings failed strict configuration validation in an initial invocation, which
correctly returned a single exit-2 error envelope without raw configuration.
No inference was sent. Native auth/config files were not deliberately modified.

## Retired ZCode boundary (v0.24.0)

ZCode execution and discovery support were removed in v0.24.0. Earlier inspection
found registry bootstrap side effects and no verified read-only catalog. The private
bridge and research probe have now been deleted. `models --all` omits ZCode;
`models --backend zcode` fails with removal and migration guidance. The historical
observation above does not describe a currently supported interface.

## Verification map

- `internal/modeldiscovery`: target selectors, private context deduplication,
  environment precedence, bounded worker/queue budgets, all outcome states, error
  envelopes, cancellation, cleanup failure, refresh and factory construction.
- `internal/modelprobe`: byte/frame limits, descendant cancellation, safe process
  failures, credential projection, conflicting duplicate metadata and provenance.
- Backend `models_test.go` fixtures: Codex RPC/pagination/hidden scope; Cursor text
  grammar; Kimi aliases/secrets and exact execution argv; OpenCode scoped/authenticated
  GETs, HTTP failure/cancellation and owned versus external runtime lifecycle. Existing lifecycle/config tests continue to cover
  manual session continuity, installation resolution and arbitrary explicit models.
- Required acceptance: gofmt, `go vet ./...`, `go test -race -count=1 ./...`, and
  strict OpenSpec validation. No catalog lookup is added to execution admission.
