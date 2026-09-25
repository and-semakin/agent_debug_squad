## 1. CLI contract and safe selection

- [x] 1.1 Add `run --config --request-id` parsing and structured command outcomes; verify required arguments and empty or whitespace-only request IDs, unknown flags/positionals, all exit mappings, no auto-update, and unchanged existing command behavior with CLI tests.
- [x] 1.2 Add a read-only selection/preflight path under session ownership using existing definition/agent fingerprint semantics; verify new request, same-request replay, changed fingerprint, corrupt/unsupported snapshots, competing ownership, and unrelated nonterminal rejection before backend calls or record mutation.
- [x] 1.3 Add the terminal-replay path with committed successful artifact verification and no listener/judge/worker startup; verify no new attempts and unchanged snapshots/manual state for valid replay and exit 1 for missing/corrupt output.

## 2. Scoped runtime and API admission

- [x] 2.1 Add workflow-only orchestrator initialization and selected-execution recovery/startup; verify no manual agent Init/Reset, no unrelated interrupted-run rewrites, conservative same-request recovery, and unchanged service startup behavior.
- [x] 2.2 Bind the configured loopback listener before dispatch and register cleanup as resources are acquired; verify occupied-port and partial-initialization failures close acquired resources without touching the port owner.
- [x] 2.3 Add immutable one-shot API admission for the selected execution and its run permission replies; verify 409 for new workflows, manual runs/resets, foreign execution controls, and foreign permissions, while selected controls and read-only history remain usable.
- [x] 2.4 Serialize the one-shot terminal fence with manager controls and terminal persistence; verify racing retries cannot reopen a committed terminal target and ordinary `serve` terminal retry semantics remain intact.

## 3. Terminal observation and shutdown

- [x] 3.1 Expose coherent selected-execution observation and fatal manager errors without changing HTTP long-poll semantics; verify durable terminal detection, lost-notification fallback, storage failure propagation, and no false success from uncommitted state.
- [x] 3.2 Implement terminal-only CLI waiting and deduplicated stderr intervention notices including the mode, selected execution ID, and control URL in quiet mode; verify these stderr fields and that paused/attention/permissions remain live without busy polling, automatic approval, retry, resume, or default execution timeout.
- [x] 3.3 Separate signal notification from execution context cancellation and serialize the cancellation decision against terminal commitment; verify SIGINT/SIGTERM codes, preserved ordinary codes when a received signal loses serialization to terminal commit, persisted cancellation intent, ignored repeated SIGINT/SIGTERM with uninterrupted cleanup and the original deadline, and absence of automatic cleanup assertions.
- [x] 3.4 Implement one shared 30-second teardown budget for HTTP, manager/judge, owned workers, and partial startup resources; verify normal completion, listener failure, cancellation-save failure, stuck workers, preserved uncertainty, and ownership release ordering with injected short test budgets.
- [x] 3.5 Verify and repair owned subprocess/readers cleanup in CLI adapters where required, keeping process targeting limited to handles/groups created for the invocation; verify descriptor-holding child fixtures are reaped and unrelated processes survive on supported platforms.
- [x] 3.6 Verify OpenCode cancellation targets only owned attempt session IDs and closes owned HTTP streams; use an HTTP stub to assert no server shutdown/unrelated-session mutation and that unresolved remote cleanup is reported without fabricated confirmation.

## 4. Final report and persistent evidence

- [x] 4.1 Define version-1 CLI summary projection and atomic `run-summary.json` persistence; verify durable-state identity, counts/errors, verdict/iteration identities, artifact paths, signal/exit reason, cleanup details, and nullable pre-selection fields against representative snapshots.
- [x] 4.2 Route final JSON exclusively to stdout and logs/notices to stderr, preserving all existing artifacts; verify exactly one summary, replay replacement of only the derived report, and no use of that report in recovery.
- [x] 4.3 Implement summary-write, stdout, and final ownership-release error handling; inject each failure to verify exit 1, best-effort diagnostics, correct summary_persisted reporting, and no shared-file writes after ownership release.

## 5. Integration coverage and documentation

- [x] 5.1 Add fake-backend subprocess regressions for all terminal outcomes, especially optional reviewer failures plus a recorded final verdict; verify actual process exit without manual stop, correct shell codes, closed listener, reacquirable session lock, and readable final artifacts.
- [x] 5.2 Add integration cases for pause/resume, attention/permission intervention, crash recovery, cancellation, and concurrent terminal/control/signal boundaries; verify one execution identity, no unintended attempts, preserved manual sessions, and continued `serve` availability after completion.
- [x] 5.3 Update README CLI/workflow/shutdown sections and add or adapt a fake-backend example; verify documented commands, request-ID replay, exit-code table, intervention controls, ownership limitations, summary path, and migration from serve/submit/wait/stop match the implemented behavior.

## 6. Required validation before handoff and archive

- [x] 6.1 Run `gofmt` on changed Go files and `go vet ./...`; verify formatting and vet complete successfully.
- [x] 6.2 Run `go test -race -count=1 ./...`; verify the full suite passes, including new process-lifecycle and existing service/manual-session tests.
- [x] 6.3 Check the implementation against every delta requirement and scenario, including the explicit serve/run scope of base submission and terminal-reopening clauses, recording any coverage gaps; verify no scope expansion into quorum, judge policy, automatic retries, or external server supervision.
- [x] 6.4 Run `openspec validate add-one-shot-workflow-run --strict`, reconcile artifacts and task checkboxes with actual implementation, and verify all required checks before a separately requested archive.
