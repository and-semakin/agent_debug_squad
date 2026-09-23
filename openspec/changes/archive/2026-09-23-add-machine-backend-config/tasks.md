# Tasks: add-machine-backend-config

## 1. Domain types and machine config loading

- [x] 1.1 Add `domain` types for machine backend settings (`proxy_url`, `no_proxy`, `ca_cert_file`, `command`, `runtime_path`, `base_url` as applicable) and a `MachineBackends` field on `SessionConfig`; verify the package builds (`go build ./...`)
- [x] 1.2 Implement `config.LoadMachineBackends(homeDir)` reading `~/.agent-debug-squad/backends.yaml` with strict `KnownFields` decoding, the per-section key allowlist, `http`/`https` `proxy_url` validation, and string-or-list `no_proxy`; verify table-driven tests cover missing file, empty file, every supported key, unknown section, unsupported key (including `opencode.proxy_url`), invalid proxy URL, and wrong value types, each failing or succeeding with the file path and key named

## 2. Translation and merge

- [x] 2.1 Implement the backend translation helper producing env entries per the design table (codex/kimi/cursor standard vars, `NODE_USE_ENV_PROXY=1` for cursor and kimi, `NODE_EXTRA_CA_CERTS` and `ZCODE_AGENT_CA_CERT` from `ca_cert_file`, zcode `ZCODE_*`-only) and verify one unit test per backend pins the exact entries, including that zcode never receives machine-derived `HTTP_PROXY`/`HTTPS_PROXY`
- [x] 2.2 Implement merge of machine defaults into `domain.AgentSpec` (string defaults only when unset; env entries prepended after suppressing keys the agent defines in `options.env` or names in `options.inherit_env`); verify unit tests cover agent `command` winning, explicit env winning, `inherit_env` naming winning, and that the merged env contains no duplicate keys
- [x] 2.3 Apply the merge in the orchestrator's `agentSpecWithDefaults`; verify tests show a facilitator agent and a workflow snapshot-recovered agent (owned-run path with a persisted spec) both receiving machine defaults, and that machine values are absent from persisted workflow snapshots

## 3. Judge proxy fallback

- [x] 3.1 Resolve the effective judge proxy as session `judge.proxy_url` first, then machine `judge.proxy_url`, in the judge setup path; verify unit tests cover machine-default-applies and session-wins cases

## 4. Wiring, logging, and startup behavior

- [x] 4.1 Load the machine file in `cmd/agent-debug-squad` startup and attach it to the session config; verify a startup-order test shows an invalid machine file fails startup before any agent runs and a missing file starts silently
- [x] 4.2 Add a startup log line naming only the backends with machine settings applied (no values); verify by inspection in a smoke run that no proxy URL or path from the file appears in logs or run artifacts

## 5. Documentation and examples

- [x] 5.1 Update README "Environment And Secrets" and the backend notes with the machine file, its schema, the per-backend translation table, and the precedence rule; include a `backends.yaml` snippet using reserved `.example` placeholders; verify documented keys match the validator's allowlist exactly
- [x] 5.2 Confirm `configs/` and `examples/` stay free of machine-specific values and, where they currently hand-configure proxy env vars, point to the machine file as the preferred mechanism

## 6. Verification

- [x] 6.1 Run `gofmt` on changed Go files, then `go vet ./...` and `go test -race -count=1 ./...`, all clean
- [x] 6.2 Run `openspec validate add-machine-backend-config --strict` and walk every scenario in the three delta specs against the implementation, recording any deviation before archive

## 7. Machine inherit_env extension

- [x] 7.1 Extend the machine settings schema with `inherit_env` for `codex`, `cursor`, `kimi`, and `zcode` (list or comma-separated string, empty entries rejected; unsupported under `opencode`/`judge`); verify loader tests cover acceptance for all four backends and rejection for the other two
- [x] 7.2 Implement `inherit_env` union semantics in `MergeMachineDefaults` (machine entries first, deduplicated, agent order preserved; union names also suppress machine-injected env entries; agent `options.env` still wins); verify merge tests cover union, dedup, agent-env precedence, kimi constrained-env selection from machine entries alone, and input-spec non-mutation
- [x] 7.3 Update README "Machine Backend Settings" and the kimi paragraph in "Environment And Secrets" with `inherit_env` usage, including the recommended `inherit_env: [HOME, PATH]` alongside a kimi proxy; verify the documented key set matches the validator allowlist
- [x] 7.4 Re-run `gofmt`, `go vet ./...`, `go test -race -count=1 ./...`, and `openspec validate add-machine-backend-config --strict`, all clean

## 8. Quorum review fix round

- [x] 8.1 Stop echoing values in `proxy_url`/`base_url` validation errors (credential leakage to stderr); verify a test asserts a credentialed invalid URL fails with file/section/key named and no value parts in the message
- [x] 8.2 Enforce `!!str` scalar tags for string keys and list items and reject blank-but-present values while treating explicit empty strings as unset; verify loader tests cover bool/int/null scalars, non-string list items, and blank values across keys
- [x] 8.3 Reject multi-document YAML files via a decoder EOF check; verify a test proves content after `---` fails startup
- [x] 8.4 Fail startup when the home directory cannot be resolved instead of silently reading a workspace-relative settings file; verify by code inspection in `cmd` (resolved once, error surfaces before any server component starts)
- [x] 8.5 Validate `opencode.base_url` as an `http`/`https` URL at load time; verify loader tests cover invalid and valid values
- [x] 8.6 Narrow the "values stay out of logs and artifacts" requirement to credential-bearing proxy URLs (validation errors included) and record executable/runtime locations as operational provenance; verify the delta spec scenarios match the implemented behavior
- [x] 8.7 Close the restart-recovery test gap as covered by composition: workflow recovery re-submission of snapshot specs is covered by `internal/workflow` recovery tests, current-machine-defaults application to a submitted snapshot spec by `TestSubmitOwnedRunAppliesMachineDefaultsToSnapshotSpec`, and per-start loading by loader tests — an e2e restart test would add no new code path; recorded here rather than implemented
- [x] 8.8 Record the accepted behavior that an empty constrained environment (nil `BuildEnv` result) inherits the full server environment as an explicit by-design decision in design.md (D5a), confirmed by the owner 2026-09-23
- [x] 8.9 Re-run `gofmt`, `go vet ./...`, `go test -race -count=1 ./...`, and `openspec validate add-machine-backend-config --strict`, all clean
