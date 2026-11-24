# ZCode Backend Specification

## Purpose

Define migration and explicit rejection behavior for the deprecated and removed ZCode execution backend while preserving historical artifacts.

## Requirements

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
