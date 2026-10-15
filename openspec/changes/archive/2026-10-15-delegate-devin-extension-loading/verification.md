# Verification

- gofmt and git diff --check passed.
- go vet ./... and go test -race -count=1 ./... passed.
- Bundled skill/CLI/skillmanager tests passed after updating embedded authoring guidance.
- Strict change validation and all main specs passed.
- Regression tests preserve native hooks, legacy MCP, required/optional plugins and import settings in the private config; active/malformed external and project sources do not cause compatibility refusals. Invalid primary user JSON still fails private copying safely.
- Existing process/protocol/model/failure tests remain in the full suite. No real extensions were launched and no native settings were edited for this change.
- Existing native-account and cleanup scenarios were retained with the new delegation boundary; obsolete extension inspection requirements were removed and replaced by Native extension loading.
