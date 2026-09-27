package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExternalModelDiscovery(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			var mu sync.Mutex
			paths := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				if r.Method != "GET" || r.Header.Get("x-opencode-directory") != "/workspace" {
					t.Errorf("wrong scoped request %s %s", r.Method, r.URL.Path)
				}
				u, p, ok := r.BasicAuth()
				if !ok || u != "user" || p != "fixture-password" {
					t.Error("missing auth")
				}
				switch r.URL.Path {
				case "/global/health":
					fmt.Fprint(w, `{"healthy":true,"version":"1.18.30"}`)
				case "/config":
					fmt.Fprint(w, `{"snapshot":false}`)
				case "/provider":
					fmt.Fprint(w, `{"all":[{"id":"p","name":"Provider","models":{"model/with/slash":{"id":"model/with/slash","name":"Name"},"other":{"id":"other"}}}],"connected":["p"],"default":{"p":"model/with/slash"}}`)
				case "/config/providers":
					if partial {
						http.NotFound(w, r)
						return
					}
					fmt.Fprint(w, `{"providers":[{"id":"p","name":"Provider","models":{"model/with/slash":{"id":"model/with/slash","name":"Name"}}}],"default":{"p":"model/with/slash"}}`)
				default:
					t.Error("unexpected route", r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			runtime := NewRuntimeWithEnv(context.Background(), "/workspace", &domain.MachineBackendSettings{Mode: "external", BaseURL: server.URL}, []string{})
			defer runtime.Close()
			a, e := runtime.Adapter(domain.AgentSpec{Backend: "opencode", StringOptions: map[string]string{"username": "user", "password": "fixture-password"}})
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			check := a.CheckInstallation(ctx, domain.InstallationInput{})
			if check.Status != "not_required" {
				t.Fatal(check)
			}
			r := a.ListModels(ctx, domain.ModelDiscoveryInput{WorkspaceDir: "/workspace"})
			want := "ok"
			if partial {
				want = "partial"
			}
			if r.Status != want || len(r.Models) != 2 {
				b, _ := json.Marshal(r)
				t.Fatal(string(b))
			}
			m := r.Models[0]
			if m.ModelID != "model/with/slash" || m.Selection.Options["model"] != "p/model/with/slash" || m.Connected == nil || !*m.Connected {
				t.Fatal(m)
			}
			if partial && m.Configured != nil {
				t.Fatal("invented configuration")
			}
			if !partial && (m.Configured == nil || !*m.Configured) {
				t.Fatal(m)
			}
			if runtime.cmd != nil {
				t.Fatal("external runtime spawned")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(paths) != 4 {
				t.Fatal(paths)
			}
		})
	}
}

func TestManagedModelDiscoverySharesOwner(t *testing.T) {
	r := helperRuntime(t, "")
	a := runtimeAgent(t, r, "unused")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result := a.ListModels(ctx, domain.ModelDiscoveryInput{WorkspaceDir: r.workspace})
	if result.Status != "empty" {
		t.Fatal(result)
	}
	pid := r.cmd.Process.Pid
	second := runtimeAgent(t, r, "different")
	result = second.ListModels(ctx, domain.ModelDiscoveryInput{WorkspaceDir: r.workspace})
	if result.Status != "empty" || r.cmd.Process.Pid != pid {
		t.Fatal(result)
	}
	if _, e := os.Stat(filepath.Join(r.workspace, "accepted")); !os.IsNotExist(e) {
		t.Fatal("prompt sent")
	}
	r.Close()
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("owned runtime leaked")
	}
}

func TestModelHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://127.0.0.1:1/secret")
				w.WriteHeader(status)
				fmt.Fprint(w, "SECRET_SENTINEL")
			}))
			defer server.Close()
			a := New(domain.AgentSpec{Backend: "opencode", StringOptions: map[string]string{"base_url": server.URL}})
			_, got, e := a.modelJSON(context.Background(), "/provider")
			want := "error"
			if status == 401 {
				want = "auth_required"
			}
			if status == 404 {
				want = "unsupported"
			}
			if got != want || e == nil || strings.Contains(e.Error(), "SECRET_SENTINEL") {
				t.Fatal(got, e)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	a := New(domain.AgentSpec{Backend: "opencode", StringOptions: map[string]string{"base_url": server.URL}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, got, e := a.modelJSON(ctx, "/provider")
	if got != "timeout" || e == nil {
		t.Fatal(got, e)
	}
}
