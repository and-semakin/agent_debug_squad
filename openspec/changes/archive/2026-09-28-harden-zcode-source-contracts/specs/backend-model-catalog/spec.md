## MODIFIED Requirements

### Requirement: ZCode discovery fails honestly without a verified safe bridge

ZCode discovery SHALL use only an independently verified session-free, configuration-read-only bridge path with structural compatibility checks before runtime loading or native credential access. An internal tool display named list_models SHALL NOT be treated as a public API. Discovery SHALL NOT create/resume a session, execute the ListModels agent tool, refresh login, mutate account configuration, or use connectivity inference probes. Missing, ambiguous or unsafe structures SHALL yield unsupported with a safe reason rather than a fabricated empty catalog. Until safe initialization is verified, read_only_catalog_unavailable SHALL be an accepted explicit unsupported outcome. Static provider files or old conversation diagnostics SHALL NOT silently substitute for the effective catalog. Verified metadata SHALL retain truncation and disabled state; choices unsupported by Squad's execution adapter SHALL be labelled accordingly.

#### Scenario: Installed runtime only exposes conversation-based listing
- **WHEN** a runtime has list_models display code but no verified session-free read-only bridge path
- **THEN** discovery returns unsupported and creates no conversation or credential/configuration mutation

#### Scenario: Compatible verified registry path
- **WHEN** a structurally compatible bridge has passed read-only lifecycle verification and returns registry records
- **THEN** discovery returns only allowed metadata, preserves truncation, and marks unsupported execution selections instead of promising they can run

#### Scenario: Plan eligibility is not the complete model catalog
- **WHEN** execution routing can read model-specific Start billing buckets but no full session-free effective registry source is verified
- **THEN** generic ZCode discovery remains unsupported with read_only_catalog_unavailable rather than presenting those buckets as a complete catalog or initializing the execution auth bridge

