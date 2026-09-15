# Repository Instructions

## OpenSpec Workflow

- Use OpenSpec for new features, behavior changes, and substantial refactors. Read `openspec/config.yaml` and relevant specs before planning implementation.
- Start with `openspec-propose` to create a change under `openspec/changes/<name>/` with a proposal, design, specs, and tasks. Use `openspec-explore` when investigation is needed first.
- Implement with `openspec-apply-change`, keeping task checkboxes and artifacts consistent with the actual work. Use `openspec-update-change` if the scope changes.
- Before archiving, run `openspec validate <name> --strict`, complete the required code checks below, and check implementation against requirements and scenarios. Use `openspec-archive-change` to sync spec deltas into `openspec/specs/` and archive completed work.
- Small fixes that restore specified behavior, typos, and formatting-only edits may be made directly; update affected specs if behavior changes.
- Keep OpenSpec artifacts in English, matching repository documentation. Communicate with the user in their preferred language.
- Treat `docs/superpowers/` as historical design context. Check its claims against current code; do not assume it is the current specification or migrate it wholesale.

## Development

- Run `gofmt` on changed Go files.
- Before handing off a code change, run `go vet ./...` and `go test -race -count=1 ./...`.
- Do not edit the embedded release version manually. Development builds use `dev`; release builds receive their version, commit, and build date from GoReleaser.

## Release Process

Git tags are the single source of truth for release versions. Releases use strict semantic versions in the form `vMAJOR.MINOR.PATCH`.

1. Choose the next version according to semantic versioning.
2. Confirm the release commit is on `main`, the worktree contains no unintended changes, and CI is green.
3. Create an annotated tag: `git tag -a vX.Y.Z -m "vX.Y.Z"`.
4. Push the tag: `git push origin vX.Y.Z`.
5. Watch the `Release` GitHub Actions workflow. It must test the repository, build macOS and Linux archives for AMD64 and ARM64, publish `checksums.txt`, and create the GitHub Release.
6. Verify the published archive names match `agent-debug-squad_<os>_<arch>.tar.gz` and that `agent-debug-squad version` reports the released version.

Never create or push a release tag unless the user explicitly asks to publish a release. Removing or replacing a published tag or release also requires explicit confirmation.
