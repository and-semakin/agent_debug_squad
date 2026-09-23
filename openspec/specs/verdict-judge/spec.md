# verdict-judge Specification

## Purpose
Classifies completed workflow task attempts through an external decision model, recording a machine-readable semantic verdict — with its confidence and full probability distribution — alongside each saved response, ready for future control flow.

## Requirements

### Requirement: Judge configuration and startup gating
The system SHALL accept an optional `judge` configuration section with `provider`, `model`, `api_key_file`, `proxy_url`, and a decision timeout. The `openrouter` provider SHALL authenticate every decision request with the key read from the configured file, whose default location is `~/.agent-debug-squad/openrouter-api-key` in the user's home directory, outside the workspace. Decision requests SHALL be sent through the configured HTTP proxy when one is set; without one, standard environment proxy behavior applies. The default model SHALL be `~typesafe/jev-latest`, overridable to an exact version. When the configured workflow contains tasks declaring verdicts, or a `judge` section is present, the key file MUST exist and be readable at startup; otherwise startup SHALL fail with an actionable error naming the expected path. Without verdict tasks and without a `judge` section, the system SHALL start and operate without any judge dependency.

#### Scenario: Missing key with verdict tasks
- **WHEN** the configured workflow declares a task with verdicts and the OpenRouter key file is absent
- **THEN** startup fails with an error naming the expected key file path, and no server starts

#### Scenario: No judge dependency without verdicts
- **WHEN** the configuration has no `judge` section and no task declares verdicts
- **THEN** the server starts and serves squads and workflows without requiring an OpenRouter key

#### Scenario: Key file override
- **WHEN** `api_key_file` points to a readable file in a nondefault location
- **THEN** the judge authenticates with that file's key and startup succeeds

#### Scenario: Proxy is applied to decision requests
- **WHEN** `proxy_url` is configured
- **THEN** decision requests are sent through that proxy

### Requirement: Verdict classification is an asynchronous post-response phase
When an attempt of a task that declares verdicts completes with a nonempty final response, the system SHALL persist the response artifact exactly as for non-verdict tasks, then enter the attempt into a `judging` phase and classify it asynchronously. While judging, the attempt SHALL NOT be treated as settled and dependent tasks SHALL NOT be dispatched. The attempt settles only after its verdict is resolved by the judge, by policy, or by manual override. The judge call MUST NOT execute under the scheduler's serialization; classification outcomes are committed through the same serialized decision path as worker completions. The classification request SHALL carry the task's declared verdict options with their descriptions and the attempt's final response; responses exceeding a size cap SHALL be truncated deterministically (head and tail) with the truncation recorded. The provider adapter SHALL implement the `choice`, `noul`, and `score` decision types; workflow verdict classification SHALL use `choice` only.

#### Scenario: Dependents wait for the verdict
- **WHEN** a verdict task's owned run stops with a saved response and its dependent is otherwise ready
- **THEN** the dependent is not dispatched until the attempt's verdict resolves and the attempt settles

#### Scenario: Non-verdict tasks are unchanged
- **WHEN** a task declares no verdicts
- **THEN** its attempts settle on response commit as before, with no judging phase and no judge call

#### Scenario: Judge never runs under the scheduler lock
- **WHEN** a verdict task completes while other tasks are being scheduled
- **THEN** the decision request executes outside the scheduler's serialized reconciliation, and its result is committed in a serialized pass

#### Scenario: Large response is truncated for the judge only
- **WHEN** the attempt's final response exceeds the size cap
- **THEN** the judge receives a deterministic head-plus-tail excerpt, the truncation is recorded on the attempt, and the saved response file remains complete and unmodified

#### Scenario: Adapter covers all decision types
- **WHEN** `noul` and `score` decisions are issued through the judge interface
- **THEN** the provider sends the corresponding typed requests and returns their typed answers with distributions

### Requirement: Confidence threshold gates uncertain outcomes
Workflow definitions SHALL accept a `confidence_threshold` between 0 and 1 exclusive of zero, defaulting to 0.8. The judge's chosen verdict SHALL apply only when its confidence is greater than or equal to the threshold. Below the threshold the attempt receives the synthetic `uncertain` outcome, which MUST NOT be declarable as a verdict name. With `on_uncertain: needs_attention` (the default) the execution SHALL enter needs_attention with an uncertain-verdict reason, keep the attempt in `judging`, and expose the full probability distribution for inspection. With `on_uncertain: error` the attempt SHALL fail with reason `uncertain_verdict`, the distribution SHALL still be recorded, and existing failure policy and retry semantics apply.

#### Scenario: Confident verdict applies
- **WHEN** the judge returns `issues_found` with confidence 0.93 against a threshold of 0.8
- **THEN** the attempt settles with verdict `issues_found` and the recorded distribution

#### Scenario: Uncertain holds for inspection
- **WHEN** the judge returns confidence 0.45 and the workflow uses the default `on_uncertain`
- **THEN** the execution enters needs_attention, the attempt remains judging, and the view shows the distribution across all declared verdicts

#### Scenario: Uncertain as failure
- **WHEN** the judge returns confidence below the threshold and the workflow sets `on_uncertain: error`
- **THEN** the attempt fails with reason `uncertain_verdict`, its dependents follow existing failure policy, and the task is retryable through the existing retry API

#### Scenario: Threshold boundary
- **WHEN** the judge's confidence equals the configured threshold exactly
- **THEN** the chosen verdict applies


#### Scenario: Explicit attention policy
- **WHEN** confidence is below the threshold and on_uncertain is needs_attention
- **THEN** the execution enters needs_attention and the attempt stays judging, with the same manual override and reclassification paths as the default policy


#### Scenario: Override to continue at the cap
- **WHEN** a held condition verdict is overridden to continue at the effective cap
- **THEN** the loop applies on_exhaustion, holding under needs_attention or completing under succeed, and never creates an iteration beyond the cap

#### Scenario: Override resolves only its own cause
- **WHEN** a valid condition override resolves one loop's action hold while another loop still needs attention
- **THEN** the override is accepted and its own cause is rederived, unrelated reasons remain, and no new task dispatch or iteration advance occurs until all causes clear
### Requirement: Judge unavailability holds rather than fails
If the decision request fails at the transport level or times out after bounded retries, the system SHALL move the execution to needs_attention with a `judge_unavailable` reason, keep the attempt in `judging` with its response artifact intact, and MUST NOT fail the task. Resuming the execution, or recovering the process, SHALL re-attempt classification for held judging attempts.

#### Scenario: Provider outage
- **WHEN** the judge endpoint is unreachable and retries are exhausted
- **THEN** the execution enters needs_attention with a judge_unavailable reason and the attempt is not failed

#### Scenario: Resume re-classifies
- **WHEN** an execution held on judge unavailability is resumed after the provider recovers
- **THEN** classification re-runs for the held attempt without re-running the agent

### Requirement: Held verdicts admit manual override
The system SHALL accept `POST /workflows/{id}/tasks/{task}/attempts/{attempt}/verdict` with a nonempty `request_id` and a `verdict` value declared by that task. For an attempt held in `judging`, the override SHALL record the verdict with a manual source and settle the attempt as succeeded with that verdict. A settled attempt whose recorded verdict is load-bearing on a live hold SHALL be overridable the same way: an attempt whose verdict currently holds a loop condition through a `needs_attention` action. The replacement verdict is recorded with a manual source on a new view of the attempt outcome, and downstream behavior — dependency evaluation and loop conditions included — follows the replacement verdict. Repeating an identical request SHALL return the recorded result without new effects; a different verdict for the same request ID SHALL return 409. Overrides against attempts not in `judging` and not holding as described SHALL return 409, undeclared verdict names SHALL return 400, and unknown execution, task, or attempt identifiers SHALL return 404. For a loop-owned attempt, a new override SHALL additionally require the complete iteration path to match the current loop and every ancestor iteration. Matching a local counter alone SHALL NOT make an old invocation current. A settled condition attempt in a done owning invocation SHALL return 409 even if that invocation's final path is still current: it has no live condition-action hold. Override SHALL NOT reopen a done invocation or any ancestor. Existing judging eligibility and direct ownership of a loop's condition task SHALL remain required where applicable. Prior iteration contexts SHALL remain immutable. Identical accepted requests SHALL replay before current-context and done-loop eligibility checks, returning their existing result without changing history or applying the old verdict to a new invocation.

#### Scenario: Human resolves an uncertain hold
- **WHEN** an attempt is held on an uncertain verdict and the facilitator posts a declared verdict for it
- **THEN** the attempt settles as succeeded with that verdict recorded as manually sourced, and the execution resumes scheduling

#### Scenario: Idempotent override
- **WHEN** the same override request ID and verdict are posted twice
- **THEN** the second call returns the recorded result without side effects

#### Scenario: Override rejected for settled attempts
- **WHEN** the addressed attempt has already settled and its verdict holds nothing
- **THEN** the request returns 409 and changes nothing

#### Scenario: Ordinary verdict name does not permit override
- **WHEN** a non-condition attempt has settled with a declared verdict named blocked or needs_input
- **THEN** the name alone does not make the attempt overridable, and a new override request returns 409

#### Scenario: Override redirects a held condition
- **WHEN** a condition task's verdict mapped to needs_attention is overridden to continue below the effective cap and no other attention reasons remain
- **THEN** the condition hold clears and the loop re-arms under the replacement verdict

#### Scenario: History stays immutable
- **WHEN** an override targets an attempt whose iteration the loop has advanced past
- **THEN** the request returns 409 and no recorded verdict or iteration counter changes

#### Scenario: Historical nested override is rejected
- **WHEN** a new override targets outer=1/inner=1 while the task is now at outer=2/inner=1
- **THEN** the endpoint returns 409 despite equal local counters and preserves both history and current holds

#### Scenario: Done invocation cannot be reopened by override
- **WHEN** a new override addresses a settled condition attempt in a done loop whose final path is still current
- **THEN** the endpoint returns 409 without reopening that loop or its ancestors

#### Scenario: Accepted override replays after completion or ancestor advance
- **WHEN** an accepted override is repeated identically after its loop completed or its ancestor advanced
- **THEN** the existing result is returned without changing a historical verdict, creating work, or applying the action to a new invocation

### Requirement: Verdict outcomes are persisted and auditable
For every classified attempt, the system SHALL persist on the attempt record the verdict name (or `uncertain`), confidence, the full probability distribution across declared verdicts, the model string, the threshold used, the verdict source (`judge` or `manual`), and whether the judge input was truncated. The complete raw decision response SHALL be persisted as a readable per-attempt artifact next to the attempt's inputs and response. Judging MUST NOT modify the saved response file or the input manifest. Workflow views SHALL expose the judging state and the recorded verdict data.

#### Scenario: Verdict data in the execution view
- **WHEN** an attempt settles with a judge verdict
- **THEN** the workflow view shows the verdict, confidence, distribution, model, and source for that attempt

#### Scenario: Raw decision artifact
- **WHEN** a decision completes
- **THEN** the attempt's directory contains a readable artifact holding the provider's complete decision response

#### Scenario: Response artifact integrity
- **WHEN** classification and truncation occur
- **THEN** the saved response file keeps its original bytes, size, and content hash
