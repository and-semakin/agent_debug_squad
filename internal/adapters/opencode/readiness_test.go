package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// TestExternalReadinessClassification covers the bounded health protocol:
// healthy:true (version optional) passes; refused connections and non-2xx
// report service_unavailable; malformed shapes report service_incompatible.
func TestExternalReadinessClassification(t *testing.T) {
	cases := []struct {
		name     string
		handler  http.HandlerFunc
		hang     bool
		wantCode string
		wantNone bool
	}{
		{
			name: "healthy with version",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"healthy":true,"version":"1.15.4"}`))
			},
			wantNone: true,
		},
		{
			name:     "healthy without version",
			handler:  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"healthy":true}`)) },
			wantNone: true,
		},
		{
			name: "unhealthy is unavailable",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{}`))
			},
			wantCode: domain.CodeServiceUnavailable,
		},
		{
			name:     "malformed is incompatible",
			handler:  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"healthy":true`)) },
			wantCode: domain.CodeServiceIncompatible,
		},
		{
			name:     "unhealthy false is incompatible",
			handler:  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"healthy":false}`)) },
			wantCode: domain.CodeServiceIncompatible,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			spec := domain.AgentSpec{StringOptions: map[string]string{"mode": "external", "base_url": server.URL}}
			adapter := New(spec)
			adapter.settings.Mode = "external"
			adapter.settings.BaseURL = server.URL
			adapter.workspace = t.TempDir()
			issue := adapter.ReadinessCheck(context.Background())
			switch {
			case tc.wantNone && issue != nil:
				t.Fatalf("unexpected issue: %+v", issue)
			case !tc.wantNone && (issue == nil || issue.Code != tc.wantCode):
				t.Fatalf("want %s, got %+v", tc.wantCode, issue)
			}
			if issue != nil && issue.Component != domain.ComponentService {
				t.Fatalf("readiness issues use the service component: %+v", issue)
			}
		})
	}

	// A refused connection reports service_unavailable without leaking
	// endpoint details.
	adapter := New(domain.AgentSpec{StringOptions: map[string]string{"mode": "external", "base_url": "http://127.0.0.1:1"}})
	adapter.settings.Mode = "external"
	adapter.settings.BaseURL = "http://127.0.0.1:1"
	adapter.workspace = t.TempDir()
	issue := adapter.ReadinessCheck(context.Background())
	if issue == nil || issue.Code != domain.CodeServiceUnavailable {
		t.Fatalf("refused connection must be service_unavailable, got %+v", issue)
	}
	if len(issue.InstallationLinks) == 0 || issue.InstallationLinks[0].URL != domain.DocOpenCodeServer {
		t.Fatalf("service issues point at server docs, got %+v", issue)
	}
}
