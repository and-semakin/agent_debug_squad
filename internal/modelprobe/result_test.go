package modelprobe

import (
	"context"
	"encoding/json"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestIssueMapping(t *testing.T) {
	for code, want := range map[string]string{"not_found": "not_installed", "unsupported_launcher": "unsupported", "unsupported_runtime": "unsupported", "unsupported_platform": "unsupported", "service_incompatible": "unsupported", "not_executable": "error", "not_readable": "error", "invalid_search_path": "error", "check_failed": "error", "start_failed": "error", "restart_required": "error", "service_unavailable": "error", "timed_out": "timeout", "cancelled": "cancelled"} {
		t.Run(code, func(t *testing.T) {
			r := New("fake", "synthetic", "test", "synthetic", domain.ModelDiscoveryInput{})
			Issue(&r, context.Background(), domain.InstallationIssue{Code: code, Phase: "readiness", RestartRequired: true})
			if r.Status != want || r.Complete || !r.Diagnostics[0].RestartRequired {
				t.Fatalf("%+v", r)
			}
		})
	}
}
func TestSanitizeAndConflicts(t *testing.T) {
	r := New("kimi", "config", "test", "configured", domain.ModelDiscoveryInput{})
	a := Model("kimi", "m", "test")
	a.DisplayName = "secret-123 https://user:password@host?q=token"
	r.Models = []domain.CatalogModel{a, Model("kimi", "secret-123", "test")}
	Sanitize(&r, []string{"secret-123", "password"})
	b, _ := json.Marshal(r)
	for _, secret := range []string{"secret-123", "password", "user:"} {
		if strings.Contains(string(b), secret) {
			t.Fatal(string(b))
		}
	}
	if len(r.Models) != 1 || r.Complete {
		t.Fatal(r)
	}
	a = Model("kimi", "m", "one")
	c := a
	c.Sources = []string{"two"}
	c.DisplayName = "conflict"
	r.Models = []domain.CatalogModel{c, a}
	Finish(&r)
	if len(r.Models) != 1 || r.Models[0].DisplayName != "" || len(r.Models[0].Sources) != 2 || r.Status != "partial" {
		t.Fatal(r)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	Issue(&r, ctx, domain.InstallationIssue{Code: "service_unavailable"})
	if r.Diagnostics[len(r.Diagnostics)-1].Code != "service_unavailable" {
		t.Fatal(r)
	}
}

func TestCatalogMapKeysAreNotCredentials(t *testing.T) {
	raw := map[string]any{"models": map[string]any{"long-token-model": map[string]any{"model": "long-token-model", "displayName": "Display"}}, "providers": map[string]any{"p": map[string]any{"apiKey": "SENTINEL"}}}
	got := Secrets(raw)
	if len(got) != 1 || got[0] != "SENTINEL" {
		t.Fatal(got)
	}
}

func TestSourceFreshnessIsNotRetrievalTime(t *testing.T) {
	r := New("codex", "rpc", "models", "account_catalog", domain.ModelDiscoveryInput{})
	Freshness(&r, 0, []byte(`{"freshness":{"state":"stale","cached":true,"as_of":"2000-01-01T05:00:00+05:00"}}`))
	f := r.Sources[0].Freshness
	if f.State != "stale" || f.Cached == nil || !*f.Cached || f.AsOf.Format(time.RFC3339) != "2000-01-01T00:00:00Z" {
		t.Fatal(f)
	}
	Freshness(&r, 0, []byte(`{}`))
	if r.Sources[0].Freshness.State != "stale" {
		t.Fatal(r)
	}
	Freshness(&r, 0, []byte(`{"freshness":{"state":"fresh"}}`))
	if r.Complete || r.Sources[0].Freshness.State != "stale" {
		t.Fatal(r)
	}
}
