package modeldiscovery

import (
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"testing"
)

func TestOpenCodeAliasDeduplication(t *testing.T) {
	targets, err := Resolve(Options{Backends: []string{"opencode", "opencode1", "opencode2"}}, domain.MachineBackends{}, nil)
	if err != nil || len(targets) != 2 {
		t.Fatalf("%+v %v", targets, err)
	}
	if targets[0].Spec.Backend != "opencode1" || targets[1].Spec.Backend != "opencode2" {
		t.Fatal(targets)
	}
}
