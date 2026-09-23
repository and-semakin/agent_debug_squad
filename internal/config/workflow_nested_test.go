package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// nestedAgents returns n distinct fake agent specs so every task can reference
// its own agent name (the validator rejects two tasks sharing an agent).
func nestedAgents(n int) []domain.AgentSpec {
	agents := make([]domain.AgentSpec, 0, n)
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("a%d", i)
		agents = append(agents, domain.AgentSpec{Name: name, Backend: "fake"})
	}
	return agents
}

// taskCount sizes the agent pool for a definition.
func taskCount(def domain.WorkflowDefinition) int {
	return len(def.Tasks) + 1
}

func validateNested(def domain.WorkflowDefinition) error {
	return ValidateWorkflowDefinition(def, nestedAgents(taskCount(def)))
}

func mustRejectDef(t *testing.T, def domain.WorkflowDefinition, want string) {
	t.Helper()
	err := validateNested(def)
	if err == nil {
		t.Fatalf("definition must be rejected (want %q)", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("rejection mismatch: got %q, want substring %q", err, want)
	}
}

func mustAcceptDef(t *testing.T, def domain.WorkflowDefinition) {
	t.Helper()
	if err := validateNested(def); err != nil {
		t.Fatalf("valid nested definition must be accepted: %v", err)
	}
}

// --- parent forest -----------------------------------------------------------

func TestNestedParentCycleRejected(t *testing.T) {
	// Two loops that name each other as parent are not a forest.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "parent-cycle", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"a": {MaxIterations: 2, Parent: "b"},
			"b": {MaxIterations: 2, Parent: "a"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"t": {Agent: "a1", Prompt: "p", Loop: "a"},
		},
	}
	mustRejectDef(t, def, "forms a cycle")
}

func TestNestedSelfParentRejected(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "self-parent", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"only": {MaxIterations: 2, Parent: "only"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"t": {Agent: "a1", Prompt: "p", Loop: "only"},
		},
	}
	mustRejectDef(t, def, "cannot be its own parent")
}

// --- allowed dependency relations and cross-branch bridges -------------------

func TestNestedUnrelatedBranchDependencyRejected(t *testing.T) {
	// Two sibling loops under one root are unrelated branches: a task in one may
	// not depend directly on a task in the other.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "unrelated", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"root": {MaxIterations: 2},
			"c1":   {MaxIterations: 2, Parent: "root"},
			"c2":   {MaxIterations: 2, Parent: "root"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"x": {Agent: "a1", Prompt: "p", Loop: "c1"},
			"y": {Agent: "a2", Prompt: "p", Loop: "c2", Needs: []string{"x"}},
		},
	}
	mustRejectDef(t, def, "unrelated branches")
}

func TestNestedBridgeThroughCommonScopeAccepted(t *testing.T) {
	// The same handoff becomes legal once routed through an explicit task in the
	// least-common enclosing scope (the root loop).
	def := domain.WorkflowDefinition{
		Version: 1, Name: "bridge", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"root": {MaxIterations: 2},
			"c1":   {MaxIterations: 2, Parent: "root"},
			"c2":   {MaxIterations: 2, Parent: "root"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"x":       {Agent: "a1", Prompt: "p", Loop: "c1"},
			"bridge":  {Agent: "a2", Prompt: "p", Loop: "root", Needs: []string{"x"}},
			"y":       {Agent: "a3", Prompt: "p", Loop: "c2", Needs: []string{"bridge"}},
			"rootend": {Agent: "a4", Prompt: "p", Loop: "root", Needs: []string{"y"}},
		},
	}
	mustAcceptDef(t, def)
}

func TestNestedAncestorDescendantDependencyAccepted(t *testing.T) {
	// A descendant consuming an ancestor output, and an ancestor consuming a
	// descendant output, are both allowed relations.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "anc-desc", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer": {MaxIterations: 2},
			"inner": {MaxIterations: 2, Parent: "outer"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"seed":    {Agent: "a1", Prompt: "p", Loop: "outer"},
			"inner1":  {Agent: "a2", Prompt: "p", Loop: "inner", Needs: []string{"seed"}},
			"outerex": {Agent: "a3", Prompt: "p", Loop: "outer", Needs: []string{"inner1"}},
		},
	}
	mustAcceptDef(t, def)
}

// --- boundary cycles across scopes -------------------------------------------

func TestNestedBoundaryCycleAcrossScopesRejected(t *testing.T) {
	// inner.A -> outer.X -> inner.B is acyclic in the raw task graph but forms a
	// cycle through the outer scope's inner-loop boundary node.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "scope-cycle", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer": {MaxIterations: 2},
			"inner": {MaxIterations: 2, Parent: "outer"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "p", Loop: "inner", Needs: []string{"x"}},
			"x": {Agent: "a2", Prompt: "p", Loop: "outer", Needs: []string{"b"}},
			"b": {Agent: "a3", Prompt: "p", Loop: "inner", Needs: []string{"a"}},
		},
	}
	mustRejectDef(t, def, "cycle through the loop boundary")
}

func TestNestedBoundaryCycleBelowThirdLevelRejected(t *testing.T) {
	// A cycle that collapses only at the middle scope (below the third level)
	// is caught even though the raw task graph stays acyclic.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "scope-cycle-3", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer":     {MaxIterations: 2},
			"middle":    {MaxIterations: 2, Parent: "outer"},
			"innermost": {MaxIterations: 2, Parent: "middle"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"a": {Agent: "a1", Prompt: "p", Loop: "innermost", Needs: []string{"x"}},
			"x": {Agent: "a2", Prompt: "p", Loop: "middle", Needs: []string{"b"}},
			"b": {Agent: "a3", Prompt: "p", Loop: "innermost"},
			"o": {Agent: "a4", Prompt: "p", Loop: "outer", Needs: []string{"a"}},
		},
	}
	mustRejectDef(t, def, "cycle through the loop boundary")
}

// --- condition-task ownership ------------------------------------------------

func TestNestedConditionTaskMustBeDirectMember(t *testing.T) {
	// An inner loop may not use an outer-owned task as its until_task.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "cond-owner", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer": {MaxIterations: 2},
			"inner": {
				MaxIterations: 2, Parent: "outer",
				UntilTask: "outercond",
				OnVerdict: map[string]string{"ok": domain.WorkflowLoopActionBreak, "again": domain.WorkflowLoopActionContinue},
			},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"outercond": {Agent: "a1", Prompt: "p", Loop: "outer", Verdicts: map[string]string{"ok": "y", "again": "n"}},
			"innerbody": {Agent: "a2", Prompt: "p", Loop: "inner"},
		},
	}
	mustRejectDef(t, def, "must be a member of the loop body")
}

// --- whole-subtree sink coverage ---------------------------------------------

func TestNestedSinkMustCoverWholeSubtree(t *testing.T) {
	// The outer loop's until_task must be reachable from every descendant task,
	// including work owned by an inner loop; an orphan inner task is uncovered.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "sink-subtree", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer": {
				MaxIterations: 2,
				UntilTask:     "end",
				OnVerdict:     map[string]string{"ok": domain.WorkflowLoopActionBreak, "again": domain.WorkflowLoopActionContinue},
			},
			"inner": {MaxIterations: 2, Parent: "outer"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"main": {Agent: "a1", Prompt: "p", Loop: "inner"},
			"side": {Agent: "a2", Prompt: "p", Loop: "inner"},
			"end": {Agent: "a3", Prompt: "p", Loop: "outer", Needs: []string{"main"},
				Verdicts: map[string]string{"ok": "y", "again": "n"}},
		},
	}
	mustRejectDef(t, def, "unique body sink")
}

func TestNestedSinkCoveringWholeSubtreeAccepted(t *testing.T) {
	def := domain.WorkflowDefinition{
		Version: 1, Name: "sink-covered", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"outer": {
				MaxIterations: 2,
				UntilTask:     "end",
				OnVerdict:     map[string]string{"ok": domain.WorkflowLoopActionBreak, "again": domain.WorkflowLoopActionContinue},
			},
			"inner": {MaxIterations: 2, Parent: "outer"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"main": {Agent: "a1", Prompt: "p", Loop: "inner"},
			"side": {Agent: "a2", Prompt: "p", Loop: "inner", Needs: []string{"main"}},
			"end": {Agent: "a3", Prompt: "p", Loop: "outer", Needs: []string{"side"},
				Verdicts: map[string]string{"ok": "y", "again": "n"}},
		},
	}
	mustAcceptDef(t, def)
}

// --- container loops ---------------------------------------------------------

func TestNestedContainerLoopAccepted(t *testing.T) {
	// A loop with no directly-owned tasks is legal as long as its subtree has
	// members through a descendant loop.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "container", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"container": {MaxIterations: 2},
			"worker":    {MaxIterations: 2, Parent: "container"},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"w1": {Agent: "a1", Prompt: "p", Loop: "worker"},
			"w2": {Agent: "a2", Prompt: "p", Loop: "worker", Needs: []string{"w1"}},
		},
	}
	mustAcceptDef(t, def)
}

func TestNestedEmptySubtreeRejected(t *testing.T) {
	// A loop whose entire subtree, descendants included, has no tasks is illegal.
	def := domain.WorkflowDefinition{
		Version: 1, Name: "empty-subtree", MaxParallel: 1, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{
			"container": {MaxIterations: 2},
			"worker":    {MaxIterations: 2},
		},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"w1": {Agent: "a1", Prompt: "p", Loop: "worker"},
		},
	}
	mustRejectDef(t, def, "has no member tasks anywhere in its subtree")
}
