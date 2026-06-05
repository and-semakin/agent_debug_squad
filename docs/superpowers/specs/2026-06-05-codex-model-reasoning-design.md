# Codex Model And Reasoning Design

## Summary

Agent Debug Squad should let each Codex-backed agent choose its own model and reasoning effort through YAML. The config loader already accepts string options such as `model`, and the OpenCode adapter already uses `options.model`. The Codex adapter currently ignores that option and has no user-friendly way to set Codex reasoning effort.

This change adds Codex support for `options.model` and `options.reasoning`. The adapter will translate those options into Codex CLI arguments while keeping existing defaults intact when either option is omitted.

## Goals

- Allow one Codex agent to run on a different model than another Codex agent.
- Allow per-agent Codex reasoning effort through a readable YAML key.
- Keep `model` optional so Codex can continue using its configured default.
- Keep `reasoning` optional so Codex can continue using its configured default.
- Avoid changing OpenCode, Kimi, fake backends, API shape, or persisted run artifacts.

## Non-Goals

- No model catalog validation in Agent Debug Squad.
- No validation of reasoning values beyond requiring YAML string values.
- No generic nested Codex config passthrough in this iteration.
- No UI changes.
- No changes to Codex session resume behavior.

## Configuration

Codex agents can set:

```yaml
agents:
  - name: Architect
    backend: codex
    startup_prompt: Think through architecture.
    options:
      command: codex
      model: gpt-5.5
      reasoning: high

  - name: Implementer
    backend: codex
    startup_prompt: Implement focused changes.
    options:
      command: codex
      model: gpt-5.3-codex
      reasoning: medium
```

`options.model` is passed to Codex as:

```sh
--model <model>
```

`options.reasoning` is passed to Codex as:

```sh
-c model_reasoning_effort='"<reasoning>"'
```

The YAML key is intentionally `reasoning`, not `model_reasoning_effort`, so the public Agent Debug Squad config stays readable and does not expose Codex's internal config key as the primary interface.

## Adapter Behavior

Codex argument construction should keep the existing base behavior:

```text
codex exec --json [--dangerously-bypass-approvals-and-sandbox] [model/reasoning args] [resume <session_id>] <message>
```

Ordering is not semantically important to Codex for these options, but the implementation should keep options before `resume` and before the prompt for readability and testability.

If `model` is empty, no model flag is added.

If `reasoning` is empty, no config override is added.

If both are present, both are added.

## State

`domain.AgentState.Model` already exists, but Codex state currently does not populate it. This change does not need state changes because the runtime source of truth is the configured `AgentSpec`. Avoid adding partially maintained state fields in this iteration.

## Error Handling

No new adapter-level error handling is needed. Codex CLI remains responsible for rejecting unsupported model names or reasoning values. Agent Debug Squad should preserve Codex stdout, stderr, and exit handling exactly as it does today.

## Backward Compatibility

Existing Codex agents without `options.model` or `options.reasoning` keep current behavior.

Existing configs that already include `options.model` for Codex will start taking effect. This is intended because the option was already accepted by the loader but ignored by the adapter.

Existing OpenCode `options.model` behavior is unchanged.

## Testing

Add focused Codex adapter tests:

- `buildArgs` adds `--model <value>` when `StringOptions["model"]` is set.
- `buildArgs` omits `--model` when `model` is absent.
- `buildArgs` adds `-c model_reasoning_effort='"medium"'` when `StringOptions["reasoning"]` is set.
- `buildArgs` places model and reasoning options before `resume` and the prompt.
- `Send` passes the generated arguments through to the configured Codex command.

Add documentation coverage:

- README shows `model` and `reasoning` under Codex options.
- The project code-review config demonstrates at least one Codex model/reasoning override.
