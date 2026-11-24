# Verification

- gofmt: no unformatted Go files.
- go vet ./...: passed.
- go test -race -count=1 ./...: passed after updating discovery target counts.
- go build ./...: passed.
- scripts/verify-skill-builds.py: passed for darwin/linux, amd64/arm64.
- openspec validate remove-zcode-backend --strict: passed before spec sync.
- openspec validate --specs --strict: all 11 specs passed after sync.
- Agent normalization covers legacy options and YAML/workflow configs; machine tests cover empty and populated sections and secret-free migration errors.
- Factory rejects ZCode, model selectors return stable exit-2 argument errors, and default/all discovery lists exactly the six supported real backends.
- Generic owned Init-failure cleanup regression retained using Devin with a nonexistent executable.
- Reviewed remaining references: active runtime references are rejection paths; skill host paths remain intentionally independent; historical documents are preserved.
- No persisted-state or credential migration code was added. README/release notes require a fresh session when switching backends.
