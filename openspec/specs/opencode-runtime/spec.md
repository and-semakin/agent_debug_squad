# opencode-runtime Specification

## Purpose

Own reusable OpenCode servers with enforceable runtime configuration and conservative session recovery without disturbing independently operated servers.

## Requirements

### Requirement: Owned server scope and readiness
Managed mode SHALL launch one owned OpenCode serve process per Squad (orchestrator) in its fixed workspace, reuse it across agent models and workflow attempts, bind only loopback with discovery disabled, select a port without a preallocation race, authenticate its own connection, and await bounded readiness before accepting work. Missing executable, early exit, cancelled startup, invalid announcement and readiness timeout SHALL fail safely with sanitized diagnostics and clean up owned resources. A failed first startup, including cancellation of its initiating request, SHALL remain failed for that runtime until Squad is explicitly restarted; later requests SHALL NOT retry startup. Squad-level cancellation SHALL close the runtime. Cancelling a request after successful startup SHALL NOT stop the shared server.

#### Scenario: Concurrent agents reuse a process
- **WHEN** ordinary and workflow agents with different models initialize concurrently in one Squad
- **THEN** exactly one owned server is used and each agent has its own session and request model

#### Scenario: Initiating request cancels startup
- **WHEN** the first initialization request is cancelled while startup is in progress and Squad remains active
- **THEN** any launched child is reaped, later initialization fails without a second launch, and an explicit Squad restart is required

#### Scenario: Another server already occupies the preferred port
- **WHEN** an independent server is listening when managed mode starts
- **THEN** Squad uses its newly launched server's actual bound port and neither adopts nor stops the independent server

#### Scenario: Startup cannot complete
- **WHEN** the child exits, is unavailable, or never becomes healthy before timeout
- **THEN** initialization fails without submitting a prompt and any started owned process is reaped

### Requirement: Effective snapshot setting precedes work
Snapshots SHALL default to false and explicit true SHALL enable them. Managed mode SHALL merge the setting into runtime inline config without writing user/project files, removing snapshots, or replacing other settings. Preservation SHALL mean semantic property values; comments and whitespace in inherited JSONC need not survive serialization into the child environment. Both modes SHALL verify the effective boolean for the selected workspace before session creation, saved-session recovery and each prompt. Missing, unreadable or conflicting values SHALL fail closed without claiming application; external mode SHALL never patch configuration.

#### Scenario: Preserve configuration while disabling snapshots
- **WHEN** inherited inline config and project config contain other settings
- **THEN** those settings survive and the effective snapshot value is verified false before creating a session

#### Scenario: Administrator override conflicts
- **WHEN** effective config disagrees with requested snapshot or cannot be checked
- **THEN** work fails before prompt submission with a safe configuration diagnostic

#### Scenario: Backend would rewrite loaded configuration
- **WHEN** a known loaded config is schema-less, legacy, unreadable or cannot be safely inspected
- **THEN** managed launch fails before starting OpenCode and leaves the file unchanged with a migration diagnostic

#### Scenario: Explicit opt in
- **WHEN** machine snapshot is true
- **THEN** effective true is required before work; existing snapshot data is untouched

### Requirement: Owned lifecycle and conservative recovery
Cancellation, startup failure and ordinary shutdown SHALL release and reap only owned processes with bounded graceful termination. Resetting an agent or completing an individual workflow attempt SHALL preserve the shared server. A dead child SHALL cause operations to fail without automatic restart or replay. Restarted Squad SHALL launch a new owned process and validate saved session IDs in the same workspace/data scope; unavailable IDs SHALL fail rather than silently start new conversations. Uncertain prompts SHALL never be automatically resent by the adapter.

#### Scenario: Stop the owner
- **WHEN** Squad is cancelled or startup fails after launching OpenCode
- **THEN** owned child resources are released and external servers remain running

#### Scenario: Child dies during a prompt
- **WHEN** the owned child dies after accepting a prompt
- **THEN** the run fails without replay and subsequent operations fail until an explicit Squad restart

#### Scenario: Recover a saved identity
- **WHEN** Squad restarts with a saved OpenCode session ID
- **THEN** it verifies that session and preserves it, or fails if unavailable, without resending interrupted work

### Requirement: Private runtime settings and output
Machine proxy values, proxy credentials, generated authentication credentials and raw process diagnostics SHALL NOT enter logs, persisted configuration, run artifacts or errors. HTTP transport from Squad SHALL bypass proxy environment variables. Child environment SHALL include only available baseline variables HOME, PATH, TMPDIR, TMP, TEMP, XDG_CONFIG_HOME, XDG_DATA_HOME, XDG_CACHE_HOME and XDG_STATE_HOME, explicitly inherited variables, runtime overrides and machine networking settings. HTTP requests in both modes SHALL NOT follow redirects, so endpoint selection and authentication are not delegated to a redirect target; operators SHALL configure the final base_url.

#### Scenario: Credentialed proxy fails
- **WHEN** a child or API response reports a configured proxy URL or credentials
- **THEN** diagnostics and persisted output redact those values; startup errors never include raw child output

#### Scenario: Ambient proxy is not opted in
- **WHEN** ambient HTTP_PROXY is set without machine proxy_url or explicit inheritance
- **THEN** it is absent from the child and Squad connects directly to OpenCode

#### Scenario: Redirect does not forward a request
- **WHEN** an OpenCode endpoint returns a redirect
- **THEN** Squad treats the response as an error and makes no request to the redirect target

### Requirement: External workspace and recovery contract
External servers SHALL expose the same workspace at the same absolute path as Squad, on the same machine or an equivalently mounted filesystem. Both modes SHALL send x-opencode-directory with Squad's workspace on instance requests. Saved-session recovery in both modes SHALL require a matching session ID and a nonempty directory resolving locally to the same canonical workspace path. Missing, inaccessible or different directories SHALL fail without replacing the conversation or replaying work. External servers with a different filesystem root SHALL be unsupported; there SHALL be no implicit path mapping or fallback to server cwd.

#### Scenario: External session shares the workspace
- **WHEN** a saved external session reports the configured workspace or a locally resolvable symlink to it
- **THEN** recovery preserves its ID and subsequent requests remain scoped to Squad's workspace

#### Scenario: External session uses a different root
- **WHEN** a saved external session reports a directory absent locally or different from Squad's workspace
- **THEN** recovery fails with a workspace diagnostic without creating a replacement session or submitting a prompt
