package opencode2

import (
	"context"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeCatalogAndPartialFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/info":
					fmt.Fprint(w, `{"version":"2.1.4"}`)
				case "/api/model":
					fmt.Fprint(w, `{"data":[{"id":"alias","modelID":"native","providerID":"custom","name":"Fixture","enabled":true,"limit":{"context":1000},"variants":[{"id":"high"}]}]}`)
				case "/api/provider":
					if fail {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"data":[{"id":"custom","name":"Provider","settings":{"apiKey":"secret"}}]}`)
				case "/api/model/default":
					fmt.Fprint(w, `{"data":{"id":"alias","providerID":"custom"}}`)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			a := New(domain.AgentSpec{Backend: "opencode2", StringOptions: map[string]string{"base_url": server.URL}})
			a.settings = domain.MachineBackendSettings{Mode: "external"}
			result := a.ListModels(context.Background(), domain.ModelDiscoveryInput{})
			if len(result.Models) != 1 {
				t.Fatalf("%+v", result)
			}
			m := result.Models[0]
			if m.ModelID != "native" || m.CatalogID != "alias" || m.Selection.Options["model"] != "custom/alias" || m.Connected != nil || m.Parameters[0].SquadOption != "" {
				t.Fatal(m)
			}
			if result.Complete == fail {
				t.Fatalf("%+v", result)
			}
		})
	}
}
