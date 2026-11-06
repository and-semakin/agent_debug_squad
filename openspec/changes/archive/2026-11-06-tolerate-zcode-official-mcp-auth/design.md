# Design

## Context

See proposal.md for motivation. Inspection of installed ZCode 3.17.2 shows `interaction/requestOfficialMcpAuthHeaders` has an `ok` discriminated response: success supplies headers; failure supplies an enumerated reason including `official_auth_unavailable`. Its native official MCP and plugin-command ports already use this negative result when no host is available. Official MCP fetch may fall back to anonymous access. This is separate from model provider runtime headers.

## Goals / Non-Goals

Support optional host-auth callbacks without aborting model inference. Do not implement authenticated official MCP, trusted-origin routing, token refresh, account switching, or change desktop settings.

## Decisions

Add an exact method case in the existing Go host dispatcher. Return only a static negative result, correlated to the RPC ID, using the existing bounded response writer. Ignore notification-only frames for this method. Since no permission or secret can be granted, no workspace or session binding is required to decline the request; foreign and malformed contexts receive the same denial. No request parameters are logged.

Do not broadly ignore unknown host requests: their current explicit failure is intentional. Do not return empty successful headers or reuse the model API key: neither implements official MCP authentication. The JS private credential bridge and model auth path remain unchanged.

The dispatcher already runs while create/resume RPCs are pending. Reuse it without new goroutines, pending state, or retries. Existing writer failures, cancellation and cleanup remain authoritative. Regression tests must exercise create and resume with the callback interleaved before their replies, as well as secret-free negative replies and the unknown-method boundary.

## Risks / Trade-offs

- Authenticated MCP functionality remains unavailable → document this distinction; let the native runtime own anonymous fallback and tool errors.
- Private protocol evolution → pin protocol-helper assertions to the inspected negative response contract and verify two real model turns on this installed version.

## Migration Plan

No configuration or state migration. Ship as a patch compatibility fix after full vet/race tests and live checks. Rollback uses the prior binary; no release tag replacement is needed.
