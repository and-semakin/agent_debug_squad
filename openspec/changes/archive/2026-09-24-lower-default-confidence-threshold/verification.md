# Verification

- `openspec validate lower-default-confidence-threshold --strict`: passed.
- `openspec validate --specs --strict`: all six current specs passed.
- `gofmt` applied to all four changed Go files.
- `go vet ./...`: passed.
- `go test -race -count=1 ./...`: passed across the repository.
- `git diff --check`: passed.

## Scenario review

- Omitted threshold: config regression pins effective 0.7 while asserting the stored field stays zero. JSON `omitempty` and the existing golden hash regression preserve identity; hashing code is unchanged.
- Scores 0.70, 0.72, 0.78, 0.79: table-driven scheduler tests verify success and recorded threshold 0.7.
- Score 0.68: scheduler tests verify uncertainty under both default attention and explicit error policies.
- Explicit 0.8: scheduler tests verify 0.79 remains uncertain and equality at 0.8 succeeds. Existing config tests continue to verify explicit threshold parsing; validation code is unchanged.
- Recovery: the real recovery test reloads the persisted execution, classifies at 0.72 with recorded threshold 0.7, and verifies agent work is not repeated.
- Explicit saved thresholds and settled history: code inspection confirms snapshot JSON decoding preserves explicit fields, recovery does not replace the saved definition, and only judging attempts are reclassified. Settled verdict records are not rewritten by this default change. Existing persistence/recovery tests pass.
- Current spec requirement blocks match the change deltas exactly. README documents hash/schema compatibility and the behavioral effect on existing omitted-threshold executions.

Only the shared constant changes production behavior. No validation, comparison, uncertainty-policy, lifecycle, or adapter code changes. Explicit values in examples and user-local workflows remain untouched; no release was published.
