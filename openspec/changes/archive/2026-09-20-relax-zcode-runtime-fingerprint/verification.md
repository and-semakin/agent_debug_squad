# Verification

## Static discovery against the installed desktop 3.14.0 bundle

Runtime: `/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs`, SHA-256 `8f5cfccf2a899b92e57bc2a5760b949c1a928f739652fffc9e6d07c24f11ba05` (the bundle the old pinned fingerprint `da61b066…` rejected).

- `discover()` returns `credentials: PM`, `registry: lkt`, `thunks: [Kat, ukt]` in ~280 ms; the autorun statement `q9t();vtc();async function vtc()` is matched exactly once and stripped.
- Original names behind the discovered identifiers confirm the mapping: `createSharedZCodeCredentialStore` (PM), `resolveSharedZCodeCredentialsPath` (fUs path builder), `startProcessProviderRegistryRuntime` (lkt), `NodeProviderRegistryRuntime` (zKe class).
- Registry dry-run (no credentials): compiled patched bundle, ran both module thunks, called the registry factory with the bridge's env; interface check passed (`runtime.configService.read` + `dispose`) and the builtin revision was read: `zcode-builtin:30:b167cf4c…`. Credential store factory interface check passed (`async load`).

## Go tests

- `TestHostDiscovery`: valid synthetic fixture discovers the expected exports, thunks, stripped autorun, and source hash; missing autorun fails with `CLI autorun anchor: 0 matches`; duplicated autorun fails with `CLI autorun anchor: 2 matches`; both messages include the bundle SHA-256 (`Unsupported ZCode runtime <hash>`).
- `TestHostGuardAndRedaction`: a bundle matching no anchors is never executed (`must not execute` probe), secret redaction unchanged.
- `gofmt`, `go vet ./...`, `go test -race -count=1 ./...` all green.

## Bounded live validation on desktop 3.14.0

- `SQUAD_ZCODE_LIVE=1 TestLiveZCode`: two turns, one session created and resumed, both replies `pong` (13.7 s total).
- `SQUAD_ZCODE_LIVE_CHILD=1 TestLiveZCodeSubagent`: foreground child observed in progress, parent replied `parent-pong` (9.9 s).
- Permission and cancel live gates were not run in this session; they exercise run-scoped paths that this change does not touch (bootstrap only) and remain opt-in.

## Scenario coverage

- Compatible signed-in account → live pong test above.
- Desktop update within the supported line → desktop 3.14.0 runs with no fingerprint update or configuration change; the same binary previously failed on this bundle.
- Unsupported installation → host tests assert fail-closed before load with the hash in the message; account-availability and credential-ambiguity errors are unchanged.
