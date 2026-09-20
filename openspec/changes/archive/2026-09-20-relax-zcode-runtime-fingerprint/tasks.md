## 1. Bridge probe

- [x] 1.1 Implement autorun discovery in `internal/adapters/zcode/host.cjs`: match the `X();Y();async function Y()` pattern with an exactly-once requirement and strip the `Y();` call; abort with an actionable error on zero or multiple matches. Verify by grepping the installed desktop 3.14.0 bundle for exactly one match and by a Node probe that the stripped source still compiles.
- [x] 1.2 Implement discovery of the credential-store factory, registry entry point, and required lazy-factory runners using the stable markers from design.md (keep-name registrations, `credentials.json` path-builder context, single `async load(` store, factory wrappers), validating every discovered name against `^[A-Za-z_$][\w$]*$` and assembling the appended tail from them. Verify by running the discovery against the installed 3.14.0 bundle and printing the found identifiers.
- [x] 1.3 Replace the pinned SHA-256 gate with the probe; keep computing the hash only for diagnostics and include it, plus the missing structure name, in failure messages. Verify that the bridge run against a fixture with no anchors fails with the new message and never executes the fixture.
- [x] 1.4 Order post-load checks explicitly: registry export must yield `runtime.configService.read()` with `zcodeBuiltinRevision` and the credential export an async `load` before any credential is read. Verify via the existing protocol tests in `internal/adapters/zcode`.

## 2. Tests and live validation

- [x] 2.1 Extend `internal/adapters/zcode/host_test.go` with synthetic fixture bundles covering successful discovery, missing autorun anchor, ambiguous anchor, and unchanged secret redaction; verify with `go test ./internal/adapters/zcode/`.
- [x] 2.2 Run the opt-in live gates against installed desktop 3.14.0 (`SQUAD_ZCODE_LIVE` pong minimum; child/permission/cancel when the account allows) and record the observed results and bundle SHA-256 in the change's verification notes.

## 3. Documentation

- [x] 3.1 Update the README ZCode support paragraph: capability probing instead of the pinned 3.12.3/0.16.5 fingerprint, the desktop 3.x envelope, and the actionable failure with bundle hash. Verify the paragraph no longer names a single supported version and matches the new behavior.

## 4. Final checks

- [x] 4.1 Run `gofmt` on changed Go files, `go vet ./...`, and `go test -race -count=1 ./...`; all must pass.
- [x] 4.2 Run `openspec validate relax-zcode-runtime-fingerprint --strict` and check the implementation against every scenario in `specs/zcode-backend/spec.md` before archiving.
