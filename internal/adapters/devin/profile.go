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
		return "", noop, configProblem(source, "mcpServers", "migrate legacy MCP configuration separately")
	}
	if e := checkExtensions(home, base, workspace, cfg); e != nil {
		return "", noop, e
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
