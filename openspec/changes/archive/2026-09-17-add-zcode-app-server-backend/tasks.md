## 1. Runtime host and transport

- [x] 1.1 Add guarded embedded Node bootstrap/auth bridge; verify incompatible-runtime and secret-redaction tests plus a bounded Flash smoke run.
- [x] 1.2 Add bidirectional JSON-lines client with bounded RPC, protocol-error handling, and process cleanup; verify transport and cancellation tests.

## 2. Adapter behavior

- [x] 2.1 Register zcode and implement create/resume/reset, model selection, startup prompt, and new-response extraction; verify lifecycle tests and live two-turn continuity.
- [x] 2.2 Implement native YOLO, run-scoped coordinator permission replies, and unsupported interaction failures; verify duplicate/stale/concurrent permission tests.
- [x] 2.3 Implement streamed events and descendant progress; verify fixture coverage and a short Flash subagent experiment.

## 3. Documentation and delivery

- [x] 3.1 Document supported runtime/account, model discovery, proxy settings, desktop visibility, and billing boundaries; provide a parseable Flash example.
- [x] 3.2 Run gofmt, go vet ./..., go test -race -count=1 ./..., go build ./..., strict OpenSpec validation, and review requirements against tests before archive.
## Delivery after implementation

Archive the completed change, commit and push main, verify CI, publish an annotated minor-version release, and verify release assets/checksums/version. Report delivery evidence in the final handoff.
