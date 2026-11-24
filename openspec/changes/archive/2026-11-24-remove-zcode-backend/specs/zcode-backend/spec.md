## REMOVED Requirements

### Requirement: Explicit compatible configuration
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Persistent explicit turns
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Permission control
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Observable bounded work
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Documented host boundaries
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Wire compatibility is established at the correct boundary
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Host interactions remain responsive and owned
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Account authentication is local and identity bound
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Model selection requires selectable local evidence
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Turn recovery never replays prompts
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Owned background work and cleanup are explicit
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

### Requirement: Optional official MCP authentication is nonfatal
**Reason**: The private ZCode execution integration is deprecated and removed.
**Migration**: Select a supported backend and start a fresh Squad session; retain old artifacts for reference.

## ADDED Requirements

### Requirement: Removed backend fails with migration guidance
The system SHALL reject zcode agents during configuration normalization and direct adapter construction with an error stating that the backend is deprecated and removed and naming supported alternatives. It SHALL NOT launch ZCode, read its credentials or convert/delete historical sessions. Documentation SHALL explain the removal and fresh-session migration.

#### Scenario: Existing ZCode agent
- **WHEN** YAML or a workflow agent selects zcode, regardless of its options
- **THEN** validation rejects it before preflight or run allocation with migration guidance

#### Scenario: Direct construction
- **WHEN** a caller attempts to construct a zcode adapter directly
- **THEN** construction fails with the removal diagnostic without starting a process

#### Scenario: Historical artifacts
- **WHEN** a user upgrades and selects a supported backend in a fresh Squad session
- **THEN** old ZCode artifacts remain unchanged and are not reused as that backend's native session
