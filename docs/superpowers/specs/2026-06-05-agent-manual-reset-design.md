# Agent Manual Reset Design

## Summary

Agent Debug Squad will support a manual API operation for resetting one configured agent's logical backend session. The agent remains configured and available, but its backend conversation state is discarded so the next run starts as a fresh session.

## Goals

- Allow a facilitator to reset one named agent without restarting the whole squad service.
- Keep reset manual in this version. There is no automatic reset-after-run policy.
- Support safe reset for idle or failed agents.
- Support explicit force reset for an agent whose current run is stuck.
- Preserve existing run artifacts and transcript history.
- Make the next run after reset behave like the first run in a new backend session, including startup prompt injection.

## Non-Goals

- No automatic reset policy in YAML.
- No deletion of old backend sessions from external tools.
- No removal or rewriting of existing run artifacts.
- No dynamic agent creation or deletion.
- No global reset-all endpoint in this version.

## REST API

Add:

```text
POST /agents/{name}/reset
POST /agents/{name}/reset?force=true
```

The response is the updated `AgentState`.

Without `force`, reset is accepted only when the agent is not running. If the agent is busy, the API returns `409 Conflict`.

With `force=true`, the orchestrator cancels the active run for that agent, waits briefly for the worker to finish, marks the active run `interrupted`, and then resets backend session state. If the active run cannot be cancelled within the reset timeout, the API returns `504 Gateway Timeout` and does not report success.

Errors:

- `404 Not Found`: the named agent does not exist.
- `409 Conflict`: reset without `force` was requested while the agent is busy.
- `500 Internal Server Error`: adapter reset or persistence failed.
- `504 Gateway Timeout`: force reset could not finish cancelling the active run within the reset timeout.

## Orchestrator Behavior

The orchestrator gets a new method:

```go
ResetAgent(ctx context.Context, name string, force bool) (domain.AgentState, error)
```

Each `agentRuntime` tracks the currently active run id and a cancel function in addition to `busy`. `runWorker` uses a per-run context derived from the orchestrator root context, then passes that context to the adapter.

For idle or failed agents, reset:

1. Finds the runtime.
2. Calls the adapter reset operation.
3. Sets state to idle, clears `LastRunID`, clears `LastError`, and updates `BackendSessionID` according to backend behavior.
4. Saves `state.json`.
5. Appends an `agent_reset` event to `transcript.jsonl`.
6. Returns the updated `AgentState`.

For busy agents with `force=true`, reset:

1. Finds the runtime and active run id.
2. Calls the active run cancel function.
3. Waits for the existing worker to finish through the existing waiter mechanism.
4. Ensures the cancelled run is recorded as `interrupted`, not `failed`, when cancellation caused the stop.
5. Runs the same adapter reset and persistence steps as idle reset.

## Adapter Boundary

Extend the adapter interface:

```go
type AgentAdapter interface {
    Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
    Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error)
    Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error)
    Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
}
```

`Init` remains startup recovery from existing persisted state. `Reset` means intentional logical session discard.

Backend behavior:

- `codex`: clear `BackendSessionID` and `LastRunID`. The next `codex exec --json` creates a new thread, because no `resume` argument is passed.
- `opencode`: create a new HTTP session immediately and store the new session id in `BackendSessionID`.
- `kimi`: clear `LastRunID`, clear `BackendSessionID`, and set the agent idle. Kimi runs are already effectively per-turn in the current adapter.
- `fake`: create a new fake session id or reuse a deterministic fake reset id; tests only require that reset clears run continuity.

## Persistence And Transcript

`AgentState` after reset:

- `Status`: `idle`
- `LastRunID`: empty string
- `LastError`: null
- `BackendSessionID`: backend-specific new or empty value
- `CreatedAt`: unchanged

`transcript.jsonl` receives:

```json
{
  "type": "agent_reset",
  "agent": "Reviewer",
  "status": "interrupted",
  "metadata": {
    "force": "true",
    "previous_run_id": "run_000123"
  }
}
```

For non-force reset, `force` is `"false"` and `previous_run_id` may be omitted.

## Testing Strategy

Unit and handler tests cover:

- Idle reset returns updated agent state.
- Unknown agent reset returns `404`.
- Busy reset without force returns `409`.
- Force reset cancels an active fake run and records the run as `interrupted`.
- Reset appends an `agent_reset` transcript event.
- Codex reset clears backend session id and last run id.
- Kimi reset clears run continuity.
- OpenCode reset creates a new HTTP session and stores its id.

## Acceptance Criteria

- `POST /agents/{name}/reset` resets an idle or failed agent and returns updated state.
- `POST /agents/{name}/reset` returns `409` for a busy agent.
- `POST /agents/{name}/reset?force=true` interrupts the current run and resets the agent when cancellation completes.
- The next run after reset starts as a fresh backend session and includes the startup prompt.
- Existing artifacts and transcript entries remain intact.
- A new `agent_reset` transcript event records each successful reset.
