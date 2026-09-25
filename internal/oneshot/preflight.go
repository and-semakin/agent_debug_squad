// Package oneshot implements the `run` command: one process owns exactly one
// workflow execution, preserves its results, and exits automatically once
// that execution settles durably.
package oneshot

import (
	"fmt"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
	"github.com/and-semakin/agent_debug_squad/internal/workflow"
)

// selection is the preflight outcome: which execution this invocation owns,
// selected read-only before any mutation or backend initialization.
type selection struct {
	// executionID is empty when the request ID is new and the live path must
	// create the execution.
	executionID string
	terminal    bool
	snapshot    domain.WorkflowSnapshot
}

// preflight inspects every authoritative snapshot under session ownership and
// resolves the request identity with the same normalization Create uses. It
// rejects damaged state, conflicting definitions, and any nonterminal
// execution other than the selected request before recovery or backend work.
func preflight(st *store.Store, cfg domain.SessionConfig, requestID, fingerprint string) (selection, error) {
	if cfg.Workflow == nil {
		return selection{}, fmt.Errorf("no workflow is configured")
	}

	ids, err := st.ListWorkflowExecutions()
	if err != nil {
		return selection{}, fmt.Errorf("list workflow executions: %w", err)
	}

	var selected selection
	foreignNonterminal := ""
	for _, id := range ids {
		snapshot, err := st.LoadWorkflowSnapshot(id)
		if err != nil {
			return selection{}, fmt.Errorf("inspect workflow %s: %w", id, err)
		}
		if snapshot.RequestID == requestID {
			if selected.executionID != "" {
				return selection{}, fmt.Errorf("workflow storage is damaged: request %q is saved on executions %s and %s", requestID, selected.executionID, id)
			}
			if snapshot.DefinitionHash != fingerprint {
				return selection{}, fmt.Errorf("%w: request %q was saved with a different resolved definition or agent configuration", workflow.ErrDefinitionChanged, requestID)
			}
			selected = selection{executionID: id, terminal: snapshot.State.Terminal(), snapshot: snapshot}
			continue
		}
		if !snapshot.State.Terminal() {
			foreignNonterminal = id
		}
	}

	if foreignNonterminal != "" {
		if selected.executionID != "" {
			return selection{}, fmt.Errorf("refusing one-shot startup: execution %s is nonterminal and not selected by request %q", foreignNonterminal, requestID)
		}
		return selection{}, fmt.Errorf("refusing one-shot startup: execution %s is nonterminal, so request %q cannot create a new execution", foreignNonterminal, requestID)
	}
	return selected, nil
}
