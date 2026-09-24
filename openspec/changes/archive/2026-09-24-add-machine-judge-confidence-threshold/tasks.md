## 1. Machine setting and runtime behavior

- [x] 1.1 Parse and validate numeric `judge.confidence_threshold` in the machine file; verify valid 0.6 and invalid type/range tests pass and other sections still reject it.
- [x] 1.2 Resolve workflow, machine, and built-in thresholds in judge classification and manual override; verify precedence, boundary, policy, and recorded-threshold tests pass.
- [x] 1.3 Verify omitted-threshold hashing and snapshot shape stay stable, recovery uses the current machine setting, and settled history is preserved; run focused tests.

## 2. Documentation and release readiness

- [x] 2.1 Update README, current specs, and example machine configuration; inspect the diff for unchanged explicit workflow settings and no private data.
- [x] 2.2 Run gofmt on changed Go files, `go vet ./...`, `go test -race -count=1 ./...`, and `openspec validate add-machine-judge-confidence-threshold --strict`; require all checks to pass.
- [x] 2.3 Compare implementation against all changed scenarios and record verification before archive.
