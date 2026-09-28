Implementation review order: milestone A (sections 1 and 3 plus task 4.3), then B (tasks 2.1–2.5, 4.1–4.2), then C (tasks 2.6–2.8 and remaining checks). Review each milestone with its tests before dependent work; one final archive covers the coherent feature.

## 1. Account and protocol contracts

- [x] 1.1 Add source-derived synthetic protocol fixtures with upstream commit/path provenance; verify legacy wire identity, required methods, preference/permission/auth requests, terminal events and additive-field tolerance without mixing v4/MCP contracts.
- [x] 1.2 Add synthetic Start balance fixtures for model capabilities, active/pending/expired offers, multiple plan instances, server time and separate available/remaining/reserved amounts; verify unknown evidence is not coerced to zero or exhausted.
- [x] 1.3 Specify and test the current Z.AI identity binding for native JWT and Individual key lookup using source contracts and synthetic stores; verify fixed unique-key versus start-first current-account scope, wrong/stale/multiple identities and expired auth fail safely with no copied cipher or login/account writes.

## 2. Account-only routing

- [x] 2.1 Add plan_policy fixed/default and start-first validation, preserving existing command/runtime and fixed provider/model defaults; verify omitted policy/provider, both exact allowed routing IDs, agent-only policy, YAML-load/direct-constructor parity and unrelated providers/unsupported policies fail before admission and no native/direct API mode is introduced.
- [x] 2.2 Implement the private bounded Start balance and Individual subscription-list reads/decoders using actual app version, native endpoint/auth/proxy context and allowed normalized evidence; verify the source-derived active Coding-product rule and malformed/absent subscription cases; verify both allowed GETs with fake-clock tests for the 15-second decision/five-second attempt/two-attempt limits, 250-ms retry, one-second busy recheck and sharing age; cover HTTP 503/429/auth failures, slow joiners, account/config invalidation, redirects, schema errors and token leakage without live inference.
- [x] 2.3 Implement exact-model eligibility and Start-first selection before each new turn; verify Flash promotion, model absent, exhausted/expired allowance, reservations, unknown balance and new-promotion scenarios, with no model/reasoning substitution.
- [x] 2.4 Supply evidence-based account overlays and selected provider auth through the existing native registry/store boundary; verify actual registry revision, model restrictions and same-account scope using fake runtime fixtures instead of unconditional entitled/current values, including fixed-mode cache-only rejection and active-plan-but-missing-model cases.
- [x] 2.5 Preserve the conversation while applying the complete selected modelSelection on each new turn and publish safe routing diagnostics; verify no startup-prompt replay or desktop configuration mutation.
- [x] 2.6 Implement source/fixture-verified terminal quota classification and one same-session Individual continuation with a fresh inputId and short continue message; verify exact model/reasoning, no original/startup prompt replay, intermediate diagnostics and final ownership across both native turns.
- [x] 2.7 Stop old-attempt owned background work and retire pending auth/permissions before continuation, sharing the original deadline with a five-second minimum remaining at continuation send; refresh the attempt's backgroundJobs state from session/read before the drain and confirm the known tasks are gone after cancellation; verify four-second rejection, exact-boundary acceptance, cleanup uncertainty, cancellation/reset, unknown EOF, generic 429/admission busy, late events and a failed Individual continuation never cause unsafe dispatch or retry loops.
- [x] 2.8 Extend the isolated protocol harness with terminal Start failure followed by a new same-session send using Individual selection; verify persisted conversation context and distinct input IDs without real inference, and record any runtime incompatibility rather than disguising it as success.

## 3. Host and lifecycle hardening

- [x] 3.1 Centralize effective runtime/config paths and environment handling for launch/preflight, including relative paths and ZCODE_DATA_BASE_DIR; verify override/proxy/CA/nil-environment behavior and no unrelated runtime fallback.
- [x] 3.2 Move common preference/interaction policy into continuous Go duplex routing with bounded frames/queues/writes; verify reverse requests during create/resume, callback response ordering and cancellation while an ordinary RPC is blocked.
- [x] 3.3 Add bounded parse-first method/schema evidence at owned execution startup and validate subsequent snapshots/events; include parse-first cancelBackgroundTask and session/read in required probes and verify their absence fails before send and never invokes runtime checks during installation/catalog.
- [x] 3.4 Keep private registry/credential extraction guarded and bind auth to generation/workspace/session/request/selection; verify ambiguous anchors, cancelled/late auth, rotated secrets and foreign requests never leak material or gain authorization.
- [x] 3.5 Harden input/turn correlation and safe permission decisions; verify early events, stale completions, offered once/always/reject, duplicate replies, unsupported questions and no automatic resend after ambiguous transport loss.
- [x] 3.6 Traverse complete historical descendant baselines with bounded pages/tree/cycle detection; verify nested historical children cannot become owned permission targets.
- [x] 3.7 Track owned background tasks and apply one five-second terminal cleanup budget; verify parent completion with active bash, hung close/write, leader-exited children and unrelated-process survival.
- [x] 3.8 Replace raw runtime error/stderr/config/auth output with safe templates and lifetime redaction; verify credentials, rotated tokens, proxy URLs and raw billing records never reach diagnostics/state/artifacts.

## 4. Shared boundaries and verification

- [x] 4.1 Preserve installation as non-executing and account-free for both plan policies; verify identical local prerequisites deduplicate and no balance/version/app-server/account calls occur in preflight.
- [x] 4.2 Keep general catalog unsupported until a complete safe external source exists; verify plan-specific buckets never become a fabricated full catalog and discovery performs no account initialization or inference.
- [ ] 4.3 Add an opt-in isolated installed-CJS protocol harness with temporary data/storage/workspace, synthetic config and denied network; verify create/resume/subscribe/stop/close without model work or writes outside temporary roots, reporting unavailable-artifact skips honestly.
- [ ] 4.4 Before claiming live Start compatibility, obtain separately authorized read-only account verification and record only safe auth/eligibility/result evidence; verify no paid request, offer activation, reset redemption or account mutation. If unavailable, explicitly retain this as unverified rather than passing the task.
- [x] 4.5 Update README/examples and a scenario-to-test checklist for fixed versus start-first, exact model routing, unknown/busy balances, bounded post-failure continuation and its original deadline, installed-runtime ownership, proxy paths and remaining internal-API coupling, fixed breaking gates, separate Start-balance versus Individual-subscription outage consequences, intentional rejection of a longer subscription TTL/stale fallback, numeric latency bounds, diagnostic codes and explicit fresh-session workflow retries; verify examples parse and every scenario has evidence.

## 5. Completion checks

- [x] 5.1 Run gofmt on changed Go files and verify no formatting drift.
- [x] 5.2 Run go vet ./... and record success after resolving findings.
- [x] 5.3 Run go test -race -count=1 ./... with paid/live inference disabled; record isolated/live-read skips separately from passed tests.
- [ ] 5.4 Run openspec validate harden-zcode-source-contracts --strict and compare implementation to every scenario before archive; verify no personal API mode, runtime fork, account mutation or release entered scope.

## Archive disposition — 2026-09-28

Archived at the user's explicit request after discussing the Start CAPTCHA limitation. Unchecked tasks remain unchecked and are not represented as completed: 4.3 (isolated installed-CJS harness), 4.4 (full live Start compatibility gate), and 5.4 (complete scenario reconciliation). Strict validation passed, but that is not full behavioral verification. Live account balance reads, fixed Individual Flash, and Start-first GLM-5.3 absence fallback were verified; Start Flash inference and real exhaustion continuation remain unverified. No CAPTCHA-to-Individual fallback is implemented. See research.md for evidence and README for supported use. Release publication was separately authorized by the user after this disposition.
