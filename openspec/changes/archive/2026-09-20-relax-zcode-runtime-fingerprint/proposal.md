# Proposal: Relax ZCode runtime fingerprint

## Why

The ZCode adapter's Node bridge accepts exactly one runtime bundle fingerprint (ZCode desktop 3.12.3 / runtime 0.16.5, SHA-256 `da61b066…`). Every ZCode desktop update breaks the adapter until a new Squad release pins the new fingerprint and its re-minified patch anchors — users on desktop 3.14.0 today see `Unsupported ZCode runtime fingerprint` with no workaround, because the bridge also refuses to run bundles it has never seen, even when the underlying host contract is unchanged.

## What Changes

- Replace the single pinned SHA-256 gate in the embedded host bridge with a structural compatibility probe that discovers the required anchors in the installed bundle at startup: the CLI autorun statement to suppress, and the native credential-reader and provider-registry entry points to load and export.
- Discovery uses stable structural markers (esbuild keep-name registrations such as `NodeProviderConfigRuntime`, unique call shapes such as the single `X();Y();async function Y()` autorun pattern and the credential store's `async load(`) instead of exact minified identifiers or a whole-file hash. Every anchor must be located unambiguously before the bundle is loaded; otherwise the run fails without executing it.
- After in-memory load, the bridge validates the discovered exports against the expected interface (credential store with `load`, registry with `runtime.configService.read()` yielding `zcodeBuiltinRevision`) before any credential is read.
- The expected compatibility envelope is the ZCode desktop 3.x line, but the gate is capability-based rather than version-number-based: a bundle works when its shapes are found and fails closed with an actionable error when they are not. Failure messages include the bundle's SHA-256 so users can report it.
- README's runtime-support paragraph is updated accordingly.
- No YAML configuration, CLI flag, HTTP API, or persisted-artifact changes; no new options.

Scope boundaries: provider support remains `account:zai-individual-coding-plan` only; credential handling stays inside the bridge with redaction; the probe does not patch, persist, or modify the installed bundle.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `zcode-backend`: the `Explicit compatible configuration` requirement changes — runtime compatibility is decided by a structural probe of the installed bundle rather than a pinned fingerprint, so the "unsupported installation" scenario covers bundles whose required shapes cannot be located unambiguously, not merely unknown hashes.

## Impact

- `internal/adapters/zcode/host.cjs`: the embedded bridge — fingerprint constant and hardcoded patch strings replaced by discovery logic.
- `internal/adapters/zcode/host_test.go`: guard test extended from "unknown hash is rejected" to probe behavior (missing anchors, ambiguous anchors, successful discovery on fixture bundles).
- `README.md`: ZCode runtime/account support paragraph.
- `openspec/specs/zcode-backend/spec.md`: requirement delta as above.
- Live validation on an installed desktop 3.14.0 bundle is required before archive (opt-in live tests already exist).
