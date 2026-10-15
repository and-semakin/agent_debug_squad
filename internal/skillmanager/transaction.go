package skillmanager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type journal struct {
	Schema     int          `json:"schema_version"`
	ID         string       `json:"transaction"`
	Stage      string       `json:"stage"`
	OldHash    string       `json:"old_hash"`
	NewHash    string       `json:"new_hash"`
	OldExists  bool         `json:"old_exists"`
	Generation uint64       `json:"generation"`
	Entry      Installation `json:"installation"`
}

func (m *Manager) loadRegistry() (Registry, error) {
	r := Registry{}
	e := readJSON(m.state, "registry.json", &r)
	if errors.Is(e, os.ErrNotExist) {
		return Registry{Schema: 1, Entries: map[string]Installation{}}, nil
	}
	if r.Schema != 0 && r.Schema != 1 {
		return r, problem("unsupported_registry")
	}
	if e != nil || r.Schema != 1 {
		return r, problem("invalid_registry")
	}
	if r.Entries == nil {
		return r, problem("invalid_registry")
	}
	for p, v := range r.Entries {
		if p != v.Path || !validInstallation(v) || overlap(p, m.state) || within(m.state, filepath.Dir(p)) {
			return r, problem("invalid_registry")
		}
		for q := range r.Entries {
			if p != q && overlap(p, q) {
				return r, problem("invalid_registry")
			}
		}
	}
	return r, nil
}
func validInstallation(v Installation) bool {
	if !filepath.IsAbs(v.Path) || filepath.Clean(v.Path) != v.Path || filepath.Base(v.Path) != Name || v.Root != filepath.Dir(v.Path) || !validID(v.ID) || !bundleHashValid(v.Hash) || len(v.Files) == 0 {
		return false
	}
	if v.Release != "" {
		n, ok := normalizeVersion(v.Release)
		if !ok || n != v.Release || !bundleHashValid(v.ReleaseHash) {
			return false
		}
	}
	seen := map[string]bool{}
	for p, f := range v.Files {
		key := strings.ToLower(p)
		if !validRelative(p) || p == receiptName || seen[key] || !hashValid(f.Hash) {
			return false
		}
		seen[key] = true
		for parent := filepath.ToSlash(filepath.Dir(p)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if _, ok := v.Files[parent]; ok {
				return false
			}
		}
	}
	return true
}
func (m *Manager) saveRegistry(r Registry) error { return atomicJSON(m.state, "registry.json", r) }
func (m *Manager) pending() ([]string, error) {
	d, e := openDir(filepath.Join(m.state, "transactions"), false)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer d.Close()
	entries, e := d.ReadDir(-1)
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".write-") {
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !validID(strings.TrimSuffix(entry.Name(), ".json")) {
			return nil, problem("recovery_conflict")
		}
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out, nil
}
func (m *Manager) recover(r *Registry) error {
	names, e := m.pending()
	if e != nil {
		return e
	}
	for _, name := range names {
		var j journal
		if e = readJSON(filepath.Join(m.state, "transactions"), name, &j); e != nil {
			return problem("recovery_conflict")
		}
		if j.Schema != 1 || j.ID+".json" != name || !validID(j.ID) || !validInstallation(j.Entry) || j.Stage != stagePath(j.Entry.Root, j.ID) || !hashValid(j.NewHash) || j.OldExists && !hashValid(j.OldHash) {
			return problem("recovery_conflict")
		}
		if e = m.safeDestination(j.Entry.Path, *r); e != nil {
			return problem("recovery_conflict")
		}
		if e = m.safeStage(j.Stage, *r); e != nil {
			return problem("recovery_conflict")
		}
		current, e := readTree(j.Entry.Path)
		if e != nil {
			return problem("recovery_conflict")
		}
		stage, e := readTree(filepath.Join(j.Stage, "tree"))
		if e != nil {
			return problem("recovery_conflict")
		}
		published := current.Exists && current.Hash == j.NewHash
		unpublished := current.Exists == j.OldExists && current.Hash == j.OldHash
		committed := reflect.DeepEqual(r.Entries[j.Entry.Path], j.Entry) && r.Generation == j.Generation+1
		if published {
			if !committed {
				if r.Generation != j.Generation {
					return problem("recovery_conflict")
				}
				r.Entries[j.Entry.Path] = j.Entry
				r.Generation++
				if e = m.saveRegistry(*r); e != nil {
					return e
				}
			}
			if stage.Exists && stage.Hash != j.OldHash {
				return problem("recovery_conflict")
			}
		} else if unpublished && r.Generation == j.Generation {
			if !stage.Exists || stage.Hash != j.NewHash {
				return problem("recovery_conflict")
			}
		} else {
			return problem("recovery_conflict")
		}
		if e = removeTree(j.Stage); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e = m.removeJournal(name); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) removeJournal(name string) error {
	return removeTree(filepath.Join(m.state, "transactions", name))
}
func stagePath(root, id string) string { return filepath.Join(filepath.Dir(root), ".squad-skill-"+id) }
func (m *Manager) safeStage(stage string, r Registry) error {
	if overlap(stage, m.state) {
		return problem("unsafe_staging")
	}
	roots := append([]string{}, m.roots...)
	for _, v := range r.Entries {
		roots = append(roots, v.Root)
	}
	for _, root := range roots {
		canonicalRoot, e := canonical(filepath.Join(root, Name))
		if e == nil && overlap(stage, filepath.Dir(canonicalRoot)) {
			return problem("unsafe_staging")
		}
	}
	return nil
}
func (m *Manager) safeDestination(p string, r Registry) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || filepath.Base(p) != Name || overlap(p, m.state) || within(m.state, filepath.Dir(p)) {
		return problem("unsafe_path")
	}
	if pluginPath(p) {
		return problem("plugin_controlled")
	}
	for q := range r.Entries {
		if p != q && overlap(p, q) {
			return problem("unsafe_path")
		}
	}
	return nil
}
func (m *Manager) backup(s snapshot) (string, error) {
	dir := filepath.Join(m.state, "backups")
	d, e := openDir(dir, true)
	if e != nil {
		return "", problem("backup_failed")
	}
	defer d.Close()
	// Archives contain no discoverable loose SKILL.md. Read back and verify each
	// archived byte and mode before permitting any live-tree replacement.
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, p := range s.Dirs {
		if e = tw.WriteHeader(&tar.Header{Name: p + "/", Mode: 0700, Typeflag: tar.TypeDir}); e != nil {
			return "", problem("backup_failed")
		}
	}
	for _, p := range sortedKeys(s.Files) {
		f := s.Files[p]
		mode := int64(0644)
		if f.Executable {
			mode = 0755
		}
		if e = tw.WriteHeader(&tar.Header{Name: p, Mode: mode, Size: int64(len(f.Data)), Typeflag: tar.TypeReg}); e != nil {
			return "", problem("backup_failed")
		}
		if _, e = tw.Write(f.Data); e != nil {
			return "", problem("backup_failed")
		}
	}
	if tw.Close() != nil || gz.Close() != nil {
		return "", problem("backup_failed")
	}
	name := uuid() + ".tar.gz"
	if e = writeAt(d, name, b.Bytes(), 0600); e != nil {
		return "", problem("backup_failed")
	}
	if e = d.Sync(); e != nil {
		return "", problem("backup_failed")
	}
	data, e := readAt(d, name)
	if e != nil {
		return "", problem("backup_failed")
	}
	if !bytes.Equal(data, b.Bytes()) {
		return "", problem("backup_failed")
	}
	gr, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return "", problem("backup_failed")
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	check := snapshot{Files: map[string]File{}, Dirs: []string{}}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", problem("backup_failed")
		}
		if h.Typeflag == tar.TypeDir {
			check.Dirs = append(check.Dirs, strings.TrimSuffix(h.Name, "/"))
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return "", problem("backup_failed")
		}
		content, e := io.ReadAll(tr)
		if e != nil {
			return "", problem("backup_failed")
		}
		check.Files[h.Name] = file(content, h.Mode&0111 != 0)
	}
	if snapshotHash(check) != s.Hash {
		return "", problem("backup_failed")
	}
	return filepath.Join(dir, name), nil
}
func inventoriesEqual(a, b map[string]File) bool {
	if len(a) != len(b) {
		return false
	}
	for p, f := range a {
		g, ok := b[p]
		if !ok || f.Hash != g.Hash || f.Executable != g.Executable {
			return false
		}
	}
	return true
}
func metadata(files map[string]File) map[string]File {
	out := map[string]File{}
	for p, f := range files {
		f.Data = nil
		out[p] = f
	}
	return out
}

func (m *Manager) reconcile(ctx context.Context, r *Registry, p string, agents []string, o Options) Result {
	result := newResult(p, agents)
	old, registered := r.Entries[p]
	if registered {
		result.Before = old.Hash
		result.BeforeBuild = &old.Installed
		result.Agents = union(old.Agents, agents)
	}
	fail := func(e error) Result { return failResult(result, e) }
	if e := m.safeDestination(p, *r); e != nil {
		return fail(e)
	}
	if e := authority(old, m.Build, o); e != nil {
		return fail(e)
	}
	before, e := readTree(p)
	if e != nil {
		return fail(e)
	}
	if !registered && before.Exists && !o.Adopt {
		return fail(problem("unmanaged_conflict"))
	}
	if !registered && before.Exists {
		if e = ValidateSkill(before.Files["SKILL.md"].Data); e != nil {
			return fail(problem("ownership_conflict"))
		}
	}
	needsBackup := !registered && before.Exists
	validReceipt := false
	if registered && before.Exists {
		var rec Receipt
		raw, ok := before.Files[receiptName]
		err := json.Unmarshal(raw.Data, &rec)
		if ok && err == nil && rec.ID != old.ID {
			return fail(problem("ownership_conflict"))
		}
		validReceipt = ok && err == nil && rec.Schema == 1 && rec.ID == old.ID && rec.Hash == old.Hash && validID(rec.Transaction)
		if !validReceipt {
			needsBackup = true
		}
		for name, previous := range old.Files {
			f, ok := before.Files[name]
			if ok && (f.Hash != previous.Hash || f.Executable != previous.Executable) {
				needsBackup = true
			}
		}
		if old.Hash != m.Bundle.Hash && (o.AllowDev || o.AllowDowngrade || old.Installed.Version == "dev") {
			needsBackup = true
		}
	}
	desired := snapshot{Files: map[string]File{}, Dirs: append([]string{}, before.Dirs...), Exists: true}
	for name, f := range before.Files {
		if name == receiptName {
			continue
		}
		if _, owned := old.Files[name]; registered && owned {
			continue
		}
		if !registered && o.Adopt {
			if _, incoming := m.Bundle.Files[name]; incoming {
				continue
			}
		}
		desired.Files[name] = f
		result.Preserved = append(result.Preserved, name)
	}
	sort.Strings(result.Preserved)
	for name, f := range m.Bundle.Files {
		if _, exists := desired.Files[name]; exists {
			return fail(problem("unowned_collision"))
		}
		desired.Files[name] = f
	}
	// File/directory structural conflicts require explicit user resolution even
	// when the directory is currently empty.
	for name := range desired.Files {
		for _, d := range before.Dirs {
			if name == d {
				return fail(problem("unowned_collision"))
			}
		}
		for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if _, ok := desired.Files[parent]; ok {
				return fail(problem("unowned_collision"))
			}
		}
	}
	next := old
	next.Path = p
	next.Root = filepath.Dir(p)
	next.Agents = result.Agents
	next.Files = metadata(m.Bundle.Files)
	next.Hash = m.Bundle.Hash
	next.Verified = m.Build
	if !registered {
		next.ID = uuid()
	}
	if v, release := normalizeVersion(m.Build.Version); release {
		next.Release = v
		next.ReleaseHash = m.Bundle.Hash
	}
	payloadCurrent := registered && validReceipt && inventoriesEqual(old.Files, m.Bundle.Files)
	if payloadCurrent {
		for name, f := range m.Bundle.Files {
			got, ok := before.Files[name]
			if !ok || got.Hash != f.Hash || got.Executable != f.Executable {
				payloadCurrent = false
				break
			}
		}
	}
	if payloadCurrent {
		if !reflect.DeepEqual(old, next) {
			r.Entries[p] = next
			r.Generation++
			if e = m.saveRegistry(*r); e != nil {
				return fail(e)
			}
		}
		result.After = next.Hash
		result.AfterBuild = &next.Installed
		return result
	}
	next.Installed = m.Build
	if needsBackup {
		result.Backup, e = m.backup(before)
		if e != nil {
			return fail(e)
		}
	}
	id := uuid()
	receipt, _ := json.Marshal(Receipt{1, next.ID, id, next.Hash})
	desired.Files[receiptName] = file(append(receipt, '\n'), false)
	stage := stagePath(next.Root, id)
	if e = m.safeStage(stage, *r); e != nil {
		return fail(e)
	}
	// The selected custom discovery root itself is never an eligible staging area.
	if overlap(stage, next.Root) {
		return fail(problem("unsafe_staging"))
	}
	parent, e := openDir(filepath.Dir(p), true)
	if e != nil {
		return fail(e)
	}
	defer parent.Close()
	if e = writableParent(parent); e != nil {
		return fail(e)
	}
	stageParent, e := openDir(filepath.Dir(stage), false)
	if e != nil {
		return fail(e)
	}
	defer stageParent.Close()
	if e = writableParent(stageParent); e != nil {
		return fail(e)
	}
	if e = materialize(filepath.Join(stage, "tree"), desired); e != nil {
		removeTree(stage)
		return fail(e)
	}
	journaled := false
	defer func() {
		if !journaled {
			removeTree(stage)
		}
	}()
	staged, e := readTree(filepath.Join(stage, "tree"))
	if e != nil {
		return fail(e)
	}
	// Include any implicit parent directories made by materialization.
	if !inventoriesEqual(staged.Files, desired.Files) {
		return fail(problem("io_error"))
	}
	current, e := readTree(p)
	if e != nil || current.Hash != before.Hash || current.Exists != before.Exists {
		return fail(problem("concurrent_edit"))
	}
	if !stillSame(parent, filepath.Dir(p)) || !stillSame(stageParent, filepath.Dir(stage)) {
		return fail(problem("unsafe_path"))
	}
	if e = ctx.Err(); e != nil {
		return fail(problem("cancelled"))
	}
	j := journal{1, id, stage, before.Hash, staged.Hash, before.Exists, r.Generation, next}
	if e = atomicJSON(filepath.Join(m.state, "transactions"), id+".json", j); e != nil {
		return fail(e)
	}
	journaled = true
	if m.fault != nil {
		if e = m.fault("before_publish"); e != nil {
			return fail(e)
		}
	}
	current, e = readTree(p)
	if e != nil || current.Hash != before.Hash || current.Exists != before.Exists {
		return fail(problem("concurrent_edit"))
	}
	if !stillSame(parent, filepath.Dir(p)) || !stillSame(stageParent, filepath.Dir(stage)) {
		return fail(problem("unsafe_path"))
	}
	source, e := openDir(stage, false)
	if e != nil {
		return fail(e)
	}
	defer source.Close()
	publish := m.publish
	if publish == nil {
		publish = exchange
	}
	if e = publish(int(source.Fd()), "tree", int(parent.Fd()), filepath.Base(p), before.Exists); e != nil {
		// Journal still describes the unchanged live tree; normal recovery removes
		// the proven unpublished stage before another mutation.
		return fail(publishError(e))
	}
	if e = parent.Sync(); e != nil {
		return fail(e)
	}
	if e = source.Sync(); e != nil {
		return fail(e)
	}
	if m.fault != nil {
		if e = m.fault("after_publish"); e != nil {
			return fail(e)
		}
	}
	// Once published, finish the small commit regardless of cancellation.
	r.Entries[p] = next
	r.Generation++
	if e = m.saveRegistry(*r); e != nil {
		return fail(e)
	}
	result.After = next.Hash
	result.AfterBuild = &next.Installed
	result.Changed = true
	result.Reread = true
	result.State = "current"
	result.Action = "updated"
	if !registered {
		result.Action = "installed"
	} else if !before.Exists || !validReceipt {
		result.Action = "repaired"
	}
	if m.fault != nil {
		if e = m.fault("after_registry"); e != nil {
			return fail(e)
		}
	}
	if e = removeTree(stage); e != nil {
		return fail(e)
	}
	if e = m.removeJournal(id + ".json"); e != nil {
		return fail(e)
	}
	return result
}
func union(a, b []string) []string {
	m := map[string]bool{}
	for _, s := range append(append([]string{}, a...), b...) {
		m[s] = true
	}
	return sortedKeys(m)
}

func (m *Manager) pendingPaths() (map[string]bool, error) {
	names, e := m.pending()
	if e != nil {
		return nil, e
	}
	paths := map[string]bool{}
	for _, name := range names {
		var j journal
		if e = readJSON(filepath.Join(m.state, "transactions"), name, &j); e != nil {
			return nil, problem("recovery_conflict")
		}
		if j.Schema != 1 || !validInstallation(j.Entry) || j.ID+".json" != name {
			return nil, problem("recovery_conflict")
		}
		paths[j.Entry.Path] = true
	}
	return paths, nil
}
