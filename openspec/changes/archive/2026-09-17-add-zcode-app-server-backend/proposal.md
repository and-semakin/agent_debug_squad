## Why

Squad cannot currently coordinate ZCode sessions. ZCode's App Server protocol exposes persistent conversations, permissions, and subagent activity that can fit the existing run API without scraping its UI.

## What Changes

- Add a `zcode` backend using a private, bidirectional App Server subprocess per turn and persistent backend session IDs.
- Add an embedded Node host bridge for the tested ZCode desktop runtime and existing signed-in Z.AI Coding Plan credentials. Fail clearly on incompatible runtime builds or ambiguous accounts.
- Support explicit model/reasoning selection, YOLO, coordinator permission replies, cancellation, streamed events, and subagent progress.
- Document environment/proxy configuration, desktop session visibility, supported compatibility boundaries, and a Flash example.

## Capabilities

### New Capabilities
- `zcode-backend`: Configure, run, resume, monitor, and cancel ZCode App Server sessions through Squad.

### Modified Capabilities
None. Existing OpenCode permission behavior is unchanged.

## Impact

New adapter and embedded JavaScript host, adapter factory, tests, README and example configuration. No existing REST or state format changes; the existing optional permission API is reused. Requires Node and a compatible locally installed ZCode runtime. No credential values are stored in configuration or run artifacts. No automatic login, billing bypass, desktop database writes, or arbitrary UI/browser integration.
