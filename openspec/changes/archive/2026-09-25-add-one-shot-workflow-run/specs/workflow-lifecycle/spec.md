## ADDED Requirements

### Requirement: One-shot invocation selects exactly one execution
The CLI SHALL provide `agent-debug-squad run --config <path> --request-id <id>` for explicitly running the configured workflow to completion. Both arguments SHALL be required; empty or whitespace-only request IDs, unknown flags, extra positional arguments, invalid configuration, or absence of a workflow SHALL fail before dispatch. The command SHALL NOT automatically update or re-execute its binary. Existing `serve` behavior, including explicit submission and remaining available after workflow completion, SHALL remain unchanged.

A new request ID SHALL create exactly one execution. Reusing the ID with the same resolved definition and agent fingerprint SHALL select the original execution without duplicating work; a changed fingerprint SHALL fail. Selection SHALL be protected by exclusive session ownership. Before recovery, backend initialization, or execution/run-record mutation, `run` SHALL reject damaged authoritative state, competing ownership, or any nonterminal execution not selected by that request. It SHALL NOT attach to, recover, cancel, or stop an unrelated execution. Manual sessions and unrelated historical run records SHALL NOT be initialized, reset, or rewritten by one-shot startup.

A selected nonterminal execution SHALL follow existing conservative recovery, pause, uncertainty, cancellation, and confirmed-retry rules. A selected terminal execution SHALL be reported without backend initialization, judge calls, listener creation, new attempts, or automatic retries; referenced committed successful artifacts SHALL be verified, with missing or corrupt artifacts reported as a command error without rewriting or replaying the execution.

#### Scenario: New batch invocation
- **WHEN** a valid `run` command names a new request ID in a session without nonterminal executions
- **THEN** exactly one execution is persisted and scheduled without requiring a separate POST request

#### Scenario: Completed request is repeated
- **WHEN** the same request and fingerprint select a terminal execution with valid committed artifacts
- **THEN** its existing outcome is reported and the process exits without backend work, a listener, or additional attempts

#### Scenario: Replay detects missing output
- **WHEN** a terminal replay references a missing or corrupt committed successful response
- **THEN** the command exits 1, identifies the artifact problem, and neither reruns the task nor changes the authoritative terminal outcome

#### Scenario: Request identity conflicts
- **WHEN** a saved request ID is reused with a changed resolved definition or agent fingerprint
- **THEN** startup exits 2 without recovery mutations or backend calls

#### Scenario: Foreign unfinished execution exists
- **WHEN** another request has a running, paused, needs_attention, or cancelling execution, even if the requested execution is already terminal
- **THEN** startup exits 2, identifies the conflict, and leaves that execution, its runs, and manual sessions unchanged

#### Scenario: Interrupted selected execution is recovered
- **WHEN** the selected request has an attempt without a committed outcome from an earlier process
- **THEN** recovery holds the same execution for attention under existing cleanup-confirmation rules and does not automatically resend the attempt

#### Scenario: Startup input or stored state is invalid
- **WHEN** arguments are invalid, no workflow is configured, or an authoritative snapshot is malformed or unsupported
- **THEN** the command exits 2 with actionable diagnostics before backend calls or dispatch

#### Scenario: Whitespace-only request identity is rejected
- **WHEN** the request ID is empty or consists only of spaces, tabs, or newlines
- **THEN** startup exits 2 before backend calls or dispatch

#### Scenario: Service lifecycle remains explicit
- **WHEN** `serve` starts with a workflow configuration and a submitted execution subsequently finishes
- **THEN** startup itself creates no execution and the service remains available after completion

### Requirement: One-shot control access is scoped to its execution
For a new or nonterminal selected execution, `run` SHALL bind the configured loopback address before backend initialization or dispatch. Failure to bind SHALL exit 2 without starting work or stopping the current port owner. It SHALL announce one-shot mode, selected execution identity, and control URL on stderr, including in quiet logging mode. Read-only observations and existing intervention controls for the selected execution SHALL remain available until teardown.

The HTTP submission behavior in "Workflow submission is explicit and idempotent" and the permission to reopen terminal executions in "Explicit retries preserve history and input consistency" SHALL describe `serve` mode; the one-shot admission and terminal-fence rules below SHALL apply instead in `run` mode. All other shared lifecycle requirements SHALL continue to apply unless explicitly qualified by mode.

In this mode, `POST /workflows`, manual run/reset mutations, mutations targeting any other execution, and permission replies targeting runs outside the selected execution SHALL return 409 with an explanatory mode error. Existing permission and workflow controls SHALL keep their normal validation and semantics for the selected nonterminal execution. Once that execution durably becomes terminal, `run` SHALL reject attempts to reopen it; this boundary SHALL be serialized with state-changing controls, including already-running HTTP handlers. These restrictions SHALL NOT change normal `serve` admission or terminal-retry behavior.

#### Scenario: Control listener cannot start
- **WHEN** another process owns the configured port
- **THEN** `run` exits 2 before dispatch, releases its acquired resources, and does not signal or contact that process to stop it

#### Scenario: Extra work is submitted
- **WHEN** a caller requests another workflow, a manual run/reset, a historical execution mutation, or a foreign run permission reply through the one-shot API
- **THEN** it receives 409 without creating work or changing the target records

#### Scenario: Selected execution needs intervention
- **WHEN** the selected execution is paused or held for attention or permission, including with quiet logging enabled
- **THEN** its normal observation, resume, cancel, eligible retry, verdict/loop control, and permission endpoints remain usable under existing guards, and stderr identifies one-shot mode, the selected execution ID, and the control URL

#### Scenario: Retry races with completion
- **WHEN** a retry of the selected execution races with its terminal commitment
- **THEN** a retry serialized first follows normal eligibility, while one serialized after terminal commitment receives 409 and cannot reopen work during shutdown

### Requirement: One-shot completion waits for a durable terminal outcome
`run` SHALL finish automatically when its selected execution durably reaches `succeeded`, `completed_with_errors`, `failed`, or `cancelled`, after owned-resource cleanup and final reporting. It SHALL NOT treat `paused`, `needs_attention`, `cancelling`, permission waits, an empty worker set, or a verdict string as terminal. There SHALL be no default overall execution timeout. Nonterminal intervention SHALL retain the process and control API and emit a notice when the intervention state or reasons change without busy-looping or repeated unchanged notices. Waiting SHALL NOT automatically resume, retry, approve permission, or confirm cleanup. Fatal persistence or runtime failure SHALL initiate error cleanup rather than wait indefinitely or report a successful outcome from uncommitted state.

#### Scenario: Degraded batch completes without manual stop
- **WHEN** optional reviewers fail, required final work succeeds with a recorded final verdict, and the workflow durably reaches completed_with_errors
- **THEN** the process saves its summary, cleans up, and exits automatically without a separate request to stop serve

#### Scenario: Paused workflow has no active workers
- **WHEN** a paused selected execution has no active workers but pending work
- **THEN** the process stays alive and waits for explicit resume or cancellation

#### Scenario: Attention long poll returns
- **WHEN** observation returns needs_attention or pending permission before terminal settlement
- **THEN** the one-shot process remains alive, reports the intervention, and does not equate the returned observation with completion

#### Scenario: Terminal persistence fails
- **WHEN** a terminal transition cannot be saved
- **THEN** the command stops new dispatch, performs error cleanup, exits 1, and reports the last durable state without claiming committed success

### Requirement: One-shot shell status distinguishes workflow and process outcomes
After successful cleanup and reporting, `run` SHALL exit 0 for `succeeded` or `completed_with_errors`, 1 for `failed`, and 3 for `cancelled` without a triggering signal. It SHALL exit 2 for argument, configuration, ownership, request-selection, listener-binding, or other pre-execution startup failure. Signal handling SHALL serialize its cancellation decision with terminal commitment. If the first SIGINT or SIGTERM cancellation decision is accepted while the selected execution is still nonterminal, it SHALL initiate cancellation and produce exit 130 or 143 respectively when cleanup/reporting succeeds. If a terminal outcome commits before that serialized decision, the command SHALL preserve its normal terminal result and exit mapping, even when the signal was received before the commit; signal receipt alone SHALL NOT determine precedence. Fatal runtime, artifact verification, persistence, summary/output, or cleanup errors SHALL produce exit 1, overriding any would-be success, cancellation, or signal code. Workflow outcomes SHALL NOT print CLI usage as though they were argument errors. Existing commands SHALL keep their existing exit behavior.

#### Scenario: Terminal state exit table
- **WHEN** separate runs settle as succeeded, completed_with_errors, failed, and API-cancelled with successful cleanup/reporting
- **THEN** they exit 0, 0, 1, and 3 respectively, with each exact workflow state retained in the summary

#### Scenario: Signal initiates cancellation
- **WHEN** the cancellation decision for the first SIGINT or SIGTERM is accepted at the serialized lifecycle boundary while the selected execution is still nonterminal
- **THEN** cancellation is requested and successful teardown exits 130 or 143 respectively while reporting the durable workflow state and signal separately

#### Scenario: Completion wins the signal race
- **WHEN** terminal commitment precedes a received SIGTERM
- **THEN** the command preserves the terminal outcome and its ordinary exit code without rewriting it as cancelled

#### Scenario: Signal arrives first but completion commits first
- **WHEN** SIGINT or SIGTERM is received while the execution is nonterminal, but succeeded, completed_with_errors, or failed commits before the queued cancellation decision is serialized
- **THEN** the committed outcome and its ordinary exit code are preserved, and the received signal does not rewrite the outcome as cancelled or force exit 130 or 143

#### Scenario: Cleanup fails after success
- **WHEN** the workflow succeeded but owned workers cannot be joined or final reporting fails
- **THEN** the process exits 1 and distinguishes its process failure from the committed workflow outcome

### Requirement: One-shot shutdown respects resource ownership and uncertainty
On terminal outcome, signal, or fatal runtime failure, `run` SHALL stop admitting mutations and perform graceful teardown with a shared 30-second maximum waiting budget, without restarting that budget for successive stages. For a nonterminal target, signal/error teardown SHALL attempt to durably request cancellation and prevent new dispatch while processing owned completions. It SHALL NOT automatically assert previous work has stopped. During teardown, a second or subsequent SIGINT or SIGTERM SHALL be ignored for lifecycle control: cleanup SHALL continue under its original deadline and exit-reason decision. Such signals SHALL NOT interrupt cleanup, restart the deadline, or bypass ownership and persistence checks.

Teardown SHALL close the owned HTTP listener and connections, stop scheduling and judge activity, cancel owned work as needed, join owned worker/readers, reap owned local child processes, and release session ownership after persistence and summary work. Normal terminal cleanup SHALL NOT change the execution to cancelled. Partial startup failures SHALL clean up acquired resources. Failure to join or verify required cleanup within the budget SHALL exit 1 and report outstanding work, preserving unresolved durable state for conservative recovery instead of inventing completion. Forced process death is outside graceful guarantees.

The command SHALL NOT kill processes by shared port, executable name, or unverified PID; stop another Squad owner; terminate an externally managed OpenCode server; or cancel unrelated backend sessions. It SHALL limit OpenCode cancellation to sessions owned by its attempts and retain remote identities/artifacts. Local worker shutdown or HTTP disconnect SHALL NOT be represented as proof that arbitrary remote tool side effects have stopped.

#### Scenario: Clean automatic teardown
- **WHEN** the selected execution commits a terminal result with all owned work settled
- **THEN** its listener closes, owned local processes/readers are joined, artifacts remain readable, session ownership is released, and the command returns without manual intervention

#### Scenario: Repeated signals leave cleanup running
- **WHEN** another SIGINT or SIGTERM arrives during an already initiated teardown
- **THEN** cleanup continues without interruption under its original deadline and exit-reason decision, subject to the existing cleanup/reporting-error override

#### Scenario: Initialization fails after resources are acquired
- **WHEN** judge or runtime initialization fails after acquiring ownership or binding the listener
- **THEN** the acquired resources are closed and a subsequent invocation can acquire the session and port

#### Scenario: Shared OpenCode server remains available
- **WHEN** a one-shot attempt is cancelled against an OpenCode server also serving unrelated sessions
- **THEN** only the owned attempt session is targeted for abort, the server and unrelated sessions remain available, and no global shutdown or process kill is issued

#### Scenario: Worker cannot be joined
- **WHEN** an owned worker fails to stop during the shared shutdown budget
- **THEN** bounded waiting ends with exit 1, outstanding run identities and incomplete cleanup are reported, and unresolved state is retained without automatic cleanup confirmation

#### Scenario: Cancellation cannot be persisted
- **WHEN** storage fails while signal-triggered cancellation is being recorded
- **THEN** new dispatch stops and owned cleanup is attempted, but the command exits 1 and reports the last durable state rather than fabricating cancelled

#### Scenario: Lock belongs to another owner
- **WHEN** the session ownership lock is held by another Squad process
- **THEN** `run` exits 2 without mutating workflow/run records or signalling the owner

### Requirement: One-shot final reporting preserves durable evidence
On orderly termination `run` SHALL emit one version-1 JSON summary to stdout, with logs and intervention/error diagnostics on stderr. Once an execution is selected, it SHALL atomically save the same derived report as `workflows/<execution_id>/run-summary.json` before stdout emission and ownership release. The report SHALL contain request/execution identity; last durable state/revision; exit code/reason and triggering signal; task counts and failed/blocked details; recorded verdicts with task, attempt, and iteration-path identity; attention reasons; session/workflow directories and snapshot/response/decision paths; cleanup status and outstanding run IDs/errors; and whether the summary was persisted. Unavailable identity/state before selection SHALL be null. A pre-selection failure SHALL NOT create a workflow summary file.

The report SHALL NOT replace authoritative snapshots, invent a single workflow verdict, embed private transcripts unnecessarily, or remove existing artifacts. It SHALL be a latest-invocation derived report, replaceable on replay and unused for scheduling/recovery. Summary persistence failure SHALL produce exit 1 with best-effort stdout/stderr reporting and `summary_persisted: false`. Output or final lock-release failures SHALL produce exit 1 with diagnostics; when they occur after the report was saved, shell status and stderr SHALL take precedence over that report's earlier planned exit code, without rewriting files after ownership release. Existing YAML and snapshot schemas SHALL remain compatible.

#### Scenario: Results survive exit
- **WHEN** a degraded workflow finishes with optional task errors and a final recorded verdict
- **THEN** its JSON summary identifies completed_with_errors, errors, verdict identities and output paths, and all existing response, decision, transcript, and snapshot files remain readable after process exit

#### Scenario: Summary write fails
- **WHEN** final summary persistence fails after the workflow snapshot was committed
- **THEN** the command exits 1, preserves the snapshot and other artifacts, and reports summary_persisted false with the failure

#### Scenario: Stdout is unavailable
- **WHEN** stdout fails after the summary file was saved
- **THEN** the command exits 1, reports the output failure on stderr, and retains the saved report as evidence of the workflow outcome

#### Scenario: Terminal replay replaces only the derived report
- **WHEN** the same terminal request is reported again
- **THEN** the latest-invocation summary can be replaced, while the workflow snapshot, attempts, artifacts, and unrelated histories remain unchanged
