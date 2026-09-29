## 1. Configuration admission

- [x] 1.1 Replace `ValidateZCodeAgent` policy validation with rejection of any `plan_policy` value and enforcement of the Individual-only `provider` option (empty allowed); verify config tests cover `fixed`, `start-first`, foreign providers, and omission, all failing/passing at YAML load before preflight
- [x] 1.2 Remove `planpolicy.go` and mirror the same rejection in adapter `Init` for direct construction; verify `go test ./internal/adapters/zcode/ ./internal/config/` passes with planpolicy tests deleted and new rejection tests added

## 2. Host contract

- [x] 2.1 Strip `host.cjs` of the network-read machinery: `readStartBalance`, `readIndividualSubscription`, `performRead`, `requestViaProxy`, `noProxyMatches`, `caCertificates`, `appVersion`, `startSourceHeaders`, `resolveIdentity`/JWT decoding, `authMaterial`, and the `squad/readStartBalance` / `squad/readIndividualSubscription` handlers; keep `readRegistryView`, its `squad/readRegistryView` handler, and their bundle dependencies; verify `node --check` on the bundle and remaining host tests pass
- [x] 2.2 Collapse `requestHeaders` to the Individual provider with the Go auth-binding round-trip intact and the CAPTCHA/Start branches removed; verify a host test asserts non-Individual providers and non-`model-request` reasons still fail as unsupported interactions
- [x] 2.3 Drop the `planPolicy` bootstrap parameter, JWT load, and `hasStart`/`identityMatch` response fields; shrink the overlay provider-family guard to the Individual ID; verify bootstrap identity tests (zero/multiple keys, unreadable secret) still pass

## 3. Adapter dispatch

- [x] 3.1 Remove the routing engine files `routing.go`, `continuation.go` and their unit tests; trim `evidence.go` to the registry-view decode, selection key, and reasoning support, and `hostbridge.go` to the registry-view bridge; verify `go build ./...` succeeds with no dangling references
- [x] 3.2 Restore `Send`: no eligibility decision between wire probes and dispatch; keep the local selection verification — one `squad/readRegistryView` call for the configured provider/model/reasoning whose failure (absent/disabled model, unsupported reasoning, unreadable view) fails the run before dispatch — followed by the unconditional `squad/applyAccountOverlay` call for the configured Individual provider; `selection()` keyed on the configured provider; drop `activeRun` provider/decision fields and `emitRoutingDiagnostic` call sites; verify adapter tests exercise create/resume with the selection check and without any pre-dispatch network read
- [x] 3.3 Remove quota-exhaustion continuation handling and its reason codes from the turn loop; verify a `turn.failed` quota-exhaustion event terminates the run as an ordinary failure with preserved session/artifacts
- [x] 3.4 Delete `testdata/billing/`, the subscription-list fixtures, and continuation-only frames in `testdata/protocol/legacy-wire.json`; keep `testdata/registry/registry-view.json` for the selection check; verify `go test ./internal/adapters/zcode/` passes
- [x] 3.5 Cover the retained selection gate: add/adjust tests that an unselectable model, an unsupported reasoning level, and an unreadable registry view each fail before dispatch with actionable errors and no network reads; verify the tests pass

## 4. Verification and documentation

- [x] 4.1 Update `README.md` and `examples/zcode-squad.yaml`: remove `plan_policy`/start-first documentation and add a migration note that the option must be removed; verify no repo mention of `plan_policy`/`account:zai-start-plan` remains outside `openspec/`
- [x] 4.2 Run `gofmt` on changed Go files, `go vet ./...`, and `go test -race -count=1 ./...`; verify all pass
- [x] 4.3 Run `openspec validate remove-zcode-start-plan-routing --strict` and check implementation against the delta requirements and scenarios; record any deviation before archive
