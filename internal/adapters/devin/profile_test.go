package devin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeProfileFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProfileDelegatesNativeConfiguration(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	source := filepath.Join(home, ".config/devin/config.json")
	body := `{"hooks":{"SessionStart":[{"command":"test"}]},"mcpServers":{"test":{"command":"test"}},"requiredPlugins":["test"],"optionalPlugins":["other"],"read_config_from":{"claude":true,"opencode":true}}`
	writeProfileFixture(t, source, body)
	// These files must not be inspected by Squad, regardless of validity.
	files := map[string]string{source: body}
	for _, path := range []string{
		filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude/settings.json"),
		filepath.Join(home, ".config/opencode/opencode.json"), filepath.Join(home, ".config/devin/mcp_config.json"),
		filepath.Join(home, ".config/devin/plugins/test/plugin.json"), filepath.Join(workspace, ".devin/config.json"),
		filepath.Join(workspace, ".devin/hooks.v1.json"), filepath.Join(workspace, ".mcp.json"),
	} {
		files[path] = "{not valid JSON"
		writeProfileFixture(t, path, files[path])
	}
	local := filepath.Join(workspace, ".devin/config.local.json")
	files[local] = `{"auto_update":true,"subagents_enabled":true,"disabled_tools":[],"mcpServers":{}}`
	writeProfileFixture(t, local, files[local])
	// A non-regular imported source previously failed bounded inspection.
	if err := os.MkdirAll(filepath.Join(home, ".config/zed/settings.json"), 0700); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := profile([]string{"HOME=" + home}, workspace)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatal(err)
	}
	want["auto_update"] = false
	want["subagents_enabled"] = false
	if !reflect.DeepEqual(got, want) {
		t.Fatal("native settings were not preserved")
	}
	for p, original := range files {
		after, err := os.ReadFile(p)
		if err != nil || string(after) != original {
			t.Fatalf("source changed: %s", p)
		}
	}
}

func TestProfileStillRequiresCopyableUserConfig(t *testing.T) {
	for _, body := range []string{"{SECRET", "null", "[]"} {
		home := t.TempDir()
		writeProfileFixture(t, filepath.Join(home, ".config/devin/config.json"), body)
		_, cleanup, err := profile([]string{"HOME=" + home}, t.TempDir())
		cleanup()
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("expected safe copy error: %v", err)
		}
	}
}
