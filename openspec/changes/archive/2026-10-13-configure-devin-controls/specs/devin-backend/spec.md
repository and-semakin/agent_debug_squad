## MODIFIED Requirements

### Requirement: Verified selection and permission controls
Configured Devin agents SHALL require a nonblank model. Model, reasoning, speed and mode SHALL be applied and verified on new and resumed sessions before prompting. Defaults SHALL be high reasoning when offered (otherwise the advertised native default), standard speed, and bypass mode with yolo true compatibility. Unsupported explicit choices or conflicting mode/yolo SHALL fail before prompting. Permission replies SHALL remain run-scoped offered once/reject decisions, without persistent grants.

Selections SHALL NOT override administrator restrictions. Sandbox, cloud and arbitrary CLI arguments SHALL remain unsupported.

Native permission requests SHALL be visible through the existing run-scoped permission interface with only supported offered decisions. The initial backend SHALL expose no persistent grant that mutates native configuration. Foreign, duplicate, inactive and stale replies SHALL be rejected. Questions or authentication interactions that Squad cannot represent SHALL fail explicitly without fabricated responses. YOLO SHALL NOT imply OS sandboxing or permission to override unexpected native approval requests.

#### Scenario: Resumed session uses another model
- **WHEN** explicit options.model differs from a loaded session's saved model
- **THEN** the requested selection is applied and verified before prompting, or the operation fails without fallback

#### Scenario: Manual approval
- **WHEN** a normal-mode owned tool requires permission
- **THEN** progress exposes waiting_for_permission and an offered once or reject reply is translated to the exact native option

#### Scenario: Approval arrives after cancellation
- **WHEN** the run is cancelled before a pending reply arrives
- **THEN** the reply is rejected and cannot authorize another run or native session

#### Scenario: Required mode cannot be established
- **WHEN** the installed CLI does not offer an identifiable supported normal or bypass control requested by the configuration
- **THEN** no prompt is submitted and the error explains the unsupported capability

#### Scenario: Missing model
- **WHEN** a configured Devin agent omits model or supplies a blank or non-string model
- **THEN** configuration fails before any native session; standalone model discovery still works without model

#### Scenario: Default controls
- **WHEN** only model is configured
- **THEN** the adapter selects and verifies that model, chooses high reasoning if offered or the offered native default, standard speed, and bypass mode

#### Scenario: Model-specific controls
- **WHEN** selecting a model changes the offered reasoning or speed controls
- **THEN** subsequent choices use the returned controls and the final response still confirms all requested controls before any prompt

#### Scenario: Model lacks optional controls
- **WHEN** a model has no reasoning or speed selector
- **THEN** omitted reasoning and standard speed are valid, but explicit reasoning or fast speed fails before prompting

#### Scenario: Explicit mode and compatibility alias
- **WHEN** mode is explicit and yolo is omitted
- **THEN** mode wins over the implicit or inherited yolo default
- **WHEN** both are explicit
- **THEN** true is compatible only with bypass and false only with non-bypass modes; contradictory pairs fail configuration

#### Scenario: Legacy yolo false
- **WHEN** yolo is false and mode is omitted
- **THEN** normal mode is required without substituting accept-edits, and absence of normal fails before prompting

#### Scenario: Later control resets an earlier one
- **WHEN** a setter resets an already selected model, reasoning, speed or mode
- **THEN** final verification rejects the turn without sending its prompt
