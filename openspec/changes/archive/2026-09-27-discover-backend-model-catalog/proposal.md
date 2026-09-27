## Why

An agent preparing a workflow currently has to guess backend-specific model IDs or query unrelated tools after creating a squad. Squad needs one attributable catalog of model choices before configuration exists, so requests such as “use Astra” can be translated into exact backend options without treating a listing as proof of inference access.

## What Changes

- Add a session-free adapter discovery contract, `ListModels(ctx, effectiveConfiguration)`, and an aggregating `models --all --json` CLI with backend filtering, workspace selection, and optional existing squad configuration.
- Return versioned, deterministic results with backend/provider identity, native model ID, display name, exact YAML selection, known choice parameters, configuration/connection evidence, source, retrieval time, completeness, and upstream freshness when known.
- Keep successful results when another backend is missing, offline, unauthenticated, incompatible, or only partly enumerable. Do not infer availability or reject explicit YAML selections from catalog membership.
- Use Codex App Server model discovery, Cursor's model-list command, Kimi's strictly allowlisted provider/model output, and the effective OpenCode server's classic provider API. Treat ZCode discovery as structurally gated private-runtime integration with an explicit unsupported outcome until a session-free read-only path is verified. Define fake as a synthetic test catalog.
- Preserve Kimi model aliases and forward a selected alias through the existing Kimi execution adapter, which currently records but does not send its model option. **Compatibility impact:** previously ignored stale or placeholder Kimi options.model values will now be sent and may fail; replace them with a configured alias or omit the option to use the CLI default. Do not expand general backend execution protocols or advertise unsupported option mappings.
- Document discovery before workflow authoring; natural-language matching and ambiguous-choice handling remain with the calling agent. Do not add an internal LLM resolver, automatic installation/login, inference probes, or new run admission checks.
- Reuse the already merged `managed-opencode` lifecycle and `check-backend-installation` launch-plan/check contracts. Do not duplicate their supervisors, settings, snapshot policy, or execution gates.
- Specify deterministic human output, safe JSON error envelopes, visible hidden-model policy and timeout budgets scaled by deduplicated target count. Document that --all can return exit 1 with useful results, including when ZCode is unsupported; partial and total failure are distinguished in JSON.
- Keep the first release CLI-only and uncached at the Squad layer. Every invocation queries sources again; backend-owned cache freshness remains explicit rather than being represented as fresh inference evidence.

## Capabilities

### New Capabilities

- `backend-model-catalog`: standalone discovery, normalized evidence and selection records, adapter-specific source contracts, safe failure aggregation, and documented workflow-authoring integration.

### Modified Capabilities

None. Existing machine-setting precedence, workflow validation, session continuity, and HTTP response contracts remain intact. The new capability specifies the CLI's one-shot configuration resolution and Kimi selection forwarding without changing an existing capability's requirements.

## Impact

- `internal/domain` gains discovery DTOs; `internal/adapters` and concrete adapters gain an independent discovery interface/factory; a small `internal/modeldiscovery` service aggregates results without constructing an orchestrator or workflow manager.
- `internal/config` supplies standalone effective configuration using existing machine defaults and environment semantics. Reuse the installation change's launch resolver and the OpenCode change's effective mode/runtime owner.
- `cmd/agent-debug-squad` gains `models`, structured JSON output, and command-specific exit handling; existing commands retain their behavior. No new HTTP route, machine YAML keys, persisted workflow schema, release version, or dependency is required.
- `internal/adapters/kimi` gets the narrow model forwarding correction and tests. Unknown discovery metadata does not justify adding unsupported reasoning/provider execution flags.
- Update README, the repository skill, and fake/recorded fixtures. No personal configuration, credentials, machine-specific paths, or live catalog snapshot is committed.

Implementation prerequisite: move the implementation base onto main containing managed OpenCode eedfa95 and installation preflight 88ec978 (verified at main 1c444a2 on 2026-09-26). They no longer await merge; this planning worktree remains at 7d21ef5. Discovery uses local installation checks only and owns its single OpenCode readiness/startup path.
