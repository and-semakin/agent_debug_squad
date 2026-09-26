package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

var directTransport = func() *http.Transport { t := http.DefaultTransport.(*http.Transport).Clone(); t.Proxy = nil; return t }()

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
