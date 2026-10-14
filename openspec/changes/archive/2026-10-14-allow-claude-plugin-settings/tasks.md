# Tasks

## 1. Compatibility fix
- [x] 1.1 Remove enabledPlugins rejection and add regression tests for user/project settings, unchanged policy and remaining extension guards.
- [x] 1.2 Update backend documentation with observed import boundaries and upgrade guidance.

## 2. Verification
- [x] 2.1 Run formatting, go vet ./..., go test -race -count=1 ./..., strict OpenSpec validation and check all scenarios before archive.
