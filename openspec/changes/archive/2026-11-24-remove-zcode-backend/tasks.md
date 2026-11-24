# Tasks

## 1. Remove execution support

- [x] 1.1 Delete the ZCode adapter, bridge, fixtures, example and probe; reject agent/factory use with migration guidance and verify rejection tests.
- [x] 1.2 Remove machine fields, defaults and preflight metadata; reject stale sections including empty ones and verify machine configuration tests.
- [x] 1.3 Exclude ZCode from model discovery and reject explicit selectors; verify selector and all-backend tests.
- [x] 1.4 Preserve generic worker cleanup coverage using a supported adapter and verify the owned Init-failure regression.
- [x] 1.5 Update README, discovery documentation, bundled skill and release notes; verify remaining ZCode references describe removal, historical evidence or independent skill hosting.

## 2. Integration verification

- [x] 2.1 Run gofmt on changed Go files, go vet ./..., go test -race -count=1 ./..., go build ./... and release skill payload verification.
- [x] 2.2 Run openspec validate remove-zcode-backend --strict and review implementation against all changed requirements/scenarios before archive.
