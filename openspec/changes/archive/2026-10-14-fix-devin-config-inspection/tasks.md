# Tasks

## 1. Profile inspection
- [x] 1.1 Implement content-aware checks and safe file/key diagnostics; cover metadata, disabled/active entries, malformed files and no mutation in regression tests.
- [x] 1.2 Resolve import switches with project/local precedence; test disabled imports and re-enabling overrides.
- [x] 1.3 Document supported inspection and remaining extension limitations, with a reviewed native import opt-out example.

## 2. Verification
- [x] 2.1 Run gofmt, go vet ./... and go test -race -count=1 ./...; verify all spec scenarios and strict OpenSpec validation.
