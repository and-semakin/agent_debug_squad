package opencode2

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerificationRejectsMismatchAndMissingEvidence(t *testing.T) {
	for _, mode := range []string{"version", "missing_snapshot", "conflicting_snapshot", "workspace", "saved_session", "cross_generation"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			foreign := t.TempDir()
			created := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/info":
					v := "2.1.4"
					if mode == "version" {
						v = "1.18.30"
					}
					json.NewEncoder(w).Encode(map[string]string{"version": v})
				case "/api/location":
					dir := workspace
					if mode == "workspace" {
						dir = foreign
					}
					json.NewEncoder(w).Encode(map[string]string{"directory": dir})
				case "/api/config":
					switch mode {
					case "missing_snapshot":
						fmt.Fprint(w, `[]`)
					case "conflicting_snapshot":
						fmt.Fprint(w, `[{"type":"document","info":{"snapshots":true}}]`)
					default:
						fmt.Fprint(w, `[{"type":"document","info":{"snapshots":false}}]`)
					}
				case "/api/session/ses_saved":
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "ses_saved", "location": map[string]string{"directory": foreign}}})
				case "/api/session":
					created = true
					w.WriteHeader(500)
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			r := NewRuntime(context.Background(), workspace, &domain.MachineBackendSettings{Mode: "external", BaseURL: server.URL})
			defer r.Close()
			a, e := r.Adapter(domain.AgentSpec{Backend: "opencode2"})
			if e != nil {
				t.Fatal(e)
			}
			state := domain.AgentState{}
			if mode == "saved_session" {
				state.BackendSessionID = "ses_saved"
			}
			if mode == "cross_generation" {
				state.Backend = "opencode"
			}
			if _, e := a.Init(context.Background(), a.spec, state); e == nil {
				t.Fatal("mismatch passed")
			}
			if created {
				t.Fatal("replacement session created")
			}
		})
	}
}
func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	a := New(domain.AgentSpec{StringOptions: map[string]string{"base_url": source.URL, "password": "secret"}})
	if e := a.doJSON(context.Background(), "GET", "/api/info", nil, nil); e == nil {
		t.Fatal("redirect accepted")
	}
	if forwarded {
		t.Fatal("credentials forwarded")
	}
}
