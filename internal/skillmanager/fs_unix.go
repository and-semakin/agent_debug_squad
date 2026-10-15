//go:build darwin || linux

package skillmanager

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// All filesystem access after canonicalization walks anchored descriptors without
// following links. Locks protect cooperating writers, not hostile same-user code.
func openDir(p string, create bool) (*os.File, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return nil, problem("unsafe_path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(e, unix.ENOENT) && create {
			e = unix.Mkdirat(fd, part, 0700)
			if e == nil || errors.Is(e, unix.EEXIST) {
				next, e = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			}
		}
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), p), nil
}
func writableParent(f *os.File) error {
	s, e := f.Stat()
	if e != nil {
		return e
	}
	if s.Mode().Perm()&0022 != 0 {
		return problem("unsafe_path")
	}
	return nil
}
func stillSame(f *os.File, p string) bool {
	g, e := openDir(p, false)
	if e != nil {
		return false
	}
	defer g.Close()
	a, e := f.Stat()
	if e != nil {
		return false
	}
	b, e := g.Stat()
	return e == nil && os.SameFile(a, b)
}
func readAt(d *os.File, name string) ([]byte, error) {
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return nil, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return nil, problem("unsafe_path")
	}
	return io.ReadAll(f)
}
func writeAt(d *os.File, name string, b []byte, mode uint32) error {
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	return f.Sync()
}
func atomicJSON(dir, name string, v any) error {
	d, e := openDir(dir, true)
	if e != nil {
		return e
	}
	defer d.Close()
	if e = writableParent(d); e != nil {
		return e
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	temp := ".write-" + uuid()
	defer unix.Unlinkat(int(d.Fd()), temp, 0)
	if e = writeAt(d, temp, b, 0600); e != nil {
		return e
	}
	if !stillSame(d, dir) {
		return problem("unsafe_path")
	}
	if e = unix.Renameat(int(d.Fd()), temp, int(d.Fd()), name); e != nil {
		return e
	}
	return d.Sync()
}
func readJSON(dir, name string, v any) error {
	d, e := openDir(dir, false)
	if e != nil {
		return e
	}
	defer d.Close()
	b, e := readAt(d, name)
	if e != nil {
		return e
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(v); e != nil {
		return e
	}
	if decoder.Decode(new(any)) != io.EOF {
		return problem("invalid_registry")
	}
	return nil
}
func uuid() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func validID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}

// Lock is released by the OS on close or process death. It is never inherited.
func Lock(dir, name string) (func(), error) {
	d, e := openDir(dir, true)
	if e != nil {
		return nil, e
	}
	defer d.Close()
	if e = writableParent(d); e != nil {
		return nil, e
	}
	info, e := d.Stat()
	if e != nil {
		return nil, e
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, problem("unsafe_path")
	}
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		unix.Close(fd)
		return nil, problem("unsafe_path")
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		unix.Close(fd)
		return nil, problem("busy")
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}

type snapshot struct {
	Files  map[string]File
	Dirs   []string
	Exists bool
	Hash   string
}

func readTree(p string) (snapshot, error) {
	s := snapshot{Files: map[string]File{}, Dirs: []string{}}
	d, e := openDir(p, false)
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e != nil {
		return s, problem("unsafe_path")
	}
	defer d.Close()
	s.Exists = true
	var walk func(*os.File, string) error
	walk = func(parent *os.File, prefix string) error {
		entries, e := parent.ReadDir(-1)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			name := entry.Name()
			rel := name
			if prefix != "" {
				rel = prefix + "/" + name
			}
			if !validRelative(rel) {
				return problem("unsafe_path")
			}
			var st unix.Stat_t
			if e = unix.Fstatat(int(parent.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
				return e
			}
			switch st.Mode & unix.S_IFMT {
			case unix.S_IFDIR:
				fd, e := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if e != nil {
					return e
				}
				child := os.NewFile(uintptr(fd), rel)
				s.Dirs = append(s.Dirs, rel)
				e = walk(child, rel)
				child.Close()
				if e != nil {
					return e
				}
			case unix.S_IFREG:
				if st.Nlink != 1 {
					return problem("unsafe_path")
				}
				b, e := readAt(parent, name)
				if e != nil {
					return e
				}
				s.Files[rel] = file(b, st.Mode&0111 != 0)
			default:
				return problem("unsafe_path")
			}
		}
		return nil
	}
	if e = walk(d, ""); e != nil {
		return s, e
	}
	s.Hash = snapshotHash(s)
	return s, nil
}
func snapshotHash(s snapshot) string {
	// Empty directories participate in journal identity too.
	dirs := append([]string{}, s.Dirs...)
	sortStrings(dirs)
	b, _ := json.Marshal(dirs)
	return digest([]byte(treeHash(s.Files) + string(b)))
}
func materialize(p string, s snapshot) error {
	d, e := openDir(p, true)
	if e != nil {
		return e
	}
	d.Close()
	for _, dir := range s.Dirs {
		d, e := openDir(filepath.Join(p, filepath.FromSlash(dir)), true)
		if e != nil {
			return e
		}
		e = d.Sync()
		d.Close()
		if e != nil {
			return e
		}
	}
	for _, name := range sortedKeys(s.Files) {
		d, e := openDir(filepath.Join(p, filepath.Dir(filepath.FromSlash(name))), true)
		if e != nil {
			return e
		}
		f := s.Files[name]
		mode := uint32(0644)
		if f.Executable {
			mode = 0755
		}
		e = writeAt(d, filepath.Base(name), f.Data, mode)
		if e == nil {
			e = d.Sync()
		}
		d.Close()
		if e != nil {
			return e
		}
	}
	// Persist every ancestor directory entry, including intermediate directories.
	return syncTreeDirs(p)
}
func syncTreeDirs(p string) error {
	d, e := openDir(p, false)
	if e != nil {
		return e
	}
	defer d.Close()
	entries, e := d.ReadDir(-1)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if e = syncTreeDirs(filepath.Join(p, entry.Name())); e != nil {
				return e
			}
		}
	}
	return d.Sync()
}
func removeTree(p string) error {
	parent, e := openDir(filepath.Dir(p), false)
	if e != nil {
		return e
	}
	defer parent.Close()
	var remove func(*os.File, string) error
	remove = func(d *os.File, name string) error {
		var st unix.Stat_t
		if e := unix.Fstatat(int(d.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
			if errors.Is(e, unix.ENOENT) {
				return nil
			}
			return e
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			return unix.Unlinkat(int(d.Fd()), name, 0)
		}
		fd, e := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		child := os.NewFile(uintptr(fd), name)
		defer child.Close()
		entries, e := child.ReadDir(-1)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if e = remove(child, entry.Name()); e != nil {
				return e
			}
		}
		return unix.Unlinkat(int(d.Fd()), name, unix.AT_REMOVEDIR)
	}
	e = remove(parent, filepath.Base(p))
	if e == nil {
		e = parent.Sync()
	}
	return e
}

func publishError(e error) error {
	if _, ok := e.(*Diagnostic); ok {
		return e
	}
	if errors.Is(e, unix.ENOSYS) || errors.Is(e, unix.ENOTSUP) || errors.Is(e, unix.EINVAL) || errors.Is(e, unix.EXDEV) {
		return problem("atomic_replace_unsupported")
	}
	return e
}
