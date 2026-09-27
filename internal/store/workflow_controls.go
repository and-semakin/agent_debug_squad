package store

import (
	"fmt"
	"math"
	"reflect"
	"strconv"

	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func validateControlSnapshot(s *domain.WorkflowSnapshot) error {
	bad := func(f string, args ...any) error { return fmt.Errorf("corrupt schema-4 control state: "+f, args...) }
	var agents []domain.AgentSpec
	for _, t := range s.Definition.Tasks {
		agents = append(agents, domain.AgentSpec{Name: t.Agent})
	}
	if err := config.ValidateWorkflowDefinition(s.Definition, agents); err != nil {
		return bad("invalid definition: %v", err)
	}
	pathValid := func(owner string, path []domain.IterationEntry) bool {
		chain := s.Definition.LoopAncestry(owner)
		if len(chain) != len(path) {
			return false
		}
		for i, n := range chain {
			if path[i].Loop != n || path[i].Iteration < 1 {
				return false
			}
		}
		return true
	}
	for id := range s.Definition.Tasks {
		if s.Tasks[id] == nil {
			return bad("missing task %q", id)
		}
	}
	for name, l := range s.Loops {
		def, ok := s.Definition.Loops[name]
		if !ok || l == nil || l.WorkflowLoopProgress == nil {
			return bad("invalid loop %q", name)
		}
		if def.MaxIterations < 1 || l.ExtendedIterations < 0 || l.ExtendedIterations > math.MaxInt-def.MaxIterations {
			return bad("invalid cap for %q", name)
		}
		if !pathValid(name, l.IterationPath) {
			return bad("invalid loop path %q", name)
		}
		if l.Entered {
			if l.IterationsStarted != l.Iteration || l.IterationsStarted > l.EffectiveCap(def.MaxIterations) {
				return bad("invalid entered budget %q", name)
			}
		} else if l.IterationsStarted != 0 || l.Iteration != 1 {
			return bad("unadmitted loop %q consumed a pass", name)
		}
		if l.Admitted && !l.Entered {
			return bad("admitted loop %s has not entered", name)
		}
		if l.Closed != (l.CloseReason != "") {
			return bad("inconsistent close marker %q", name)
		}
		if l.State.Settled() && !l.Closed {
			return bad("done loop %q has no close", name)
		}
	}
	decisions := map[string]*domain.WorkflowControlDecision{}
	revisions := map[string]int{}
	for i := range s.Decisions {
		d := &s.Decisions[i]
		t, ok := s.Definition.Tasks[d.TaskID]
		if d.ID != "lcd_"+strconv.Itoa(i+1) || !ok || len(t.Control) == 0 || !pathValid(t.Loop, d.IterationPath) {
			return bad("invalid decision %q", d.ID)
		}
		key := d.TaskID + ":" + domain.RenderIterationPath(d.IterationPath)
		revisions[key]++
		if d.OutcomeRevision != revisions[key] || d.MappedAction != t.Control[d.Verdict.Value] {
			return bad("invalid decision revision/action %q", d.ID)
		}
		if d.Status != "held" && d.Status != "released" && d.Status != "closed" {
			return bad("invalid decision status %q", d.ID)
		}
		if d.EffectiveAction != d.MappedAction && !(d.MappedAction == "needs_attention" && d.EffectiveAction == "proceed" && d.ResolvedBy == "stop") {
			return bad("invalid effective action %q", d.ID)
		}
		found := false
		for _, a := range s.Tasks[d.TaskID].Attempts {
			if a.Attempt == d.Attempt && a.State == domain.WorkflowAttemptSucceeded && domain.PathsEqual(a.IterationPath, d.IterationPath) {
				found = true
			}
		}
		if !found {
			return bad("orphan decision %q", d.ID)
		}
		switch d.EffectiveAction {
		case "proceed":
			if d.Status != "released" {
				return bad("proceed decision is not released %s", d.ID)
			}
		case "needs_attention":
			if d.Status != "held" && !(d.Status == "closed" && d.ResolvedBy == "override") {
				return bad("invalid attention decision %s", d.ID)
			}
		case "break", "continue":
			if d.Status != "closed" {
				return bad("closing decision is not closed %s", d.ID)
			}
		default:
			return bad("unknown action %s", d.ID)
		}
		if d.Status != "closed" || d.ResolvedBy != "override" {
			var outcome *domain.AttemptVerdict
			for _, a := range s.Tasks[d.TaskID].Attempts {
				if a.Attempt == d.Attempt {
					outcome = a.Verdict
				}
			}
			if outcome == nil || !reflect.DeepEqual(*outcome, d.Verdict) {
				return bad("decision evidence differs from settled outcome %s", d.ID)
			}
		}
		decisions[d.ID] = d
	}
	for id, t := range s.Tasks {
		td, ok := s.Definition.Tasks[id]
		if !ok || t == nil {
			return bad("unknown task %q", id)
		}
		hasCurrentSkip := false
		for _, skip := range t.Skips {
			if l := s.Loops[td.Loop]; l != nil && domain.PathsEqual(l.IterationPath, skip.IterationPath) {
				hasCurrentSkip = true
			}
		}
		if (t.State == domain.WorkflowTaskSkipped) != hasCurrentSkip {
			return bad("task skip state differs from current outcome %s", id)
		}
		seen := map[string]bool{}
		for _, skip := range t.Skips {
			d := decisions[skip.DecisionID]
			key := domain.RenderIterationPath(skip.IterationPath)
			if d == nil || !pathValid(td.Loop, skip.IterationPath) || seen[key] {
				return bad("invalid skip of %q", id)
			}
			seen[key] = true
			for _, entry := range skip.IterationPath[len(d.IterationPath):] {
				if entry.Iteration != 1 {
					return bad("skipped child is not planned at pass 1")
				}
			}
			if skip.Reason != "loop_"+d.MappedAction || (d.MappedAction != "break" && d.MappedAction != "continue") {
				return bad("invalid skip reason of %q", id)
			}
			owner := s.Definition.Tasks[d.TaskID].Loop
			g := s.Definition.ScopeGraph(owner)
			if !g.Reachable(domain.ScopeNode{Task: d.TaskID}, false)[s.Definition.ProjectTask(owner, id)] || !domain.IsPathPrefix(d.IterationPath, skip.IterationPath) {
				return bad("skip outside decision suffix %q", id)
			}
			for _, a := range t.Attempts {
				if domain.PathsEqual(a.IterationPath, skip.IterationPath) {
					return bad("attempt in skipped context %q", id)
				}
			}
		}
		for i, a := range t.Attempts {
			if len(td.Control) > 0 && a.State == domain.WorkflowAttemptSucceeded {
				found := false
				for _, d := range s.Decisions {
					if d.TaskID == id && d.Attempt == a.Attempt {
						found = true
					}
				}
				if !found {
					return bad("settled control %s has no decision", id)
				}
			}
			if a.Attempt != i+1 {
				return bad("nonmonotonic attempts %q", id)
			}
		}
	}
	histories := map[string]domain.WorkflowLoopHistory{}
	for _, h := range s.LoopHistory {
		key := domain.RenderIterationPath(h.IterationPath)
		if !pathValid(h.Loop, h.IterationPath) || !h.Closed || h.CloseReason == "" {
			return bad("invalid close summary %s", key)
		}
		if _, ok := histories[key]; ok {
			return bad("duplicate close summary %s", key)
		}
		local := h.IterationPath[len(h.IterationPath)-1].Iteration
		def := s.Definition.Loops[h.Loop]
		if h.ExtendedIterations < 0 || h.ExtendedIterations > math.MaxInt-def.MaxIterations {
			return bad("invalid historical cap %s", key)
		}
		if h.Entered {
			if !h.Admitted || h.IterationsStarted != local || local > def.MaxIterations+h.ExtendedIterations {
				return bad("invalid historical budget %s", key)
			}
		} else if h.IterationsStarted != 0 || local != 1 || h.CloseReason != "skipped" {
			return bad("invalid planned historical invocation %s", key)
		}
		switch h.CloseReason {
		case "natural", "continue", "break", "stop", "skipped":
		default:
			return bad("unknown close reason %s", key)
		}
		histories[key] = h
		children := s.Definition.ChildLoops(h.Loop)
		if len(children) != len(h.FinalChildren) {
			return bad("incomplete final child paths %s", key)
		}
		for _, child := range children {
			p := h.FinalChildren[child]
			if !pathValid(child, p) || !domain.IsPathPrefix(h.IterationPath, p) {
				return bad("invalid final child %s", child)
			}
		}
		if h.CloseReason == "skipped" {
			if h.Entered || h.IterationsStarted != 0 || h.DecisionID == "" {
				return bad("skipped child consumed budget %s", key)
			}
		}
	}
	for id, t := range s.Tasks {
		owner := s.Definition.Tasks[id].Loop
		if owner == "" {
			continue
		}
		l := s.Loops[owner]
		if l == nil {
			return bad("missing owner %s", owner)
		}
		for _, a := range t.Attempts {
			if domain.PathsEqual(a.IterationPath, l.IterationPath) {
				if !l.Entered || !l.Admitted {
					return bad("attempt in unadmitted context %s", id)
				}
			} else {
				h, ok := histories[domain.RenderIterationPath(a.IterationPath)]
				if !ok || !h.Entered || !h.Admitted {
					return bad("attempt without entered historical context %s", id)
				}
			}
		}
	}
	for _, h := range s.LoopHistory {
		for _, p := range h.FinalChildren {
			if _, ok := histories[domain.RenderIterationPath(p)]; !ok {
				return bad("missing child close summary")
			}
		}
	}
	for name, l := range s.Loops {
		if l.Closed {
			h, ok := histories[domain.RenderIterationPath(l.IterationPath)]
			if ok {
				if !reflect.DeepEqual(h.WorkflowLoopProgress, *l.WorkflowLoopProgress) {
					return bad("current close summary differs from loop %s", name)
				}
				for child, path := range h.FinalChildren {
					if s.Loops[child] == nil || !domain.PathsEqual(s.Loops[child].IterationPath, path) {
						return bad("current final child path differs %s", child)
					}
				}
			}
			if !ok {
				return bad("missing close summary %s", name)
			}
		}
	}
	// Every terminal action records its entire suffix, including all descendant
	// tasks and planned child/grandchild invocations. Partial commits fail closed.
	for _, d := range s.Decisions {
		if d.MappedAction != "break" && d.MappedAction != "continue" {
			continue
		}
		owner := s.Definition.Tasks[d.TaskID].Loop
		g := s.Definition.ScopeGraph(owner)
		suffix := g.Reachable(domain.ScopeNode{Task: d.TaskID}, false)
		for _, node := range g.Nodes {
			if !suffix[node] {
				continue
			}
			ids := []string{node.Task}
			if node.Loop != "" {
				ids = s.Definition.LoopSubtreeTasks(node.Loop)
			}
			for _, id := range ids {
				found := false
				for _, skip := range s.Tasks[id].Skips {
					if skip.DecisionID == d.ID {
						found = true
					}
				}
				if !found {
					return bad("incomplete suffix for %s: %s", d.ID, id)
				}
			}
			if node.Loop != "" {
				for name := range s.Definition.Loops {
					if !s.Definition.LoopContains(node.Loop, name) {
						continue
					}
					found := false
					for _, h := range s.LoopHistory {
						if h.Loop == name && h.DecisionID == d.ID && h.CloseReason == "skipped" {
							found = true
						}
					}
					if !found {
						return bad("missing skipped invocation %s for %s", name, d.ID)
					}
				}
			}
		}
	}
	previousClosed := func(path []domain.IterationEntry) bool {
		if len(path) == 0 || path[len(path)-1].Iteration == 1 {
			return true
		}
		p := append([]domain.IterationEntry(nil), path...)
		p[len(p)-1].Iteration--
		_, ok := histories[domain.RenderIterationPath(p)]
		return ok
	}
	for _, h := range s.LoopHistory {
		if !previousClosed(h.IterationPath) {
			return bad("missing preceding closed pass")
		}
	}
	for _, l := range s.Loops {
		if !previousClosed(l.IterationPath) {
			return bad("advanced without a preceding close")
		}
	}
	for _, d := range s.Decisions {
		if d.MappedAction == "break" || d.MappedAction == "continue" {
			h, ok := histories[domain.RenderIterationPath(d.IterationPath)]
			if !ok || h.DecisionID != d.ID || (h.CloseReason != d.MappedAction && h.CloseReason != "stop") {
				return bad("decision %s has no consistent owner close", d.ID)
			}
		}
		if d.Status == "held" {
			l := s.Loops[s.Definition.Tasks[d.TaskID].Loop]
			if l == nil || l.Closed || !domain.PathsEqual(l.IterationPath, d.IterationPath) {
				return bad("held decision outside live pass %s", d.ID)
			}
		}
	}
	for _, l := range s.Loops {
		if l.DecisionID != "" && decisions[l.DecisionID] == nil {
			return bad("orphan close decision")
		}
	}
	return nil
}
