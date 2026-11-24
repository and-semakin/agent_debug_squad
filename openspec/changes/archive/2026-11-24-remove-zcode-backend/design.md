# Design

## Context

See proposal.md for motivation. Agent normalization is shared by YAML and workflow dispatch; the adapter factory is a second boundary. Machine settings and model discovery maintain independent backend lists. Skill installation hosts are independent of executable adapters.

## Goals / Non-Goals

Reject removed backend use before process creation and remove all private runtime integration. Preserve other backend behavior and historical artifacts. Do not automate Desktop, migrate native conversations, modify local credentials, or remove the independent ZCode skill host.

## Decisions

Delete internal/adapters/zcode, its fixtures, the probe and runnable example. Keep a small shared deprecation diagnostic in domain and reject zcode in normalization, factory, explicit model selectors and machine parsing (including empty sections). A warning-only approach would leave an unreliable executable path and contradict removal.

Remove ZCode machine fields and network translation rather than retaining inert settings. Existing global sections require explicit removal; silently ignoring them would conceal migration work. All discovery excludes ZCode; explicit discovery returns a migration error with the existing argument-error exit behavior.

Replace the orchestrator worker-cleanup regression's ZCode dependency with a supported adapter that fails Init deterministically. Other concurrency and cancellation paths stay unchanged. No persisted artifact schema changes or destructive migration are needed; users start a fresh Squad session when switching backends.

## Risks / Trade-offs

- Existing configs stop loading, even with an unused ZCode agent or section → explicit migration errors and release notes.
- Removing adapter tests could drop a generic lifecycle regression → preserve that regression using a supported backend.
- Desktop skill hosting could be confused with execution support → document the distinction and retain host management.

## Migration Plan

Remove zcode from backends.yaml, replace agents with supported backends and their own options, and start a fresh Squad session/state directory. Preserve old artifacts for reference. Publish the pre-1.0 breaking change as v0.24.0 after checks and CI. Rollback means explicitly installing an earlier release; it does not restore upstream runtime compatibility.
