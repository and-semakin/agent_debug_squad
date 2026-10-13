package devin

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// Explicit opt-in only: consumes the operator's native account quota.
func TestLiveLocalContinuity(t *testing.T) {
	if os.Getenv("SQUAD_DEVIN_LIVE") != "1" {
		t.Skip("set SQUAD_DEVIN_LIVE=1 to use the authenticated native account")
	}
	nativeConfig := filepath.Join(os.Getenv("HOME"), ".config", "devin", "config.json")
	before, e := os.ReadFile(nativeConfig)
	if e != nil {
		t.Fatal("cannot snapshot native config")
	}
	defer func() {
		after, e := os.ReadFile(nativeConfig)
		if e != nil || sha256.Sum256(before) != sha256.Sum256(after) {
			t.Error("native config changed")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s := domain.AgentSpec{Name: "probe", Backend: "devin", StringOptions: map[string]string{"model": "swe-2-high"}, ListOptions: map[string][]string{"inherit_env": {"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}}}
	a := New(s)
	catalog := a.ListModels(ctx, domain.ModelDiscoveryInput{WorkspaceDir: t.TempDir(), AmbientEnv: os.Environ()})
	if catalog.Status != "ok" || len(catalog.Models) == 0 {
		t.Fatal("live catalog unavailable", catalog.Status)
	}
	st, e := a.Init(ctx, s, domain.AgentState{WorkspaceDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer a.ClosePreparation()
	r, st, e := a.Send(ctx, st, domain.RunRequest{RunID: "live-1", Message: "Reply exactly SQUAD_LIVE_OK. Do not use tools."}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.FinalMessage != "SQUAD_LIVE_OK" {
		t.Fatalf("unexpected first answer: %q", r.FinalMessage)
	}
	a.spec.StringOptions["model"] = "swe-2-high"
	r, _, e = a.Send(ctx, st, domain.RunRequest{RunID: "live-2", Message: "Repeat exactly your previous answer. Do not use tools."}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.FinalMessage != "SQUAD_LIVE_OK" {
		t.Fatalf("unexpected resumed answer: %q", r.FinalMessage)
	}
	t.Log("fresh and loaded turns succeeded with exact model selection")
}
