## Why

A finished batch workflow currently leaves `agent-debug-squad serve` running until someone stops it separately. A one-shot invocation should own one workflow execution, preserve its results, and exit automatically when that execution settles, including an acceptable `completed_with_errors` outcome.

## What Changes

- Add `agent-debug-squad run --config squad.yaml --request-id <id>` as an explicit one-shot entry point. Keep `serve` a long-lived, explicitly submitted service.
- Select exactly one execution using the existing request identity and definition fingerprint: create it once, recover the same nonterminal execution conservatively, or report its already terminal result without replaying work. Reject unrelated nonterminal state before recovery or backend initialization.
- Retain a loopback control API for the selected execution while running, paused, or needing attention. Prevent new executions, manual runs/resets, and mutations of unrelated historical executions through this one-shot process.
- Define terminal-only waiting, shell exit codes, signal cancellation, bounded graceful cleanup, and ownership boundaries for workers, child processes, and externally managed OpenCode servers.
- Preserve ordinary workflow artifacts and write a final machine-readable CLI summary with the durable state, outcomes, artifact locations, and cleanup result.
- Cover startup failures, cancellation races, storage failures, idempotent restart, and automatic exit after tolerated reviewer failures with regression tests and usage documentation.

Early quorum, judge policy changes, automatic retries, remote-server shutdown, and changes to workflow outcome derivation are outside scope.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `workflow-lifecycle`: Add an explicitly selected one-shot lifecycle, terminal waiting, scoped control access, exit reporting, and owned-resource cleanup alongside the existing service lifecycle.

## Impact

Changes will touch CLI dispatch/runtime wiring, workflow manager startup and observation, orchestrator initialization/worker draining, API admission policy, and summary persistence. Backend cancellation paths need targeted verification and fixes where owned child cleanup does not meet the lifecycle contract; the adapter protocol and external OpenCode server ownership remain unchanged.

The CLI addition is opt-in. Existing `serve` flags, YAML definitions, HTTP behavior in `serve`, snapshot schemas, manual session continuity, and release-version handling remain compatible. The one-shot API deliberately restricts mutations; its mode and target will be documented. The new `run-summary.json` is a derived artifact, not a replacement for `workflow.json`, and needs no snapshot migration. No new external service or dependency is required.
