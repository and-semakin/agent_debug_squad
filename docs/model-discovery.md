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

## ZCode boundary

The installed bundle contains `createModelCatalogPort`, which maps
`registry.getView().providers` into model records. This is not a public `list_models`
API. The registry factory `startProcessProviderRegistryRuntime` constructs and
starts configuration repositories before a registry view is available. The existing
execution bridge also reads account files, acquires credentials and then starts
an app-server conversation path; that lifecycle is inappropriate for discovery.

The opt-in `scripts/probe-zcode-model-bootstrap.cjs` harness patches out CLI
autorun only after the existing structural checks, uses fixture configuration and
an isolated HOME, blocks filesystem writes, networking, subprocess creation and
native storage module loading, and has a bounded lifetime. On the observed family,
it reached `registry_start` and was stopped by `persistent_write`. It printed only
`{"stage":"registry_start","blocked":"persistent_write","verified":false}`.
This establishes a blocking bootstrap side effect, not a working catalog bridge.
No account credentials, agent tools or conversations were used.

Production discovery therefore returns
`unsupported/read_only_catalog_unavailable`. It never loads this bundle to obtain
a catalog, and never substitutes static provider files or historical diagnostics.
The harness is an optional research tool, not part of normal discovery or tests.
Tests separately verify structural inspection under write denial and that the
unsupported lister cannot execute even a deliberately side-effecting bundle.

## Verification map

- `internal/modeldiscovery`: target selectors, private context deduplication,
  environment precedence, bounded worker/queue budgets, all outcome states, error
  envelopes, cancellation, cleanup failure, refresh and factory construction.
- `internal/modelprobe`: byte/frame limits, descendant cancellation, safe process
  failures, credential projection, conflicting duplicate metadata and provenance.
- Backend `models_test.go` fixtures: Codex RPC/pagination/hidden scope; Cursor text
  grammar; Kimi aliases/secrets and exact execution argv; OpenCode scoped/authenticated
  GETs, HTTP failure/cancellation and owned versus external runtime lifecycle;
  ZCode unsupported boundary. Existing lifecycle/config tests continue to cover
  manual session continuity, installation resolution and arbitrary explicit models.
- Required acceptance: gofmt, `go vet ./...`, `go test -race -count=1 ./...`, and
  strict OpenSpec validation. No catalog lookup is added to execution admission.
