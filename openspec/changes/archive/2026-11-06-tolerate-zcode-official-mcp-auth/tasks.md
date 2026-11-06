# Tasks

## 1. Optional authentication compatibility

- [x] 1.1 Implement the exact negative callback response and add regression coverage for create/resume, both consumers, foreign/malformed contexts, notifications and unknown methods; verify focused adapter tests.
- [x] 1.2 Document optional auth behavior and runtime evidence in README and verification notes; check claims against the inspected protocol and live results.

## 2. Integration verification

- [x] 2.1 Run gofmt, go vet ./..., and go test -race -count=1 ./... successfully.
- [x] 2.2 Verify live creation and continuation ping/pong against ZCode 3.17.2 and preserve ignored artifacts.
- [x] 2.3 Run strict OpenSpec validation, reconcile every scenario with evidence, and archive the change with spec sync.
