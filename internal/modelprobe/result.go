// Package modelprobe contains session-free discovery primitives shared by adapters.
package modelprobe

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"sort"
	"time"
)

const MaxFrame = 8 << 20
const MaxBytes = 32 << 20
const MaxRows = 10000
const MaxPages = 100

func Bool(v bool) *bool { return &v }
func New(backend, kind, operation, scope string, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	requested := "visible_only"
	if in.IncludeHidden {
		requested = "include_hidden"
	}
	return domain.ModelDiscoveryResult{Backend: backend, Agents: []string{}, Status: "ok", Complete: true, Models: []domain.CatalogModel{}, Diagnostics: []domain.ModelDiagnostic{}, InferenceVerification: "not_checked", Sources: []domain.ModelSource{{ID: operation, Kind: kind, Operation: operation, Scope: scope, RetrievedAt: time.Now().UTC(), Complete: true, HiddenPolicy: domain.HiddenPolicy{Requested: requested, Applied: "unknown"}, Freshness: domain.ModelFreshness{State: "unknown"}}}}
}
func Fail(r *domain.ModelDiscoveryResult, status, code string) {
	r.Complete = false
	r.Status = status
	if len(r.Models) > 0 {
		r.Status = "partial"
	}
	for i := range r.Sources {
		r.Sources[i].Complete = false
	}
	for _, d := range r.Diagnostics {
		if d.Code == code {
			return
		}
	}
	r.Diagnostics = append(r.Diagnostics, domain.ModelDiagnostic{Code: code, Message: "model discovery: " + code})
}
func ContextStatus(ctx context.Context, fallback string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if ctx.Err() != nil {
		return "cancelled"
	}
	return fallback
}
func Issue(r *domain.ModelDiscoveryResult, ctx context.Context, issue domain.InstallationIssue) {
	status := "error"
	switch issue.Code {
	case "not_found":
		status = "not_installed"
	case "unsupported_launcher", "unsupported_runtime", "unsupported_platform", "service_incompatible":
		status = "unsupported"
	case "timed_out":
		status = "timeout"
	case "cancelled":
		status = "cancelled"
	}
	Fail(r, ContextStatus(ctx, status), issue.Code)
	d := &r.Diagnostics[len(r.Diagnostics)-1]
	d.Phase = issue.Phase
	d.RestartRequired = issue.RestartRequired
}
func Model(backend, id, source string) domain.CatalogModel {
	return domain.CatalogModel{ModelID: id, Listed: true, Sources: []string{source}, Selection: domain.ModelSelection{Backend: backend, Supported: true, Options: map[string]string{"model": id}}}
}
func Identity(m domain.CatalogModel) string {
	b, _ := json.Marshal(m.Selection.Options)
	return m.ProviderID + "\x00" + m.ModelID + "\x00" + m.Alias + "\x00" + string(b)
}
func Finish(r *domain.ModelDiscoveryResult) {
	// Group before merging so conflicting duplicates never win by arrival order.
	groups := map[string][]domain.CatalogModel{}
	for _, m := range r.Models {
		groups[Identity(m)] = append(groups[Identity(m)], m)
	}
	r.Models = []domain.CatalogModel{}
	for _, group := range groups {
		m := group[0]
		sources := map[string]bool{}
		conflict := false
		for _, x := range group {
			for _, s := range x.Sources {
				sources[s] = true
			}
			a, b := m, x
			a.Sources = nil
			b.Sources = nil
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(b)
			if string(aa) != string(bb) {
				conflict = true
			}
		}
		if conflict {
			m = domain.CatalogModel{ModelID: m.ModelID, ProviderID: m.ProviderID, Alias: m.Alias, Listed: true, Selection: m.Selection}
			Fail(r, "partial", "conflicting_metadata")
		}
		m.Sources = nil
		for s := range sources {
			m.Sources = append(m.Sources, s)
		}
		sort.Strings(m.Sources)
		r.Models = append(r.Models, m)
	}
	sort.Slice(r.Models, func(i, j int) bool { return Identity(r.Models[i]) < Identity(r.Models[j]) })
	sort.Slice(r.Sources, func(i, j int) bool { return r.Sources[i].ID < r.Sources[j].ID })
	sort.Slice(r.Diagnostics, func(i, j int) bool {
		a, b := r.Diagnostics[i], r.Diagnostics[j]
		return a.Phase+a.Code+a.Message < b.Phase+b.Code+b.Message
	})
	if r.Complete {
		r.Status = "ok"
		if len(r.Models) == 0 {
			r.Status = "empty"
		}
	} else if len(r.Models) > 0 {
		r.Status = "partial"
	}
}
