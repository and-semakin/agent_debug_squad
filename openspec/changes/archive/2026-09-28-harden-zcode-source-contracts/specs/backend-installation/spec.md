## MODIFIED Requirements

### Requirement: Backend checks reflect actual prerequisites
Codex, Cursor, Kimi and managed OpenCode SHALL check their effective executable and recognized launcher dependencies. A self-contained CLI SHALL NOT require a separately installed Node or Python solely because another distribution uses one. The ambient-versus-explicit environment rules SHALL be identical between checking and actual launch for every CLI backend. Fake and external OpenCode SHALL report that local installation is not required, without CLI discovery.

ZCode SHALL independently check the effective Node executable, readable runtime bundle and readable built-in provider configuration from effective environment variable ZCODE_BUILTIN_PROVIDER_CONFIG_FILE when nonempty, otherwise ../config/provider/zcode-builtin.json relative to the resolved runtime bundle directory. A relative override SHALL resolve against the effective workspace. It SHALL check required runtime structures with the existing structural compatibility rule, without loading/executing the runtime bundle, reading account/credential files or spawning app-server. Missing or ambiguous structures SHALL fail with unsupported_runtime; compatibility SHALL NOT depend on an exact hash/version allowlist. Missing independent components SHALL all be reported, without derivative duplicate issues for probes that cannot run. Installation success SHALL NOT imply authentication, account availability or model access. Start/Individual plan-policy selection SHALL NOT add account or billing checks to installation. Relative runtime and provider paths SHALL resolve consistently against the effective workspace for both checking and launch; no unrelated runtime fallback SHALL occur.

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
#### Scenario: Start-first installation is account free
- **WHEN** an agent selects start-first policy
- **THEN** local preflight checks the same runtime prerequisites without reading account data, requesting a balance or starting app-server

