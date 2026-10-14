package devin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionInspection(t *testing.T) {
	for _, tc := range []struct{ name, path, body, key string }{
		{"claude metadata", ".claude.json", `{"numStartups":42,"projects":{"/other":{"mcpServers":{"unused":{"command":"test"}}}}}`, ""},
		{"claude settings", ".claude/settings.json", `{"permissions":{"deny":["Bash(sudo *)"]},"hooks":{"SessionStart":[]},"enabledPlugins":{"plugin":false}}`, ""},
		{"claude hook", ".claude/settings.json", `{"hooks":{"SessionStart":[{"command":"SECRET"}]}}`, "hooks"},
		{"claude plugin", ".claude/settings.json", `{"enabledPlugins":{"SECRET":true}}`, "enabledPlugins"},
		{"claude server", ".claude.json", `{"mcpServers":{"SECRET":{"command":"SECRET"}}}`, "mcpServers"},
		{"disabled server", ".claude.json", `{"mcpServers":{"SECRET":{"disabled":true,"command":"SECRET"}}}`, ""},
		{"opencode plugin only", ".config/opencode/opencode.json", `{"plugin":["example-plugin"],"model":"example"}`, ""},
		{"opencode disabled", ".config/opencode/opencode.json", `{"mcp":{"server":{"enabled":false,"command":["test"]}}}`, ""},
		{"opencode enabled", ".config/opencode/opencode.json", `{"mcp":{"SECRET":{"enabled":true,"command":["SECRET"]}}}`, "mcp"},
		{"zed server", ".config/zed/settings.json", `{"context_servers":{"SECRET":{"command":"SECRET"}}}`, "context_servers"},
		{"native empty", ".config/devin/mcp_config.json", `{"mcpServers":{}}`, ""},
		{"jsonc", ".claude.json", "{ // comment\n \"mcpServers\": {}, }", ""},
		{"invalid", ".claude.json", `{ SECRET`, "$"},
		{"null", ".claude.json", `null`, "$"},
		{"unknown server shape", ".claude.json", `{"mcpServers":"SECRET"}`, "mcpServers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, tc.path)
			putConfig(t, path, tc.body)
			_, cleanup, err := profile([]string{"HOME=" + home}, t.TempDir())
			defer cleanup()
			if tc.key == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `key "`+tc.key+`"`) {
					t.Fatalf("missing source/key: %v", err)
				}
				if strings.Contains(err.Error(), "SECRET") {
					t.Fatal("configuration leaked")
				}
			}
			after, e := os.ReadFile(path)
			if e != nil || string(after) != tc.body {
				t.Fatal("source modified")
			}
		})
	}
}

func TestExtensionImportPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, user, project, local string
		blocked                    bool
	}{
		{"default", "{}", "{}", "{}", true},
		{"user disables", `{"read_config_from":{"claude":false}}`, "{}", "{}", false},
		{"project disables", "{}", `{"read_config_from":{"claude":false}}`, "{}", false},
		{"project enables", `{"read_config_from":{"claude":false}}`, `{"read_config_from":{"claude":true}}`, "{}", true},
		{"local disables", "{}", `{"read_config_from":{"claude":true}}`, `{"read_config_from":{"claude":false}}`, false},
		{"local enables", "{}", `{"read_config_from":{"claude":false}}`, `{"read_config_from":{"claude":true}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			root := t.TempDir()
			workspace := filepath.Join(root, "nested")
			if e := os.Mkdir(workspace, 0700); e != nil {
				t.Fatal(e)
			}
			putConfig(t, filepath.Join(home, ".config/devin/config.json"), tc.user)
			putConfig(t, filepath.Join(root, ".devin/config.json"), tc.project)
			putConfig(t, filepath.Join(workspace, ".devin/config.local.json"), tc.local)
			putConfig(t, filepath.Join(home, ".claude/settings.json"), `{"enabledPlugins":{"example":true}}`)
			_, cleanup, err := profile([]string{"HOME=" + home}, workspace)
			defer cleanup()
			if (err != nil) != tc.blocked {
				t.Fatalf("blocked=%v err=%v", tc.blocked, err)
			}
		})
	}
}

func TestDisabledImportsSkipUnreadableSources(t *testing.T) {
	home := t.TempDir()
	putConfig(t, filepath.Join(home, ".config/devin/config.json"), `{"read_config_from":{"claude":false,"opencode":false}}`)
	putConfig(t, filepath.Join(home, ".claude.json"), `not json`)
	if e := os.MkdirAll(filepath.Join(home, ".config/opencode/opencode.json"), 0700); e != nil {
		t.Fatal(e)
	}
	_, cleanup, err := profile([]string{"HOME=" + home}, t.TempDir())
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
}

func TestClaudeWorkspaceMCPAndBoundedFiles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	// Temp paths contain no JSON escapes on supported test hosts.
	putConfig(t, path, `{"projects":{"`+workspace+`":{"mcpServers":{"SECRET":{"command":"SECRET"}}}}}`)
	_, cleanup, err := profile([]string{"HOME=" + home}, workspace)
	cleanup()
	if err == nil || !strings.Contains(err.Error(), "projects.<workspace>.mcpServers") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unexpected error: %v", err)
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	_, cleanup, err = profile([]string{"HOME=" + home}, workspace)
	cleanup()
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("directory accepted: %v", err)
	}
}

func TestStandaloneNativeHooks(t *testing.T) {
	for _, body := range []string{`{}`, `{"PreToolUse":[]}`, `{"PreToolUse":[{"hooks":[{"type":"command","command":"SECRET"}]}]}`} {
		workspace := t.TempDir()
		path := filepath.Join(workspace, ".devin/hooks.v1.json")
		putConfig(t, path, body)
		_, cleanup, err := profile([]string{"HOME=" + t.TempDir()}, workspace)
		cleanup()
		active := strings.Contains(body, "SECRET")
		if (err != nil) != active {
			t.Fatalf("unexpected result: %v", err)
		}
		if err != nil && (!strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "SECRET")) {
			t.Fatal("unsafe error")
		}
	}
}
