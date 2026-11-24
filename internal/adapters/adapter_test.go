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

func TestNewRejectsRemovedZCodeBackend(t *testing.T) {
	adapter, err := New(domain.AgentSpec{Name: "Legacy", Backend: "zcode"})
	if adapter != nil || err != domain.ErrZCodeRemoved {
		t.Fatalf("New(zcode) = %v, %v", adapter, err)
	}
}
