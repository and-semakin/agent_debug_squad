# Repository Instructions

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
