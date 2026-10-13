# Tasks

## 1. Configuration contract

- [x] 1.1 Require model and validate reasoning/speed/mode/yolo combinations with config tests, retaining standalone catalog discovery and machine proxy/env behavior.
- [x] 1.2 Update README, Devin docs and example with defaults, mode/yolo precedence, model migration and proxy settings; verify example parsing.

## 2. ACP controls

- [x] 2.1 Apply refreshed model-specific controls and verify the final selection; test new/load defaults, explicit choices, missing/ambiguous/ignored/reset controls and zero prompts on failure.
- [x] 2.2 Verify controls in small live sessions through proxy, including continuation and fast on a supporting model; record evidence without private transcripts.

## 3. Delivery

- [x] 3.1 Run gofmt, go vet ./..., go test -race -count=1 ./..., go build ./..., strict OpenSpec validation and scenario review; sync main specs.
- [x] 3.2 Commit and push directly to main, verify CI, publish v0.21.0 and verify four archives, checksums and binary version.
- [x] 3.3 Configure machine-local Squad and native Devin proxy defaults with backups; verify session-free catalog and a short turn without inherited proxy variables.

- [x] 3.4 Archive the completed change after release and local proxy verification.
