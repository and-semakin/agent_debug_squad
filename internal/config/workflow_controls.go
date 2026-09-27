package config

import (
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"gopkg.in/yaml.v3"
	"sort"
)

func sortedRawLoopNames(loops map[string]rawWorkflowLoop) []string {
	var names []string
	for name := range loops {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func controlErrorf(name, message string, args ...any) error {
	return fmt.Errorf("loop/task %q: %s\nexample: version: 2; task: {loop: review, verdicts: {clean: Clean, issues: Issues}, control: {clean: break, issues: continue}}", name, fmt.Sprintf(message, args...))
}
func parseTaskControl(id string, n *yaml.Node) (map[string]string, error) {
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return nil, controlErrorf(id, "control must be a nonempty verdict-to-action map")
	}
	result := map[string]string{}
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Tag != "!!str" || v.Tag != "!!str" {
			return nil, controlErrorf(id, "control keys and actions must be strings")
		}
		if _, ok := result[k.Value]; ok {
			return nil, controlErrorf(id, "duplicate control verdict %q", k.Value)
		}
		result[k.Value] = v.Value
	}
	return result, nil
}
func validateTaskControls(d domain.WorkflowDefinition) error {
	for _, id := range sortedTaskIDs(d.Tasks) {
		t := d.Tasks[id]
		if t.Control == nil {
			continue
		}
		if d.Version != 2 || t.Loop == "" || t.AllowedToFail || len(t.Verdicts) < 2 || len(t.Control) == 0 {
			return controlErrorf(id, "control requires version: 2, a direct loop owner, at least two verdicts, and allowed_to_fail: false")
		}
		for v := range t.Verdicts {
			switch t.Control[v] {
			case domain.WorkflowLoopActionProceed, domain.WorkflowLoopActionBreak, domain.WorkflowLoopActionContinue, domain.WorkflowLoopActionNeedsAttention:
			default:
				return controlErrorf(id, "control verdict %q requires proceed, continue, break, or needs_attention", v)
			}
		}
		for v := range t.Control {
			if _, ok := t.Verdicts[v]; !ok {
				return controlErrorf(id, "control maps undeclared verdict %q", v)
			}
		}
		g := d.ScopeGraph(t.Loop)
		node := domain.ScopeNode{Task: id}
		before, after := g.Reachable(node, true), g.Reachable(node, false)
		for _, other := range g.Nodes {
			if other != node && !before[other] && !after[other] {
				return controlErrorf(id, "control in loop %q is incomparable with %q; order it before or after the control using needs (example: needs: [%s])", t.Loop, other.String(), id)
			}
		}
	}
	for _, name := range sortedLoopNames(d.Loops) {
		l := d.Loops[name]
		if l.OnExhaustion != "" {
			if len(d.ControlTasks(name)) == 0 {
				return controlErrorf(name, "on_exhaustion requires a directly owned control task")
			}
			if l.OnExhaustion != domain.WorkflowExhaustionNeedsAttention && l.OnExhaustion != domain.WorkflowExhaustionSucceed {
				return controlErrorf(name, "on_exhaustion must be needs_attention or succeed")
			}
		}
	}
	return nil
}

// Raw nodes deliberately avoid coercion. Check keys before migration diagnostics
// so a removed field cannot hide a malformed nested map.
func validateWorkflowMappingKeys(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Tag != "!!str" {
				return fmt.Errorf("workflow mapping keys must be strings")
			}
			if seen[k.Value] {
				return fmt.Errorf("duplicate mapping key %q already defined", k.Value)
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := validateWorkflowMappingKeys(child); err != nil {
			return err
		}
	}
	return nil
}
