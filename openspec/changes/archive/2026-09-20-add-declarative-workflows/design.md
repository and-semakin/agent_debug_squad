## Context

See proposal.md for motivation. `Orchestrator.SubmitRun` reserves an agent, allocates a sequential run ID, persists a queued run and transcript entry, and starts a worker. `runWorker` streams artifacts, saves backend state, and publishes a terminal run. Different agents already execute concurrently. The current startup path marks queued/running records interrupted. `store.writeFileAtomic` syncs a temporary file, renames it, and syncs its directory; it does not provide a multi-file transaction. The current fake adapter and orchestrator tests provide a starting point for deterministic scheduling tests.

Only OpenCode permissions and ZCode backend behavior have main specs. Their completion, permission, cancellation, and session contracts remain applicable. The facilitator-only description in repository context becomes outdated with this addition and must be revised during implementation.

## Goals / Non-Goals

**Goals:** a single-process scheduler with durable decisions, predictable dependency evaluation, observable intervention, and independently owned backend sessions. A coordinator can submit a review graph and disconnect without stopping it.

**Non-Goals:** distributed scheduling, concurrent server owners, filesystem isolation, worktrees, write-intent detection, automatic retries, cyclic or dynamically expanded graphs, conditional expressions, artifact schema languages, guaranteed semantic correctness of LLM output, or exactly-once external side effects. Backend-native subagents remain governed by their adapters and are not workflow nodes.

## Decisions

### 1. Configuration and explicit submission

Extend the existing squad YAML with one optional `workflow` definition. Loading/serving configuration does not start it. `POST /workflows` explicitly creates an execution of the configured definition. This avoids accidental repeat reviews on server restart and keeps the existing CLI useful.

```yaml
session_name: review
workspace_dir: .
defaults:
  yolo: false
agents:
  - name: reviewer_a
    backend: codex
    startup_prompt: "Review code independently. Do not edit files."
  - name: reviewer_b
    backend: codex
    startup_prompt: "Review code independently. Do not edit files."
  - name: verifier
    backend: opencode
    startup_prompt: "Verify findings against the code. Do not edit files."
workflow:
  version: 1
  name: review-and-verify
  max_parallel: 2
  task_timeout_seconds: 1800
  tasks:
    review_a:
      agent: reviewer_a
      prompt: "Review the requested diff; report evidence and severity."
      allowed_to_fail: true
    review_b:
      agent: reviewer_b
      prompt: "Review the requested diff; report evidence and severity."
      allowed_to_fail: true
    verify:
      agent: verifier
      needs: [review_a, review_b]
      min_successful_dependencies: 1
      prompt: "Validate findings, remove duplicates, align severity, and report missing reviews."
```

`version`, nonempty `name`, positive `max_parallel`, and nonempty `tasks` are required. Each task has a nonempty `agent` and `prompt`. Defaults: `needs: []`, `allowed_to_fail: false`, `min_successful_dependencies: 0`, workflow timeout 1800 seconds. A task can override `timeout_seconds` with a positive integer. Timeout starts at dispatch and includes permission/subagent waits, not queue time. Zero does not disable it. These timeout defaults are design choices, not existing behavior.

Validate unknown workflow/task fields, duplicate YAML keys, identifier safety, references, repeated dependency entries, self-dependencies, cycles, and integer ranges before admitting any execution. Threshold is in `[0, len(needs)]`. An agent name can be referenced by only one task per definition; different agent names can have identical backend/model/options. No model-level uniqueness restriction exists. Stable lexicographic task ID order breaks ties among ready tasks; YAML map iteration is never scheduling policy.

Alternative: a standalone workflow language or arbitrary JSON graph submission. Reusing the existing config and a single configured definition minimizes the first version's surface. Editing config affects future submissions only; each execution stores its resolved definition and agent options.

### 2. Runtime ownership and isolation

Add `internal/workflow` with a scheduler that calls an internal execution interface implemented by `internal/orchestrator`. Never call the HTTP API from the scheduler. Introduce typed definition/execution/task/attempt structures in `internal/domain` and workflow parsing in `internal/config`.

Create a new workflow-owned runtime from the selected agent definition for each task attempt. Its storage identity includes execution ID, task ID, and attempt number using safe path components; its backend session starts empty. Do not load or reset the existing manual agent's state. A retry starts a fresh conversation and receives the same task instructions and dependency inputs. Manual conversations retain current continuity.

Own these runtimes through workflow APIs; reject manual turn/reset mutation of a workflow-owned runtime with 409. Expose workflow runs through existing run observation and permission endpoints, including workflow/task/attempt identity. Use separate ID namespaces to avoid collision with `run_000001` and its current numbering logic.

Each process admits at most one nonterminal workflow execution in v1. `max_parallel` bounds dispatched/running task attempts within it, including permission waits and cancelling attempts until the worker has stopped. It does not count backend-native subagents or independent manual runs. The skill must not launch unrelated manual work as a way to bypass the workflow limit. Multiple historical executions remain queryable.

No workspace scheduling locks: two runnable tasks can edit the same files. Instructions such as 'read only' are agent instructions, not a new sandbox guarantee.

### 3. Dependency evaluation and outcomes

For task T, wait until every direct dependency has settled. A dependency is acceptable only if it is `succeeded`, or `failed` with that dependency's `allowed_to_fail: true`. Then require the number of `succeeded` direct dependencies to meet T's threshold. A threshold never overrides a mandatory failure. Roots with threshold zero are immediately eligible.

Task states: `pending`, `ready`, `dispatching`, `running`, `succeeded`, `failed`, `interrupted`, `blocked`, `cancelled`. Reason fields distinguish dependency failure, threshold shortfall, timeout, output failure, and recovery uncertainty. `blocked` means no attempt ran because dependencies were unacceptable; its own `allowed_to_fail` does not waive this condition for descendants. Propagate blocking to a fixed point. `interrupted` represents an uncertain attempt, never an acceptable failure, and holds the execution for intervention.

The scheduler is a serialized reconciliation loop over persisted state. Notifications are wake-ups only; reconcile at startup, after control commands and run transitions, and periodically. Successful completion must be durably published before it enables dependents. Losing or duplicating a notification cannot lose work or dispatch a duplicate. Failure in one branch does not cancel independent branches.

Workflow states: `running`, `paused`, `needs_attention`, `cancelling`, `cancelled`, `succeeded`, `completed_with_errors`, `failed`. When all work settles, any blocked task or non-tolerated failed task makes the execution `failed`; otherwise tolerated failures produce `completed_with_errors`; otherwise it is `succeeded`. Recovery uncertainty or persistence damage produces `needs_attention`, not a misleading final verdict. Permissions are observable attention reasons while tasks remain running, without globally pausing healthy branches.

Alternative: trigger downstream tasks immediately after any threshold is reached. Rejected: the user requires all reviewer outcomes to be available before aggregation, including failures.

### 4. Durable handoff and success boundary

In v1 the guaranteed result is the adapter's nonempty final response. Publish a UTF-8 result file and an input manifest per attempt under the execution directory. The manifest lists every direct dependency in sorted task-ID order: task/agent/attempt/run IDs, final status, error, output path, byte size, and SHA-256 for successful responses. A failed dependency has no successful output even if partial text exists in diagnostic artifacts.

Build the downstream message from its own prompt plus a clearly delimited dependency manifest and explicit instructions to read the referenced local response files. Persist the exact message and manifest before dispatch. Do not paste entire unbounded responses or transcripts into prompts, silently truncate them, or interpret their text as scheduler instructions. Downstream agents receive no upstream backend chat histories.

Only verified files inside Squad's execution artifact area qualify as result references. Validate existence and hash before dispatch; a missing or changed committed result holds execution in `needs_attention`. Do not accept arbitrary agent-supplied paths as trusted handoff artifacts. Referenced repository files, patches, or external reports inside response text are not automatically snapshotted in v1; authors must account for their mutability.

A task succeeds only after a completed adapter turn, nonempty final response, stopped owned execution, and durable publication of the result and completion state. An empty response is `failed` with `missing_output`. A timeout becomes `failed` only after cancellation/cleanup is confirmed; inability to confirm cleanup becomes `interrupted`. LLM refusal or incorrect analysis expressed as ordinary text cannot reliably be detected automatically; the verifier task checks substantive quality. Arbitrary output validators are deferred.

### 5. Persistence and dispatch protocol

Use one versioned `workflow.json` snapshot per execution under `sessions/<session>/workflows/<execution>/`, including immutable resolved definition, request identity, revision, mode, tasks, attempts, dispatch reservations, session references, outcomes, and control history. The snapshot is authoritative for workflow decisions. Results, manifests, existing run files, and human-readable event logs are separate artifacts, not a second source of workflow truth.

Reuse atomic file replacement with file and directory synchronization. Serialize all snapshot mutations, including API controls and scheduler transitions. Hold an OS-backed exclusive session-directory lock before any startup mutation; a second process using the same session state must fail before dispatch or recovery. A workflow storage error stops new dispatch, exposes an in-memory error if possible, and never gets reported as durable success.

Protocol for each attempt:

1. Persist its input manifest and exact prompt; atomically reserve a stable attempt/run ID and `dispatching` state in the snapshot.
2. Ask the orchestrator to execute that specific ID. Refactor dispatch to reject duplicate IDs and to reserve a runtime before starting the worker. No new ID allocation on a repeated command.
3. Persist running state and backend identity as available. Execute through the existing adapters, sinks, progress, environment restrictions, and cancellation machinery.
4. Flush the final response; atomically commit its hash/reference and task outcome to the authoritative snapshot after worker cleanup. Project state into existing run records for inspection. A workflow must not infer authoritative success from a transcript event or run projection alone.

Crash after reservation but before/after backend send is deliberately conservative: without a committed outcome, mark the attempt interrupted and require intervention. v1 does not attempt backend-specific reattachment or automatic re-send. This sacrifices automatic progress in an ambiguous crash window rather than claiming exactly-once side effects.

Recovery reloads the saved definition, verifies committed artifacts, preserves completed tasks, and recomputes readiness. If there were no uncertain attempts and mode was running, it continues automatically; paused remains paused; cancelling continues cancellation and never resumes scheduling. If an active attempt was uncertain, hold all new dispatch with `needs_attention`. Separate workflow recovery from the current unconditional `MarkActiveRunsInterrupted` path so projections cannot overwrite authoritative workflow outcomes.

SQLite was considered. One owner and one atomic workflow snapshot avoid introducing a database and migration into this change. This is viable only with the explicit authority boundary above: do not spread scheduler truth over independently written task/run JSON files. A distributed or multi-owner future would revisit this decision.

### 6. API and control contract

New loopback routes (JSON):

| Route | Contract |
| --- | --- |
| `POST /workflows` | Body `{ "request_id": "..." }`; start the configured definition; 202 on creation, 200 for a replay of the same request and definition, 409 for reused ID with a changed definition or another nonterminal execution, 400 for invalid/missing definition or request ID. |
| `GET /workflows` | Execution summaries, including historical executions. |
| `GET /workflows/{id}` | Definition identity, revision, state, task/attempt states, blocked reasons, counters, progress, pending permissions and result paths. Supports `wait=true&timeout_seconds=N`. |
| `POST /workflows/{id}/pause` | Stop new reservations; already dispatched attempts finish. Idempotent while paused; 409 for terminal/cancelling state. |
| `POST /workflows/{id}/resume` | Revalidate artifacts/storage and resume a paused or needs_attention workflow; 409 while any recovery/artifact uncertainty is unresolved. |
| `POST /workflows/{id}/cancel` | Persist cancelling before cancelling workers; prevent downstream work; become cancelled when all workers stop. Optional `confirm_previous_stopped: true` records caller confirmation for all interrupted attempts when cleanup cannot be checked after restart. Repeated cancel is idempotent; other terminal states return 409. |
| `POST /workflows/{id}/tasks/{task}/retry` | Body `{ "request_id": "...", "expected_attempt": N }`, plus `"confirm_previous_stopped": true` for interrupted attempts. Reserve a new attempt; replay is idempotent, stale/conflicting requests return 409. |

Successful control calls return the current snapshot with 200; retry creation returns 202. Unknown executions/tasks return 404, invalid input 400, storage failures 5xx without claiming a committed action. Wait duration defaults to 30 seconds and must be an integer in 1..600; expiry returns 200 with current state and does not cancel work. Wait returns on terminal state or intervention (permission request, failed auto-approval, recovery uncertainty), within one second of local publication under normal operation. Cancellation is not tied to the requesting HTTP context.

Retry is permitted only for failed/interrupted tasks, before any transitive descendant has an attempt reservation, and outside cancelled/cancelling executions. Retry of a terminal failed or completed_with_errors execution can reopen it if no other execution is active. Create a queued attempt identity on acceptance; reserve dispatch only when scheduling permits it. Reset derived blocked descendants to pending and recompute; preserve independent completed work and prior attempts. A paused execution stays paused after retry. A retried execution otherwise runs if no unresolved attention reason remains. Once a tolerated failure has been consumed by a downstream attempt, reject retry; start a new workflow to obtain a different consistent result set.

For interrupted attempts, retry requires the caller to confirm prior backend work has stopped; cancellation can use the same assertion to close an execution after externally verified cleanup. The API records this assertion and cannot guarantee remote cleanup after a process crash. It does not override a worker known to remain active in the current process. Missing or damaged result files can be restored byte-for-byte and revalidated with resume; otherwise cancel and start a new execution. No generic 'mark succeeded' or 'ignore interruption' endpoint in v1. Whole-workflow cancel takes precedence over `allowed_to_fail`. The latter waives only known failed outcomes, not cancellation, interruption, storage damage, or missing artifacts.

### 7. Compatibility and facilitator behavior

Preserve manual YAML, CLI flags, endpoints, response meanings, artifact readability, and manual session continuity because the additive layer allows it. The key user requirement is that a familiar request for a reviewer quorum still suffices; the skill can translate it into an ordinary fan-out/fan-in graph without requiring workflow syntax from the user.

Update the repository-owned skill to configure distinct reviewer sessions, a verifier receiving all results, optional reviewer failures when appropriate, and a positive success threshold. Preserve user model, backend, tool, and review constraints. Explain model IDs must be checked in the user's environment. The verifier checks evidence against code, deduplicates, aligns severity, and reports missing reviews. The coordinator submits once, waits/handles intervention, and returns the final artifact. No runtime 'review mode' or implicit LLM graph planner is added to the Go service.

## Risks / Trade-offs

- Concurrent file edits can conflict -> documented author responsibility; no worktree or write lock feature is hidden in this change.
- A crash can leave external agent work alive -> interrupted attempts stop scheduling; explicit cleanup confirmation gates retry. No exactly-once promise.
- Snapshot rewrites grow with graph/history size -> acceptable for local small squads; do not store streams/output bodies in snapshots.
- Permission waits can last until timeout -> expose them immediately and make finite timeout configurable; retain existing approval rules.
- Filesystem corruption cannot always be repaired automatically -> stop dispatch and require intervention; never reconstruct success from partial logs.
- A tolerated failure can make a workflow finish without useful content -> callers use `min_successful_dependencies`; examples explicitly demonstrate all-reviewers-failed behavior.
- New runtime namespaces can accidentally reuse a manual conversation -> test identical model configurations, manual pre-existing state, separate executions and retries for session isolation.

## Migration Plan

Implement as an additive feature, keeping old state readable. Config without `workflow` continues to serve manual agents. Snapshot schema version 1 is mandatory; unknown versions fail closed. Existing histories are not imported into task sessions. Update repository docs/context and the distributed skill source together; installation into a user's personal skill directory is a separate action, not a side effect of server startup.

For rollback, stop the new server and its owned work before running an older binary. Old binaries can serve old manual configurations/state but cannot resume workflow executions. Keep workflow artifacts intact. No release/tag/push is part of this implementation handoff.
