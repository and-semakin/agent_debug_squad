package skillmanager

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Build struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	Date       string `json:"date"`
	BundleHash string `json:"bundle_hash"`
}
type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Diagnostic) Error() string { return e.Code + ": " + e.Message }
func problem(code string) error     { return &Diagnostic{code, messages[code]} }

var messages = map[string]string{
	"invalid_arguments": "Invalid arguments; see skill --help.", "invalid_override": "Selected host override must be absolute.",
	"unsupported_platform": "Skill mutation requires macOS or Linux.", "busy": "Another maintenance process holds the lock; retry later.",
	"unsafe_path": "Unsafe path, file type, permissions or overlapping destination.", "unmanaged_conflict": "Manual copy exists; use skill install --path DIR --adopt explicitly.",
	"ambiguous_destination": "Several managed copies are eligible; select an exact --path.", "plugin_controlled": "Detach or export this plugin-managed copy before adopting it.",
	"invalid_registry": "Registry is damaged; preserve it and restore a verified backup.", "unsupported_registry": "This binary cannot read the registry schema.",
	"ownership_conflict": "Destination identity differs from its registered owner.", "not_registered": "No managed installation; use skill install with an explicit host or path.",
	"unowned_collision": "Move the conflicting unowned file explicitly, then retry skill sync.", "backup_failed": "Could not save and verify backup; destination left unchanged.",
	"atomic_replace_unsupported": "Filesystem cannot publish the tree atomically; use a supported local filesystem.",
	"unsafe_staging":             "No safe staging location outside discovery roots on this filesystem.", "recovery_conflict": "Pending transaction conflicts with disk contents; preserve journal and both trees.",
	"pending_recovery": "A pending transaction requires skill sync to reconcile it.", "cancelled": "Operation cancelled; retry skill sync to reconcile unfinished work.",
	"newer_managed_release":     "A newer release manages this path; use that binary or explicitly allow downgrade.",
	"release_identity_conflict": "Same release has different bundle bytes; obtain the correct release binary.",
	"development_build":         "Development mutation requires --allow-dev; update is unavailable, use explicit skill sync.",
	"io_error":                  "Filesystem operation failed; check access and free space, then retry.", "concurrent_edit": "Destination changed during preparation; retry after external edits stop.",
}

func diag(err error) *Diagnostic {
	if e, ok := err.(*Diagnostic); ok {
		return e
	}
	return &Diagnostic{"io_error", messages["io_error"]}
}

type Installation struct {
	Path        string          `json:"path"`
	ID          string          `json:"id"`
	Root        string          `json:"discovery_root"`
	Agents      []string        `json:"selected_agents"`
	Files       map[string]File `json:"files"`
	Hash        string          `json:"bundle_hash"`
	Installed   Build           `json:"installed_from"`
	Verified    Build           `json:"last_verified_with"`
	Release     string          `json:"accepted_release,omitempty"`
	ReleaseHash string          `json:"accepted_release_hash,omitempty"`
}
type Registry struct {
	Schema     int                     `json:"schema_version"`
	Generation uint64                  `json:"generation"`
	Entries    map[string]Installation `json:"installations"`
}
type Receipt struct {
	Schema      int    `json:"schema_version"`
	ID          string `json:"id"`
	Transaction string `json:"transaction"`
	Hash        string `json:"bundle_hash"`
}
type Result struct {
	Path        string      `json:"path"`
	Agents      []string    `json:"selected_agents"`
	State       string      `json:"state"`
	Action      string      `json:"action"`
	Before      string      `json:"before_hash,omitempty"`
	After       string      `json:"after_hash,omitempty"`
	BeforeBuild *Build      `json:"before_build,omitempty"`
	AfterBuild  *Build      `json:"after_build,omitempty"`
	Changed     bool        `json:"changed"`
	Reread      bool        `json:"reread_required"`
	Preserved   []string    `json:"preserved_paths"`
	Backup      string      `json:"backup_path,omitempty"`
	Error       *Diagnostic `json:"diagnostic,omitempty"`
	Retry       string      `json:"retry,omitempty"`
}
type Observation struct {
	Path      string `json:"path"`
	Code      string `json:"code"`
	Effective string `json:"effective_selection"`
}
type BinaryResult struct {
	Before string      `json:"before"`
	After  string      `json:"after"`
	Status string      `json:"status"`
	Error  *Diagnostic `json:"diagnostic,omitempty"`
}
type Report struct {
	Schema       int           `json:"schema_version"`
	Program      Build         `json:"program"`
	Status       string        `json:"status"`
	Binary       *BinaryResult `json:"binary,omitempty"`
	Destinations []Result      `json:"destinations"`
	Observations []Observation `json:"observations"`
	Error        *Diagnostic   `json:"error,omitempty"`
}

func NewReport(b Build) Report {
	return Report{Schema: 1, Program: b, Status: "ok", Destinations: []Result{}, Observations: []Observation{}}
}
func (r *Report) Finish() int {
	failed, ok := 0, 0
	for _, d := range r.Destinations {
		if d.Error != nil {
			failed++
		} else {
			ok++
		}
	}
	if r.Error != nil {
		failed++
	}
	if r.Binary != nil {
		if r.Binary.Error != nil {
			failed++
		} else {
			ok++
		}
	}
	sort.Slice(r.Destinations, func(i, j int) bool { return r.Destinations[i].Path < r.Destinations[j].Path })
	if failed == 0 {
		r.Status = "ok"
		return 0
	}
	r.Status = "failed"
	if ok > 0 {
		r.Status = "partial"
	}
	return 1
}
func retry(p string) string {
	return "agent-debug-squad skill sync --path '" + strings.ReplaceAll(p, "'", "'\"'\"'") + "'"
}
func normalizeVersion(v string) (string, bool) {
	p := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(p) != 3 {
		return "", false
	}
	for _, s := range p {
		if s == "" || len(s) > 1 && s[0] == '0' {
			return "", false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return "", false
			}
		}
		if _, e := strconv.ParseUint(s, 10, 64); e != nil {
			return "", false
		}
	}
	return "v" + strings.Join(p, "."), true
}
func compareVersion(a, b string) int {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")
	aa := strings.Split(a, ".")
	bb := strings.Split(b, ".")
	for i := range aa {
		x, _ := strconv.ParseUint(aa[i], 10, 64)
		y, _ := strconv.ParseUint(bb[i], 10, 64)
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
func authority(old Installation, b Build, o Options) error {
	v, release := normalizeVersion(b.Version)
	if !release {
		if !o.AllowDev {
			return problem("development_build")
		}
		return nil
	}
	if old.Release != "" {
		n := compareVersion(v, old.Release)
		if n < 0 && !o.AllowDowngrade {
			return problem("newer_managed_release")
		}
		if n == 0 && old.ReleaseHash != b.BundleHash {
			return problem("release_identity_conflict")
		}
	}
	return nil
}
func hashValid(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func bundleHashValid(h string) bool {
	return strings.HasPrefix(h, "sha256-v1:") && hashValid(strings.TrimPrefix(h, "sha256-v1:"))
}
func normalizeBuild(b Build, bundle Bundle) Build {
	if v, ok := normalizeVersion(b.Version); ok {
		b.Version = v
	}
	b.BundleHash = bundle.Hash
	return b
}
func newResult(p string, a []string) Result {
	return Result{Path: p, Agents: a, State: "current", Action: "unchanged", Preserved: []string{}}
}
func failResult(r Result, err error) Result {
	r.State = "conflict"
	r.Action = "failed"
	r.Error = diag(err)
	r.Retry = retry(r.Path)
	return r
}
func invalidArg(format string, args ...any) error {
	return &Diagnostic{"invalid_arguments", fmt.Sprintf(format, args...)}
}
