package opencode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tailscale/hujson"
)

// OpenCode 1.18.30 loadFile inserts $schema into existing files and loadGlobal
// migrates a legacy TOML file. Refuse those inputs rather than let serve rewrite
// them. This guards known config loading, not arbitrary plugin/tool writes.
func guardConfigFiles(workspace string, environ []string) error {
	env := map[string]string{}
	for _, item := range environ {
		if k, v, ok := strings.Cut(item, "="); ok {
			env[k] = v
		}
	}
	home := env["HOME"]
	if home == "" {
		return errors.New("managed opencode requires HOME to inspect configuration safely")
	}
	configHome := env["XDG_CONFIG_HOME"]
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	global := filepath.Join(configHome, "opencode")
	legacy := filepath.Join(global, "config")
	if _, err := os.Stat(legacy); err == nil || !os.IsNotExist(err) {
		return errors.New("opencode legacy global config may be rewritten; migrate it explicitly before managed launch")
	}
	files := []string{filepath.Join(global, "config.json")}
	dirs := []string{global, filepath.Join(home, ".opencode")}
	disabled := env["OPENCODE_DISABLE_PROJECT_CONFIG"] == "true" || env["OPENCODE_DISABLE_PROJECT_CONFIG"] == "1"
	if !disabled {
		dir, err := filepath.Abs(workspace)
		if err != nil {
			return errors.New("cannot inspect opencode workspace configuration")
		}
		for {
			dirs = append(dirs, dir, filepath.Join(dir, ".opencode"))
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if custom := env["OPENCODE_CONFIG_DIR"]; custom != "" {
		if !filepath.IsAbs(custom) {
			custom = filepath.Join(workspace, custom)
		}
		dirs = append(dirs, custom)
	}
	if custom := env["OPENCODE_CONFIG"]; custom != "" {
		if !filepath.IsAbs(custom) {
			custom = filepath.Join(workspace, custom)
		}
		files = append(files, custom)
	}
	managed := env["OPENCODE_TEST_MANAGED_CONFIG_DIR"]
	if managed == "" {
		switch runtime.GOOS {
		case "darwin":
			managed = "/Library/Application Support/opencode"
		case "windows":
			managed = filepath.Join(env["ProgramData"], "opencode")
		default:
			managed = "/etc/opencode"
		}
	}
	dirs = append(dirs, managed)
	for _, dir := range dirs {
		for _, name := range []string{"opencode.json", "opencode.jsonc"} {
			files = append(files, filepath.Join(dir, name))
		}
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return errors.New("cannot inspect an OpenCode config file; check permissions before managed launch")
		}
		if len(data) == 0 {
			continue
		}
		standard, err := hujson.Standardize(data)
		var cfg map[string]json.RawMessage
		if err != nil || json.Unmarshal(standard, &cfg) != nil || cfg == nil {
			return errors.New("cannot safely inspect an OpenCode JSONC config; correct it before managed launch")
		}
		var schema string
		if json.Unmarshal(cfg["$schema"], &schema) != nil || schema == "" || strings.Contains(schema, "{") {
			return errors.New("OpenCode would rewrite a config without literal $schema; add $schema explicitly before managed launch (files left unchanged)")
		}
	}
	return nil
}
