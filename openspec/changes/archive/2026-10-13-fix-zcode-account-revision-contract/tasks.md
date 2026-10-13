# Tasks

## 1. Native revision contract

- [x] 1.1 Add hermetic subprocess regression coverage for both registry snapshots and App Server overlays, including safe rejection, and demonstrate failure before the fix.
- [x] 1.2 Correct both native revision keys and remove the unused helper request field; verify the new host tests pass.
- [x] 1.3 Add v0.21.1 release notes explaining the fix, unchanged configuration and unsupported model catalog; verify notes match the implementation.

## 2. Integration and completion

- [x] 2.1 Run gofmt on changed Go files, git diff --check, go vet ./..., go test -race -count=1 ./... and go build ./...; require passing results.
- [x] 2.2 Run the short local opt-in ZCode continuity smoke test and record its result and any environment limitation without claiming remote live verification.
- [x] 2.3 Run openspec validate fix-zcode-account-revision-contract --strict, check all delta scenarios against implementation/tests, and sync and archive the completed change.
