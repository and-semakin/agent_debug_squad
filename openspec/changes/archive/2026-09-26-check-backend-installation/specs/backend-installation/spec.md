## Purpose

Detect unavailable local backend prerequisites before requested work starts, with bounded checks and actionable, credential-free diagnostics that distinguish installation from service readiness.

## ADDED Requirements

### Requirement: Checks use the effective launch configuration
Every backend SHALL provide an installation check. Checks SHALL use normalized agent options after machine defaults, the effective child environment and the same absolute working directory and executable resolution as the associated launch. For Codex, Cursor, Kimi and ZCode, a nil environment from the existing launch builder SHALL mean the captured full ambient environment, including PATH; a non-nil explicit environment SHALL remain explicit even if empty. This includes empty env/inherit_env options and inherit_env entries whose absent values leave the builder result nil. Managed OpenCode SHALL retain its separate baseline-allowlist environment contract. Machine configuration SHALL remain a server-startup snapshot; checks SHALL NOT reload backends.yaml. Within one pass identical installation configurations SHALL be checked once with all affected agent names attached. Distinct explicit command/runtime paths, modes, working directories or effective environments SHALL remain distinct. Agent name, prompt and model choice alone SHALL NOT prevent deduplication. Deduplication data SHALL remain private and memory-only.

#### Scenario: Agent override wins
- **WHEN** a machine default command is valid but a referenced agent explicitly selects a missing command
- **THEN** preflight reports that agent's executable failure and does not substitute the machine default

#### Scenario: Same backend with different installations
- **WHEN** three agents use the same backend, two with identical installation configuration and one with a different explicit command path
- **THEN** two checks occur and a failure for the shared configuration names both affected agents

#### Scenario: Effective environment differs
- **WHEN** two agents select the same bare command with different effective PATH values
- **THEN** they are checked independently against their respective child environments

#### Scenario: Machine edits need restart
- **WHEN** backends.yaml changes while the server is running
- **THEN** a subsequent preflight uses the already loaded machine settings and only a server restart picks up the edit

### Requirement: Executable resolution has explicit path semantics
Commands SHALL denote a single executable, without shell expansion or arguments embedded in the command string. Bare names SHALL search only absolute directories in the effective child PATH, in order; absent or empty PATH SHALL fail name resolution, empty PATH entries SHALL be ignored and any relative PATH entry SHALL fail the entire bare-name lookup with invalid_search_path, even if an earlier absolute entry contains the requested file. Explicit command paths SHALL bypass PATH validation unless a launcher interpreter requires name lookup. A command containing a path separator SHALL resolve directly; a relative command path SHALL be relative to the effective workspace. Invalid explicitly selected commands SHALL NOT fall back to other paths, backend defaults or aliases. Symlinks SHALL be followed to regular files, with broken links, loops, directories and unavailable execution access rejected. On supported macOS/Linux hosts checks SHALL assess effective-user execute access and known shebang interpreter requirements. Recognized interpreter chains SHALL be bounded and cycle-checked; unsupported shebang syntax SHALL report unsupported_launcher without executing wrapper contents. Successful checks SHALL NOT claim coverage of arbitrary wrapper dependencies or dynamic loader behavior.

#### Scenario: Child PATH is authoritative
- **WHEN** the server PATH contains codex but the agent's effective PATH does not
- **THEN** a bare codex command fails even though a server-level executable lookup would succeed

#### Scenario: Explicit relative path and spaces
- **WHEN** command is `./tools/my agent` and that regular executable exists under the workspace
- **THEN** the check and launch use that literal file, without splitting at the space or using the server's working directory

#### Scenario: Broken explicit path cannot be repaired by PATH
- **WHEN** an explicit absolute command is missing but a default executable exists in PATH
- **THEN** preflight reports not_found and never substitutes the PATH executable

#### Scenario: Symlink and file type boundaries
- **WHEN** command resolves to a symlink
- **THEN** a regular executable target passes, while a broken target, symlink loop or directory target fails before agent work

#### Scenario: Missing execution permission
- **WHEN** a regular command file exists but is not executable by the server user on macOS or Linux
- **THEN** preflight reports not_executable rather than installed

#### Scenario: Launcher interpreter missing
- **WHEN** a selected executable declares `/usr/bin/env node` but node is absent from the effective child PATH
- **THEN** preflight reports the interpreter prerequisite and does not start the launcher

#### Scenario: No implicit working-directory search
- **WHEN** effective PATH includes a relative directory or consists only of empty entries
- **THEN** preflight reports invalid_search_path or not_found respectively and does not find an implicit workspace executable

### Requirement: Backend checks reflect actual prerequisites
Codex, Cursor, Kimi and managed OpenCode SHALL check their effective executable and recognized launcher dependencies. A self-contained CLI SHALL NOT require a separately installed Node or Python solely because another distribution uses one. The ambient-versus-explicit environment rules SHALL be identical between checking and actual launch for every CLI backend. Fake and external OpenCode SHALL report that local installation is not required, without CLI discovery.

ZCode SHALL independently check the effective Node executable, readable runtime bundle and readable built-in provider configuration from effective environment variable ZCODE_BUILTIN_PROVIDER_CONFIG_FILE when nonempty, otherwise ../config/provider/zcode-builtin.json relative to the resolved runtime bundle directory. A relative override SHALL resolve against the effective workspace. It SHALL check required runtime structures with the existing structural compatibility rule, without loading/executing the runtime bundle, reading account/credential files or spawning app-server. Missing or ambiguous structures SHALL fail with unsupported_runtime; compatibility SHALL NOT depend on an exact hash/version allowlist. Missing independent components SHALL all be reported, without derivative duplicate issues for probes that cannot run. Installation success SHALL NOT imply authentication, account availability or model access.

#### Scenario: Multiple ZCode prerequisites missing
- **WHEN** both the configured Node executable and runtime bundle are absent
- **THEN** the report contains both component failures and installation links for Node and ZCode without attempting a runtime probe

#### Scenario: Structural compatibility without credentials
- **WHEN** a ZCode bundle has the required unambiguous structures and all local prerequisites but the account is signed out
- **THEN** installation checking succeeds without touching credential/account files, and authentication remains a later launch concern

#### Scenario: Unsupported bundle is not loaded
- **WHEN** ZCode runtime structures are missing or ambiguous
- **THEN** the check fails before loading the bundle or starting app-server and can include the non-secret bundle fingerprint

#### Scenario: External mode needs no CLI
- **WHEN** effective OpenCode mode is external and no local opencode executable exists
- **THEN** local installation checking reports not_required and HTTP readiness is evaluated separately

#### Scenario: Managed mode requires CLI
- **WHEN** effective OpenCode mode is managed and its configured command cannot be resolved
- **THEN** preflight fails before starting any owned server

#### Scenario: Fake remains self-contained
- **WHEN** only fake agents are selected and no real backend is installed
- **THEN** local checks succeed without filesystem prerequisites or network requests

### Requirement: Installation and service readiness are separate phases
All selected local installation checks SHALL finish successfully before any managed OpenCode server startup. After that gate, preflight SHALL check readiness for every required OpenCode service through the separately defined lifecycle/transport integration, including external services. Readiness SHALL use GET `/global/health` with redirects disabled and require a 2xx JSON response containing healthy:true. Version SHALL be optional metadata and SHALL NOT be required by this change; the existing managed lifecycle health predicate SHALL remain unchanged. Unreachable/non-2xx services SHALL report service_unavailable; invalid health shape SHALL report service_incompatible. Managed startup failure SHALL report start_failed. Readiness SHALL NOT create sessions, submit prompts or certify all API methods/model compatibility. No agent task SHALL start until both phases pass. Failed initial managed startup, including initiating cancellation or timeout, SHALL preserve the existing failure latch until explicit Squad restart. The immediate cause SHALL retain its code and carry restart_required:true; subsequent live requests SHALL report restart_required without another launch. Local installation rejection before startup and external service errors SHALL NOT set a managed latch. Once managed startup succeeds, the shared server SHALL remain owned until Squad shutdown even when admission later fails or the caller cancels. Failed-start cleanup SHALL remain the supervisor's responsibility, and unrelated/external servers SHALL never be terminated. Existing bounded effective snapshot/config verification SHALL still precede session creation and work.

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
Each unique local check SHALL be bounded to five seconds. The local installation phase SHALL have a thirty-second budget including cleanup. Only after it passes, service readiness SHALL receive a separate sixty-second budget including managed failure cleanup; the existing thirty-second managed startup and up-to-ten-second effective-config check SHALL use that second budget. Thus the normal pass SHALL be bounded to ninety seconds, subject to earlier caller deadline/cancellation. A deadline that aborts initial managed startup SHALL report timed_out with restart_required:true if that runtime latched the failure; explicit cancellation SHALL analogously report cancelled. Timeout after successful startup SHALL NOT itself imply a startup latch. No more than four checks SHALL run concurrently. Failures in one group SHALL NOT skip independent groups while budget remains. On cancellation or deadline, the report SHALL preserve completed issues and explicitly classify unfinished groups as cancelled or timed_out. Probe output SHALL be bounded to 64 KiB and never forwarded raw. Installation checks SHALL NOT issue model/auth smoke requests, read credentials, auto-install/update anything, create backend conversations, or execute the selected backend CLI merely to request its version. A bundled ZCode probe may run the resolved Node interpreter only to inspect local files; it SHALL suppress environment-driven preload hooks for that inspector without altering the actual backend environment. Each service health request SHALL have a five-second deadline within the readiness phase budget; managed startup may retain its existing shorter per-poll limit. No successful evidence SHALL be cached across admissions, dispatches, retries or restarts.

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

### Requirement: Manual use and validation remain independently usable
Pure YAML parsing and structural configuration validation SHALL remain possible without installed CLIs, filesystem installation discovery or HTTP checks. Serving a fresh squad SHALL NOT check all configured agents or create external sessions for unused agents. A manual turn SHALL check only its selected effective agent before creating a run or submitting agent work. Consecutive successful manual turns SHALL retain session continuity; checks SHALL NOT reset session identity. Reset SHALL defer creation of a replacement external session until a subsequent checked turn; force reset SHALL still cancel and join existing work. Ephemeral SHALL NOT mean disabled. This change SHALL introduce no enabled/disabled setting.

#### Scenario: Unused backend does not block work
- **WHEN** an unused ZCode agent has no installation and the selected manual agent uses fake
- **THEN** server startup and the manual turn succeed without checking ZCode

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

The official links SHALL be Codex `https://learn.chatgpt.com/docs/codex/cli`, Cursor `https://cursor.com/docs/cli/installation`, Kimi `https://www.kimi.com/code/docs/en/`, OpenCode `https://opencode.ai/docs/`, ZCode `https://zcode.z.ai/en/docs/install`, Node `https://nodejs.org/en/download`, and OpenCode readiness `https://opencode.ai/docs/server/`. OS-specific choices SHALL be delegated to these pages. Documentation SHALL distinguish upstream OS availability from Squad's supported macOS/Linux release targets. Native Windows local backend checks SHALL return unsupported_platform rather than applying Unix executable bits or claiming full support; fake/external checks SHALL require no local executable. WSL SHALL use Linux semantics. ZCode's implicit runtime location SHALL remain macOS-specific; Linux users SHALL configure runtime_path explicitly.

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
- **WHEN** Codex, Cursor, Kimi or ZCode has no effective env options and its default executable is on ambient PATH
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
