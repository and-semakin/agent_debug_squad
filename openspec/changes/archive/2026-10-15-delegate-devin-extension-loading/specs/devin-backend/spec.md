## MODIFIED Requirements

### Requirement: Bounded owned work and honest cleanup
Cancellation, force reset, startup failure and normal turn teardown SHALL close and reap owned processes/readers within a five-second cleanup budget subject to the enclosing shutdown budget. Cancellation SHALL request native cancellation when possible before terminating the owned process group. Independent Devin processes and sessions SHALL remain untouched. Squad SHALL terminate its owned process group at teardown; native extensions SHALL NOT be rejected merely because their external effects cannot be isolated or reversed. Cleanup failure or uncertainty SHALL prevent successful completion. The system SHALL NOT claim that stopping a local process reverses or proves cessation of arbitrary remote side effects.

#### Scenario: Descriptor-holding child
- **WHEN** an owned shell descendant holds output pipes after the main process exits
- **THEN** cleanup terminates the owned group, closes and joins readers and finishes within its budget or reports cleanup failure

#### Scenario: Unrelated interactive session
- **WHEN** Squad cancels its Devin operation while a separate user Devin session is active
- **THEN** only Squad's owned resources are stopped

#### Scenario: Background completion is uncertain
- **WHEN** a native prompt ends while owned background work cannot be settled or accounted for
- **THEN** the run does not report success or promise all work has stopped

### Requirement: Native account and diagnostic boundaries
Squad SHALL NOT log in, install/update the CLI, rewrite native user/project configuration or auto-switch models to repair failures. It SHALL delegate native configuration loading, extension compatibility and migrations to Devin without extension-specific preflight refusals. Squad itself SHALL NOT rewrite real native settings; this does not prohibit native CLI side effects. Authentication and entitlement SHALL remain separate from executable availability. Credentials, proxy secrets and raw authentication/configuration messages SHALL NOT reach logs, saved diagnostics or model output envelopes. Documentation SHALL distinguish local tools from remote inference, plan/quota consumption, configuration imports, unsupported sandbox guarantees and the tested release/profile.

#### Scenario: Authentication is required
- **WHEN** a compatible installed CLI reports an explicit authentication requirement
- **THEN** the run fails with operator login guidance without opening a browser, starting authentication or persisting credential text

#### Scenario: Startup would migrate native configuration
- **WHEN** the selected launch profile is known to rewrite native configuration and no verified non-mutating launch is available
- **THEN** Squad does not reject it solely for native migration behavior and delegates loading to Devin without itself editing the source files

#### Scenario: Secret-bearing backend error
- **WHEN** backend diagnostics include a configured credential or credentialed proxy value
- **THEN** persisted diagnostics and public errors contain only safe classified information

## REMOVED Requirements

### Requirement: Content-aware extension inspection
**Reason**: Configuration and extension loading are delegated to Devin.
**Migration**: No Squad opt-out is required; retain native import settings as desired.

### Requirement: Import-aware extension inspection
**Reason**: Configuration and extension loading are delegated to Devin.
**Migration**: No Squad opt-out is required; retain native import settings as desired.

## ADDED Requirements

### Requirement: Native extension loading
Squad SHALL NOT inspect or reject native/project/imported configuration for hooks, MCP, plugins, lifecycle overrides or legacy migration keys. It SHALL preserve these settings in its private user configuration copy and leave native import resolution to Devin. Failure to read or parse the user config needed for that copy SHALL still fail safely; native launch failures SHALL remain failed operations.

#### Scenario: Active native extensions
- **WHEN** the user config contains hooks, plugins or legacy MCP entries
- **THEN** Squad preserves them in its private copy without a compatibility refusal or source mutation

#### Scenario: External or project settings exist
- **WHEN** external or project configuration is active, malformed, unreadable or contains lifecycle overrides
- **THEN** Squad does not preflight those files and lets Devin interpret them

#### Scenario: User configuration cannot be copied
- **WHEN** the primary user config is malformed or unreadable
- **THEN** Squad fails private-copy preparation without exposing its contents
