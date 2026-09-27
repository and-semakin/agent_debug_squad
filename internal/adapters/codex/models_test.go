package codex

import (
	"context"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestModelDiscoveryProtocol(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(map[bool]string{false: "pages", true: "repeat"}[repeat], func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "codex")
			next := "null"
			if repeat {
				next = `"again"`
			}
			body := `#!/bin/sh
read -r init
case "$init" in *initialize*) ;; *) exit 2;; esac
printf '%s\n' '{"id":1,"result":{}}'
read -r initialized
read -r list
case "$list" in *model/list*includeHidden*) ;; *) exit 3;; esac
printf '%s\n' '{"method":"notification","params":{}}' '{"id":2,"result":{"data":[{"id":"catalog-id","model":"native-id","displayName":"Display","hidden":true,"supportedReasoningEfforts":[{"reasoningEffort":"high"}],"defaultReasoningEffort":"high"}],"nextCursor":"again"}}'
read -r second
case "$second" in *cursor*again*) ;; *) exit 4;; esac
printf '%s\n' '{"id":3,"result":{"data":[{"id":"id2","model":"id2"}],"nextCursor":` + next + `}}'
read -r unexpected
exit 5
`
			if e := os.WriteFile(script, []byte(body), 0700); e != nil {
				t.Fatal(e)
			}
			a := New(domain.AgentSpec{Backend: "codex", StringOptions: map[string]string{"command": script}})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := a.ListModels(ctx, domain.ModelDiscoveryInput{WorkspaceDir: dir, IncludeHidden: true})
			want := "ok"
			if repeat {
				want = "partial"
			}
			if r.Status != want || len(r.Models) != 2 {
				t.Fatalf("%+v", r)
			}
			if r.Sources[0].HiddenPolicy.Applied != "include_hidden" {
				t.Fatal(r)
			}
			found := false
			for _, m := range r.Models {
				if m.ModelID == "native-id" {
					found = true
					if m.CatalogID != "catalog-id" || m.Selection.Options["model"] != "native-id" || m.Parameters[0].SquadOption != "reasoning" {
						t.Fatal(m)
					}
				}
			}
			if !found {
				t.Fatal("native id lost")
			}
		})
	}
}
