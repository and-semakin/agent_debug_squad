package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/skillmanager"
)

// Maintenance coordinates executable and skill changes without coupling them
// to backend or workspace state. Download/install stays in the existing Client.
type Maintenance struct {
	Client     Client
	Manager    *skillmanager.Manager
	Executable string
	// Injection points allow tests to use fixture executables and offline releases.
	Check   func(context.Context) (*Release, error)
	Install func(context.Context, Release, string) error
	Handoff func(context.Context, string, string) (skillmanager.Report, error)
}

func executableDigest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func updateFailure(code, message string) *skillmanager.Diagnostic {
	return skillmanager.Failure(code, message)
}
func (u Maintenance) Run(ctx context.Context) (skillmanager.Report, bool) {
	r := skillmanager.NewReport(u.Manager.Build)
	binary := &skillmanager.BinaryResult{Before: u.Manager.Build.Version, After: u.Manager.Build.Version, Status: "unchanged"}
	r.Binary = binary
	if !skillmanager.IsRelease(u.Manager.Build.Version) {
		r.Error = updateFailure("development_build", "Self-update is unavailable for dev; use skill sync --allow-dev explicitly.")
		r.Finish()
		return r, false
	}
	executable, e := filepath.EvalSymlinks(u.Executable)
	if e != nil {
		r.Error = updateFailure("executable_unavailable", "Cannot resolve the installed executable.")
		r.Finish()
		return r, false
	}
	unlock, e := u.Manager.UpdateLock(executable)
	if e != nil {
		r.Error = updateFailure("busy", "Cannot acquire executable maintenance lock; retry after other maintenance finishes.")
		r.Finish()
		return r, false
	}
	defer unlock()
	check := u.Check
	if check == nil {
		check = u.Client.Check
	}
	install := u.Install
	if install == nil {
		install = u.Client.Install
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	release, e := check(checkCtx)
	cancel()
	updated := false
	uncertain := false
	if e != nil {
		binary.Status = "failed"
		binary.Error = updateFailure("release_check_failed", "Release check failed; local skill reconciliation is still attempted.")
	}
	if e == nil && release != nil {
		beforeHash, hashErr := executableDigest(executable)
		if hashErr != nil {
			binary.Status = "unknown"
			binary.Error = updateFailure("executable_unavailable", "Cannot verify the executable before replacement.")
			uncertain = true
		} else {
			installCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			err := install(installCtx, *release, executable)
			cancel()
			if err != nil {
				binary.Status = "failed"
				binary.Error = updateFailure("binary_install_failed", "Binary installation failed; verify the installed executable before retrying.")
				afterHash, hashErr := executableDigest(executable)
				if hashErr != nil || beforeHash != afterHash {
					uncertain = true
					binary.Status = "unknown"
					binary.After = "unknown"
				}
			} else {
				updated = true
				binary.Status = "installed"
				binary.After = release.Version
			}
		}
	}
	if uncertain {
		r.Error = updateFailure("binary_outcome_unknown", "Skill synchronization skipped. Verify the installed binary, then run its skill sync command.")
		r.Finish()
		return r, updated
	}
	if updated {
		before := u.Manager.InspectCommitted()
		handoff := u.Handoff
		if handoff == nil {
			handoff = SyncInstalled
		}
		handoffCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		child, err := handoff(handoffCtx, executable, release.Version)
		cancel()
		if err != nil {
			observed := u.Manager.InspectCommitted()
			r.Destinations = observed.Destinations
			for i := range r.Destinations {
				d := &r.Destinations[i]
				if d.Error != nil {
					continue
				}
				if d.AfterBuild == nil || !skillmanager.SameVersion(d.AfterBuild.Version, release.Version) {
					d.State = "unknown"
					d.Error = updateFailure("skill_sync_unconfirmed", "Installed release was not confirmed at this destination; retry skill sync.")
					continue
				}
				for _, previous := range before.Destinations {
					if previous.Path == d.Path && (previous.Before != d.After || previous.Error != nil) {
						d.Changed = true
						d.Reread = true
						d.Action = "verified_updated"
						d.Before = previous.Before
					}
				}
			}
			r.Error = updateFailure("skill_handoff_failed", "New executable remains installed; skill outcomes are unknown. Run the installed executable's skill sync --json to reconcile and inspect them.")
		} else {
			r = child
			r.Binary = binary
		}
	} else {
		r, _ = u.Manager.Execute(ctx, skillmanager.Options{Command: "sync"})
		r.Binary = binary
	}
	r.Finish()
	return r, updated
}

// boundedBuffer limits untrusted child diagnostics without killing the reader.
type boundedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.Len()
	if len(p) > left {
		p = p[:left]
		b.overflow = true
	}
	b.Buffer.Write(p)
	return n, nil
}
func SyncInstalled(ctx context.Context, path, version string) (skillmanager.Report, error) {
	var report skillmanager.Report
	cmd := exec.CommandContext(ctx, path, "skill", "sync", "--json")
	out := &boundedBuffer{limit: 4 << 20}
	stderr := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	// Never inherit the private serve continuation into a child or later backend.
	env := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, ContinuationEnv+"=") {
			env = append(env, v)
		}
	}
	cmd.Env = env
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil || out.overflow {
		return report, fmt.Errorf("handoff interrupted or output too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&report); decodeErr != nil || decoder.Decode(new(any)) != io.EOF {
		return report, fmt.Errorf("invalid handoff report")
	}
	if report.Schema != 1 || !skillmanager.SameVersion(report.Program.Version, version) || !validBundleHash(report.Program.BundleHash) || report.Destinations == nil || report.Observations == nil {
		return report, fmt.Errorf("incompatible handoff identity")
	}
	expected := report.Status
	code := report.Finish()
	if expected != report.Status {
		return report, fmt.Errorf("inconsistent handoff status")
	}
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 1 || code != 1 {
			return report, fmt.Errorf("handoff exit disagrees with report")
		}
	} else if code != 0 {
		return report, fmt.Errorf("handoff unexpectedly succeeded")
	}
	return report, nil
}
func validBundleHash(h string) bool {
	if !strings.HasPrefix(h, "sha256-v1:") {
		return false
	}
	b, e := hex.DecodeString(strings.TrimPrefix(h, "sha256-v1:"))
	return e == nil && len(b) == 32
}

const ContinuationEnv = "_AGENT_DEBUG_SQUAD_UPDATE_CONTINUATION"

// ConsumeContinuation removes the private marker before any backend environment
// can be captured. A marker applies only to this process and installed version.
func ConsumeContinuation(version string) bool {
	value := os.Getenv(ContinuationEnv)
	os.Unsetenv(ContinuationEnv)
	prefix := fmt.Sprintf("%d:", os.Getpid())
	return strings.HasPrefix(value, prefix) && skillmanager.SameVersion(strings.TrimPrefix(value, prefix), version)
}
func ContinuationEnvironment(version string) []string {
	env := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, ContinuationEnv+"=") {
			env = append(env, v)
		}
	}
	return append(env, fmt.Sprintf("%s=%d:%s", ContinuationEnv, os.Getpid(), version))
}
