# Verification

- Confirmed the remote diagnostic bundle's native parser field by user-run static inspection; runtime bundle SHA256: `31965ce1d5d4a808ef156784f67996de42ccab335591d2386a61ea8f6f506a54`.
- Before the fix, the real-host subprocess test failed registry projection with the reported materialization error and overlay receipt with the missing-field TypeError. After the fix, all four cases pass.
- Native account revision contract: the fixture enforces exact casing and the original native revision for local snapshots and the forwarded App Server DTO. Registry projection and overlay receipt are checked separately.
- Rejection contract: the host retains its fixed registry error template when the native registry throws private details; an App Server overlay rejection remains a failure. Existing adapter sequencing performs both operations before session creation and prompt dispatch.
- Passed gofmt, git diff --check, go vet ./..., go test -race -count=1 ./..., go build ./..., strict change and main-spec validation.
- The opt-in local ZCode 3.14.4 smoke test progressed through registry and overlay setup to an actual model request, then failed with HTTP 429 / provider code 1308 (five-hour account quota). Two-turn continuity and successful inference remain unverified; no retry or quota workaround was attempted.
