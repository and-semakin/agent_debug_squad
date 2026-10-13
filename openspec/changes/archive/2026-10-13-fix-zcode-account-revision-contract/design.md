# Design

## Context

See proposal.md for the failure. `host.cjs` reads the native configuration property `zcodeBuiltinRevision`, but the native account snapshot and wire DTO use the differently capitalized `basedOnZCodeBuiltinRevision`. The current bridge misspells the latter in both operative paths and in an unused internal request field. The broad registry catch hides the native TypeError behind the safe materialization error. Existing Go RPC stubs skip the real JavaScript bridge and therefore miss this defect.

## Goals / Non-Goals

**Goals:** restore the native account revision contract at both boundaries and exercise the actual host in a hermetic subprocess test.

**Non-Goals:** change account routing, relax selection checks, expose native exception bodies, implement model discovery, or certify every feature of desktop 3.15.3.

## Decisions

- Correct the two native DTO keys; remove the unused revision member passed to the local read helper. Keep `zcodeBuiltinRevision` unchanged where reading native configuration. Do not send both spellings or add a permissive fallback: these would conceal contract drift.
- Test the real embedded host with a small synthetic structurally discoverable bundle. Its strict parser follows the observed native field contract; its fake App Server validates account overlays independently. Bootstrap uses an isolated temporary credential fixture, never the operator's credentials. Verify successful registry projection and wire overlay receipt, plus safe fail-closed rejection. No extra production test exports or source-text assertions are needed.
- Keep existing lifecycle, cancellation and secret-redaction paths intact. Document the failure and model-catalog limitation in patch release notes.

## Risks / Trade-offs

- [Other native contract drift may remain] → Run the short opt-in local Flash continuity smoke test where available; distinguish local live evidence from static confirmation on the remote bundle.
- [Synthetic fixtures can drift] → Anchor the exact native field and its casing to the inspected parser and independently check both host paths; show the regression fails before the fix.
- [Native errors can contain private data] → Preserve fixed public error templates.

## Migration Plan

Publish v0.21.1 after local checks and green main CI. Existing agent configuration and state need no migration. Install the patch and restart the Squad process before retrying failed work. A rollback restores the previous binary but also restores this known startup defect.
