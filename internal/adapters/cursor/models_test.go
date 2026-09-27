package cursor

import (
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"testing"
)

func TestModelTextGrammar(t *testing.T) {
	for _, tt := range []struct {
		text, status string
		count        int
	}{{"Available models\n\nauto - Auto (default)\nmodel[effort=high] - Display\n", "ok", 2}, {"No models available", "empty", 0}, {"Available models\n", "unsupported", 0}, {"\x1b[1mAvailable models\x1b[0m\nx - X", "ok", 1}, {"new format", "unsupported", 0}, {"Available models\nx - X\nmalformed", "partial", 1}} {
		r := parseModels([]byte(tt.text), domain.ModelDiscoveryInput{})
		modelprobe.Finish(&r)
		if r.Status != tt.status || len(r.Models) != tt.count {
			t.Fatalf("%q %+v", tt.text, r)
		}
		for _, m := range r.Models {
			if len(m.Parameters) > 0 {
				t.Fatal("invented reasoning")
			}
		}
	}
}

func TestObservedTipFooter(t *testing.T) {
	r := parseModels([]byte("Available models\n\nm - Name\nTip: use --model <id> (or /model <id> in interactive mode) to switch. Parameterized models also accept quoted overrides."), domain.ModelDiscoveryInput{})
	modelprobe.Finish(&r)
	if r.Status != "ok" || len(r.Models) != 1 {
		t.Fatal(r)
	}
}
