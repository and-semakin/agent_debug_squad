package opencode2

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstalledOpenCode2Smoke(t *testing.T) {
	if os.Getenv("SQUAD_OPENCODE2_SMOKE") != "1" {
		t.Skip("opt-in installed OpenCode 2 smoke")
	}
	home := t.TempDir()
	workspace := t.TempDir()
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	for _, key := range []string{"CONFIG", "DATA", "CACHE", "STATE"} {
		env = append(env, "XDG_"+key+"_HOME="+filepath.Join(home, key))
	}
	configPath := filepath.Join(workspace, "opencode.json")
	original := []byte(`{"snapshots":false}`)
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1,"model":"fixture","choices":[{"index":0,"delta":{"role":"assistant","content":"fixture answer"},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1,"model":"fixture","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	cfg, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{"package": "@opencode/ai/providers/openai/chat", "settings": map[string]any{"baseURL": provider.URL + "/v1", "apiKey": "fixture-key"}, "models": map[string]any{"fixture": map[string]any{}}}}})
	env = append(env, "OPENCODE_CONFIG_CONTENT="+string(cfg))
	r := NewRuntimeWithEnv(ctx, workspace, &domain.MachineBackendSettings{InheritEnv: []string{"OPENCODE_CONFIG_CONTENT"}}, env)
	defer r.Close()
	a, err := r.Adapter(domain.AgentSpec{Name: "smoke", Backend: "opencode2", StringOptions: map[string]string{"model": "fixture/fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	if issue := a.ReadinessCheck(ctx); issue != nil {
		t.Fatalf("readiness: %+v", issue)
	}
	state, err := a.Init(ctx, a.spec, domain.AgentState{})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("created isolated session", state.BackendSessionID)
	catalog := a.ListModels(ctx, domain.ModelDiscoveryInput{WorkspaceDir: workspace, AmbientEnv: env})
	found := false
	for _, m := range catalog.Models {
		if m.Selection.Options["model"] == "fixture/fixture" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture model missing: %+v", catalog.Diagnostics)
	}
	if current, err := os.ReadFile(configPath); err != nil || string(current) != string(original) {
		t.Fatal("native config was rewritten")
	}

	recovered, err := a.Recover(ctx, state)
	if err != nil || recovered.BackendSessionID != state.BackendSessionID {
		t.Fatalf("recovery: %v", err)
	}
	for i := 0; i < 2; i++ {
		result, next, err := a.Send(ctx, state, domain.RunRequest{RunID: fmt.Sprintf("run_%d", i), Message: "reply briefly"}, nil)
		if err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		if result.FinalMessage != "fixture answer" {
			t.Fatalf("answer: %q", result.FinalMessage)
		}
		state = next
		state.LastRunID = fmt.Sprintf("run_%d", i)
	}
	if requests.Load() < 2 {
		t.Fatal("provider was not invoked")
	}
	if err := a.doJSON(ctx, "DELETE", sessionPath(state.BackendSessionID), nil, nil); err != nil {
		t.Fatal(err)
	}
}
