package devin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"
)

// This is a compatibility check for the owned launch profile, not a sandbox.
// Never include arbitrary config values, entry names or parser errors in diagnostics.
func configProblem(path, key, reason string) error {
	return fmt.Errorf("devin: configuration %q key %q: %s", path, key, reason)
}

func inspectConfig(path string) (map[string]any, error) {
	b, err := readConfig(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, configProblem(path, "$", "cannot inspect bounded regular file")
	}
	b, err = hujson.Standardize(b)
	var cfg map[string]any
	if err != nil || json.Unmarshal(b, &cfg) != nil || cfg == nil {
		return nil, configProblem(path, "$", "invalid JSON configuration")
	}
	return cfg, nil
}

func nonempty(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	default:
		return true
	}
}

func checkFields(path string, cfg map[string]any, keys ...string) error {
	for _, key := range keys {
		v := cfg[key]
		if key == "hooks" {
			if entries, ok := v.(map[string]any); ok {
				active := false
				for _, entry := range entries {
					if nonempty(entry) {
						active = true
						break
					}
				}
				if !active {
					continue
				}
			}
		}
		if nonempty(v) {
			return configProblem(path, key, "active or unrecognized extension configuration is unsupported in the owned launch profile")
		}
	}
	return nil
}

func checkServers(path, key string, v any, openCode bool) error {
	if !nonempty(v) {
		return nil
	}
	if servers, ok := v.(map[string]any); ok {
		for _, value := range servers {
			entry, ok := value.(map[string]any)
			if ok && ((!openCode && entry["disabled"] == true) || (openCode && entry["enabled"] == false)) {
				continue
			}
			return configProblem(path, key, "active or unrecognized MCP configuration is unsupported in the owned launch profile")
		}
		return nil
	}
	return configProblem(path, key, "unrecognized MCP configuration")
}

func mergeImports(path string, cfg map[string]any, imports map[string]bool) error {
	v, exists := cfg["read_config_from"]
	if !exists {
		return nil
	}
	entries, ok := v.(map[string]any)
	if !ok {
		return configProblem(path, "read_config_from", "expected an object of boolean import switches")
	}
	for _, name := range []string{"agents_standard", "cursor", "windsurf", "claude", "copilot", "opencode", "zed"} {
		if v, exists := entries[name]; exists {
			enabled, ok := v.(bool)
			if !ok {
				return configProblem(path, "read_config_from."+name, "expected boolean")
			}
			imports[name] = enabled
		}
	}
	return nil
}

type extensionSource struct{ path, tool, key string }

func checkExtensions(home, base, workspace string, cfg map[string]any) error {
	native := filepath.Join(base, "devin", "config.json")
	if err := checkFields(native, cfg, "hooks", "requiredPlugins", "optionalPlugins"); err != nil {
		return err
	}
	imports := map[string]bool{"claude": true, "cursor": true, "windsurf": true, "opencode": true, "zed": true}
	if err := mergeImports(native, cfg, imports); err != nil {
		return err
	}
	var dirs []string
	for dir := filepath.Clean(workspace); ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if filepath.Dir(dir) == dir {
			break
		}
	}
	// Resolve root-to-leaf project overrides, with local after shared settings.
	for i := len(dirs) - 1; i >= 0; i-- {
		for _, name := range []string{"config.json", "config.local.json"} {
			path := filepath.Join(dirs[i], ".devin", name)
			project, err := inspectConfig(path)
			if err != nil {
				return err
			}
			for _, key := range []string{"mcpServers", "auto_update", "subagents_enabled", "disabled_tools"} {
				if _, exists := project[key]; exists {
					return configProblem(path, key, "unsupported project lifecycle override or legacy MCP migration")
				}
			}
			if err := checkFields(path, project, "hooks", "requiredPlugins", "optionalPlugins"); err != nil {
				return err
			}
			if err := mergeImports(path, project, imports); err != nil {
				return err
			}
		}
	}
	sources := []extensionSource{
		{filepath.Join(base, "devin", "mcp_config.json"), "", "mcpServers"},
		{filepath.Join(home, ".claude.json"), "claude", "mcpServers"},
		{filepath.Join(home, ".claude", "settings.json"), "claude", "mcpServers"},
		{filepath.Join(home, ".claude", "settings.local.json"), "claude", "mcpServers"},
		{filepath.Join(home, ".claude", "mcp_servers.json"), "claude", "mcpServers"},
		{filepath.Join(base, "opencode", "opencode.json"), "opencode", "mcp"},
		{filepath.Join(base, "zed", "settings.json"), "zed", "context_servers"},
	}
	pluginDirs := []string{filepath.Join(base, "devin", "plugins")}
	for _, channel := range []string{"windsurf", "windsurf-next", "windsurf-insiders"} {
		sources = append(sources, extensionSource{filepath.Join(home, ".codeium", channel, "mcp_config.json"), "windsurf", "mcpServers"})
	}
	for _, dir := range dirs {
		for _, source := range []extensionSource{
			{".devin/hooks.v1.json", "", "hooks"}, {".devin/mcp_config.json", "", "mcpServers"}, {".devin/mcp_config.local.json", "", "mcpServers"},
			{".mcp.json", "claude", "mcpServers"}, {".cursor/mcp.json", "cursor", "mcpServers"},
			{".claude/settings.json", "claude", "mcpServers"}, {".claude/settings.local.json", "claude", "mcpServers"},
			{"opencode.json", "opencode", "mcp"}, {".zed/settings.json", "zed", "context_servers"},
		} {
			source.path = filepath.Join(dir, source.path)
			sources = append(sources, source)
		}
		pluginDirs = append(pluginDirs, filepath.Join(dir, ".devin", "plugins"))
	}
	for _, source := range sources {
		if source.tool != "" && !imports[source.tool] {
			continue
		}
		doc, err := inspectConfig(source.path)
		if err != nil {
			return err
		}
		if source.key == "hooks" {
			// Standalone hooks.v1.json is the event map itself, without a hooks wrapper.
			if err := checkFields(source.path, map[string]any{"hooks": doc}, "hooks"); err != nil {
				return err
			}
		} else if err := checkServers(source.path, source.key, doc[source.key], source.tool == "opencode"); err != nil {
			return err
		}
		if source.tool == "claude" {
			// Claude enabledPlugins is not evidence of a Devin-loaded plugin.
			// Keep native import policy intact; inspect actual imported hooks/MCP.
			if err := checkFields(source.path, doc, "hooks"); err != nil {
				return err
			}
			// Claude's global state can also hold MCP entries for this workspace.
			if source.path == filepath.Join(home, ".claude.json") {
				if projects, ok := doc["projects"].(map[string]any); ok {
					for _, dir := range dirs {
						if project, ok := projects[dir].(map[string]any); ok {
							if err := checkServers(source.path, "projects.<workspace>.mcpServers", project["mcpServers"], false); err != nil {
								return err
							}
						}
					}
				}
			}
		}
	}
	for _, path := range pluginDirs {
		entries, err := os.ReadDir(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return configProblem(path, "plugins", "cannot inspect plugin directory")
		}
		if len(entries) > 0 {
			return configProblem(path, "plugins", "native plugin directory is nonempty and unsupported in the owned launch profile")
		}
	}
	return nil
}
