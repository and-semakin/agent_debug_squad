package skillmanager

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	bundled "github.com/and-semakin/agent_debug_squad/skills"
)

const testSkill = "---\nname: agent-debug-squad\ndescription: Manage a squad.\n---\nInstructions.\n"

func testBundle(t *testing.T, extra map[string]string) Bundle {
	t.Helper()
	fs := fstest.MapFS{"SKILL.md": {Data: []byte(testSkill)}}
	for p, s := range extra {
		fs[p] = &fstest.MapFile{Data: []byte(s)}
	}
	b, e := LoadBundle(fs, nil)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func manager(t *testing.T, b Bundle, version string) *Manager {
	t.Helper()
	home := t.TempDir()
	home, e := filepath.EvalSymlinks(home)
	if e != nil {
		t.Fatal(e)
	}
	m, e := New(home, home, nil, Build{Version: version}, b)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func run(t *testing.T, m *Manager, o Options) Report {
	t.Helper()
	r, c := m.Execute(context.Background(), o)
	if c != 0 {
		t.Fatalf("exit %d: %+v, results %+v", c, r, r.Destinations)
	}
	return r
}
func install(t *testing.T, m *Manager) string {
	t.Helper()
	r := run(t, m, Options{Command: "install", Agents: []string{"codex"}})
	return r.Destinations[0].Path
}
func write(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func requireCode(t *testing.T, r Report, want string) {
	t.Helper()
	if r.Error != nil && r.Error.Code == want {
		return
	}
	for _, d := range r.Destinations {
		if d.Error != nil && d.Error.Code == want {
			return
		}
	}
	t.Fatalf("missing %s: %+v %+v", want, r.Error, r.Destinations)
}

func TestEmbeddedSourceInventory(t *testing.T) {
	b, e := Embedded()
	if e != nil {
		t.Fatal(e)
	}
	executables, e := bundled.Executables()
	if e != nil {
		t.Fatal(e)
	}
	source := os.DirFS("../../skills/agent-debug-squad")
	actual, e := LoadBundle(source, executables)
	if e != nil {
		t.Fatal(e)
	}
	if b.Hash != actual.Hash || !inventoriesEqual(b.Files, actual.Files) {
		t.Fatal("embedded files differ from source")
	}
	for p, f := range actual.Files {
		s, e := os.Stat(filepath.Join("../../skills/agent-debug-squad", p))
		if e != nil {
			t.Fatal(e)
		}
		if (s.Mode()&0111 != 0) != f.Executable {
			t.Fatalf("executable manifest drift: %s", p)
		}
	}
	for _, guidance := range []string{"models --all --json", "reviewer-quorum"} {
		if !strings.Contains(string(b.Files["SKILL.md"].Data), guidance) {
			t.Fatal("lost existing guidance", guidance)
		}
	}
}
func TestBundleIdentity(t *testing.T) {
	a := testBundle(t, map[string]string{".hidden": "secret", "references/a": "a"})
	b := testBundle(t, map[string]string{"references/a": "a", ".hidden": "secret"})
	if a.Hash != b.Hash {
		t.Fatal("order dependent")
	}
	for _, extra := range []map[string]string{{".hidden": "secret"}, {".hidden": "secret", "references/b": "a"}, {".hidden": "secret", "references/a": "b"}} {
		if testBundle(t, extra).Hash == a.Hash {
			t.Fatal("missed tree change")
		}
	}
	changed := a.Files["references/a"]
	changed.Executable = true
	a.Files["references/a"] = changed
	if treeHash(a.Files) == b.Hash {
		t.Fatal("mode not hashed")
	}
	golden := treeHash(map[string]File{"a": file([]byte("x"), false)})
	const want = "sha256-v1:ddd2cdc226558846ebfcb561b8eaea59a04dcf2ec76a4fc7557cd5ca2a7b90a7"
	if golden != want {
		t.Fatalf("golden %s", golden)
	}
}
func TestInvalidBundle(t *testing.T) {
	for name, fs := range map[string]fstest.MapFS{
		"symlink":     {"SKILL.md": {Data: []byte(testSkill)}, "link": {Mode: os.ModeSymlink, Data: []byte("target")}},
		"case":        {"SKILL.md": {Data: []byte(testSkill)}, "a": {}, "A": {}},
		"receipt":     {"SKILL.md": {Data: []byte(testSkill)}, receiptName: {}},
		"frontmatter": {"SKILL.md": {Data: []byte("bad")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := LoadBundle(fs, nil); e == nil {
				t.Fatal("accepted invalid bundle")
			}
		})
	}
}
func TestFreshSharedInstallAndNoop(t *testing.T) {
	m := manager(t, testBundle(t, map[string]string{"references/a": "a", ".hidden": "h"}), "1.0.0")
	r := run(t, m, Options{Command: "install", Agents: []string{"kimi", "cursor", "codex", "opencode"}})
	if len(r.Destinations) != 1 || len(r.Destinations[0].Agents) != 4 {
		t.Fatalf("not shared: %+v", r)
	}
	p := r.Destinations[0].Path
	before, _ := os.Stat(filepath.Join(p, "SKILL.md"))
	m.Build.Version = "v1.0.1"
	r = run(t, m, Options{Command: "sync"})
	after, _ := os.Stat(filepath.Join(p, "SKILL.md"))
	if r.Destinations[0].Changed || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("rewrote current payload")
	}
	reg, e := m.loadRegistry()
	if e != nil || reg.Entries[p].Release != "v1.0.1" {
		t.Fatal("authority not advanced", e)
	}
	run(t, m, Options{Command: "status"})
}
func TestReconcileLocalEditsAndDeletion(t *testing.T) {
	m := manager(t, testBundle(t, map[string]string{"references/obsolete": "old"}), "v1.0.0")
	p := install(t, m)
	write(t, filepath.Join(p, "SKILL.md"), testSkill+"local edit")
	write(t, filepath.Join(p, "notes.txt"), "unowned")
	m.Bundle = testBundle(t, map[string]string{"references/new": "new"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	r := run(t, m, Options{Command: "sync"})
	d := r.Destinations[0]
	if d.Backup == "" || !d.Changed || !reflect.DeepEqual(d.Preserved, []string{"notes.txt"}) {
		t.Fatalf("result %+v", d)
	}
	if _, e := os.Stat(filepath.Join(p, "references/obsolete")); !os.IsNotExist(e) {
		t.Fatal("obsolete remains")
	}
	b, _ := os.ReadFile(filepath.Join(p, "notes.txt"))
	if string(b) != "unowned" {
		t.Fatal("unowned lost")
	}
	if e := os.RemoveAll(p); e != nil {
		t.Fatal(e)
	}
	r = run(t, m, Options{Command: "sync"})
	if r.Destinations[0].Action != "repaired" {
		t.Fatal("not repaired")
	}
}
func TestAdoptionAndUnownedCollision(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	p := filepath.Join(m.Home, "custom", "skills", Name)
	write(t, filepath.Join(p, "SKILL.md"), testSkill)
	write(t, filepath.Join(p, "legacy"), "keep")
	r, _ := m.Execute(context.Background(), Options{Command: "install", Path: p})
	requireCode(t, r, "unmanaged_conflict")
	r = run(t, m, Options{Command: "install", Path: p, Adopt: true})
	if r.Destinations[0].Backup == "" {
		t.Fatal("no adoption backup")
	}
	m.Bundle = testBundle(t, map[string]string{"legacy": "replace"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	r, _ = m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "unowned_collision")
	b, _ := os.ReadFile(filepath.Join(p, "legacy"))
	if string(b) != "keep" {
		t.Fatal("overwrote collision")
	}
}
func TestAuthority(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v2.0.0")
	p := install(t, m)
	m.Build.Version = "v1.0.0"
	r, _ := m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "newer_managed_release")
	run(t, m, Options{Command: "sync", Path: p, AllowDowngrade: true})
	m.Bundle = testBundle(t, map[string]string{"new": "resource"})
	m.Build = normalizeBuild(Build{Version: "v1.0.0"}, m.Bundle)
	r, _ = m.Execute(context.Background(), Options{Command: "sync", AllowDowngrade: true})
	requireCode(t, r, "release_identity_conflict")
	m.Build.Version = "dev"
	r, _ = m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "development_build")
	run(t, m, Options{Command: "sync", AllowDev: true})
	m.Bundle = testBundle(t, nil)
	m.Build = normalizeBuild(Build{Version: "v1.0.0"}, m.Bundle)
	r = run(t, m, Options{Command: "sync"})
	if r.Destinations[0].Backup == "" {
		t.Fatal("lost dev payload")
	}
}
func TestCrashRecovery(t *testing.T) {
	for _, point := range []string{"before_publish", "after_publish", "after_registry"} {
		t.Run(point, func(t *testing.T) {
			m := manager(t, testBundle(t, nil), "v1.0.0")
			p := install(t, m)
			m.Bundle = testBundle(t, map[string]string{"next": "value"})
			m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
			m.fault = func(s string) error {
				if s == point {
					return errors.New("injected crash")
				}
				return nil
			}
			r, _ := m.Execute(context.Background(), Options{Command: "sync"})
			if r.Status == "ok" {
				t.Fatal("no fault")
			}
			m.fault = nil
			r, _ = m.Execute(context.Background(), Options{Command: "status"})
			requireCode(t, r, "pending_recovery")
			run(t, m, Options{Command: "sync"})
			run(t, m, Options{Command: "status"})
			b, _ := os.ReadFile(filepath.Join(p, "next"))
			if string(b) != "value" {
				t.Fatal("not recovered")
			}
		})
	}
}
func TestPostCrashEditConflict(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	p := install(t, m)
	m.Bundle = testBundle(t, map[string]string{"next": "value"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	m.fault = func(s string) error {
		if s == "after_publish" {
			return errors.New("crash")
		}
		return nil
	}
	m.Execute(context.Background(), Options{Command: "sync"})
	m.fault = nil
	write(t, filepath.Join(p, "next"), "outside edit")
	r, _ := m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "recovery_conflict")
	b, _ := os.ReadFile(filepath.Join(p, "next"))
	if string(b) != "outside edit" {
		t.Fatal("clobbered edit")
	}
}
func TestMissingRegistryAndInvalidState(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	run(t, m, Options{Command: "status"})
	run(t, m, Options{Command: "sync"})
	if _, e := os.Stat(m.state); !os.IsNotExist(e) {
		t.Fatal("read-only inspection created state")
	}
	p := install(t, m)
	if e := os.Remove(filepath.Join(m.state, "registry.json")); e != nil {
		t.Fatal(e)
	}
	run(t, m, Options{Command: "sync"})
	r, _ := m.Execute(context.Background(), Options{Command: "install", Path: p})
	requireCode(t, r, "unmanaged_conflict")
	write(t, filepath.Join(m.state, "registry.json"), `{"schema_version":2,"generation":0,"installations":{}}`)
	r, _ = m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "unsupported_registry")
	write(t, filepath.Join(m.state, "registry.json"), "bad")
	r, _ = m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "invalid_registry")
}
func TestPathsAndForeignReceipt(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "receipt", "traversal", "plugin", "nested"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t, testBundle(t, nil), "v1.0.0")
			p := install(t, m)
			switch kind {
			case "symlink":
				if e := os.Symlink("/etc/passwd", filepath.Join(p, "link")); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(filepath.Join(p, "SKILL.md"), filepath.Join(p, "hard")); e != nil {
					t.Fatal(e)
				}
			case "receipt":
				write(t, filepath.Join(p, receiptName), `{"schema_version":1,"id":"foreign"}`)
			case "traversal":
				r, _ := m.loadRegistry()
				v := r.Entries[p]
				v.Files["../outside"] = File{Hash: strings.Repeat("a", 64)}
				r.Entries[p] = v
				if e := m.saveRegistry(r); e != nil {
					t.Fatal(e)
				}
			case "plugin":
				path := filepath.Join(m.Home, ".claude", "plugins", "cache", Name)
				r, _ := m.Execute(context.Background(), Options{Command: "install", Path: path})
				requireCode(t, r, "plugin_controlled")
				return
			case "nested":
				r, _ := m.Execute(context.Background(), Options{Command: "install", Path: filepath.Join(p, "sub", Name)})
				requireCode(t, r, "unsafe_path")
				return
			}
			r, c := m.Execute(context.Background(), Options{Command: "sync"})
			if c == 0 {
				t.Fatalf("accepted %s: %+v", kind, r)
			}
		})
	}
}
func TestOverrides(t *testing.T) {
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	for _, tc := range []struct{ host, key, suffix string }{{"claude-code", "CLAUDE_CONFIG_DIR", "skills"}, {"opencode", "XDG_CONFIG_HOME", "opencode/skills"}, {"opencode", "OPENCODE_CONFIG_DIR", "skills"}, {"kimi", "KIMI_CODE_HOME", "skills"}} {
		t.Run(tc.host+tc.key, func(t *testing.T) {
			root := filepath.Join(home, "profile")
			h, e := hostRoots(home, func(k string) string {
				if k == tc.key {
					return root
				}
				return ""
			}, tc.host)
			if e != nil || h.Preferred != filepath.Join(root, tc.suffix, Name) {
				t.Fatal(h, e)
			}
			_, e = hostRoots(home, func(k string) string {
				if k == tc.key {
					return "relative"
				}
				return ""
			}, tc.host)
			if e == nil {
				t.Fatal("relative override")
			}
		})
	}
}
func TestPartialAndCancellation(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	r := run(t, m, Options{Command: "install", Agents: []string{"codex", "claude-code"}})
	p := r.Destinations[0].Path
	write(t, filepath.Join(p, "extra"), "local")
	m.Bundle = testBundle(t, map[string]string{"extra": "bundled"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	r, c := m.Execute(context.Background(), Options{Command: "sync"})
	if c != 1 || r.Status != "partial" {
		t.Fatalf("not partial: %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, c = m.Execute(ctx, Options{Command: "sync"})
	if c != 1 {
		t.Fatal("cancel ignored")
	}
	requireCode(t, r, "cancelled")
}
func TestLockAndJSON(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	install(t, m)
	release, e := Lock(m.state, "lock")
	if e != nil {
		t.Fatal(e)
	}
	r, _ := m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "busy")
	release()
	run(t, m, Options{Command: "sync"})
	var b strings.Builder
	if e = Print(&b, r, true); e != nil {
		t.Fatal(e)
	}
	var decoded Report
	if e = json.Unmarshal([]byte(b.String()), &decoded); e != nil {
		t.Fatal(e)
	}
}

func TestInitialAdoptionRecovery(t *testing.T) {
	for _, point := range []string{"before_publish", "after_publish", "after_registry"} {
		t.Run(point, func(t *testing.T) {
			m := manager(t, testBundle(t, nil), "v1.0.0")
			p := filepath.Join(m.Home, "manual", "skills", Name)
			write(t, filepath.Join(p, "SKILL.md"), testSkill+"local")
			write(t, filepath.Join(p, "extra"), "preserve")
			m.fault = func(s string) error {
				if s == point {
					return errors.New("crash")
				}
				return nil
			}
			m.Execute(context.Background(), Options{Command: "install", Path: p, Adopt: true})
			m.fault = nil
			run(t, m, Options{Command: "install", Path: p, Adopt: true})
			run(t, m, Options{Command: "status"})
			data, _ := os.ReadFile(filepath.Join(p, "extra"))
			if string(data) != "preserve" {
				t.Fatal("lost adoption extras")
			}
		})
	}
}
func TestUnsafeParentsBackupAndExternalEdits(t *testing.T) {
	for _, kind := range []string{"parent", "backup", "external_edit", "ancestor"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t, testBundle(t, nil), "v1.0.0")
			p := install(t, m)
			write(t, filepath.Join(p, "SKILL.md"), testSkill+"modified")
			switch kind {
			case "parent":
				if e := os.Chmod(filepath.Dir(p), 0777); e != nil {
					t.Fatal(e)
				}
				defer os.Chmod(filepath.Dir(p), 0700)
			case "backup":
				write(t, filepath.Join(m.state, "backups"), "not a directory")
			case "external_edit":
				m.fault = func(s string) error {
					if s == "before_publish" {
						write(t, filepath.Join(p, "edited"), "concurrent")
					}
					return nil
				}
			case "ancestor":
				m.fault = func(s string) error {
					if s == "before_publish" {
						parent := filepath.Dir(p)
						if e := os.Rename(parent, parent+"-moved"); e != nil {
							t.Fatal(e)
						}
						if e := os.Mkdir(parent, 0700); e != nil {
							t.Fatal(e)
						}
					}
					return nil
				}
			}
			r, c := m.Execute(context.Background(), Options{Command: "sync"})
			if c != 1 {
				t.Fatalf("expected failure: %+v", r)
			}
			want := map[string]string{"parent": "unsafe_path", "backup": "backup_failed", "external_edit": "concurrent_edit", "ancestor": "concurrent_edit"}[kind]
			requireCode(t, r, want)
		})
	}
}
func TestStableSymlinkAncestorAndDestinationLink(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	real := filepath.Join(m.Home, "real")
	if e := os.Mkdir(real, 0700); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(m.Home, "alias")
	if e := os.Symlink(real, alias); e != nil {
		t.Fatal(e)
	}
	r := run(t, m, Options{Command: "install", Path: filepath.Join(alias, "skills", Name)})
	if r.Destinations[0].Path != filepath.Join(real, "skills", Name) {
		t.Fatal("not canonical")
	}
	link := filepath.Join(m.Home, "linked", Name)
	os.MkdirAll(filepath.Dir(link), 0700)
	os.Symlink(r.Destinations[0].Path, link)
	r, c := m.Execute(context.Background(), Options{Command: "install", Path: link, Adopt: true})
	if c == 0 {
		t.Fatal("followed skill symlink")
	}
	requireCode(t, r, "unsafe_path")
}
func TestAmbiguousCopiesAndStatusFilter(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	run(t, m, Options{Command: "install", Agents: []string{"codex", "claude-code"}})
	r, _ := m.Execute(context.Background(), Options{Command: "install", Agents: []string{"cursor"}})
	requireCode(t, r, "ambiguous_destination")
	r = run(t, m, Options{Command: "status", Agents: []string{"codex", "kimi"}})
	if len(r.Destinations) != 2 {
		t.Fatalf("missing unregistered selected host %+v", r)
	}
	if len(r.Observations) < 2 || r.Observations[0].Code != "duplicate_or_shadowed" {
		t.Fatal("duplicates not disclosed")
	}
}
func TestBadArgumentsAreReadOnly(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	for _, o := range []Options{{Command: "install"}, {Command: "sync", Agents: []string{"codex"}}, {Command: "install", Path: "relative"}, {Command: "install", Agents: []string{"fake"}}, {Command: "status", AllowDev: true}, {Command: "install", Path: filepath.Join(m.Home, "a", "..", Name), Agents: []string{"codex"}}} {
		_, code := m.Execute(context.Background(), o)
		if code != 2 {
			t.Fatalf("arguments accepted: %+v", o)
		}
	}
	if _, e := os.Stat(m.state); !os.IsNotExist(e) {
		t.Fatal("argument handling wrote state")
	}
	var b strings.Builder
	code := Main(context.Background(), []string{"sync", "--bogus", "--json"}, Build{Version: "dev"}, &b)
	if code != 2 {
		t.Fatal(code)
	}
	var r Report
	if e := json.Unmarshal([]byte(b.String()), &r); e != nil || r.Error == nil {
		t.Fatal("not one error envelope", e)
	}
}
func TestReceiptRepairAndForeignOwner(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	p := install(t, m)
	os.Remove(filepath.Join(p, receiptName))
	r := run(t, m, Options{Command: "sync"})
	if r.Destinations[0].Backup == "" || !r.Destinations[0].Reread {
		t.Fatal("missing receipt not backed up/repaired")
	}
}
func TestCancellationAfterPublicationFinishesCommit(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	install(t, m)
	m.Bundle = testBundle(t, map[string]string{"next": "new"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.fault = func(s string) error {
		if s == "after_publish" {
			cancel()
		}
		return nil
	}
	r, c := m.Execute(ctx, Options{Command: "sync"})
	if c != 0 || !r.Destinations[0].Changed {
		t.Fatalf("commit not finished: %+v", r)
	}
	m.fault = nil
	run(t, m, Options{Command: "status"})
}

func TestAtomicUnsupportedLeavesLiveTree(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	p := install(t, m)
	before, _ := readTree(p)
	m.Bundle = testBundle(t, map[string]string{"next": "new"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	m.publish = func(int, string, int, string, bool) error { return problem("atomic_replace_unsupported") }
	r, _ := m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "atomic_replace_unsupported")
	after, _ := readTree(p)
	if before.Hash != after.Hash {
		t.Fatal("live tree changed")
	}
	m.publish = nil
	run(t, m, Options{Command: "sync"})
}
func TestFrozenDestinationAndLegacyCodexObservation(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	profile := filepath.Join(m.Home, "profile")
	m.Env = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return profile
		}
		return ""
	}
	r := run(t, m, Options{Command: "install", Agents: []string{"claude-code"}})
	p := r.Destinations[0].Path
	m.Env = func(string) string { return "" }
	os.RemoveAll(p)
	r = run(t, m, Options{Command: "sync"})
	if r.Destinations[0].Path != p {
		t.Fatal("retargeted registration")
	}
	legacy := filepath.Join(m.Home, ".codex", "skills", Name)
	write(t, filepath.Join(legacy, "SKILL.md"), testSkill)
	r, _ = m.Execute(context.Background(), Options{Command: "install", Agents: []string{"codex"}})
	requireCode(t, r, "unmanaged_conflict")
}
func TestUnsafeStagingAndStructuralCollision(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	p := install(t, m)
	m.roots = append(m.roots, filepath.Dir(filepath.Dir(p)))
	m.Bundle = testBundle(t, map[string]string{"next": "new"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	r, _ := m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "unsafe_staging")
	m.roots = nil
	os.Mkdir(filepath.Join(p, "next"), 0700)
	r, _ = m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "unowned_collision")
}

func TestRecoveryDoesNotObscureOtherCommittedTargets(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	run(t, m, Options{Command: "install", Agents: []string{"codex", "claude-code"}})
	m.Bundle = testBundle(t, map[string]string{"next": "new"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	count := 0
	m.fault = func(s string) error {
		if s == "after_publish" {
			count++
			if count == 2 {
				return errors.New("crash")
			}
		}
		return nil
	}
	m.Execute(context.Background(), Options{Command: "sync"})
	r := m.InspectCommitted()
	current, pending := 0, 0
	for _, d := range r.Destinations {
		if d.Error == nil {
			current++
		}
		if d.State == "pending_recovery" {
			pending++
		}
	}
	if current != 1 || pending != 1 {
		t.Fatalf("lost individual outcomes: %+v", r.Destinations)
	}
}

func TestMaintenanceLockReleasedAfterProcessDeath(t *testing.T) {
	const key = "SQUAD_TEST_LOCK_CHILD"
	if dir := os.Getenv(key); dir != "" {
		unlock, e := Lock(dir, "lock")
		if e != nil {
			os.Exit(3)
		}
		defer unlock()
		os.Stdout.WriteString("locked\n")
		select {}
	}
	m := manager(t, testBundle(t, nil), "v1.0.0")
	install(t, m)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMaintenanceLockReleasedAfterProcessDeath$")
	cmd.Env = append(os.Environ(), key+"="+m.state)
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	line, e := bufio.NewReader(pipe).ReadString('\n')
	if e != nil || line != "locked\n" {
		t.Fatal("child did not acquire lock", e)
	}
	r, _ := m.Execute(context.Background(), Options{Command: "sync"})
	requireCode(t, r, "busy")
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	cmd.Wait()
	run(t, m, Options{Command: "sync"})
}

func TestUnwritableDestinationDoesNotBlockAnother(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses filesystem write permissions")
	}
	m := manager(t, testBundle(t, nil), "v1.0.0")
	r := run(t, m, Options{Command: "install", Agents: []string{"codex", "claude-code"}})
	p := filepath.Dir(r.Destinations[0].Path)
	if e := os.Chmod(p, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(p, 0700)
	m.Bundle = testBundle(t, map[string]string{"next": "new"})
	m.Build = normalizeBuild(Build{Version: "v1.1.0"}, m.Bundle)
	r, c := m.Execute(context.Background(), Options{Command: "sync"})
	if c != 1 || r.Status != "partial" {
		t.Fatalf("not partial: %+v", r)
	}
	changed := 0
	for _, d := range r.Destinations {
		if d.Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatal("other target not updated")
	}
}

func TestPlatformAndHumanEscaping(t *testing.T) {
	for _, goos := range []string{"windows", "freebsd", "plan9"} {
		if supportedPlatform(goos) {
			t.Fatal("unsupported platform accepted", goos)
		}
	}
	if readable("a\nb\x1b") != "a\\nb\\x1b" {
		t.Fatal("human control characters not escaped", readable("a\nb\x1b"))
	}
}

func TestRegistryRequiresSchemaAndRejectsFutureFields(t *testing.T) {
	m := manager(t, testBundle(t, nil), "v1.0.0")
	for _, tc := range []struct{ contents, code string }{{`{}`, "invalid_registry"}, {`{"installations":{}}`, "invalid_registry"}, {`{"schema_version":2,"future":true}`, "unsupported_registry"}} {
		write(t, filepath.Join(m.state, "registry.json"), tc.contents)
		r, _ := m.Execute(context.Background(), Options{Command: "status"})
		requireCode(t, r, tc.code)
	}
}
