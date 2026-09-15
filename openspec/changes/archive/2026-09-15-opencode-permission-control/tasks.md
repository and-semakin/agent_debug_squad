## 1. Permission control

- [x] 1.1 Add optional permission reply contract and progress types with deep copies; verify serialization and race-safe snapshots in tests.
- [x] 1.2 Implement owned SSE request tracking and YOLO once replies with auditing, bounded errors and cancellation; verify manual/automatic, duplicates, foreign sessions, descendants and failure tests.

## 2. Coordinator API

- [x] 2.1 Add run-scoped validated reply routing and early wait return; verify HTTP errors, accepted decisions, concurrent/stale replies and both long-poll endpoints.
- [x] 2.2 Update README and packaged skill for YOLO behavior and permission intervention; verify examples match implemented routes and response fields.

## 3. Validation and release preparation

- [x] 3.1 Run gofmt on changed Go files, go vet ./..., go test -race -count=1 ./..., and strict OpenSpec validation; check each requirement against implementation and tests.
- [x] 3.2 Smoke-check the installed OpenCode permission API without model calls or unrelated-session mutations; record results and archive the completed change with specs synced.
