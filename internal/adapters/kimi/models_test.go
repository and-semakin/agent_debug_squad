package kimi

import (
	"context"
	"encoding/json"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModelAliasesAndSecrets(t *testing.T) {
	b := []byte(`{"providers":{"p":{"apiKey":"SECRET_SENTINEL","oauth":{"access_token":"TOKEN_SENTINEL"}}},"models":{"alias-a":{"provider":"p","model":"native","displayName":"SECRET_SENTINEL","supportEfforts":["high"],"defaultEffort":"high"},"alias-b":{"provider":"p","model":"native","unknown":"TOKEN_SENTINEL"}}}`)
	r := parseModels(b, domain.ModelDiscoveryInput{})
	modelprobe.Finish(&r)
	if r.Status != "ok" || len(r.Models) != 2 || r.Models[0].Selection.Options["model"] != "alias-a" || r.Models[0].Parameters[0].SquadOption != "" {
		t.Fatal(r)
	}
	out, _ := json.Marshal(r)
	if strings.Contains(string(out), "SENTINEL") {
		t.Fatal(string(out))
	}
	r = parseModels([]byte(`{"models":{}}`), domain.ModelDiscoveryInput{})
	modelprobe.Finish(&r)
	if r.Status != "empty" {
		t.Fatal(r)
	}
}

func TestSendForwardsExplicitModelAlias(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-kimi")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > args\nprintf '%s\\n' '{\"type\":\"assistant\",\"message\":{\"content\":\"done\"}}'\n"
	if e := os.WriteFile(script, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	for _, alias := range []string{"", "local/alias[effort=high]"} {
		a := New(domain.AgentSpec{Backend: "kimi", StringOptions: map[string]string{"command": script, "model": alias}})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, e := a.Send(ctx, domain.AgentState{WorkspaceDir: dir}, domain.RunRequest{Message: "prompt"}, nil)
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(dir, "args"))
		if e != nil {
			t.Fatal(e)
		}
		text := string(b)
		if alias == "" && strings.Contains(text, "--model") {
			t.Fatal(text)
		}
		if alias != "" && !strings.Contains(text, "--model\n"+alias+"\n") {
			t.Fatal(text)
		}
	}
}
