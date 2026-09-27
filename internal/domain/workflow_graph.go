package domain

import "sort"

// ScopeNode keeps child-loop identities distinct from task identifiers.
type ScopeNode struct {
	Task string
	Loop string
}

func (n ScopeNode) String() string {
	if n.Loop != "" {
		return "loop:" + n.Loop
	}
	return n.Task
}

// ScopeGraph projects an entire subtree onto direct tasks and child invocations.
// Predecessors point backwards along needs; internal child edges are checked in
// the child scope, never turned into a self edge here.
type ScopeGraph struct {
	Nodes        []ScopeNode
	Predecessors map[ScopeNode][]ScopeNode
	Successors   map[ScopeNode][]ScopeNode
}

func (d WorkflowDefinition) ProjectTask(scope, task string) ScopeNode {
	owner := d.Tasks[task].Loop
	if owner == scope {
		return ScopeNode{Task: task}
	}
	chain := d.LoopAncestry(owner)
	if scope == "" && len(chain) > 0 {
		return ScopeNode{Loop: chain[0]}
	}
	for i, name := range chain {
		if name == scope && i+1 < len(chain) {
			return ScopeNode{Loop: chain[i+1]}
		}
	}
	return ScopeNode{Task: task}
}

func (d WorkflowDefinition) ScopeGraph(scope string) ScopeGraph {
	g := ScopeGraph{Predecessors: map[ScopeNode][]ScopeNode{}, Successors: map[ScopeNode][]ScopeNode{}}
	nodes := map[ScopeNode]bool{}
	edges := map[[2]ScopeNode]bool{}
	inside := func(id string) bool { return scope == "" || d.LoopContains(scope, d.Tasks[id].Loop) }
	for id, task := range d.Tasks {
		if !inside(id) {
			continue
		}
		to := d.ProjectTask(scope, id)
		nodes[to] = true
		for _, dep := range task.Needs {
			if !inside(dep) {
				continue
			}
			from := d.ProjectTask(scope, dep)
			if from == to || edges[[2]ScopeNode{from, to}] {
				continue
			}
			edges[[2]ScopeNode{from, to}] = true
			g.Predecessors[to] = append(g.Predecessors[to], from)
			g.Successors[from] = append(g.Successors[from], to)
		}
	}
	less := func(a, b ScopeNode) bool { return a.String() < b.String() }
	for n := range nodes {
		g.Nodes = append(g.Nodes, n)
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return less(g.Nodes[i], g.Nodes[j]) })
	for _, n := range g.Nodes {
		sort.Slice(g.Predecessors[n], func(i, j int) bool { return less(g.Predecessors[n][i], g.Predecessors[n][j]) })
		sort.Slice(g.Successors[n], func(i, j int) bool { return less(g.Successors[n][i], g.Successors[n][j]) })
	}
	return g
}

// Reachable excludes the start node and works defensively even on invalid DAGs.
func (g ScopeGraph) Reachable(start ScopeNode, backwards bool) map[ScopeNode]bool {
	edges := g.Successors
	if backwards {
		edges = g.Predecessors
	}
	seen := map[ScopeNode]bool{start: true}
	queue := []ScopeNode{start}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, next := range edges[n] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	delete(seen, start)
	return seen
}

func (d WorkflowDefinition) ControlTasks(loop string) []string {
	var ids []string
	for id, t := range d.Tasks {
		if t.Loop == loop && len(t.Control) > 0 {
			ids = append(ids, id)
		}
	}
	g := d.ScopeGraph(loop)
	sort.Slice(ids, func(i, j int) bool {
		if g.Reachable(ScopeNode{Task: ids[i]}, false)[ScopeNode{Task: ids[j]}] {
			return true
		}
		if g.Reachable(ScopeNode{Task: ids[j]}, false)[ScopeNode{Task: ids[i]}] {
			return false
		}
		return ids[i] < ids[j]
	})
	return ids
}

// UsesIterationPaths includes flat v2 loops; HasNesting remains a topology query.
func (d WorkflowDefinition) UsesIterationPaths() bool { return d.Version == 2 || d.HasNesting() }
