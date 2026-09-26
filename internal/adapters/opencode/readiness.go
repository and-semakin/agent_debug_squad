package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

var directTransport = func() *http.Transport { t := http.DefaultTransport.(*http.Transport).Clone(); t.Proxy = nil; return t }()

// ReadinessCheck verifies service readiness for this adapter's effective
// configuration without creating sessions or submitting prompts. Managed
// runtimes go through the existing lifecycle hook (owned startup plus the
// healthy:true health predicate); external services use one bounded
// GET /global/health with redirects disabled. A latched runtime reports
// restart_required without another launch.
func (a *Adapter) ReadinessCheck(ctx context.Context) *domain.InstallationIssue {
	links := []domain.InstallationLink{{Label: "OpenCode server documentation", URL: domain.DocOpenCodeServer}}
	mode := a.settings.Mode
	if mode == "" {
		mode = "managed"
	}
	if mode == "managed" {
		err := a.runtime.ensure(ctx)
		if err == nil {
			return nil
		}
		issue := domain.InstallationIssue{
			Phase:             domain.InstallationPhaseReadiness,
			Component:         domain.ComponentService,
			Code:              domain.CodeStartFailed,
			Message:           "the owned OpenCode server is not available; correct the cause and restart Squad explicitly",
			InstallationLinks: links,
		}
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			issue.Code = domain.CodeTimedOut
		case errors.Is(err, context.Canceled):
			issue.Code = domain.CodeCancelled
		}
		issue.RestartRequired = a.runtime.FailureLatched()
		return &issue
	}
	return a.externalHealthCheck(ctx, links)
}

// externalHealthCheck requires a 2xx JSON response containing healthy:true.
// Unreachable or non-2xx endpoints report service_unavailable; a malformed or
// unsupported health shape reports service_incompatible. Raw response bodies
// and credentials never enter diagnostics.
func (a *Adapter) externalHealthCheck(ctx context.Context, links []domain.InstallationLink) *domain.InstallationIssue {
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := a.newRequest(healthCtx, http.MethodGet, "/global/health", nil)
	if err != nil {
		return a.healthIssue(domain.CodeServiceUnavailable, links)
	}
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return a.healthIssue(domain.CodeServiceUnavailable, links)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.healthIssue(domain.CodeServiceUnavailable, links)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return a.healthIssue(domain.CodeServiceUnavailable, links)
	}
	var health struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &health); err != nil || !health.Healthy {
		return a.healthIssue(domain.CodeServiceIncompatible, links)
	}
	return nil
}

func (a *Adapter) healthIssue(code string, links []domain.InstallationLink) *domain.InstallationIssue {
	message := "the OpenCode service did not answer its health endpoint; verify the server is running and reachable at the configured endpoint"
	if code == domain.CodeServiceIncompatible {
		message = "the OpenCode service returned an incompatible health response; verify it runs a compatible server version"
	}
	return &domain.InstallationIssue{
		Phase:             domain.InstallationPhaseReadiness,
		Component:         domain.ComponentService,
		Code:              code,
		Message:           message,
		InstallationLinks: links,
	}
}

func (a *Adapter) checkReady(ctx context.Context) error {
	if a.runtime == nil {
		return nil
	} // bare transport constructor for protocol tests
	if err := a.runtime.ctx.Err(); err != nil {
		return err
	}
	if a.settings.Mode == "managed" {
		if err := a.runtime.ensure(ctx); err != nil {
			return err
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var cfg struct {
		Snapshot *bool `json:"snapshot"`
	}
	if err := a.doJSON(checkCtx, http.MethodGet, "/config", nil, &cfg); err != nil {
		return errors.New("cannot verify effective opencode.snapshot via GET /config; no work submitted")
	}
	expected := a.settings.Snapshot != nil && *a.settings.Snapshot
	if cfg.Snapshot == nil || *cfg.Snapshot != expected {
		return fmt.Errorf("effective opencode.snapshot must be %t; check external or administrator configuration; no work submitted", expected)
	}
	return nil
}

func (a *Adapter) verifySession(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var session sessionResponse
	if err := a.doJSON(ctx, http.MethodGet, "/session/"+url.PathEscape(id), nil, &session); err != nil || session.ID != id || !sameDirectory(session.Directory, a.workspace) {
		return errors.New("saved OpenCode session is unavailable in this workspace; restore its data or choose a new Squad session; no work replayed")
	}
	return nil
}

func (a *Adapter) redact(value string) string {
	if a.runtime == nil {
		return value
	}
	a.runtime.mu.Lock()
	r := a.runtime.redactor
	a.runtime.mu.Unlock()
	if r == nil {
		return value
	}
	return r.Replace(value)
}
func (a *Adapter) safeError(err error) error {
	if err == nil {
		return nil
	}
	safe := a.redact(err.Error())
	if safe == err.Error() {
		return err
	}
	return errors.New(safe)
}

// Sanitize only data crossing the adapter boundary, not protocol identifiers
// used internally to correlate messages, sessions and permission replies.
type redactingSink struct {
	domain.RunSink
	adapter *Adapter
}

func (s redactingSink) StdoutLine(line string) { s.RunSink.StdoutLine(s.adapter.redact(line)) }
func (s redactingSink) StderrLine(line string) { s.RunSink.StderrLine(s.adapter.redact(line)) }
func (s redactingSink) DiagnosticLine(line string) {
	domain.ReportRunDiagnostic(s.RunSink, s.adapter.redact(line))
}
func (s redactingSink) Progress(p domain.RunProgress) {
	data, _ := json.Marshal(p)
	var value any
	_ = json.Unmarshal(data, &value)
	var clean func(any) any
	clean = func(v any) any {
		switch x := v.(type) {
		case string:
			return s.adapter.redact(x)
		case map[string]any:
			for k, v := range x {
				x[k] = clean(v)
			}
		case []any:
			for i, v := range x {
				x[i] = clean(v)
			}
		}
		return v
	}
	data, _ = json.Marshal(clean(value))
	var safe domain.RunProgress
	if json.Unmarshal(data, &safe) == nil {
		domain.ReportRunProgress(s.RunSink, safe)
	}
}

func sameDirectory(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	l, err := filepath.EvalSymlinks(left)
	if err != nil {
		return false
	}
	r, err := filepath.EvalSymlinks(right)
	return err == nil && l == r
}
