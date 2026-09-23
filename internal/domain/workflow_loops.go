package domain

import (
	"sort"
	"strconv"
	"strings"
)

// This file holds the pure loop-topology and iteration-path helpers shared by
// the config (validation) and workflow (scheduling) packages. A definition is
// a forest of globally named loops; each task has exactly one direct owner
// loop (possibly none, meaning workflow scope). These helpers derive ancestry,
// subtrees, and common ancestors purely from the definition so no caller has
// to reimplement the tree walk, and so nesting depth is unbounded.

// HasNesting reports whether any declared loop names a parent, which is the
// sole trigger for schema-3 nested behavior. A definition whose loops are all
// roots stays byte-identical to the pre-nesting representation.
func (d WorkflowDefinition) HasNesting() bool {
	for _, loop := range d.Loops {
		if loop.Parent != "" {
			return true
		}
	}
	return false
}

// RootLoops returns the sorted names of loops with no parent.
func (d WorkflowDefinition) RootLoops() []string {
	var roots []string
	for name, loop := range d.Loops {
		if loop.Parent == "" {
			roots = append(roots, name)
		}
	}
	sort.Strings(roots)
	return roots
}

// ChildLoops returns the sorted names of loops whose parent is loopName.
func (d WorkflowDefinition) ChildLoops(loopName string) []string {
	var children []string
	for name, loop := range d.Loops {
		if loop.Parent == loopName {
			children = append(children, name)
		}
	}
	sort.Strings(children)
	return children
}

// LoopAncestry returns the chain of loop names from the root loop down to
// loopName inclusive. It returns nil for an undeclared name and stops before
// looping if the parent chain is malformed (validation rejects such graphs).
func (d WorkflowDefinition) LoopAncestry(loopName string) []string {
	if _, ok := d.Loops[loopName]; !ok {
		return nil
	}
	var reversed []string
	seen := map[string]bool{}
	current := loopName
	for current != "" {
		if seen[current] {
			break
		}
		seen[current] = true
		reversed = append(reversed, current)
		current = d.Loops[current].Parent
	}
	ancestry := make([]string, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		ancestry = append(ancestry, reversed[i])
	}
	return ancestry
}

// TaskOwnerLoop returns the direct owner loop of a task, or "" for a
// workflow-scope task or an unknown task.
func (d WorkflowDefinition) TaskOwnerLoop(taskID string) string {
	return d.Tasks[taskID].Loop
}

// LoopContains reports whether loopName is the same as, or an ancestor of,
// candidate — that is, whether candidate lies in loopName's subtree (inclusive).
func (d WorkflowDefinition) LoopContains(loopName, candidate string) bool {
	if loopName == "" || candidate == "" {
		return false
	}
	for _, name := range d.LoopAncestry(candidate) {
		if name == loopName {
			return true
		}
	}
	return false
}

// LoopSubtreeTasks returns the sorted task IDs whose direct owner is loopName
// or any descendant of loopName — the loop's whole task subtree.
func (d WorkflowDefinition) LoopSubtreeTasks(loopName string) []string {
	var ids []string
	for taskID, task := range d.Tasks {
		if task.Loop != "" && d.LoopContains(loopName, task.Loop) {
			ids = append(ids, taskID)
		}
	}
	sort.Strings(ids)
	return ids
}

// LoopDirectMembers returns the sorted task IDs whose direct owner is exactly
// loopName.
func (d WorkflowDefinition) LoopDirectMembers(loopName string) []string {
	var ids []string
	for taskID, task := range d.Tasks {
		if task.Loop == loopName {
			ids = append(ids, taskID)
		}
	}
	sort.Strings(ids)
	return ids
}

// CommonAncestorLoop returns the deepest loop that is an ancestor-or-self of
// both a and b, or "" when they share no enclosing loop. The same owner counts
// as a common loop.
func (d WorkflowDefinition) CommonAncestorLoop(a, b string) string {
	if a == "" || b == "" {
		return ""
	}
	inA := map[string]bool{}
	for _, name := range d.LoopAncestry(a) {
		inA[name] = true
	}
	// Walk b's chain from its owner upward; the first name present in a's chain
	// is the deepest common ancestor.
	chainB := d.LoopAncestry(b)
	for i := len(chainB) - 1; i >= 0; i-- {
		if inA[chainB[i]] {
			return chainB[i]
		}
	}
	return ""
}

// PathsEqual reports whether two iteration paths name the identical ordered
// invocation.
func PathsEqual(a, b []IterationEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// IsPathPrefix reports whether prefix is an ordered prefix of full (equal
// counts as a prefix of itself).
func IsPathPrefix(prefix, full []IterationEntry) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i := range prefix {
		if prefix[i] != full[i] {
			return false
		}
	}
	return true
}

// RenderIterationPath formats a path as the canonical reason token:
// root-to-owner `name=iteration` entries joined by `/` with no spaces. It is
// the single source of that spelling for every loop reason string.
func RenderIterationPath(entries []IterationEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.Loop+"="+strconv.Itoa(entry.Iteration))
	}
	return strings.Join(parts, "/")
}
