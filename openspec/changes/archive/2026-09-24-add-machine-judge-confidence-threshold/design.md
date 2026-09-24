## Context

The server loads `~/.agent-debug-squad/backends.yaml` once before constructing the workflow manager. Its `judge` section currently supports a proxy only. Omitted workflow thresholds remain zero and JSON-omitted in persisted definitions; the domain helper resolves them to 0.7 at use. The manager reads that helper during automatic classification and manual overrides. Definition hashing serializes the raw definition, not runtime settings.

## Goals / Non-Goals

**Goals:** Add one numeric machine default with explicit workflow precedence and consistent verdict auditing. Allow this computer to use 0.6 without editing shared workflow files.

**Non-Goals:** Change workflow YAML syntax, uncertainty policy, judge adapter requests, snapshot schema, idempotency hashing, active-server hot reload, or already settled outcomes.

## Decisions

1. Extend the existing `judge` section of `backends.yaml` with `confidence_threshold`. This keeps machine defaults in the already loaded private file. An alternative dedicated file would add another path and startup mechanism for one setting.
2. Parse only unquoted YAML integer/float scalars into a finite number greater than zero and at most one. Reject null, strings, booleans, NaN, infinity, and out-of-range values at startup with the file/section/key named. Other machine sections continue to reject the key. A pointer distinguishes absence from a present value.
3. Keep the machine default on `SessionConfig.MachineBackends`, outside snapshot JSON. Resolve it at the two manager use sites: judge classification and manual override. An explicit saved workflow value wins; otherwise the startup-loaded machine value wins; otherwise the domain default applies. Normalizing the workflow definition at load/create time would change hashes and make a computer-specific setting part of portable execution identity.
4. Recovery reconstructs the manager from the current machine file and reclassifies only unresolved judging attempts. A changed machine value after restart can therefore change their effective threshold. Already settled records retain their recorded threshold and outcome. Concurrent judge requests and cancellation behavior remain unchanged because resolution occurs in the same serialized outcome path as before.

## Risks / Trade-offs

- A machine default can change future decisions for existing omitted-threshold executions after restart → document the effect and record the actual threshold on each outcome.
- Older binaries reject the new machine key at startup → write the local setting only after the new release is published; running servers apply it after restart with the new binary.
- Numeric YAML edge cases could bypass range checks → require numeric node tags and finite `(0, 1]` validation, with focused parser tests.

## Migration Plan

No persisted data migration. Release with the usual tag-driven process. Configure `judge.confidence_threshold: 0.6` in this computer's private machine file after publication, preserving existing values and credentials. A running server must restart on the new release before the setting takes effect. Removing the key restores the built-in fallback for future classifications after restart.
