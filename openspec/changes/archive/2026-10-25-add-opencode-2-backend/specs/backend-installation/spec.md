## MODIFIED Requirements

### Requirement: Installation and service readiness are separate phases
All selected local installation checks SHALL finish successfully before any managed OpenCode server startup. After that gate, preflight SHALL check readiness for every required OpenCode service through the separately defined lifecycle/transport integration, including external services. OpenCode 1 readiness SHALL use GET `/global/health` with redirects disabled and require a 2xx JSON response containing healthy:true; an advertised version SHALL identify major 1, while absent version metadata remains compatible. OpenCode 2 readiness SHALL use GET `/api/info` with redirects disabled and require a 2xx JSON response advertising major 2. Version mismatches SHALL fail before task work. Unreachable/non-2xx services SHALL report service_unavailable; invalid health shape SHALL report service_incompatible. Managed startup failure SHALL report start_failed. Readiness SHALL NOT create sessions, submit prompts or certify all API methods/model compatibility. No agent task SHALL start until both phases pass. Failed initial managed startup, including initiating cancellation or timeout, SHALL preserve the existing failure latch until explicit Squad restart. The immediate cause SHALL retain its code and carry restart_required:true; subsequent live requests SHALL report restart_required without another launch. Local installation rejection before startup and external service errors SHALL NOT set a managed latch. Once managed startup succeeds, the shared server SHALL remain owned until Squad shutdown even when admission later fails or the caller cancels. Failed-start cleanup SHALL remain the supervisor's responsibility, and unrelated/external servers SHALL never be terminated. Existing bounded effective snapshot/config verification SHALL still precede session creation and work.

#### Scenario: Another missing backend prevents server startup
- **WHEN** a workflow selects valid managed OpenCode and a missing Kimi executable
- **THEN** installation reports Kimi failure and no OpenCode server starts

#### Scenario: External service fails health
- **WHEN** an external OpenCode server refuses the connection or returns an incompatible health response
- **THEN** preflight reports a readiness failure with official server documentation and does not call the backend locally missing

#### Scenario: Managed startup is permitted between gates
- **WHEN** every local prerequisite passes but a required managed server is stopped
- **THEN** preflight may start that owned server and wait for health, while all session creation and task prompts remain blocked
### Requirement: Checks are bounded and cannot perform model work
Each unique local check SHALL be bounded to five seconds. The local installation phase SHALL have a thirty-second budget including cleanup. Only after it passes, service readiness SHALL receive a separate sixty-second budget including managed failure cleanup; the existing thirty-second managed startup and up-to-ten-second effective-config check SHALL use that second budget. Thus the normal pass SHALL be bounded to ninety seconds, subject to earlier caller deadline/cancellation. A deadline that aborts initial managed startup SHALL report timed_out with restart_required:true if that runtime latched the failure; explicit cancellation SHALL analogously report cancelled. Timeout after successful startup SHALL NOT itself imply a startup latch. No more than four checks SHALL run concurrently. Failures in one group SHALL NOT skip independent groups while budget remains. On cancellation or deadline, the report SHALL preserve completed issues and explicitly classify unfinished groups as cancelled or timed_out. Probe output SHALL be bounded to 64 KiB and never forwarded raw. Installation checks SHALL NOT issue model/auth smoke requests, read credentials, auto-install/update anything, create backend conversations, or execute the selected backend CLI merely to request its version, except that managed OpenCode SHALL run a bounded `--version` probe to reject the wrong major generation before startup. A bundled ZCode probe may run the resolved Node interpreter only to inspect local files; it SHALL suppress environment-driven preload hooks for that inspector without altering the actual backend environment. Each service health request SHALL have a five-second deadline within the readiness phase budget; managed startup may retain its existing shorter per-poll limit. No successful evidence SHALL be cached across admissions, dispatches, retries or restarts.

#### Scenario: Inspector cannot preload account code
- **WHEN** the effective backend environment contains Node preload hooks
- **THEN** the installation inspector omits those hooks, while retaining effective resolved installation paths and leaving the subsequent backend environment unchanged

#### Scenario: Hanging probe
- **WHEN** one probe does not finish within five seconds and another backend has a missing executable
- **THEN** the aggregate contains timed_out and the independent executable failure within the overall budget, with no task started

#### Scenario: Request cancellation
- **WHEN** the submitting request is cancelled during preflight
- **THEN** checking stops, owned probe work is cancelled/cleaned up, uncompleted groups are identified and no execution/run is admitted

#### Scenario: Installation changes between turns
- **WHEN** a manual turn succeeds and the executable is then removed before another turn
- **THEN** the next turn performs a fresh check and is rejected before creating a run
