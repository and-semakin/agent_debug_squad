# Design

## Context

See proposal.md. The initial backend was merged through PR #2 onto main during this change. Subsequent work is committed directly to main as requested. Existing setOption discards each returned configOptions array. Native 3000.11.1 advertises model-specific controls; defaults cannot be determined before model selection.

## Goals / Non-Goals

Goals: explicit reproducible controls with compatible environment/proxy behavior and unchanged session ownership. Non-goals: Fusion, sandbox, extensions, arbitrary native flags, new retry policy or silent fallback.

## Decisions

- Keep options flat: model, reasoning, speed, mode, yolo, command, env, inherit_env. Require model for execution/configured agents, not standalone catalog operations. Use string enums standard/fast and the known local modes normal/accept-edits/ask/plan/smart/bypass; runtime advertisement remains authoritative.
- Explicit mode overrides implicit/inherited yolo. Explicit contradictory mode/yolo pairs fail. Without mode, effective yolo false requests normal; otherwise bypass. Squad's existing default yolo is true. Existing defaults.yolo remains an explicit squad-wide policy when mode is absent.
- Select model first, then reasoning, speed and mode. Each setter returns refreshed controls. Verify the complete final selection after the last setter to catch resets. Match model/mode/thought_level categories; speed uses the observed id speed with category model_config, never an arbitrary model_config control.
- Prefer high when reasoning is omitted; if high is unavailable, keep and verify the advertised current value. A model without reasoning accepts omission only. A missing speed selector supports standard only; never infer fast from a model-name suffix. Reject ambiguous controls and unadvertised values.
- Native probe on 2026-10-13 with 3000.11.1: swe-2-high offers medium/high/max and no speed selector. gpt-6-sol-medium offers none/low/medium/high/xhigh/max plus speed standard/fast (category model_config). Setting reasoning high, fast and then standard preserved the model and other choices. Ask selection worked. Account metadata can be temporarily incomplete; fail before prompting without automatic prompt replay.
- Preserve machine command/proxy_url/no_proxy/inherit_env and agent env precedence. Configure the user's real proxy only in machine-local files after release, with restrictive backup permissions and no values in repository or logs. Set native proxy mode manual if required for standalone Devin as requested.

## Risks / Trade-offs

- Native choices vary by account/model/version -> validate offered values and final controls every turn; add opt-in live tests alongside synthetic failure tests.
- Missing model breaks existing YAML -> update examples and release migration notes; no persisted identity replacement.
- Conflicting defaults -> explicit mode is authoritative; explicitly conflicting yolo is a configuration error.
- Network metadata can be unavailable -> no guessed choices or fallback model.

## Migration Plan

Update configured Devin agents to include an exact ACP model ID. Publish v0.21.0 from main after CI; this includes the previously unpublished backend. Preserve native state and keep backups of local proxy configuration. Rollback uses the prior release; no native session migration is performed.

## Acceptance evidence

On 2026-10-13 the opt-in live test passed (19.18 seconds): new SWE-2 turn, resumed SWE-2 with medium reasoning and Ask, then resumed GPT-6 Sol with high reasoning and Fast. Final-control verification ran before each prompt. Native user configuration remained byte-identical. Offline control fixtures cover new and loaded defaults, optional selector absence, unsupported values, ambiguity, ignored selection and a later mode setter resetting reasoning. No prompt is sent in these failure cases.
