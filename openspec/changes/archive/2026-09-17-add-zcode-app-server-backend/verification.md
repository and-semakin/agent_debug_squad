# Verification

## Contract coverage

- Configuration: factory and example-config tests; environment allowlist/explicit proxy test; Node fingerprint rejection and escaped/plain credential-redaction tests. Unsupported builds never execute.
- Continuity: protocol subprocess tests exercise create, resume, startup prompt once, recover, reset, and full model selection. Foreign/historical completion events cannot supply the response.
- Failure/lifecycle: EOF, malformed/oversized frames, rejection, empty reply, failed turn, unsupported questions, concurrent sends, and cancellation are tested without a paid service.
- Permissions: native build mode is checked, and once/always/reject, duplicate concurrent replies, inactive/foreign run replies, unsupported always, and cancelled submissions are covered. Pending permissions are removed at lifecycle end.
- Progress: deterministic root/child/grandchild fixture verifies nested parent relationships and excludes historical descendants on resume. Cleanup marks remaining owned active children cancelled.

## Bounded live validation

Tested ZCode desktop 3.12.3, runtime 0.16.5, Node 26. All model calls used GLM-5.3-Flash with low reasoning; persisted model metadata also confirmed Flash in child sessions. No private session IDs or credentials are included here.

- Direct App Server contract probe: `pong`, approximately 6 seconds.
- Adapter create/resume: two consecutive `pong` responses in one session, 13.5 seconds total.
- Foreground subagent: child observed in progress and parent returned `parent-pong`, 12.9 seconds.
- Manual tool permission: pending Bash request resolved with `once`, expected file created in a temporary workspace, 15.2 seconds.
- Cancellation after child appeared: cleanup completed in 0.28 seconds; whole experiment 9.3 seconds.
- CLI + REST create-run with long poll: completed in 9.8 seconds; the normal output artifact contained `pong` after the standard run metadata header.

Live tests are opt-in through SQUAD_ZCODE_LIVE, SQUAD_ZCODE_LIVE_CHILD, SQUAD_ZCODE_LIVE_PERMISSION, and SQUAD_ZCODE_LIVE_CANCEL. Default test runs never use account credentials or a model service.

## Limits

The native bootstrap uses a pinned runtime fingerprint. Other bundle builds, providers, sign-in refresh, CAPTCHA, browser integration, and interactive questionnaires need future host implementations. Proxy environment forwarding is tested; external proxy routing and subscription billing/bonuses are not inferred from these experiments. Child activity timestamps describe observed state transitions, not continuous child token streaming.
