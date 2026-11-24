package modeldiscovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"reflect"
	"strings"
	"testing"
)

func TestRemovedZCodeDiscovery(t *testing.T) {
	for _, o := range []Options{{}, {All: true}} {
		targets, err := Resolve(o, domain.MachineBackends{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, target := range targets {
			got = append(got, target.Spec.Backend)
		}
		want := []string{"codex", "cursor", "devin", "kimi", "opencode1", "opencode2"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("targets = %v", got)
		}
	}
	if _, err := Resolve(Options{Backends: []string{"zcode"}}, domain.MachineBackends{}, nil); !errors.Is(err, domain.ErrZCodeRemoved) {
		t.Fatalf("selector: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	for _, jsonMode := range []bool{false, true} {
		args := []string{"--backend", "zcode"}
		if jsonMode {
			args = append(args, "--json")
		}
		var out, stderr bytes.Buffer
		if code := Main(context.Background(), args, &out, &stderr); code != 2 {
			t.Fatalf("exit = %d", code)
		}
		message := stderr.String()
		if jsonMode {
			var report domain.ModelCatalog
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Error == nil || report.Error.Code != "invalid_arguments" {
				t.Fatalf("report = %+v", report)
			}
			message = report.Error.Message
		}
		if !strings.Contains(message, "deprecated and removed") || !strings.Contains(message, "codex") {
			t.Fatalf("diagnostic = %s", message)
		}
	}
}
