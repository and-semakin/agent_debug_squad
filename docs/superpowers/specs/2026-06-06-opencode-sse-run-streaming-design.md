# OpenCode SSE Run Streaming Design

## Summary

OpenCode runs are currently opaque until the synchronous HTTP request completes. The adapter sends `POST /session/{sessionID}/message`, waits for the final response, then returns one final text artifact. This blocks live visibility into the OpenCode agent's current step, tool calls, text deltas, and errors.

This change moves the OpenCode adapter to an async run flow:

1. Subscribe to OpenCode `GET /event`.
2. Send the prompt with `POST /session/{sessionID}/prompt_async`.
3. Stream relevant OpenCode raw event JSON into the existing `RunSink`.
4. Treat `session.idle` for the same OpenCode session as the end of the run.
5. Fetch the final assistant response from OpenCode message history and return it as `RunResult.FinalMessage`.

The orchestrator, run records, transcript behavior, and artifact layout stay unchanged.

## Goals

- Persist OpenCode live events into `runs/<run_id>/<agent>.events.jsonl` while the run is active.
- Preserve the existing final answer artifact, `runs/<run_id>/<agent>.txt`.
- Keep OpenCode run completion reliable for multi-step turns, tool calls, retries, and compaction.
- Reuse the existing `RunSink` and orchestrator lifecycle rather than adding a new streaming API in Agent Debug Squad.
- Preserve Basic Auth, model, agent, startup prompt, timeout, reset, and error handling behavior.

## Non-Goals

- No browser UI or live dashboard.
- No Agent Debug Squad SSE/WebSocket API.
- No normalization of OpenCode events into a custom event schema.
- No synthetic "current file and line" status beyond what OpenCode events expose.
- No support for concurrent Agent Debug Squad runs against the same named OpenCode agent.
- No attempt to implement OpenCode YOLO or permission bypass behavior.

## OpenCode API Facts

OpenCode 1.15.13 exposes:

- `GET /event`: instance server-sent events. The first event is `server.connected`; subsequent events include `session.next.*`, `message.updated`, `message.part.updated`, `session.idle`, and `session.error`.
- `POST /session/{sessionID}/prompt_async`: async prompt submission. The request body matches `/session/{sessionID}/message` and may include `messageID`, `model`, `agent`, `system`, `variant`, `tools`, and `parts`. It returns `204 No Content`.
- `GET /session/{sessionID}/message`: session message history, including user and assistant messages with `info` and `parts`.

`session.next.*` events mostly correlate by `properties.sessionID`, not by `messageID`. Some `message.*` and part payloads contain `messageID`. The design therefore relies on the existing Agent Debug Squad invariant that a named agent can only have one active run at a time.

## Run Flow

### Existing Flow

```text
adapter.Send
  POST /session/{sessionID}/message
  wait for response
  return final text
```

### New Flow

```text
adapter.Send
  build prompt body
  generatedMessageID = msg_ads_<run_id-compatible suffix>
  start SSE reader on GET /event
  wait until reader has observed server.connected
  POST /session/{sessionID}/prompt_async with messageID=generatedMessageID
  for each relevant event:
    sink.StdoutLine(raw_event_json)
    update local run state if the event is terminal or useful for fallback text
  on session.idle for this sessionID:
    stop reading events
    fetch GET /session/{sessionID}/message
    find assistant message where info.parentID == generatedMessageID
    extract final text from text parts
    return RunResult{FinalMessage: finalText}
```

The SSE reader must be active before sending `prompt_async` to avoid losing early events such as `session.next.prompted` or the first text/tool events.

## Event Filtering And Artifacts

The adapter writes raw OpenCode event JSON to `sink.StdoutLine` when the event is relevant to the current run.

An event is relevant when:

- `event.type == "server.connected"` and it is used only to mark the SSE stream ready. This event does not need to be written to the run artifact.
- `event.properties.sessionID == state.BackendSessionID`.
- Or the event contains a nested message or part with the same `sessionID` and, when available, `messageID == generatedMessageID` or `parentID == generatedMessageID`.

The `.events.jsonl` file stores one compact JSON event per line exactly as OpenCode emitted it after parsing SSE framing. The adapter should not redact, re-shape, or summarize events.

Useful event families include:

- `session.next.prompted`
- `session.next.step.started`
- `session.next.step.ended`
- `session.next.step.failed`
- `session.next.text.started`
- `session.next.text.delta`
- `session.next.text.ended`
- `session.next.reasoning.*`
- `session.next.tool.*`
- `message.updated`
- `message.part.updated`
- `session.error`
- `session.idle`

The run completes on `session.idle` for the same OpenCode session, not on `session.next.step.ended`. `session.next.step.ended` can describe one model step, while `session.idle` means OpenCode has returned the session to an idle state after any internal continuation, tool execution, retry, or compaction.

## Final Answer Extraction

The canonical final answer comes from message history after `session.idle`.

Steps:

1. Fetch `GET /session/{sessionID}/message`.
2. Find the newest assistant message where `info.parentID == generatedMessageID`.
3. Extract all text parts from that assistant message's `parts`.
4. Join non-empty text parts with newlines.
5. Return the joined string as `RunResult.FinalMessage`.

The adapter should avoid using "latest assistant message" as the primary selector because the OpenCode session may contain older turns, synthetic messages, compaction messages, or unrelated updates from before the current run.

The adapter may maintain a fallback text buffer from `session.next.text.delta`, `session.next.text.ended`, or `message.part.updated`. This fallback is used only if message history does not contain an assistant message with `parentID == generatedMessageID`.

If both message history and fallback text are empty after `session.idle`, the run fails with a clear error such as:

```text
opencode run completed without assistant message for messageID <id>
```

## Errors, Cancellation, And Timeouts

`session.error` for the same session marks the run failed. The error text should be derived from the event payload and returned through `RunResult.ErrorMessage`.

If `prompt_async` fails, the adapter should cancel the SSE reader and return the HTTP error.

If the SSE stream fails before a terminal event, the adapter should return a streaming error unless `session.idle` was already observed.

If the adapter context is canceled, such as during force reset, the adapter should:

1. Stop the SSE reader.
2. Call `POST /session/{sessionID}/abort` best-effort.
3. Return context cancellation so the orchestrator can mark the run interrupted through the existing reset path.

`timeout_seconds` continues to configure the OpenCode adapter's maximum wait. The timeout should cover the full async lifecycle: SSE readiness, `prompt_async`, event wait until `session.idle`, and final message fetch.

## Authentication And HTTP Client

All OpenCode HTTP requests must preserve existing behavior:

- Use `base_url` or the default `http://127.0.0.1:4096`.
- Use Basic Auth when `password` is configured.
- Default Basic Auth username to `opencode` when only `password` is configured.
- Respect `timeout_seconds`.

The SSE request needs an HTTP client that does not time out before the run can complete. Use the same effective timeout for the whole Send context rather than a short per-request timeout on the SSE response body.

## Implementation Shape

Keep changes localized to the OpenCode adapter where possible.

Suggested internal helpers:

- `buildPromptBody(message string, run domain.RunRequest) map[string]any`
- `generatedMessageID(runID string) string`
- `streamEvents(ctx, sessionID string, messageID string, sink domain.RunSink) streamResult`
- `readSSEEventLines(reader io.Reader) (...)`
- `isRunEvent(event map[string]any, sessionID string, messageID string) bool`
- `fetchFinalMessage(ctx, sessionID string, messageID string, fallback string) (string, error)`
- `abortSession(ctx, sessionID string)`

The adapter can keep using generic map-based JSON parsing for OpenCode events. It does not need generated OpenAPI types.

## Backward Compatibility

- `POST /agents/{name}/runs` and `GET /runs/{run_id}` behavior remains unchanged.
- Existing OpenCode YAML config remains valid.
- Existing final output and transcript files remain valid.
- OpenCode `.events.jsonl` becomes populated during active runs, matching CLI-backed agent behavior.
- Tests that asserted `/session/{id}/message` for OpenCode send should be updated to assert `/session/{id}/prompt_async` and final history fetch.

## Testing

Add focused tests around a fake HTTP server:

- `Send` opens `/event` before posting `/session/{id}/prompt_async`.
- `Send` includes the generated `messageID`, configured `model`, configured `agent`, and startup prompt on the first run.
- Relevant SSE events for the run session are written to the sink as raw JSON lines.
- Irrelevant session events are ignored.
- `session.idle` ends the run and triggers final message history fetch.
- Final text is selected from assistant message where `info.parentID == generatedMessageID`.
- Fallback text from text delta events is used only when message history lacks the assistant response.
- `session.error` returns a failed run result.
- SSE disconnect before `session.idle` returns an adapter error.
- Context cancellation attempts `POST /session/{id}/abort`.
- Basic Auth is applied to `/event`, `/prompt_async`, `/message`, and `/abort`.

Regression tests should keep existing coverage for timeout parsing, reset session creation, model payload splitting, agent option inclusion, startup prompt inclusion, and unsupported YOLO warning.

## Operational Notes

Operators can inspect OpenCode progress with:

```sh
tail -f .agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.events.jsonl
```

The events show OpenCode-level progress such as tool calls, text deltas, reasoning deltas when exposed, retries, failures, and `session.idle`. They do not guarantee exact editor cursor location or exact source line unless OpenCode includes that in the event payload.
