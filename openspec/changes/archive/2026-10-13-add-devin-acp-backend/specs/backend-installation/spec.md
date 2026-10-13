## ADDED Requirements

### Requirement: Devin installation is a local executable check
Devin SHALL check the effective single executable and recognized launcher dependencies using the same explicit environment and absolute workspace as execution. A native binary SHALL NOT require Node or Python merely because unofficial clients use them. The check SHALL reuse existing admission, deduplication, timing, platform and diagnostic rules and SHALL NOT execute the CLI, read credentials, authenticate, list models, create sessions or install/update software. Its official installation link SHALL be https://docs.devin.ai/cli. Ready SHALL mean local prerequisites are available, not that the executable is an authenticated compatible official release. Protocol compatibility SHALL be checked separately before work.

#### Scenario: Missing Devin
- **WHEN** the effective Devin executable is absent
- **THEN** preflight rejects work with affected agent names and the official CLI installation link, without launching or installing anything

#### Scenario: Native binary without language runtimes
- **WHEN** a valid self-contained Devin binary exists but Node and Python do not
- **THEN** installation succeeds without inventing those dependencies

#### Scenario: Native Windows is outside Squad release support
- **WHEN** a native Windows source build checks Devin installation
- **THEN** it reports unsupported_platform and the official product link while documenting upstream Windows support separately
