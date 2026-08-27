package adapters

import (
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestNewSupportsCursorBackend(t *testing.T) {
	adapter, err := New(domain.AgentSpec{Name: "CursorCritic", Backend: "cursor"})
	if err != nil {
		t.Fatalf("New(cursor) error = %v", err)
	}
	if adapter == nil {
		t.Fatal("New(cursor) adapter = nil")
	}
}
