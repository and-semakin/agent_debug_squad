## MODIFIED Requirements



### Requirement: Manual use and validation remain independently usable
Pure YAML parsing and structural configuration validation SHALL remain possible without installed CLIs, filesystem installation discovery or HTTP checks. Serving a fresh squad SHALL NOT check all configured agents or create external sessions for unused agents. A manual turn SHALL check only its selected effective agent before creating a run or submitting agent work. Consecutive successful manual turns SHALL retain session continuity; checks SHALL NOT reset session identity. Reset SHALL defer creation of a replacement external session until a subsequent checked turn; force reset SHALL still cancel and join existing work. Ephemeral SHALL NOT mean disabled. This change SHALL introduce no enabled/disabled setting.

#### Scenario: Unused backend does not block work
- **WHEN** an unused Kimi agent has no installation and the selected manual agent uses fake
- **THEN** server startup and the manual turn succeed without checking Kimi

#### Scenario: Pure validation without tools
- **WHEN** a structurally valid configuration is loaded and validated on a host without any backend CLI
- **THEN** structural validation succeeds without checking installation or reaching any server

#### Scenario: Manual rejection has no run side effects
- **WHEN** the selected manual agent has a missing executable
- **THEN** the API returns 503 before allocating/persisting a run, appending the new facilitator message or creating a backend session

#### Scenario: Concurrent reset invalidates admission
- **WHEN** force reset wins serialization while a manual preflight is in flight
- **THEN** the stale admission cannot launch work or reuse invalidated session state
### Requirement: Diagnostics are structured concise and safe
Preflight rejection SHALL return HTTP 503 with `error`, `code: backend_preflight_failed`, and `issues`. Each issue SHALL contain phase, backend, sorted affected agents, component, stable code, restart_required boolean, concise message and labeled documentation links in installation_links. Reports SHALL be deterministically ordered by phase, backend, agent list, component and code. Installation errors SHALL point to the relevant official product installation page; service errors SHALL point to official server documentation. They SHALL NOT include long installation instructions, raw environment/PATH/option values, proxy credentials, raw HTTP bodies or child output. Existing 400/404/409 and unrelated 500 semantics SHALL remain. Caller cancellation SHALL propagate internally and stop admission without attempting a synthetic response to the cancelled/disconnected HTTP request; no 408 or custom 499 response is promised. Existing executions SHALL retain sanitized failure/latch diagnostics for later observation. Server-side deadline expiry for a live request SHALL produce 503 with timed_out issues, including restart_required when applicable. Diagnostics SHALL identify the offending option source and may name built-in default commands, but SHALL omit arbitrary configured basenames as well as full paths. This stricter public preflight policy SHALL NOT remove the existing permission for operational paths in invocation provenance. A latched runtime diagnostic SHALL explicitly instruct the user to correct the cause and restart Squad. Actual CLI startup errors SHALL retain exit code 2, while recoverable workflow installation holds SHALL keep serve running.

The official links SHALL be Codex `https://learn.chatgpt.com/docs/codex/cli`, Cursor `https://cursor.com/docs/cli/installation`, Kimi `https://www.kimi.com/code/docs/en/`, OpenCode `https://opencode.ai/docs/`, Node `https://nodejs.org/en/download`, and OpenCode readiness `https://opencode.ai/docs/server/`. OS-specific choices SHALL be delegated to these pages. Documentation SHALL distinguish upstream OS availability from Squad's supported macOS/Linux release targets. Native Windows local backend checks SHALL return unsupported_platform rather than applying Unix executable bits or claiming full support; fake/external checks SHALL require no local executable. WSL SHALL use Linux semantics.

#### Scenario: Multiple actionable failures
- **WHEN** two backend installations are missing and two agents share one of them
- **THEN** one response reports both failures with all affected names and concise relevant official links

#### Scenario: Secret-bearing input cannot leak
- **WHEN** effective environment, endpoint or command options contain credential-like sentinel values and checks fail
- **THEN** no sentinel appears in the HTTP response, logs or persisted preflight diagnostics

#### Scenario: Product supports Windows but Squad does not certify it
- **WHEN** a native Windows source build checks a local CLI backend
- **THEN** it returns unsupported_platform and the official product link without claiming a Unix execute-permission failure or extending release support

#### Scenario: Executable disappears after checking
- **WHEN** an executable disappears after its final check but before process creation
- **THEN** the real launch fails under existing run failure/cleanup semantics without fallback, automatic prompt retry or a false success claim
### Requirement: Environment and readiness boundaries remain observable
Preflight SHALL distinguish retryable installation failures from runtime failures requiring explicit restart, and SHALL preserve launch environment inheritance when no explicit child environment is produced.

#### Scenario: Default CLI agents inherit ambient PATH
- **WHEN** Codex, Cursor or Kimi has no effective env options and its default executable is on ambient PATH
- **THEN** executable discovery uses that ambient PATH instead of reporting it absent

#### Scenario: Empty resolved inheritance retains nil semantics
- **WHEN** the existing CLI builder returns nil because none of the requested inherit_env variables exists
- **THEN** the check and launch use the captured ambient environment, rather than inventing a constrained empty environment

#### Scenario: Machine proxy changes effective PATH availability
- **WHEN** machine settings produce a non-nil child environment with proxy entries but no PATH
- **THEN** a bare command fails with guidance to configure PATH or an absolute command, without leaking the proxy

#### Scenario: Mixed PATH rejects the entire bare lookup
- **WHEN** PATH contains an absolute directory with the executable followed by a relative directory
- **THEN** a bare command fails with invalid_search_path while an explicit absolute command can still pass

#### Scenario: Local repair does not need a runtime restart
- **WHEN** stage 1 rejects a missing executable before managed startup is attempted and the installation is repaired
- **THEN** the same unaccepted request ID can be submitted again without a managed failure latch

#### Scenario: Startup cancellation is latched
- **WHEN** the initiating request disconnects during initial managed startup
- **THEN** startup is cancelled and cleaned up, no execution is admitted, and a subsequent live request receives 503 with restart_required:true and explicit Squad restart guidance without another launch

#### Scenario: Cold startup gets an independent budget
- **WHEN** installation takes twenty seconds and managed startup/config checks then take thirty-five seconds in total
- **THEN** no thirty-second whole-pass deadline truncates that otherwise successful readiness phase

#### Scenario: Versionless health remains compatible
- **WHEN** an OpenCode service returns 2xx JSON with healthy:true and no version
- **THEN** health checking succeeds without extending the existing managed health contract

#### Scenario: Ready server survives unrelated admission failure
- **WHEN** managed startup succeeds but an external service in the same pass fails
- **THEN** no task starts and the already healthy shared managed server remains available until Squad shutdown

## REMOVED Requirements

### Requirement: Backend checks reflect actual prerequisites
**Reason**: Reissued without retired ZCode execution scenarios.
**Migration**: Use the supported-backend requirement; ZCode configuration now fails before preflight.

### Requirement: Checks are bounded and cannot perform model work
**Reason**: Reissued without retired ZCode execution scenarios.
**Migration**: Use the supported-backend requirement; ZCode configuration now fails before preflight.

## ADDED Requirements

### Requirement: Supported backend checks reflect actual prerequisites
Codex, Cursor, Kimi and managed OpenCode SHALL check their effective executable and recognized launcher dependencies. A self-contained CLI SHALL NOT require a separately installed Node or Python solely because another distribution uses one. The ambient-versus-explicit environment rules SHALL be identical between checking and actual launch for every CLI backend. Fake and external OpenCode SHALL report that local installation is not required, without CLI discovery.





#### Scenario: External mode needs no CLI
- **WHEN** effective OpenCode mode is external and no local opencode executable exists
- **THEN** local installation checking reports not_required and HTTP readiness is evaluated separately

#### Scenario: Managed mode requires CLI
- **WHEN** effective OpenCode mode is managed and its configured command cannot be resolved
- **THEN** preflight fails before starting any owned server

#### Scenario: Fake remains self-contained
- **WHEN** only fake agents are selected and no real backend is installed
- **THEN** local checks succeed without filesystem prerequisites or network requests

### Requirement: Supported checks are bounded and cannot perform model work
Each unique local check SHALL be bounded to five seconds. The local installation phase SHALL have a thirty-second budget including cleanup. Only after it passes, service readiness SHALL receive a separate sixty-second budget including managed failure cleanup; the existing thirty-second managed startup and up-to-ten-second effective-config check SHALL use that second budget. Thus the normal pass SHALL be bounded to ninety seconds, subject to earlier caller deadline/cancellation. A deadline that aborts initial managed startup SHALL report timed_out with restart_required:true if that runtime latched the failure; explicit cancellation SHALL analogously report cancelled. Timeout after successful startup SHALL NOT itself imply a startup latch. No more than four checks SHALL run concurrently. Failures in one group SHALL NOT skip independent groups while budget remains. On cancellation or deadline, the report SHALL preserve completed issues and explicitly classify unfinished groups as cancelled or timed_out. Probe output SHALL be bounded to 64 KiB and never forwarded raw. Installation checks SHALL NOT issue model/auth smoke requests, read credentials, auto-install/update anything, create backend conversations, or execute the selected backend CLI merely to request its version, except that managed OpenCode SHALL run a bounded `--version` probe to reject the wrong major generation before startup. Each service health request SHALL have a five-second deadline within the readiness phase budget; managed startup may retain its existing shorter per-poll limit. No successful evidence SHALL be cached across admissions, dispatches, retries or restarts.


#### Scenario: Hanging probe
- **WHEN** one probe does not finish within five seconds and another backend has a missing executable
- **THEN** the aggregate contains timed_out and the independent executable failure within the overall budget, with no task started

#### Scenario: Request cancellation
- **WHEN** the submitting request is cancelled during preflight
- **THEN** checking stops, owned probe work is cancelled/cleaned up, uncompleted groups are identified and no execution/run is admitted

#### Scenario: Installation changes between turns
- **WHEN** a manual turn succeeds and the executable is then removed before another turn
- **THEN** the next turn performs a fresh check and is rejected before creating a run
