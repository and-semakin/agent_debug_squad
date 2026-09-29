## Why

v0.19.0 shipped ZCode Start Plan support (commit `c567ad6`, change `harden-zcode-source-contracts`): the `plan_policy` option, start-first routing across the Z.AI Start/Individual account pair, guarded account reads, and a tightened fixed-mode gate that requires a fresh `https://api.z.ai/api/biz/subscription/list` read before every dispatched turn. On 2026-09-29 that gate broke a real squad run on another machine: in the PR-651 review bundle, `run_000008` (ZCodeReviewer, default `fixed` policy) failed in 2.5 s with `routing_individual_unknown` / "Individual entitlement could not be established" because the pre-dispatch subscription read failed with a non-auth error; no session was ever created. Before this change, fixed-mode dispatch had no network dependency on `api.z.ai` and ran on a unique readable credential plus cached availability. The plan-routing apparatus is the direct cause of the regression, its fixed-mode hardening was declared intentionally breaking in the implementing commit, and the Start Plan routing itself is no longer wanted.

## What Changes

- **BREAKING** Remove the `plan_policy` agent option entirely (both `fixed` and `start-first`). YAML or direct adapter configuration that sets `plan_policy` fails validation with a clear error naming the removed option; omission remains the only supported form and keeps the Individual default.
- **BREAKING** Remove `account:zai-start-plan` support and the Start/Individual "routing family": the provider option is again restricted to `account:zai-individual-coding-plan` (or omitted).
- Remove the network-bound half of the per-turn eligibility engine: Start balance reads, Individual subscription-list reads (the active-subscription gate), billing/subscription evidence decoding, busy rechecks, evidence sharing, and their HTTP budgets and retry policy. Keep the local selectability preflight: the exact provider/model/reasoning is still verified against the live registry view of the installed runtime before dispatch, with no network dependency. Fixed-mode dispatch requires only a unique readable Individual credential plus the entitled/current overlay.
- Remove the Start JWT / cross-plan identity binding from bootstrap; identity is again exactly one Individual Coding Plan credential key.
- Remove the Start-to-Individual quota-exhaustion continuation (one-shot fallback, `start_plan_quota_exhausted` classification, pre-continuation cleanup choreography) and the Start CAPTCHA guard branch; unsupported verification challenges fail like any other unsupported provider interaction.
- Remove `zcode.routing` diagnostics and all routing reason codes (`routing_start_available`, `routing_balance_unknown`, `routing_individual_unknown`, `continuation_budget_insufficient`, etc.). Existing wire/transport/cleanup diagnostics and the `zcode.models` diagnostic remain.
- Keep everything the plan work rode in with but that is not plan routing or subscription checking: the local registry-view model/reasoning selectability preflight, runtime wire compatibility probes, reverse-request servicing (preferences, provider auth binding, overlay), owned background work tracking and bounded cleanup, safe stderr provenance, redaction, permission scoping, session/turn semantics, and machine-backend proxy/env injection for the host process.
- Update README and the ZCode example to drop `plan_policy`/start-first documentation.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `zcode-backend`: restore pre-plan configuration and dispatch contracts. Rewrite "Explicit compatible configuration" (no `plan_policy`, Individual-only provider, explicit rejection of the removed option); reissue "Account authentication is identity bound" as "Account authentication is local and identity bound" (no entitlement gate, no Start JWT identity) and "Turn recovery never implies prompt replay" as "Turn recovery never replays prompts" (plan-switching clause dropped; OpenSpec 1.13 cannot drop scenarios from a MODIFIED requirement); reissue "Individual eligibility has explicit evidence" as "Model selection requires selectable local evidence" (subscription half dropped, local registry-view half kept); remove the requirements "Start verification limitations are explicit", "Model-specific Start-first routing", "Exhaustion during a turn does not replay external effects", "Plan policy configuration is validated before admission", "Eligibility freshness and waits have numeric bounds", and "Routing diagnostics and explicit retries are distinct".

## Impact

- Code: `internal/adapters/zcode/` loses `planpolicy.go`, `routing.go`, `continuation.go`, the network-read half of `host.cjs` (balance/subscription reads, proxy CONNECT/TLS client, app-version resolution, Start source headers, CAPTCHA branch) and the routing call sites in `zcode.go`; `evidence.go` and `hostbridge.go` shrink to the registry-view decode and bridge; `internal/config/zcode.go` flips from policy validation to option rejection; corresponding tests and fixtures (`testdata/billing/`, subscription-list and quota-continuation fixtures) are removed, with `testdata/registry/registry-view.json` kept for the selectability preflight.
- Configuration: squads using the documented default (no `plan_policy`, Individual provider) keep working; configs that set `plan_policy` now fail YAML validation with an actionable message.
- Behavior: ZCode runs no longer depend on `api.z.ai`/`zcode.z.ai` reachability before dispatch; start-first users lose automatic Start preference and Start-to-Individual continuation (Individual-only execution).
- Compatibility: run artifacts no longer contain `zcode.routing` diagnostics; no persisted state depends on them. Released v0.19.0 configs with `plan_policy: fixed` must drop the key.
