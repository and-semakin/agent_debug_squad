package skillmanager

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func sortStrings(a []string) { sort.Strings(a) }
func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}
func overlap(a, b string) bool { return within(a, b) || within(b, a) }
func canonical(p string) (string, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", problem("unsafe_path")
	}
	// Resolve ancestors, never a final skill symlink.
	if s, e := os.Lstat(p); e == nil && s.Mode()&os.ModeSymlink != 0 {
		return "", problem("unsafe_path")
	}
	var resolve func(string) (string, error)
	resolve = func(p string) (string, error) {
		r, e := filepath.EvalSymlinks(p)
		if e == nil {
			return r, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", e
		}
		r, e = resolve(parent)
		return filepath.Join(r, filepath.Base(p)), e
	}
	parent, e := resolve(filepath.Dir(p))
	if e != nil {
		return "", e
	}
	return filepath.Join(parent, filepath.Base(p)), nil
}

var hostOrder = []string{"codex", "claude-code", "cursor", "opencode", "zcode", "kimi", "kimi-cli"}

type host struct {
	Preferred string
	Eligible  []string
}

func hostRoots(home string, env func(string) string, name string) (host, error) {
	h := func(p string) string { return filepath.Join(home, p) }
	override := func(key, def string) (string, error) {
		v := env(key)
		if v == "" {
			return def, nil
		}
		if !filepath.IsAbs(v) || filepath.Clean(v) != v {
			return "", problem("invalid_override")
		}
		return v, nil
	}
	generic := h(".agents/skills")
	native := ""
	eligible := []string{}
	switch name {
	case "codex":
		native = generic
	case "claude-code":
		r, e := override("CLAUDE_CONFIG_DIR", h(".claude"))
		if e != nil {
			return host{}, e
		}
		native = filepath.Join(r, "skills")
	case "cursor":
		native = generic
		eligible = []string{h(".cursor/skills"), h(".claude/skills"), h(".codex/skills")}
	case "opencode", "opencode1", "opencode2":
		r, e := override("XDG_CONFIG_HOME", h(".config"))
		if e != nil {
			return host{}, e
		}
		native = filepath.Join(r, "opencode/skills")
		eligible = []string{native, generic, h(".claude/skills")}
		if env("OPENCODE_CONFIG_DIR") != "" {
			r, e = override("OPENCODE_CONFIG_DIR", "")
			if e != nil {
				return host{}, e
			}
			native = filepath.Join(r, "skills")
		}
	case "zcode":
		native = h(".zcode/skills")
	case "kimi":
		r, e := override("KIMI_CODE_HOME", h(".kimi-code"))
		if e != nil {
			return host{}, e
		}
		native = filepath.Join(r, "skills")
		eligible = []string{generic}
	case "kimi-cli":
		native = h(".kimi/skills")
	default:
		return host{}, invalidArg("Unknown skill host %q", name)
	}
	eligible = append([]string{native}, eligible...)
	out := host{}
	for i, r := range eligible {
		p, e := canonical(filepath.Join(r, Name))
		if e != nil {
			return host{}, e
		}
		if i == 0 {
			out.Preferred = p
		}
		out.Eligible = append(out.Eligible, p)
	}
	return out, nil
}
func knownRoots(home, cwd string, env func(string) string) []string {
	m := map[string]bool{}
	for _, name := range hostOrder {
		h, e := hostRoots(home, env, name)
		if e == nil {
			for _, p := range h.Eligible {
				m[filepath.Dir(p)] = true
			}
		}
	}
	for _, p := range []string{".agents/skills", ".claude/skills", ".codex/skills", ".cursor/skills", ".kimi/skills", ".kimi-code/skills", ".config/agents/skills", ".zcode/skills"} {
		m[filepath.Join(home, p)] = true
	}
	if v := env("CODEX_HOME"); filepath.IsAbs(v) {
		m[filepath.Join(v, "skills")] = true
	}
	// Only walk project ancestors when a repository boundary can be found.
	dirs := []string{}
	for p := cwd; p != ""; p = filepath.Dir(p) {
		dirs = append(dirs, p)
		if _, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
			for _, d := range dirs {
				for _, n := range []string{".agents", ".claude", ".codex", ".cursor", ".opencode", ".kimi", ".kimi-code", ".zcode"} {
					m[filepath.Join(d, n, "skills")] = true
				}
			}
			break
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return sortedKeys(m)
}
func pluginPath(p string) bool {
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i, part := range parts {
		if part == "plugins" && i > 0 {
			switch parts[i-1] {
			case ".codex", ".claude", ".cursor", ".zcode", ".kimi-code", ".opencode":
				return true
			}
		}
	}
	for d := filepath.Dir(p); d != filepath.Dir(d); d = filepath.Dir(d) {
		for _, marker := range []string{".claude-plugin/plugin.json", ".codex-plugin/plugin.json", ".cursor-plugin/plugin.json"} {
			if _, e := os.Lstat(filepath.Join(d, marker)); e == nil {
				return true
			}
		}
	}
	return false
}
