# Validation

- `gofmt` applied to changed Go files; `git diff --check` passed.
- `go vet ./...` passed.
- `go test -race -count=1 ./...` passed across all packages.
- `openspec validate opencode-permission-control --strict` passed.
- Packaged coordinator skill passed `quick_validate.py`.
- OpenCode 1.18.20 smoke check: started an isolated temporary server, inspected `/doc`, confirmed classic `permission.asked` / `permission.replied` event properties and `POST /permission/{requestID}/reply` accepts `once`, `always`, `reject`, and optional `message`; verified `GET /permission` returns an array. Stopped that server. No model invocation or unrelated-session mutation.

## Requirements review

- Effective YOLO: adapter tests cover true, false and implicit true; API tests cover inherited squad defaults. Foreign sessions, stale root message IDs and duplicate request delivery are ignored. Only asked events trigger replies, so explicit denials and questions are unchanged.
- Visible waits: tests cover complete request details, three simultaneous requests, child sessions, phase restoration, HTTP errors, and a real five-second automatic reply timeout.
- Coordinator replies: end-to-end API tests cover success, invalid bodies, unknown run/request, backend failure and stale/reset requests; adapter tests cover once/always/reject forwarding and concurrent replies. Unsupported adapter routing was checked against the optional-interface branch returning 409.
- Long polls: both create-run and GET-run return actionable pending requests early; successful YOLO runs complete without intervention. Repeated integration runs checked asynchronous event ordering.
- Lifecycle: tests cover cancelling an in-flight automatic reply, force reset, startup interruption clearing pending requests, and acknowledged replies whose HTTP response arrives after the terminal SSE event.

The release workflow must still pass on the release commit and publish four platform archives plus checksums before the release is considered complete.
