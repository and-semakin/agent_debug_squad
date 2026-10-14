# Verification

- Formatting, go vet ./..., go test -race -count=1 ./... and git diff --check passed.
- Strict change validation and all 10 main specifications passed.
- Regression coverage includes five global/project Claude paths, preserved enabled import policy/source files, and coexisting hooks, MCP, required/optional native plugins and native plugin directories.
- TestLiveLocalContinuity passed in 40.56 seconds with an isolated HOME containing enabledPlugins.gopls-lsp@claude-plugins-official=true, existing XDG data for authentication and copied native config/proxy. Three actual turns verified fresh/loaded sessions and model/Fast switching. This local test exercises the Squad guard; registry-import conclusions come from the separately supplied Devin 3000.11.3 evidence, not this metadata-only fixture.
- No real user settings or supplied evidence were modified.
