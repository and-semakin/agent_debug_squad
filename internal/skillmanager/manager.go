package skillmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

type Options struct {
	Command        string
	Agents         []string
	Path           string
	Adopt          bool
	AllowDev       bool
	AllowDowngrade bool
	JSON           bool
}
type Manager struct {
	Home    string
	CWD     string
	Env     func(string) string
	Build   Build
	Bundle  Bundle
	state   string
	roots   []string
	fault   func(string) error // crash-boundary injection, never controlled by environment
	publish func(int, string, int, string, bool) error
}

func New(home, cwd string, env func(string) string, b Build, bundle Bundle) (*Manager, error) {
	if !filepath.IsAbs(home) {
		return nil, problem("unsafe_path")
	}
	h, e := filepath.EvalSymlinks(home)
	if e != nil {
		return nil, e
	}
	state, e := canonical(filepath.Join(h, ".agent-debug-squad", "skill-management"))
	if e != nil {
		return nil, e
	}
	if env == nil {
		env = func(string) string { return "" }
	}
	roots := knownRoots(h, cwd, env)
	for _, root := range roots {
		p, err := canonical(filepath.Join(root, Name))
		if err == nil && within(state, filepath.Dir(p)) {
			return nil, problem("unsafe_path")
		}
	}
	return &Manager{Home: h, CWD: cwd, Env: env, Build: normalizeBuild(b, bundle), Bundle: bundle, state: state, roots: roots}, nil
}
func Default(b Build) (*Manager, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	cwd, e := os.Getwd()
	if e != nil {
		return nil, e
	}
	bundle, e := Embedded()
	if e != nil {
		return nil, e
	}
	return New(home, cwd, os.Getenv, b, bundle)
}
func (m *Manager) StateDir() string { return m.state }
func (m *Manager) validate(o Options) error {
	if o.Command != "install" && o.Command != "sync" && o.Command != "status" {
		return invalidArg("Expected skill install, status or sync")
	}
	if o.Path != "" && (len(o.Agents) > 0 || !filepath.IsAbs(o.Path) || filepath.Clean(o.Path) != o.Path || filepath.Base(o.Path) != Name) {
		return invalidArg("--path must be one absolute exact skill directory, exclusive with --agent")
	}
	if o.Command == "install" && o.Path == "" && len(o.Agents) == 0 {
		return invalidArg("Installation requires --agent or --path")
	}
	if o.Command == "sync" && len(o.Agents) > 0 {
		return invalidArg("Sync selects registered paths, not hosts")
	}
	if o.Adopt && (o.Command != "install" || o.Path == "") {
		return invalidArg("--adopt requires install --path")
	}
	if o.AllowDev && o.AllowDowngrade || o.Command == "status" && (o.AllowDev || o.AllowDowngrade || o.Adopt) {
		return invalidArg("Incompatible authority flags")
	}
	_, release := normalizeVersion(m.Build.Version)
	if o.AllowDev && release || o.AllowDowngrade && !release {
		return invalidArg("Authority flag does not match this build kind")
	}
	for _, name := range o.Agents {
		if _, e := hostRoots(m.Home, m.Env, name); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) observe(r Registry) []Observation {
	paths := map[string]bool{}
	for _, root := range m.roots {
		p := filepath.Join(root, Name)
		if _, e := os.Lstat(p); e == nil {
			paths[p] = true
		}
	}
	for p := range r.Entries {
		paths[p] = true
	}
	out := []Observation{}
	for _, p := range sortedKeys(paths) {
		code := "unmanaged"
		if _, ok := r.Entries[p]; ok {
			code = "managed"
		}
		if len(paths) > 1 {
			code = "duplicate_or_shadowed"
		}
		out = append(out, Observation{p, code, "unknown"})
	}
	return out
}
func (m *Manager) targets(o Options, r Registry) (map[string][]string, []Result, error) {
	selected := map[string][]string{}
	failures := []Result{}
	if o.Path != "" {
		p, e := canonical(o.Path)
		if e != nil {
			return nil, nil, e
		}
		if o.Command == "sync" {
			if _, ok := r.Entries[p]; !ok {
				return nil, nil, problem("not_registered")
			}
		}
		selected[p] = []string{}
		return selected, failures, nil
	}
	if len(o.Agents) == 0 {
		for p, v := range r.Entries {
			selected[p] = v.Agents
		}
		return selected, failures, nil
	}
	for _, name := range hostOrder {
		requested := false
		for _, n := range o.Agents {
			if n == "opencode1" || n == "opencode2" {
				n = "opencode"
			}
			if n == name {
				requested = true
			}
		}
		if !requested {
			continue
		}
		h, e := hostRoots(m.Home, m.Env, name)
		if e != nil {
			return nil, nil, e
		}
		if o.Command == "status" {
			found := false
			for p, v := range r.Entries {
				for _, a := range v.Agents {
					if a == name {
						found = true
						selected[p] = v.Agents
					}
				}
			}
			if !found {
				selected[h.Preferred] = []string{name}
			}
			continue
		}
		registered := []string{}
		conflict := false
		for _, p := range union(h.Eligible, nil) {
			if _, ok := r.Entries[p]; ok {
				registered = append(registered, p)
			} else if _, e := os.Lstat(p); e == nil {
				failures = append(failures, failResult(newResult(p, []string{name}), problem("unmanaged_conflict")))
				conflict = true
			}
		}
		// Observe the legacy Codex root too; never silently multiply a manual copy.
		if name == "codex" {
			legacy := m.Env("CODEX_HOME")
			if legacy == "" {
				legacy = filepath.Join(m.Home, ".codex")
			}
			if filepath.IsAbs(legacy) {
				p := filepath.Join(legacy, "skills", Name)
				if _, managed := r.Entries[p]; !managed {
					if _, e := os.Lstat(p); e == nil {
						failures = append(failures, failResult(newResult(p, []string{name}), problem("unmanaged_conflict")))
						conflict = true
					}
				}
			}
		}
		if conflict {
			continue
		}
		if len(registered) > 1 {
			failures = append(failures, failResult(newResult(h.Preferred, []string{name}), problem("ambiguous_destination")))
			continue
		}
		target := h.Preferred
		if len(registered) == 1 {
			target = registered[0]
		} else {
			for _, p := range h.Eligible {
				if _, ok := selected[p]; ok {
					target = p
					break
				}
			}
		}
		selected[target] = union(selected[target], []string{name})
	}
	return selected, failures, nil
}
func (m *Manager) status(p string, agents []string, r Registry, pending bool) Result {
	out := newResult(p, agents)
	v, ok := r.Entries[p]
	if pending {
		out.State = "pending_recovery"
		out.Error = diag(problem("pending_recovery"))
		out.Retry = retry(p)
		return out
	}
	if !ok {
		out.State = "not_registered"
		out.Action = "inspect"
		return out
	}
	out.Before = v.Hash
	out.BeforeBuild = &v.Installed
	if pending {
		return failResult(out, problem("pending_recovery"))
	}
	if e := m.safeDestination(p, r); e != nil {
		return failResult(out, e)
	}
	s, e := readTree(p)
	if e != nil {
		return failResult(out, e)
	}
	state := "current"
	if !s.Exists {
		state = "missing"
	} else {
		var rec Receipt
		if e = json.Unmarshal(s.Files[receiptName].Data, &rec); e != nil || rec.Schema != 1 || rec.ID != v.ID || rec.Hash != v.Hash {
			state = "modified"
		}
		for name, f := range v.Files {
			g, exists := s.Files[name]
			if !exists || g.Hash != f.Hash || g.Executable != f.Executable {
				state = "modified"
			}
		}
		for name := range s.Files {
			if name != receiptName {
				if _, managed := v.Files[name]; !managed {
					out.Preserved = append(out.Preserved, name)
				}
			}
		}
		sortStrings(out.Preserved)
		if state == "current" && v.Hash != m.Bundle.Hash {
			state = "outdated"
		}
	}
	if e = authority(v, m.Build, Options{AllowDev: true}); e != nil {
		return failResult(out, e)
	}
	out.State = state
	out.Action = "inspect"
	if state != "current" {
		out.Error = &Diagnostic{state, "Registered files differ; run skill sync with the appropriate binary."}
		out.Retry = retry(p)
	} else {
		out.After = v.Hash
		out.AfterBuild = &v.Installed
	}
	return out
}
func (m *Manager) Execute(ctx context.Context, o Options) (Report, int) {
	report := NewReport(m.Build)
	if e := m.validate(o); e != nil {
		report.Error = diag(e)
		report.Finish()
		return report, 2
	}
	mutating := o.Command != "status"
	if mutating && !supportedPlatform(runtime.GOOS) {
		report.Error = diag(problem("unsupported_platform"))
		return report, report.Finish()
	}
	if mutating {
		if e := authority(Installation{}, m.Build, o); e != nil {
			report.Error = diag(e)
			return report, report.Finish()
		}
	}
	// Inspect state first so missing-registry sync/status doesn't create files.
	r, e := m.loadRegistry()
	if e != nil {
		report.Error = diag(e)
		return report, report.Finish()
	}
	if mutating && len(r.Entries) == 0 && o.Command == "sync" && o.Path == "" {
		pending, e := m.pending()
		if e != nil {
			report.Error = diag(e)
			return report, report.Finish()
		}
		if len(pending) == 0 {
			report.Observations = m.observe(r)
			return report, 0
		}
	}
	if mutating {
		if e = authority(Installation{}, m.Build, o); e != nil {
			report.Error = diag(e)
			return report, report.Finish()
		}
		release, e := Lock(m.state, "lock")
		if e != nil {
			report.Error = diag(e)
			return report, report.Finish()
		}
		defer release()
		r, e = m.loadRegistry()
		if e == nil {
			e = m.recover(&r)
		}
		if e != nil {
			report.Error = diag(e)
			return report, report.Finish()
		}
	}
	targets, failed, e := m.targets(o, r)
	report.Destinations = append(report.Destinations, failed...)
	if e != nil {
		report.Error = diag(e)
		return report, report.Finish()
	}
	report.Observations = m.observe(r)
	pending, e := m.pendingPaths()
	if e != nil {
		report.Error = diag(e)
		return report, report.Finish()
	}
	if !mutating && o.Path == "" && len(o.Agents) == 0 {
		for p := range pending {
			if _, ok := targets[p]; !ok {
				targets[p] = []string{}
			}
		}
	}
	for _, p := range sortedKeys(targets) {
		if !mutating {
			res := m.status(p, targets[p], r, pending[p])
			if res.Error != nil && res.Error.Code == "pending_recovery" {
				res.State = "pending_recovery"
			}
			report.Destinations = append(report.Destinations, res)
			continue
		}
		if ctx.Err() != nil {
			report.Destinations = append(report.Destinations, failResult(newResult(p, targets[p]), problem("cancelled")))
			continue
		}
		// Reload/recover after any preceding partial outcome before another write.
		r, e = m.loadRegistry()
		if e == nil {
			e = m.recover(&r)
		}
		if e != nil {
			report.Error = diag(e)
			break
		}
		report.Destinations = append(report.Destinations, m.reconcile(ctx, &r, p, targets[p], o))
	}
	if !mutating && len(pending) > 0 && len(report.Destinations) == 0 {
		report.Error = diag(problem("pending_recovery"))
	}
	return report, report.Finish()
}
func (m *Manager) UpdateLock(executable string) (func(), error) {
	p, e := filepath.EvalSymlinks(executable)
	if e != nil {
		return nil, e
	}
	return Lock(m.state, "update-"+digest([]byte(p))+".lock")
}

// Expose stable, content-free operational diagnostics to update orchestration.
func Failure(code, message string) *Diagnostic { return &Diagnostic{Code: code, Message: message} }
func IsRelease(v string) bool                  { _, ok := normalizeVersion(v); return ok }
func SameVersion(a, b string) bool {
	x, ok := normalizeVersion(a)
	y, ok2 := normalizeVersion(b)
	return ok && ok2 && x == y
}
func IsMissing(err error) bool { return errors.Is(err, os.ErrNotExist) }
func HasJSON(args []string) bool {
	for _, a := range args {
		if a == "--json" || a == "--json=true" {
			return true
		}
	}
	return false
}
func readable(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			quoted := strconv.QuoteRune(r)
			b.WriteString(quoted[1 : len(quoted)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// InspectCommitted reports disk state against each registry-owned payload after
// an uncertain child outcome. It never writes or interprets old embedded bytes
// as the newly installed executable's desired bundle.
func (m *Manager) InspectCommitted() Report {
	report := NewReport(m.Build)
	r, e := m.loadRegistry()
	if e != nil {
		report.Error = diag(e)
		report.Finish()
		return report
	}
	pending, e := m.pendingPaths()
	if e != nil {
		report.Error = diag(e)
		report.Finish()
		return report
	}
	for _, p := range sortedKeys(r.Entries) {
		entry := r.Entries[p]
		view := *m
		view.Build = entry.Verified
		view.Bundle = Bundle{Hash: entry.Hash, Files: entry.Files}
		d := view.status(p, entry.Agents, r, pending[p])
		d.Action = "inspected_after_handoff"
		report.Destinations = append(report.Destinations, d)
	}
	report.Finish()
	return report
}

func supportedPlatform(goos string) bool { return goos == "darwin" || goos == "linux" }
