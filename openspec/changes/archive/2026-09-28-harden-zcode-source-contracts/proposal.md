## Why

The ZCode adapter currently supports only Z.AI Individual Coding Plan and relies on private functions discovered in the installed bundle. Published source can make its host responsibilities more reliable and enable account-plan selection that consumes an applicable Start Plan allowance before Individual quota, without replacing the installed ZCode runtime.

## What Changes

- Keep one ZCode backend, the installed CJS runtime and the existing Node/host launch contract. Do not add native/direct launch, personal API providers, integration modes or session-mode migration.
- Harden the existing account bridge, duplex protocol handling, permissions, owned turn correlation, background cleanup, diagnostics and recovery using inspected source contracts.
- Add opt-in `plan_policy: start-first` for Z.AI account plans. Omission remains `fixed` for existing configurations; fixed keeps the configured provider and current defaults. Start-first chooses the same requested model through Start Plan when an active, applicable allowance is spendable, otherwise Individual only on confirmed absence, expiry or exhaustion. It never substitutes Flash for another model.
- Obtain account-bound model entitlement and balance evidence using the source-defined read operations. Keep secrets in the host/native credential boundary; do not change desktop account selections or redeem/activate promotions.
- Recheck before each new Squad turn; new promotions and replenishment restore Start preference. After bounded read retries, unknown balance/auth/network failures block start-first work even if Individual inference is healthy; they do not silently authorize spending Individual quota.
- On a confirmed Start quota-exhaustion failure, finish the failed native turn, switch the same model/reasoning to Individual and automatically send one short continuation message in the same conversation. This is a new native turn within the existing Squad run, not a live model-step switch or replay of the original task. Unknown transport outcomes and other failures never trigger this continuation.
- Preserve installation as local, non-executing and credential-free, and preserve the common catalog's honest unsupported outcome until a full session-free effective catalog is verified. A plan-specific billing response is not a full backend model catalog.
- **BREAKING safety correction:** ambiguous/cross-account auth and unverified wire shapes previously accepted by accident fail closed. Fixed configuration syntax/default selection and session IDs remain supported, but fixed now rejects missing/unknown Individual entitlement, unavailable model/reasoning evidence or required wire methods; the old cache/key plus fabricated availability is no longer sufficient. Current-account cross-plan identity resolution is additionally required only by start-first.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `zcode-backend`: existing-host hardening and model-specific Start-first account routing, including uncertainty and in-flight exhaustion boundaries.
- `backend-installation`: consistent effective runtime/config path checks without adding account/balance checks to installation.
- `backend-model-catalog`: distinguish limited plan eligibility data from an externally verified full effective catalog.

## Impact

Affected implementation areas remain internal/adapters/zcode, its tests, option validation and README/examples, using existing preflight, diagnostics, process ownership and session persistence interfaces. No separate backend, runtime fork, copied credential cipher, private account data in artifacts, account mutation, auto-install/update or release. Team, BigModel and personal API providers remain out of scope.

Upstream baseline is 29628c9acdb81b703bbd4080c207a0e7ce5e276e (v3.14.3). See design.md for rollout and the remaining safe-continuation verification gate. This is planning only; no live account/billing request or inference was performed.
