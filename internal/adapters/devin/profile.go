package devin

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"
)

// profile is a private semantic copy; native user/project files are never
// rewritten. Controls below restrict lifecycle, not authentication or proxy policy.
func profile(env []string, workspace string) (string, func(), error) {
	noop := func() {}
	home := envValue(env, "HOME")
	base := envValue(env, "XDG_CONFIG_HOME")
	if base == "" {
		if home == "" {
			return "", noop, errors.New("devin: HOME or XDG_CONFIG_HOME is required")
		}
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return "", noop, errors.New("devin: configuration root must be absolute")
	}
	source := filepath.Join(base, "devin", "config.json")
	data, e := readConfig(source)
	if e != nil && !os.IsNotExist(e) {
		return "", noop, errors.New("devin: native configuration unreadable")
	}
	cfg := map[string]any{}
	if e == nil {
		if len(data) > maxFrame {
			return "", noop, errors.New("devin: native configuration too large")
		}
		data, e = hujson.Standardize(data)
		if e != nil || json.Unmarshal(data, &cfg) != nil || cfg == nil {
			return "", noop, errors.New("devin: invalid native configuration")
		}
	}
	// Old embedded MCP configuration triggers native on-start migration. Reject
	// rather than changing policy or letting the CLI write sibling files.
	if _, ok := cfg["mcpServers"]; ok {
		return "", noop, errors.New("devin: migrate legacy MCP configuration separately")
	}
	if e := checkExtensions(home, base, workspace, cfg); e != nil {
		return "", noop, e
	}
	for dir := workspace; ; dir = filepath.Dir(dir) {
		for _, name := range []string{"config.json", "config.local.json"} {
			b, err := readConfig(filepath.Join(dir, ".devin", name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || len(b) > maxFrame {
				return "", noop, errors.New("devin: project configuration unreadable")
			}
			b, err = hujson.Standardize(b)
			var v map[string]any
			if err != nil || json.Unmarshal(b, &v) != nil || v == nil {
				return "", noop, errors.New("devin: invalid project configuration")
			}
			for _, key := range []string{"mcpServers", "auto_update", "subagents_enabled", "disabled_tools", "requiredPlugins", "optionalPlugins"} {
				if _, ok := v[key]; ok {
					return "", noop, errors.New("devin: unsupported project lifecycle override or legacy MCP configuration")
				}
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	cfg["auto_update"] = false
	cfg["subagents_enabled"] = false
	tmp, e := os.MkdirTemp("", "squad-devin-")
	if e != nil {
		return "", noop, errors.New("devin: private configuration unavailable")
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	b, e := json.Marshal(cfg)
	if e != nil {
		cleanup()
		return "", noop, errors.New("devin: configuration encoding failed")
	}
	path := filepath.Join(tmp, "config.json")
	if os.WriteFile(path, b, 0600) != nil {
		cleanup()
		return "", noop, errors.New("devin: private configuration unavailable")
	}
	return path, cleanup, nil
}

func readConfig(path string) ([]byte, error) {
	info, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > maxFrame {
		return nil, errors.New("devin: unsupported configuration file")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxFrame+1))
	if len(b) > maxFrame {
		return nil, errors.New("devin: configuration size limit")
	}
	return b, e
}

// Hooks and external MCP/plugin processes are outside this initial ownership
// contract. Reject their presence rather than dropping native security policy.
// Rule/skill imports remain intact. Guards are deliberately conservative even
// for currently disabled imports, whose project overrides can re-enable them.
func checkExtensions(home, base, workspace string, cfg map[string]any) error {
	for _, key := range []string{"hooks", "requiredPlugins", "optionalPlugins"} {
		if v, ok := cfg[key]; ok && v != nil {
			b, _ := json.Marshal(v)
			if string(b) != "{}" && string(b) != "[]" {
				return errors.New("devin: hooks and plugins are unsupported in the owned launch profile")
			}
		}
	}
	paths := []string{filepath.Join(base, "devin", "mcp_config.json"), filepath.Join(base, "devin", "plugins"), filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude", "settings.local.json"), filepath.Join(home, ".claude", "mcp_servers.json"), filepath.Join(base, "opencode", "opencode.json"), filepath.Join(base, "zed", "settings.json")}
	for _, channel := range []string{"windsurf", "windsurf-next", "windsurf-insiders"} {
		paths = append(paths, filepath.Join(home, ".codeium", channel, "mcp_config.json"))
	}
	for dir := workspace; ; dir = filepath.Dir(dir) {
		for _, name := range []string{".devin/hooks.v1.json", ".devin/mcp_config.json", ".devin/mcp_config.local.json", ".devin/plugins", ".mcp.json", ".cursor/mcp.json", ".claude/settings.json", ".claude/settings.local.json", "opencode.json", ".zed/settings.json"} {
			paths = append(paths, filepath.Join(dir, name))
		}
		for _, name := range []string{".devin/config.json", ".devin/config.local.json"} {
			b, e := readConfig(filepath.Join(dir, name))
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return errors.New("devin: configuration inspection failed")
			}
			b, e = hujson.Standardize(b)
			var v map[string]any
			if e != nil || json.Unmarshal(b, &v) != nil {
				return errors.New("devin: invalid project configuration")
			}
			for _, k := range []string{"hooks", "requiredPlugins", "optionalPlugins"} {
				if _, ok := v[k]; ok {
					return errors.New("devin: project hooks and plugins are unsupported")
				}
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	for _, path := range paths {
		_, e := os.Stat(path)
		if e == nil {
			return errors.New("devin: external hooks, MCP or plugin configuration is unsupported in the owned launch profile")
		}
		if !os.IsNotExist(e) {
			return errors.New("devin: external configuration inspection failed")
		}
	}
	return nil
}
