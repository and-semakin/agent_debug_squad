## 1. Default and regression coverage

- [x] 1.1 Set the shared fallback to 0.7 without normalizing saved definitions; verify config tests pin the literal default and preserve omitted storage.
- [x] 1.2 Cover the new confidence boundary, motivating scores, explicit 0.8, and both uncertainty policies in scheduler tests; verify targeted tests pass.
- [x] 1.3 Verify persisted omitted thresholds use 0.7 on recovered classification and retain stable hashing; preserve explicit thresholds and settled history using regression tests and recovery code inspection.

## 2. Documentation and verification

- [x] 2.1 Update current specs and README defaults and compatibility guidance; inspect the diff to ensure explicit examples and user-local configuration are untouched.
- [x] 2.2 Run gofmt on changed Go files, go vet ./..., go test -race -count=1 ./..., and openspec validate lower-default-confidence-threshold --strict; require all checks to pass.
- [x] 2.3 Compare implementation and tests against each changed scenario and record verification results before handoff.
