# Verification

## Source evidence

Inspected installed ZCode Desktop 3.17.2 on 2026-11-06. Runtime bundle SHA-256: `d9afd58d694f98a5279bf548cdc7646413c125762e86730605a6071f9e0346d6` (evidence only, not an allowlist).

The bundled `createOfficialMcpAuthHeadersPort` and `createOfficialCommandAuthHeadersPort` request `interaction/requestOfficialMcpAuthHeaders`. The native result schema accepts `{ok:false,reason:"official_auth_unavailable"}`; both ports already return that result when no host is available. Official MCP fetch handles it using its native anonymous fallback. Squad's generic unsupported-request branch had escalated this optional callback into a fatal run error.

## Checks

- Focused `TestOfficialMCP*` and `TestUnknownHost*` tests passed.
- `gofmt` on all changed Go files; no formatting differences remain.
- `go vet ./...` passed.
- `go test -race -count=1 ./...` passed, including ZCode and workflow packages.
- `go build ./...` passed.
- Strict change validation passed.

## Live checks

Using a development binary through the local REST API, with low reasoning and YOLO disabled:

- GLM-5.3-Flash: new conversation and follow-up asking it to repeat its previous reply both completed with exactly `pong` (runs 4 and 5).
- GLM-5.3: new conversation and the same continuity follow-up both completed with exactly `pong` (runs 6 and 7).

The first post-fix attempt tried to resume the incomplete session left by the original failed reproduction and returned `Session not found`. Resetting only the test agent cleared this stale ID; the subsequent new and resumed turns succeeded. No automatic replay or recovery change was introduced.

Configs, run transcripts, test output and binaries remain under ignored `.agent-debug-squad/`; no credentials or private transcripts are committed. Test servers were stopped.

## Requirement reconciliation

- Creation/resume: protocol helper withholds the session reply until the correlated native auth-unavailable response arrives; both turns return pong.
- MCP and plugin-command consumers, foreign workspace and malformed context: protocol cases require exactly the two negative result fields, no headers or JSON-RPC error.
- Notification: direct dispatcher test proves no wire reply is attempted.
- Unknown interaction: separate protocol test requires explicit failure and no assistant result.
- Real model inference and continuity: verified for both selectable account models as above.

Authenticated official MCP tools remain unsupported; this patch preserves ordinary model conversations without extending credential access.
