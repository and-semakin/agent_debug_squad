## 1. Shared contracts and configuration

- [x] 1.1 Update the implementation base to main containing eedfa95 and 88ec978 while preserving planning artifacts; verify commit ancestry and actual CheckInstallation/InstallationInput, preflight resolver, NewRuntime/Runtime.Adapter/ReadinessCheck/Close APIs. Use local stage-1 checks only and one command-owned runtime; add a narrow captured-environment constructor/helper where needed, not an assumed public lease API. Verify installation checks spawn no server and discovery creates/closes at most one managed owner.
- [x] 1.2 Add discovery input/result/source/model/selection/diagnostic DTOs in domain and an independent ModelLister factory covering every registered backend; verify compile-time coverage and tests that construction invokes no Init/Recover/session method.
- [x] 1.3 Implement config-free and optional-config target resolution using existing precedence, one machine/environment snapshot, selectors and workspace rules; verify tests for absent machine file, malformed config, agent overrides, constrained HOME/PATH, Kimi ambient exception, repeated backend filters and conflicting workspaces.
- [x] 1.4 Add private effective-configuration deduplication and deterministic public target origins; verify equivalent reviewers query once, distinct endpoints/auth/env/workspaces do not merge, and no private values/hashes reach JSON.

## 2. Aggregate discovery and CLI

- [x] 2.1 Add the bounded discovery service with per-target installation evidence and isolated failures; verify fake listers cover every result/aggregate status, usable empty results and successful rows retained beside errors; table tests must cover every installation/readiness code, coarse service_unavailable, typed auth/transport causes and caller-context precedence.
- [x] 2.2 Implement three-worker concurrency, worker-start target budgets and target-count-based overall budgets (max(60s, ceil(N/3)*(B+5s)+5s)), cancellation and owned cleanup; verify timed fixtures cover five/seven targets (75s/110s), custom B, overflow, explicit overall timeout shorter than B, queue-time accounting, queued cancellation, hanging subprocess descendants, HTTP cancellation, cleanup failure and external/shared server survival.
- [x] 2.3 Implement response/frame/total-byte/page/row limits, duplicate provenance merging and conflict handling; verify repeated cursors, oversize frames, duplicate identities and conflicting metadata produce deterministic partial/error results without false completeness.
- [x] 2.4 Add the models CLI flags, human output and schema_version 1 JSON renderer; verify command-level tests for all selection modes, one JSON document, exact exit-2 error envelopes/codes, deterministic human target blocks/partial summaries, escaped terminal controls, UTC timestamps, hidden_policy, delimiter-bearing target IDs, exits 0/1/2/130, no backend-error usage dump and unchanged existing command behavior.
- [x] 2.5 Implement allowlisted projection, safe diagnostics and terminal/credential sanitization without raw logging; verify sentinel secrets in provider objects, stderr, HTTP bodies, encoded proxy URLs, labels and unknown fields never appear in stdout/stderr or artifacts, including trace settings.
- [x] 2.6 Preserve source provenance, nullable evidence and upstream freshness without Squad caching; verify a second invocation queries again, stale timestamps stay stale, missing freshness remains unknown and failures never reuse prior catalogs.

## 3. Public CLI and server-backed listers

- [x] 3.1 Implement Codex's private stdio initialize/initialized/model-list lifecycle under the effective launch plan; verify protocol fixtures for multiple pages, hidden inclusion, differing catalog/model IDs, missing optional metadata, notifications, auth failure and zero thread/turn calls.
- [x] 3.2 Implement Cursor's verified text-list parser and capability failure handling; verify fixtures representing the observed CLI grammar, ANSI framing, default markers, opaque bracketed IDs, explicit empty framing, malformed mixed rows and incompatible formats, with no inferred reasoning from suffixes.
- [x] 3.3 Implement Kimi provider-list JSON projection and alias mapping; verify fixtures for aliases sharing a model, camelCase metadata, unknown fields, malformed JSON, unsupported older commands, absent efforts and secret-bearing provider configuration.
- [x] 3.4 Correct Kimi Send to forward explicit options.model through --model without changing omission behavior or silently rescuing stale aliases; verify captured argv tests preserve the exact alias and do not add unverified effort flags or expand the existing run protocol.
- [x] 3.5 Implement OpenCode classic provider/config-provider reads through the effective command-owned runtime/transport; verify HTTP fixtures for known/configured/connected distinctions, per-provider defaults, IDs containing slashes, workspace headers, auth, redirects, partial route support and incompatible v2-shaped responses.
- [x] 3.6 Verify OpenCode ownership integration with managed/external fixtures: managed starts/releases only its owned runtime, external never probes local CLI or adopts another endpoint, snapshot/proxy/auth policy comes from the shared runtime, and neither mode uses session/prompt/config-write routes.

## 4. ZCode boundary and synthetic backend

- [x] 4.1 Trace the identified registry.getView/createModelCatalogPort path through bootstrap in an isolated runtime harness with fixture-only account/config data and denied persistent writes; deliver a short compatibility result proving session-free read-only initialization and cleanup or documenting why the installed family remains unsupported. Do not test by invoking an agent tool or creating a conversation.
- [x] 4.2 Implement the ZCode lister outcome selected by that evidence: a separately gated read-only bridge if verified, otherwise explicit unsupported/read_only_catalog_unavailable; verify missing/ambiguous structures cannot load the bundle or touch credentials and successful fixtures retain truncation/disabled metadata plus selection.supported=false for unsupported execution choices.
- [x] 4.3 Implement the fixed fake synthetic catalog without calling the existing session initializer; verify deterministic output, no backend filesystem/network access and continued acceptance of arbitrary explicit fake model values.

## 5. Authoring documentation and end-to-end acceptance

- [x] 5.1 Update README and skills/agent-debug-squad/SKILL.md to discover before composing YAML, inspect partial JSON/nonzero exits, preserve exact options and resolve multiple backend/provider matches explicitly; verify examples cover standalone all-backend discovery, backend filtering, optional squad context and an illustrative returned Astra choice without hardcoded runtime lookup tables; include a shell example capturing exit 0/1 and inspecting status/results instead of &&, explicitly explaining --all with unsupported ZCode and shared exit 1 for partial/failed.
- [x] 5.2 Document the support matrix, observed rather than guaranteed version bounds, ZCode fallback, Kimi forwarding compatibility impact (stale previously ignored values can now fail; choose an alias or omit model)/unverified broader protocol, unknown inference availability, no Squad cache, repeat-command refresh, and deferred REST; verify documentation matches the final implemented output and CLI help.
- [x] 5.3 Add end-to-end fixtures proving discovery creates no squad/workflow/conversation state, sends no inference, does not mutate native auth/config and preserves manual session continuity; verify unknown explicit YAML models remain structurally accepted without discovery gates.
- [x] 5.4 Run opt-in no-inference smoke checks against installed supported interfaces under controlled effective configurations, including classic OpenCode 1.18.30 managed or explicitly configured external mode; record only sanitized outcomes/version evidence, never live credentials or catalogs. Verify unsupported sources remain explicit and do not silently pass.

## 6. Required verification before implementation handoff and archive

- [x] 6.1 Run gofmt on all changed Go files and verify no formatting diff remains.
- [x] 6.2 Run `go vet ./...` and resolve every reported issue; record the passing result.
- [x] 6.3 Run `go test -race -count=1 ./...` and resolve failures/races; record the passing result.
- [x] 6.4 Run `openspec validate discover-backend-model-catalog --strict` and check implementation against every requirement/scenario, including limits, source uncertainty, unsupported ZCode and integration ownership; keep artifacts/task checkboxes consistent with actual results before archive.

## Completion evidence

Implemented on fresh origin/main 1c444a2, containing eedfa95 and 88ec978.
See docs/model-discovery.md for the compatibility matrix and test coverage map.
All six factories are covered without initialization; existing lifecycle,
configuration, preflight and transport tests complement the new discovery tests.
The read-only ZCode bridge was deliberately not enabled: the isolated installed
runtime harness blocked a persistent write during registry_start. Successful
bridge-only selection/disabled/truncation cases are consequently not applicable;
the specified unsupported fallback and no-load boundary are implemented and tested.

No-inference smoke: Codex 0.156.1, Cursor 2026.09.23-86fc751 and Kimi 2.1.1
returned complete catalogs. Managed OpenCode 1.18.30 returned usable partial
results with unsafe_model_metadata, not false success. Raw live catalogs and
credentials were not saved. Documentation records exact selection handling,
Kimi compatibility, ZCode fallback and exit-1 scripting. Required gofmt, go vet,
full uncached race tests and strict change/spec validation passed before archive.
