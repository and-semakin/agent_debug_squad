## Context

See proposal.md for the user-visible problem. The existing packages already provide most execution semantics, but their startup and shutdown boundaries are service-oriented:

- `cmd/agent-debug-squad/main.go` offers `serve`, `version`, and `update`. `serve` waits for an HTTP error or process signal, not workflow completion. Its signal context is also the workers' parent context; shutdown uses separate five-second waits. `main` currently maps every returned error to exit 2 and prints usage.
- `workflow.Manager.Start` recovers all stored executions and can schedule a previous active execution immediately. `Create` implements request-ID/fingerprint replay. `Wait` returns for terminal state, attention, permissions, or timeout; it is not a terminal-only wait.
- `orchestrator.New` saves config, marks active runs interrupted, and initializes all manual agents, potentially making backend calls. Owned workflow attempts instead allocate fresh runtimes in `SubmitOwnedRun` and expose worker-stopped callbacks plus `WaitForWorkers`.
- `api.Server` currently admits new workflows and manual runs/resets. Historical terminal executions can be reopened by eligible explicit retries. A one-shot shutdown must close that race without changing `serve` behavior.
- `store.AcquireSessionOwnership` protects a session directory. `workflow.json` is authoritative; other artifacts remain readable on disk. A fatal storage error is held internally by the scheduler and must be exposed to the CLI without mistaking ordinary attention for a fatal error.
- `AgentAdapter` has Init/Send/Recover/Reset but no universal Close. CLI adapters own subprocesses; ZCode already closes an owned process group. OpenCode uses HTTP against a configured external server and aborts a particular session, not the server.

## Goals / Non-Goals

**Goals:** Compose existing workflow semantics into a single-execution process lifetime; make ownership, terminal commitment, intervention, and cleanup observable and testable. Keep the process alive for necessary intervention and reliably finish it after terminal settlement.

**Non-Goals:** Attach to or stop an existing Squad server; supervise an OpenCode daemon; guarantee exactly-once external effects; delete backend conversations; alter quorum, verdict judging, retry policy, or outcome derivation. A general-purpose process supervisor is outside scope.

## Decisions

### 1. Separate command with an explicit durable request identity

Use `agent-debug-squad run --config squad.yaml --request-id <id>`. Both values are required and empty or whitespace-only request IDs, unknown flags, and positional arguments are errors. The command runs the YAML workflow; it does not accept a second workflow definition or a server URL. A new request ID creates one execution, while reusing it with the same resolved definition selects the original execution. A changed fingerprint fails before backend activity. Exactly one execution is selected per invocation; idempotent replay can create zero new executions.

A separate command distinguishes process lifetime and API admission from `serve`. An `--exit-when-idle` service flag could exit before a submission or after an unrelated execution. A shell wrapper around HTTP polling cannot safely own a shared server's lifetime. No automatic update/re-exec happens in `run`: batch identity and stdout must remain stable; `serve` automatic update and explicit `update` keep their current behavior.

### 2. Preflight selection before any recovery or manual initialization

Load config and machine backend defaults, require a valid workflow, acquire session ownership, and inspect all authoritative snapshots before mutation or backend initialization. Resolve the request identity and fingerprint using the same normalization as `Create`. Reject damaged state, conflicting definitions, multiple nonterminal executions, or any nonterminal execution other than the selected request. Even a terminal replay fails when another execution is nonterminal. Do not invoke the current broad recovery path and then discover the conflict.

For a nonterminal/new target, bind the configured loopback host/port synchronously before backend calls or dispatch; a port conflict is a startup error, never permission to stop its owner. Establish cleanup immediately for each acquired resource. Construct an orchestrator in a workflow-only initialization mode: retain config persistence and run ID bookkeeping, but do not initialize, reset, or rewrite manual agent sessions or unrelated run records. Narrow interrupted-run reconciliation to the selected workflow where needed. Initialize the judge only when actual execution/recovery needs it. Start the manager with an explicit selection constraint, recover only the selected execution under existing conservative rules, or call `Create` once for a new request. Expose the API only after its immutable target is installed.

For an already terminal target, skip listener, judge setup, orchestrator, and recovery; load the durable outcome, verify referenced committed successful artifacts, produce the summary, and exit. Missing/corrupt committed artifacts cause exit 1 without rewriting the terminal snapshot as success or rerunning work. Existing terminal histories and manual sessions remain readable and unchanged.

Reusing the explicit request ID after a crash is the supported recovery path. Paused work stays paused; uncertain work needs existing confirmed retry/cancellation; a new request cannot silently take over that work. No snapshot schema or ownership-lock format changes are needed.

### 3. Scoped control API and an atomic terminal fence

The delta explicitly scopes the base HTTP-submission and terminal-reopening clauses to `serve`, with the added admission and terminal-fence rules taking their place for `run`. Other lifecycle rules remain shared unless qualified by mode, avoiding contradictory unqualified requirements after archive.

Reuse the configured loopback address and existing observation/control response shapes. Announce mode, target execution ID, and control URL on stderr even in quiet logging mode so intervention remains possible. In `run`, reject `POST /workflows`, manual run/reset mutations, mutations of other executions, and permission replies for runs outside the selected execution with HTTP 409 and an actionable one-shot-mode error. Read-only history remains available. The selected execution retains pause/resume/cancel, eligible explicit retries, verdict and loop controls, and permission replies while nonterminal.

An immutable admission policy is enforced inside the serialized manager/control boundary as well as the HTTP layer: after the selected execution durably reaches terminal state, no control may reopen it. A retry that serialized before terminal commitment follows existing eligibility rules; a retry after commitment is rejected. This avoids a view/cleanup race. Service mode retains terminal retry behavior and unrestricted normal admission. Drain HTTP requests only after admission closes, including requests already admitted to handlers but not yet committed.

### 4. Observe committed terminal state, not the HTTP wait condition

Add a manager observation surface returning a coherent view plus fatal persistence/runtime error; use bounded polling or revision notifications with a periodic reconciliation fallback. Keep the existing public HTTP `Wait` contract unchanged. The CLI observer waits on the selected ID and recognizes only `succeeded`, `completed_with_errors`, `failed`, or `cancelled` as workflow completion. Never infer completion from a verdict string, empty worker list, or task counts. A recorded final verdict is ordinary workflow output.

`paused`, `needs_attention`, pending permission, and `cancelling` keep the process and API available without a default overall timeout. Emit an intervention notice when its state/reasons change, not on every poll. Do not resume, approve, retry, or assert cleanup automatically. A fatal storage error, listener failure, or other inability to maintain the runtime triggers bounded cleanup and exit 1, with the last durable state reported separately. Only a committed terminal snapshot can support a successful final result.

### 5. Explicit exit contract

| Result after cleanup/reporting | Exit code |
| --- | --- |
| `succeeded` | 0 |
| `completed_with_errors` from tolerated failures | 0, preserving degradation in the summary |
| `failed` | 1 |
| Fatal runtime, artifact verification, persistence, reporting, or cleanup error | 1 |
| Invalid arguments/config, missing workflow, request conflict, ownership conflict, or other pre-execution startup failure | 2 |
| `cancelled` through API or previously persisted cancellation | 3 |
| SIGINT cancellation decision is serialized while nonterminal | 130 |
| SIGTERM cancellation decision is serialized while nonterminal | 143 |

Cleanup/reporting failure overrides a would-be success, cancellation, or signal exit with 1 and includes the triggering signal in diagnostics. The serialized cancellation decision, not signal receipt time, determines precedence. If a terminal outcome commits before that decision, its normal exit code is preserved even when the signal arrived first. If the signal cancellation decision is accepted while nonterminal, the corresponding signal code applies after successful cleanup/reporting. Preserve existing codes for other commands. Refactor CLI result handling so expected workflow failure/cancellation does not print usage; usage is for argument errors. No strict-on-tolerated-failure flag is needed for this change.

### 6. Own cancellation and bounded teardown

Keep the run's execution context independent of the signal notification context. On the first signal, serialize the cancellation decision against terminal commitment. If terminal commitment wins, retain its outcome and ordinary exit code, including when the signal was received earlier. Otherwise accept the signal cancellation decision while nonterminal, durably request cancellation, stop further ordinary dispatch, and leave the manager alive to process worker completions. Never pass `ConfirmPreviousStopped: true` automatically. On fatal runtime errors, prevent dispatch and attempt the same durable cancellation if storage permits; preserve the primary error if it does not.

Use a shared 30-second graceful shutdown budget starting at terminal detection, signal, or fatal runtime error; do not reset it per cleanup stage. Close mutation admission, gracefully shut down HTTP (close remaining connections on budget exhaustion), cancel only owned active work when necessary, let cancellation/completion persistence settle, stop the manager and in-flight judge calls, cancel remaining owned contexts, and join orchestrator workers and adapter readers/children. Normal terminal shutdown does not change the snapshot to cancelled. Initialization failure uses the same cleanup owner for whatever was actually acquired. A second or subsequent SIGINT/SIGTERM is ignored for lifecycle control: cleanup continues without interruption under the original deadline and exit-reason decision, subject to cleanup/reporting-error overrides; SIGKILL remains inherently outside graceful guarantees.

If joins or cancellation cannot complete, exit 1 after bounded waits, preserve uncertainty in the durable snapshot, and name outstanding run IDs and cleanup failures in the summary/diagnostics. Do not fabricate `cancelled`, success, or proof that external tool side effects stopped. Write the final derived summary after worker settlement (or the failed cleanup result), then release session ownership as the last resource before returning. Avoid `os.Exit` inside lifecycle functions so defers execute. A lock-release failure is reported as exit 1; after releasing ownership do not rewrite shared artifacts to avoid a new owner's write race.

Owned local processes must be reaped; adapter cancellation must address only process handles/groups established for those children, including descriptor-holding descendants where supported. Verify Codex/Cursor/Kimi cancellation and reuse ZCode's owned-group approach where needed, without broad PID/port/name matching. Fresh OpenCode sessions are owned attempt resources: cancellation can abort those session IDs and close their request/stream connections, but cannot shut down the external server, delete unrelated sessions, or treat an HTTP disconnect as proof all remote side effects ceased. Persist remote identities for manual diagnosis and use existing uncertainty/cleanup-assertion semantics. Do not add server launch/stop behavior or a universal adapter Close method unless a concrete owned resource requires it.

### 7. Final output is derived, durable, and separate from logs

Emit exactly one JSON summary on stdout on orderly `run` termination; lifecycle/intervention messages go to stderr. If an execution was selected, atomically save the same summary at `workflows/<execution_id>/run-summary.json` before stdout and lock release. It is a derived latest-invocation report, never read to decide recovery or dispatch; subsequent replay may replace it. Do not remove transcripts, prompt/manifests, responses, verdict decisions, or authoritative snapshots.

Define summary version 1 with request/execution identity (nullable execution ID for pre-selection errors), last durable workflow state/revision (nullable when unavailable), exit code and exit reason, triggering signal if any, task counts, failed/blocked task details, recorded verdicts with task/attempt/iteration-path identity, attention reasons, session/workflow directory and snapshot/response/decision paths, and cleanup status (`complete`, `incomplete`, or `not_started`) with outstanding run IDs/errors. Reuse execution-view projections instead of inventing a single workflow verdict or inlining transcripts. Include `summary_persisted`; on write failure emit a best-effort stdout summary with false, exit 1, and stderr diagnostics. A startup failure with no selected execution has no workflow summary file. Broken stdout yields exit 1 with stderr diagnostics; the persisted report remains available. Late output/ownership-release failures can leave the saved report showing the earlier planned exit code, so the shell status and stderr take precedence for those transport/teardown failures; workflow state is unaffected.

### 8. Verification strategy

Add CLI parsing/exit-table tests and subprocess tests using fake backends and temporary sessions/ports. Assert actual process exit, readable committed artifacts, listener closure, released ownership, and no extra attempts. Cover degraded review with a recorded final verdict, all terminal outcomes, pause/attention/permission intervention, same-request replay/recovery, request/port/lock conflicts, malformed state, forbidden API writes, and terminal-vs-retry/signal races, including signal receipt before a terminal commit that wins serialization. Verify that repeated SIGINT/SIGTERM leaves cleanup running under its original deadline, and that quiet-mode intervention stderr includes the mode, execution ID, and control URL. Use injected store/observer/listener failures for errors after startup and short injectable shutdown budgets for stuck workers. Use controlled child fixtures and an OpenCode HTTP stub to verify owned cleanup without terminating a shared server or mutating unrelated sessions. Existing service/manual-session tests must remain green. Run the repository-required race suite and vet before implementation handoff.

## Risks / Trade-offs

- [Intervention can leave `run` alive indefinitely] → This is deliberate for nonterminal states; print the control URL and reason, retain normal controls, and support signal cancellation.
- [Shared initialization refactoring affects `serve`] → Keep service defaults and test explicit submission, recovery, manual continuity, and terminal retry behavior independently.
- [Process groups and remote work have different cleanup guarantees] → Track ownership explicitly; test supported local platforms; preserve unresolved remote work instead of claiming certainty.
- [A terminal record can race with retry or signal] → Serialize selection/admission/terminal commitment and separate durable workflow outcome from process-exit reason.
- [Disk/output failure prevents a perfect final report] → Preserve the authoritative snapshot, fail the command, and state the missing evidence on stderr; never silently claim a clean completion.

## Migration Plan

This is an additive opt-in command with no state migration. Existing users keep `serve`; batch users replace the separate serve/submit/wait/stop sequence with `run` and retain their request ID for safe replay. Recovery can also use existing `serve` after the one-shot owner has exited. Older binaries ignore the derived summary and continue reading supported snapshots; they simply lack `run`. No release is published as part of this planning change.
