package skillmanager

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	bundled "github.com/and-semakin/agent_debug_squad/skills"
	"gopkg.in/yaml.v3"
)

const Name = bundled.Name
const receiptName = ".agent-debug-squad-skill.json"

type File struct {
	Data       []byte `json:"-"`
	Hash       string `json:"hash"`
	Executable bool   `json:"executable"`
}
type Bundle struct {
	Files map[string]File
	Hash  string
}

func validRelative(p string) bool {
	return fs.ValidPath(p) && p != "." && !strings.Contains(p, "\\") && utf8.ValidString(p)
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func file(b []byte, executable bool) File {
	return File{Data: b, Hash: digest(b), Executable: executable}
}
func sortedKeys[V any](m map[string]V) []string {
	a := make([]string, 0, len(m))
	for k := range m {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}

// treeHash v1: domain bytes followed by sorted records: big-endian uint64 path
// length, UTF-8 path bytes, uint64 content length, one executable byte, SHA256.
func treeHash(files map[string]File) string {
	h := sha256.New()
	h.Write([]byte("agent-debug-squad/skill/sha256-v1\x00"))
	for _, p := range sortedKeys(files) {
		f := files[p]
		binary.Write(h, binary.BigEndian, uint64(len(p)))
		h.Write([]byte(p))
		binary.Write(h, binary.BigEndian, uint64(len(f.Data)))
		bit := byte(0)
		if f.Executable {
			bit = 1
		}
		h.Write([]byte{bit})
		sum := sha256.Sum256(f.Data)
		h.Write(sum[:])
	}
	return "sha256-v1:" + hex.EncodeToString(h.Sum(nil))
}
func ValidateSkill(b []byte) error {
	if len(b) >= 100000 || !utf8.Valid(b) {
		return fmt.Errorf("invalid skill size or encoding")
	}
	lines := bytes.SplitN(b, []byte("\n"), 2)
	if len(lines) != 2 || strings.TrimSpace(string(lines[0])) != "---" {
		return fmt.Errorf("missing skill frontmatter")
	}
	end := bytes.Index(lines[1], []byte("\n---"))
	if end < 0 {
		return fmt.Errorf("missing frontmatter delimiter")
	}
	var meta map[string]any
	if err := yaml.Unmarshal(lines[1][:end], &meta); err != nil {
		return fmt.Errorf("invalid frontmatter")
	}
	desc, ok := meta["description"].(string)
	if meta["name"] != Name || !ok || strings.TrimSpace(desc) == "" || utf8.RuneCountInString(desc) > 1024 {
		return fmt.Errorf("invalid skill name or description")
	}
	return nil
}
func LoadBundle(source fs.FS, executables []string) (Bundle, error) {
	out := Bundle{Files: map[string]File{}}
	modes := map[string]bool{}
	seen := map[string]bool{}
	for _, p := range executables {
		if !validRelative(p) || modes[p] {
			return out, fmt.Errorf("invalid executable inventory")
		}
		modes[p] = true
	}
	err := fs.WalkDir(source, ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == "." {
			return nil
		}
		folded := strings.ToLower(p)
		if !validRelative(p) || p == receiptName || seen[folded] {
			return fmt.Errorf("invalid or colliding bundle path: %s", p)
		}
		seen[folded] = true
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular bundle entry: %s", p)
		}
		b, e := fs.ReadFile(source, p)
		if e != nil {
			return e
		}
		out.Files[p] = file(b, modes[p])
		return nil
	})
	if err != nil {
		return out, err
	}
	for p := range modes {
		if _, ok := out.Files[p]; !ok {
			return out, fmt.Errorf("executable missing from bundle: %s", p)
		}
	}
	if err := ValidateSkill(out.Files["SKILL.md"].Data); err != nil {
		return out, err
	}
	for p := range out.Files {
		for q := path.Dir(p); q != "."; q = path.Dir(q) {
			if _, ok := out.Files[q]; ok {
				return out, fmt.Errorf("bundle path conflict")
			}
		}
	}
	out.Hash = treeHash(out.Files)
	return out, nil
}
func Embedded() (Bundle, error) {
	e, err := bundled.Executables()
	if err != nil {
		return Bundle{}, err
	}
	return LoadBundle(bundled.Files(), e)
}
