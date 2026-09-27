package workflow

import (
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// exactOutcomePath follows recorded child-final links from a historical pass.
func exactOutcomePath(s *domain.WorkflowSnapshot, owner string, prefix []domain.IterationEntry) []domain.IterationEntry {
	path := append([]domain.IterationEntry(nil), prefix...)
	chain := s.Definition.LoopAncestry(owner)
	for len(path) < len(chain) {
		child := chain[len(path)]
		var next []domain.IterationEntry
		if len(path) == 0 {
			next = loopCurrentPath(s, child)
		} else {
			closed := false
			for _, h := range s.LoopHistory {
				if domain.PathsEqual(h.IterationPath, path) {
					next = h.FinalChildren[child]
					closed = true
					break
				}
			}
			if next == nil && !closed {
				l := s.Loops[child]
				if l != nil && domain.IsPathPrefix(path, l.IterationPath) {
					next = l.IterationPath
				}
			}
		}
		if next == nil {
			return nil
		}
		path = append([]domain.IterationEntry(nil), next...)
	}
	return path
}

func outcomeAt(s *domain.WorkflowSnapshot, id string, path []domain.IterationEntry) domain.WorkflowDependencyInput {
	t := s.Tasks[id]
	entry := domain.WorkflowDependencyInput{TaskID: id, Agent: t.Agent, IterationPath: path, Status: "absent", Reason: "no_outcome_in_context"}
	if len(path) > 0 {
		entry.Iteration = path[len(path)-1].Iteration
	}
	for _, skip := range t.Skips {
		if domain.PathsEqual(skip.IterationPath, path) {
			entry.Status = "skipped"
			entry.Reason = skip.Reason
			entry.DecisionID = skip.DecisionID
			return entry
		}
	}
	for i := len(t.Attempts) - 1; i >= 0; i-- {
		a := &t.Attempts[i]
		if domain.PathsEqual(a.IterationPath, path) {
			if a.State.Committed() {
				return dependencyInputFor(t, a)
			}
			entry.Reason = "outcome_unsettled"
			return entry
		}
	}
	return entry
}

func assertNoV2Consumption(s *domain.WorkflowSnapshot, id string) error {
	def := s.Definition
	owner := def.Tasks[id].Loop
	path := loopCurrentPath(s, owner)
	for _, d := range s.Decisions {
		scope := def.Tasks[d.TaskID].Loop
		g := def.ScopeGraph(scope)
		if !g.Reachable(domain.ScopeNode{Task: d.TaskID}, true)[def.ProjectTask(scope, id)] {
			continue
		}
		if owner == "" || domain.IsPathPrefix(path, d.IterationPath) || domain.IsPathPrefix(d.IterationPath, path) {
			return fmt.Errorf("control decision %s already consumed task %s", d.ID, id)
		}
	}
	// Admission itself consumes projected predecessor results; no raw edge from
	// this producer to a hidden child root is necessary.
	for _, name := range sortedLoopNames(def.Loops) {
		l := s.Loops[name]
		if !l.Admitted {
			continue
		}
		scope := def.Loops[name].Parent
		g := def.ScopeGraph(scope)
		if !g.Reachable(domain.ScopeNode{Loop: name}, true)[def.ProjectTask(scope, id)] {
			continue
		}
		if owner == "" || domain.IsPathPrefix(path, l.IterationPath) || domain.IsPathPrefix(l.IterationPath, path) {
			return fmt.Errorf("loop %s admission already consumed task %s", name, id)
		}
	}
	return nil
}

func dependencyOutcomePath(s *domain.WorkflowSnapshot, consumer, id string) []domain.IterationEntry {
	def := s.Definition
	owner := def.Tasks[id].Loop
	scope := def.Tasks[consumer].Loop
	if owner == "" {
		return nil
	}
	if scope == owner || (scope != "" && def.LoopContains(owner, scope)) {
		return loopCurrentPath(s, owner)
	}
	if scope == "" {
		scope = rootLoopOf(def, owner)
	}
	return exactOutcomePath(s, owner, loopCurrentPath(s, scope))
}
