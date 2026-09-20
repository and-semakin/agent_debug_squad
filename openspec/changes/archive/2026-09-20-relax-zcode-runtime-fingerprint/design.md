## Context

See proposal.md — Why. Today `internal/adapters/zcode/host.cjs` gates on one SHA-256 (`da61b066…`, desktop 3.12.3 / runtime 0.16.5), then patches the bundle by literal minified anchors: it strips the CLI autorun call (`source.replace('RSe();HMs();async function HMs()', …)`) and appends `HPt();V3e();module.exports={credentials:gb,registry:q3e};` before `_compile`. Minified names change on every ZCode rebuild, so the pinned hash and the literals rot together.

Probing the installed desktop 3.14.0 bundle (`/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs`, SHA-256 `8f5cfccf…`, 14.8 MB) shows the structures the bridge needs are discoverable without exact names:

- The autorun statement keeps the exact shape `q9t();vtc();async function vtc()` — i.e. `X();Y();async function Y()` — and this pattern occurs exactly once in the bundle; `vtc` is the CLI main (it slices `process.argv`). 3.12.3's anchor had the same shape (`RSe();HMs();async function HMs()`).
- The bundle is built with esbuild keep-names: classes register readable names via `r(this,"NodeProviderConfigRuntime")` and an `…rRegistryRuntime` class exposing `configService`/`registryService`/`dispose()`.
- Lazy modules are wrapped as `Ptr=Y(()=>{cA();OKe();…})`; the old `HPt();V3e();` calls were such factory runners for the modules defining `gb` (credential store factory) and `q3e` (registry factory).
- The credential store is the only `async load(` in the bundle, next to the `".zcode","v2","credentials.json"` path builder derived from `{env, baseDir}`.

Go side (`internal/adapters/zcode/zcode.go`) is untouched by the gate: it embeds host.cjs, spawns `node -e hostSource <runtime_path>`, and consumes the same bridge protocol. Run lifecycle, permissions, and cancellation do not change.

## Goals / Non-Goals

**Goals:**

- Any installed ZCode desktop 3.x bundle whose required structures are found unambiguously works without a Squad release.
- Unusable bundles still fail closed, before being loaded, with an actionable error that carries the bundle SHA-256.
- Keep the bridge dependency-free single-file Node, credential confinement, and redaction exactly as today.

**Non-Goals:**

- Supporting providers other than `account:zai-individual-coding-plan`.
- Guessing or reimplementing credential decryption; the native reader keeps being loaded in memory from the installed bundle.
- A version-number parser or allowlist of desktop versions; capability is the signal, the 3.x line is the expected envelope.
- An override flag to force-run bundles that fail the probe.

## Decisions

- **Structural probe replaces the fingerprint.** Alternative: a `{hash → anchors}` table per validated version — rejected because every ZCode update would still need a Squad release, which is the reported failure. Alternative: semver range check — rejected: the runtime version does not identify the private host API (already established in the archived backend design), and the desktop version is not a reliable property of the runtime file.
- **All discovery is static, on the source text, before `_compile`.** The probe (1) requires exactly one match of the autorun pattern `X();Y();async function Y()` and strips the `Y();` call; (2) locates the credential-store factory and registry entry points via stable markers (keep-name registrations, the `credentials.json` path-builder context, the single `async load(` store shape) plus the lazy-factory wrappers that must run first; (3) assembles the appended tail from the discovered identifiers only after validating each matches `^[A-Za-z_$][\w$]*$`. Any miss or ambiguity aborts before the bundle is loaded, preserving "unusable bundles never execute".
- **Explicit post-load interface validation.** After `_compile`, the bridge verifies the exports before touching credentials: `credentials({env})` yields an object with an async `load(name)`, and `registry(env)` yields `.runtime.configService.read()` containing `zcodeBuiltinRevision`. The revision read already exists; credential-load ordering moves after these checks. A bundle that passes static discovery but fails here produces the same actionable error shape.
- **Error contract.** Failures name the missing structure (autorun anchor, credential store, provider registry), include the runtime SHA-256 for reporting, and keep the revalidate/update guidance. The Go test that asserts an unknown fixture "must not execute" still holds: a fixture matching no anchors is never run.
- **Security posture unchanged in substance.** The hash was a compatibility guard, not a security boundary: `runtime_path` has always been user-configurable and the bridge runs on the user's machine. The static gate plus interface validation keep the pre-execution check; secret confinement and redaction are untouched.

## Risks / Trade-offs

- [A future bundle renames structures so the probe misses] → fails closed with the actionable error and SHA-256; adapter update then extends the probe rules. Same recovery path as today, but the window of guaranteed breakage shrinks from "every update" to "only incompatible rebuilds".
- [Discovery rule accidentally matches a wrong-but-similar structure] → mitigated by exactly-once constraints, identifier validation, and the post-load interface checks; a wrong match that passes all checks would still have to behave like a credential store and registry.
- [3.12.3 is no longer installed locally, so the probe cannot be re-verified against it] → acceptable: if its shapes no longer match, it fails closed exactly as it does today (its hash is already rejected only by luck of the constant); users on 3.12.3 keep the older binary.
- [Structural rules encode esbuild output conventions] → inherent to bridging a private API; rules stay in one small section of host.cjs with the bundle evidence above recorded here for the next maintainer.

## Migration Plan

Behavior change only in the embedded bridge; no YAML, CLI, HTTP, or persisted-state changes, so no migration. Rollback selects an earlier binary. README's ZCode support paragraph is updated to describe capability probing and the supported 3.x envelope. Live validation on installed desktop 3.14.0 (existing opt-in `SQUAD_ZCODE_LIVE*` tests) precedes archive.

## Open Questions

- Which exact live-test gates to run before archive (pong vs. full permission/cancel matrix) depends on account availability at implementation time; any subset still validates the probe on the real bundle.
