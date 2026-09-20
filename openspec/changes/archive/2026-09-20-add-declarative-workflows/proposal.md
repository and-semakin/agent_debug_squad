## Why

Facilitators currently issue every agent turn, wait for its completion, and manually forward results. A persisted declarative task graph will let Squad execute chains and parallel review/aggregation workflows without an LLM deciding each scheduling step.

## What Changes

- Add an optional versioned YAML workflow with tasks, agent references, prompts, dependencies, and a maximum concurrency limit.
- Give each task a distinct backend conversation; different tasks may use identical backends/models. Dependencies carry explicit results rather than shared chat history.
- Schedule a static DAG automatically, including parallel fan-out and all-dependency fan-in.
- Add per-task `allowed_to_fail` (default false) and `min_successful_dependencies` (default zero). Tolerated failures remain visible and never count as successes.
- Persist execution decisions, attempts, input manifests, and final output references; recover conservatively without silently repeating uncertain agent work.
- Provide status/wait, pause/resume, cancellation, finite task timeouts, and explicit retries with history.
- Update the repository skill and examples so familiar review requests can produce a reviewers-to-verifier workflow. Review is an ordinary DAG, not a special execution mode.
- Keep existing manual configuration and run APIs as an additive implementation choice; prompt-level task compatibility is the user's requirement, not identical orchestration. Do not migrate existing agent histories into workflow conversations.
- Do not create worktrees, infer file-write intent, serialize workspace access, or prevent simultaneous edits. Workspace coordination remains the workflow author's responsibility.

## Capabilities

### New Capabilities

- `declarative-workflows`: Graph configuration, validation, task identity, scheduling, dependency policies, result handoff, and ordinary review composition.
- `workflow-lifecycle`: Persisted attempts, recovery, observable state, controls, idempotent submission, and compatibility boundaries.

### Modified Capabilities

None. Existing OpenCode permission and ZCode turn-completion/session contracts still apply to each owned run. Workflow tasks receive fresh conversations, while ordinary manual follow-up turns retain continuity.

## Impact

- `internal/config`, `internal/domain`: workflow definitions and execution records; existing agent options remain reusable.
- New `internal/workflow` scheduler above `internal/orchestrator`; adjust run dispatch to support durable identities and workflow-owned runtimes without routing through loopback HTTP.
- `internal/store`: authoritative atomic workflow snapshots and immutable attempt artifacts alongside existing readable run files; no database dependency is required for this single-owner first version.
- `internal/api` and CLI startup/shutdown: workflow creation, observation and control; exclusive state ownership; existing loopback and environment restrictions remain.
- `README.md`, `examples/`, and `skills/agent-debug-squad/SKILL.md`: executable examples, limits, recovery behavior, and prompt-to-workflow instructions.
- Update the facilitator-only wording in `openspec/config.yaml` during implementation to describe both explicit manual turns and declarative execution.
- This change contains planning artifacts only. Implementation, tests, skill installation, and archival belong to the implementing agent's subsequent work; no release is authorized.
