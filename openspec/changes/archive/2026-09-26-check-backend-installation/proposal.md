## Why

Missing executables and incomplete installations currently fail inside an agent attempt, potentially after other workflow tasks have already changed the workspace. Squad should report all missing local prerequisites for the requested work before admitting it, with concise official installation links.

## What Changes

- Require every adapter to implement a bounded, cancellable `CheckInstallation` contract using the effective launch configuration. Check Codex, Cursor, Kimi, managed OpenCode, and ZCode according to their actual launch requirements; fake and external OpenCode have no local installation requirement.
- Aggregate failures by effective installation configuration and report affected agent names, component, stable reason code, and official installation links without environment or proxy values.
- Gate new workflow admission across all referenced agents, manual turns across the selected agent, recovery/resume, target-only retry acceptance, and each later dispatch. Apply these gates to the existing one-shot run command as well. Keep pure configuration validation independent of installed tools.
- **BREAKING**: execution requests with unusable prerequisites return HTTP 503 before a run/execution is created, rather than accepting work that subsequently fails. Bare executable names resolve using the effective child PATH instead of accidentally using the server PATH.
- Defer external session initialization until requested use. Preserve manual conversation continuity, workflow fresh-session rules, idempotency, and conservative recovery.
- Integrate with the separately planned OpenCode managed/external lifecycle: complete local checks before any owned server startup; distinguish subsequent HTTP readiness from installation. Preserve its failed-start latch and shared ownership; report when explicit Squad restart is required. Retain its healthy:true readiness predicate without requiring version.
- Do not install/update tools, test model/auth availability, contact model providers, add a doctor CLI, broaden native Windows support, or redesign loops, one-shot terminal/signal semantics, or confidence thresholds.

## Capabilities

### New Capabilities

- `backend-installation`: adapter checks, effective executable resolution, bounded aggregate reporting, manual admission, and local-versus-service preflight boundaries.

### Modified Capabilities

- `workflow-lifecycle`: installation/readiness gates on admission, recovery and dispatch, with idempotency retained and recoverable installation holds.

## Impact

- `internal/adapters` and each adapter gain installation checks; shared domain result types and a small internal preflight helper avoid adapter import cycles. Launch and check resolution must share effective working directory and environment.
- `internal/orchestrator` changes eager initialization and manual/owned admission. `internal/workflow` gains context-aware preflight integration and gates. `internal/api` exposes structured 503 errors. `internal/config` remains structural-only; machine settings remain a startup snapshot.
- No new YAML keys, public CLI commands, or snapshot schema version. Existing `serve` startup failures and one-shot `run` pre-selection preflight failures retain exit code 2; installation holds on recovered workflows keep the API available. Additive structured HTTP error fields and optional persisted sanitized recovery diagnostics require tests.
- README and tests document installation links, PATH compatibility, recovery and limits. No new external dependency is required.
- Revision reviewed on 2026-09-26 against local 7d21ef5, main b78b530 (including one-shot run f584713), and archived managed OpenCode eedfa95 in c06b. Reconcile these baselines before implementation and spec sync; the checkout is not rebased by this planning change. OpenCode machine settings, proxy, snapshot and supervisor ownership remain defined by its separate change. Local inspection and service readiness have separate 30/60-second phase budgets, with cancellation and explicit restart-required diagnostics.
