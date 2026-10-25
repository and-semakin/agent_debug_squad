## ADDED Requirements

### Requirement: Runtime generation boundaries
Classic protocol requirements in this specification SHALL apply to OpenCode 1, including the opencode alias. OpenCode 2 SHALL satisfy its own protocol specification with equivalent ownership safeguards. Each Squad SHALL own at most one managed runtime per generation; aliases SHALL share the v1 owner. Neither generation SHALL adopt or stop an unrelated server.

#### Scenario: Mixed generation squad
- **WHEN** v1, legacy-alias and v2 agents initialize concurrently
- **THEN** at most one v1 and one v2 managed process are launched and each agent retains its separate conversation
