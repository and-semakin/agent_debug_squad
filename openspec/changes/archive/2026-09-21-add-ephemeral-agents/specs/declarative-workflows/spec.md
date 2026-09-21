# declarative-workflows Delta

## ADDED Requirements

### Requirement: Ephemeral agents declare a one-shot lifecycle
Agent definitions SHALL accept an optional boolean `ephemeral` field, defaulting to false. An ephemeral agent declares a one-shot execution model for workflows: every invocation — every task attempt, every retry, and any future repeated visit such as a loop iteration — MUST execute on a newly created runtime whose backend session starts empty, and MUST NOT inherit conversation history, backend session identity, or runtime state from any earlier invocation of that agent, whether within the same execution or another. The declaration SHALL be captured in the execution's immutable saved agent configuration at submission, SHALL participate in the definition identity used for idempotent replay, and MUST be preserved through recovery so a recovered execution keeps its original flag value. In this version the flag MUST NOT relax graph validation: one agent may still be referenced by at most one task. It MUST NOT alter facilitator-driven manual turns, which retain backend session continuity exactly as for non-ephemeral agents, and it MUST NOT change HTTP response shapes.

#### Scenario: Declaration persists with the execution
- **WHEN** a workflow referencing an agent configured with `ephemeral: true` is submitted
- **THEN** the execution's saved agent configuration records the flag, and the persisted snapshot retains that value across recovery restarts

#### Scenario: Flag participates in definition identity
- **WHEN** the same request ID is resubmitted after the referenced agent's `ephemeral` value changed
- **THEN** the submission is treated as a changed definition and rejected, rather than replayed as the original execution

#### Scenario: Fresh runtime on every invocation
- **WHEN** a task attempt of an ephemeral agent is dispatched, and later a retry of that task is dispatched
- **THEN** each attempt runs on a new runtime whose backend session starts empty, with no conversation or session state carried between the attempts

#### Scenario: Default preserves behavior
- **WHEN** the `ephemeral` field is omitted or set to false
- **THEN** configuration loading, graph validation, scheduling, and observable results are indistinguishable from the behavior without this field

#### Scenario: One reference per task still enforced
- **WHEN** a definition references the same ephemeral agent from two different tasks
- **THEN** validation rejects the graph with the same reused-agent explanation as for non-ephemeral agents

#### Scenario: Facilitator continuity unchanged
- **WHEN** a facilitator sends consecutive manual turns to an agent declared ephemeral
- **THEN** the manual conversation preserves backend session continuity exactly as it does for non-ephemeral agents

#### Scenario: Non-boolean flag value is rejected
- **WHEN** an agent definition sets `ephemeral` to a non-boolean value
- **THEN** configuration loading fails with an actionable error and no workflow execution starts
