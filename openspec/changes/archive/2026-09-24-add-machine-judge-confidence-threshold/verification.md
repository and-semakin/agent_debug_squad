# Verification

- `openspec validate add-machine-judge-confidence-threshold --strict`: passed.
- `openspec validate --specs --strict`: all six current specs passed.
- `gofmt` applied to every changed Go file.
- `go vet ./...`: passed.
- `go test -race -count=1 ./...`: passed.
- `git diff --check`: passed.

## Requirement review

- `judge.confidence_threshold` parses as an unquoted YAML number, accepts 0.6 and 1, rejects zero, negatives, values above one, null, strings, booleans, NaN, infinity, and collections; other backend sections reject the key. Existing judge proxy settings coexist with the threshold.
- The manager applies explicit workflow > startup-loaded machine > 0.7 at both automatic classification and manual override. Tests cover 0.6 and 0.59 boundaries, both uncertainty policies, explicit 0.8 precedence, and the recorded effective threshold.
- A created execution with a machine default still saves an omitted workflow threshold and the same definition hash. Recovery reclassifies an unresolved attempt with the current machine threshold and does not rerun agent work. Recovery leaves settled verdicts unchanged.
- The machine file is read only during server startup. No reload path was introduced, so edits require a restart. HTTP and persisted schemas are unchanged. The README includes an example and compatibility guidance; current specs match both delta specs.

The user-local machine setting will be changed to 0.6 after the new release is published, because older binaries reject the new key.
