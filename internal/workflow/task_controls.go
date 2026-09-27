package workflow

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func currentSkip(s *domain.WorkflowSnapshot, taskID string) *domain.WorkflowTaskSkip {
	t := s.Tasks[taskID]
	if t == nil {
		return nil
	}
	path := loopCurrentPath(s, s.Definition.Tasks[taskID].Loop)
	for i := len(t.Skips) - 1; i >= 0; i-- {
		if domain.PathsEqual(t.Skips[i].IterationPath, path) {
			return &t.Skips[i]
		}
	}
	return nil
}

func currentControlDecision(s *domain.WorkflowSnapshot, taskID string) *domain.WorkflowControlDecision {
	path := loopCurrentPath(s, s.Definition.Tasks[taskID].Loop)
	for i := len(s.Decisions) - 1; i >= 0; i-- {
		d := &s.Decisions[i]
		if d.TaskID == taskID && domain.PathsEqual(d.IterationPath, path) {
			return d
		}
	}
	return nil
}

func recordClosedPass(s *domain.WorkflowSnapshot, name string) {
	l := s.Loops[name]
	h := domain.WorkflowLoopHistory{Loop: name, IterationPath: loopCurrentPath(s, name), WorkflowLoopProgress: *l.WorkflowLoopProgress, ExtendedIterations: l.ExtendedIterations, FinalChildren: map[string][]domain.IterationEntry{}}
	for _, child := range s.Definition.ChildLoops(name) {
		h.FinalChildren[child] = loopCurrentPath(s, child)
	}
	for i := range s.LoopHistory {
		if domain.PathsEqual(s.LoopHistory[i].IterationPath, h.IterationPath) {
			s.LoopHistory[i] = h
			return
		}
	}
	s.LoopHistory = append(s.LoopHistory, h)
}

func closePass(s *domain.WorkflowSnapshot, name, reason, decisionID string) {
	l := s.Loops[name]
	l.Closed = true
	l.CloseReason = reason
	l.DecisionID = decisionID
	if reason == "break" || reason == "stop" || reason == "skipped" {
		l.State = domain.WorkflowLoopDone
	}
	if reason == "skipped" {
		l.State = domain.WorkflowLoopSkipped
	}
	recordClosedPass(s, name)
}

func skipTask(s *domain.WorkflowSnapshot, id, reason, decisionID string) {
	if currentSkip(s, id) != nil {
		return
	}
	s.Tasks[id].Skips = append(s.Tasks[id].Skips, domain.WorkflowTaskSkip{IterationPath: loopCurrentPath(s, s.Definition.Tasks[id].Loop), Reason: reason, DecisionID: decisionID})
	s.Tasks[id].State = domain.WorkflowTaskSkipped
	s.Tasks[id].BlockedReason = ""
}

func skipLoop(s *domain.WorkflowSnapshot, name, reason, decisionID string) {
	for _, id := range loopBodyTasks(s.Definition, name) {
		skipTask(s, id, reason, decisionID)
	}
	for _, child := range s.Definition.ChildLoops(name) {
		skipLoop(s, child, reason, decisionID)
	}
	closePass(s, name, "skipped", decisionID)
}

// applyTaskControl is part of the verdict transaction. No scheduler can see a
// successful control without its decision, complete suffix outcomes and close.
func applyTaskControl(s *domain.WorkflowSnapshot, id string, a *domain.WorkflowAttempt, now time.Time) error {
	t := s.Definition.Tasks[id]
	if len(t.Control) == 0 || a.State != domain.WorkflowAttemptSucceeded || a.Verdict == nil {
		return nil
	}
	l := s.Loops[t.Loop]
	if l == nil || !domain.PathsEqual(a.IterationPath, l.IterationPath) {
		return fmt.Errorf("control %s is outside its current context", id)
	}
	revision := 1
	if previous := currentControlDecision(s, id); previous != nil {
		if previous.Status != "held" {
			return fmt.Errorf("control %s decision already committed", id)
		}
		previous.Status = "closed"
		previous.ResolvedBy = "override"
		revision = previous.OutcomeRevision + 1
	}
	if len(s.Decisions) == math.MaxInt {
		return fmt.Errorf("control decision sequence exhausted")
	}
	action := t.Control[a.Verdict.Value]
	switch action {
	case "proceed", "continue", "break", "needs_attention":
	default:
		return fmt.Errorf("control %s has no action for verdict %s", id, a.Verdict.Value)
	}
	d := domain.WorkflowControlDecision{ID: fmt.Sprintf("lcd_%d", len(s.Decisions)+1), TaskID: id, Attempt: a.Attempt, OutcomeRevision: revision, IterationPath: loopCurrentPath(s, t.Loop), Verdict: *a.Verdict, MappedAction: action, EffectiveAction: action, At: now, Status: "closed"}
	if action == "needs_attention" && l.StopRequested {
		d.EffectiveAction = "proceed"
		d.ResolvedBy = "stop"
	}
	switch d.EffectiveAction {
	case "needs_attention":
		d.Status = "held"
	case "proceed":
		d.Status = "released"
	case "break", "continue":
		g := s.Definition.ScopeGraph(t.Loop)
		suffix := g.Reachable(domain.ScopeNode{Task: id}, false)
		for _, n := range g.Nodes {
			if !suffix[n] {
				continue
			}
			if n.Loop != "" {
				skipLoop(s, n.Loop, "loop_"+action, d.ID)
			} else {
				skipTask(s, n.Task, "loop_"+action, d.ID)
			}
		}
		reason := action
		if l.StopRequested && action == "continue" {
			reason = "stop"
		}
		closePass(s, t.Loop, reason, d.ID)
	}
	s.Decisions = append(s.Decisions, d)
	settleV2Passes(s)
	return nil
}

func nodeAcceptable(s *domain.WorkflowSnapshot, n domain.ScopeNode) bool {
	if n.Loop != "" {
		return s.Loops[n.Loop] != nil && s.Loops[n.Loop].State == domain.WorkflowLoopDone && s.Loops[n.Loop].CloseReason != "skipped"
	}
	t := s.Tasks[n.Task]
	return t != nil && dependencyAcceptable(t, s.Definition.Tasks[n.Task])
}

// The projected gate applies to the entire child, including hidden roots with
// no raw dependency. Ancestor phase gates compose recursively.
func scopeNodeReady(s *domain.WorkflowSnapshot, scope string, node domain.ScopeNode) bool {
	if scope != "" {
		l := s.Loops[scope]
		if l == nil || !l.Entered || !l.Admitted || l.Closed {
			return false
		}
	}
	g := s.Definition.ScopeGraph(scope)
	for _, p := range g.Predecessors[node] {
		if !nodeAcceptable(s, p) {
			return false
		}
	}
	for _, id := range s.Definition.ControlTasks(scope) {
		cn := domain.ScopeNode{Task: id}
		if cn == node {
			continue
		}
		if g.Reachable(cn, false)[node] {
			d := currentControlDecision(s, id)
			if d == nil || d.Status != "released" {
				return false
			}
		}
	}
	return true
}

func taskPhaseOpen(s *domain.WorkflowSnapshot, id string) bool {
	owner := s.Definition.Tasks[id].Loop
	if owner == "" {
		return true
	}
	l := s.Loops[owner]
	if l == nil || !l.Entered || !l.Admitted || l.Closed {
		return false
	}
	// Ordinary dependency failures must become blocked; only control phase
	// gates postpone evaluation. A control itself waits for all projected ancestors.
	if !scopeNodeReady(s, "", domain.ScopeNode{Loop: rootLoopOf(s.Definition, owner)}) {
		return false
	}
	g := s.Definition.ScopeGraph(owner)
	node := domain.ScopeNode{Task: id}
	for _, c := range s.Definition.ControlTasks(owner) {
		if c == id {
			for n := range g.Reachable(node, true) {
				if !nodeAcceptable(s, n) {
					return false
				}
			}
			continue
		}
		if g.Reachable(domain.ScopeNode{Task: c}, false)[node] {
			d := currentControlDecision(s, c)
			if d == nil || d.Status != "released" {
				return false
			}
		}
	}
	return true
}

func resolveV2Stops(s *domain.WorkflowSnapshot) {
	for i := range s.Decisions {
		d := &s.Decisions[i]
		owner := s.Definition.Tasks[d.TaskID].Loop
		l := s.Loops[owner]
		if d.Status == "held" && l != nil && l.StopRequested && domain.PathsEqual(d.IterationPath, l.IterationPath) {
			d.Status = "released"
			d.EffectiveAction = "proceed"
			d.ResolvedBy = "stop"
		}
	}
}

// settleV2Passes never enters a new pass and is legal while paused.
func settleV2Passes(s *domain.WorkflowSnapshot) {
	resolveV2Stops(s)
	for _, name := range loopPostorder(s.Definition) {
		l := s.Loops[name]
		if !l.Entered || l.State.Settled() {
			continue
		}
		held := false
		for _, id := range s.Definition.ControlTasks(name) {
			if d := currentControlDecision(s, id); d != nil && d.Status == "held" {
				held = true
			}
		}
		if held {
			continue
		}
		if !l.Closed && iterationSettledAcceptably(s, name) {
			reason := "natural"
			if l.StopRequested {
				reason = "stop"
			}
			closePass(s, name, reason, "")
		}
		if !l.Closed {
			continue
		}
		if l.StopRequested {
			l.State = domain.WorkflowLoopDone
			l.CloseReason = "stop"
			recordClosedPass(s, name)
			continue
		}
		if l.IterationsStarted >= l.EffectiveCap(s.Definition.Loops[name].MaxIterations) {
			if len(s.Definition.ControlTasks(name)) == 0 || s.Definition.Loops[name].EffectiveOnExhaustion() == "succeed" {
				l.State = domain.WorkflowLoopDone
			} else {
				l.State = domain.WorkflowLoopNeedsAttention
			}
		}
	}
}

func advanceV2Passes(s *domain.WorkflowSnapshot) {
	for _, name := range loopPostorder(s.Definition) {
		l := s.Loops[name]
		if !l.Entered || !l.Closed || l.State.Settled() || l.StopRequested || l.IterationsStarted >= l.EffectiveCap(s.Definition.Loops[name].MaxIterations) {
			continue
		}
		advanceLoopIterationLocked(s, l, name, true)
		l.IterationsStarted++
		l.Admitted = false
		l.Closed = false
		l.CloseReason = ""
		l.DecisionID = ""
		l.State = domain.WorkflowLoopRunning
		for _, child := range childLoopsUnderParentAdvance(s.Definition, name) {
			s.Loops[child].WorkflowLoopProgress = &domain.WorkflowLoopProgress{}
		}
	}
}

func admitV2Children(s *domain.WorkflowSnapshot) {
	order := loopPostorder(s.Definition)
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		l := s.Loops[name]
		parent := s.Definition.Loops[name].Parent
		if l.Admitted || l.Closed {
			continue
		}
		if scopeNodeReady(s, parent, domain.ScopeNode{Loop: name}) {
			l.Admitted = true
			l.Entered = true
			if l.IterationsStarted == 0 {
				l.IterationsStarted = 1
			}
		}
	}
}

// A projected prerequisite failure blocks admission, including hidden roots.
// A preceding unresolved control still keeps its entire suffix unopened.
func projectedBlockReason(s *domain.WorkflowSnapshot, id string) string {
	def := s.Definition
	for _, name := range def.LoopAncestry(def.Tasks[id].Loop) {
		parent := def.Loops[name].Parent
		g := def.ScopeGraph(parent)
		node := domain.ScopeNode{Loop: name}
		for _, c := range def.ControlTasks(parent) {
			if g.Reachable(domain.ScopeNode{Task: c}, false)[node] {
				d := currentControlDecision(s, c)
				if d == nil || d.Status != "released" {
					return ""
				}
			}
		}
		for _, pred := range g.Predecessors[node] {
			if pred.Task == "" {
				continue
			}
			t := s.Tasks[pred.Task]
			if t != nil && t.State.Settled() && !dependencyAcceptable(t, def.Tasks[pred.Task]) {
				if skip := currentSkip(s, pred.Task); skip != nil {
					return "dependency_skipped:" + pred.Task + ":" + skip.DecisionID
				}
				return fmt.Sprintf("dependency_%s:%s", t.State, pred.Task)
			}
		}
	}
	return ""
}

func v2HoldReasons(s *domain.WorkflowSnapshot) []string {
	var reasons []string
	for _, name := range sortedLoopNames(s.Definition.Loops) {
		l := s.Loops[name]
		if !l.Entered || l.State.Settled() {
			continue
		}
		l.State = domain.WorkflowLoopRunning
		ids := loopBodyTasks(s.Definition, name)
		if !l.Admitted {
			ids = s.Definition.LoopSubtreeTasks(name)
		}
		for _, id := range ids {
			t := s.Tasks[id]
			if t.State == domain.WorkflowTaskFailed && !s.Definition.Tasks[id].AllowedToFail || t.State == domain.WorkflowTaskBlocked {
				kind := "loop_failure"
				if t.State == domain.WorkflowTaskBlocked {
					kind = "loop_blocked"
				}
				guidance := "retry_or_cancel"
				if strings.HasPrefix(t.BlockedReason, "dependency_skipped:") {
					guidance = "cancel_or_new_definition"
				}
				reasons = append(reasons, fmt.Sprintf("%s:%s:%s:%s", kind, loopReasonContext(s, name), id, guidance))
				l.State = domain.WorkflowLoopNeedsAttention
			}
			if d := currentControlDecision(s, id); d != nil && d.Status == "held" {
				reasons = append(reasons, fmt.Sprintf("loop_attention:%s:%s:%s:override_or_stop_or_cancel", loopReasonContext(s, name), id, d.Verdict.Value))
				l.State = domain.WorkflowLoopNeedsAttention
			}
		}
		if l.Closed && l.IterationsStarted >= l.EffectiveCap(s.Definition.Loops[name].MaxIterations) {
			reasons = append(reasons, "loop_exhausted:"+loopReasonContext(s, name)+":extend_or_stop_or_cancel")
			l.State = domain.WorkflowLoopNeedsAttention
		}
	}
	return reasons
}

func (m *Manager) reconcileV2Locked(approved map[string]bool) {
	if approved == nil {
		approved = map[string]bool{}
	}
	if m.storageErr != nil {
		return
	}
	before := m.active.snapshot
	s, err := cloneWorkflowSnapshot(before)
	if err != nil {
		m.setStorageErrorLocked(err)
		return
	}
	m.enforceTimeoutsLocked(s)
	m.applyCancellingLocked(s)
	m.recomputeTaskStatesLocked(s)
	if s.Mode != domain.WorkflowModeCancelling {
		settleV2Passes(s)
		m.recomputeTaskStatesLocked(s)
		m.refreshExecutionStateLocked(s)
	}
	m.refreshExecutionStateLocked(s)
	if !reflect.DeepEqual(before, s) {
		if err := m.commitControlLocked(s.ExecutionID, s); err != nil {
			return
		}
	} else {
		s = before
	}
	if s.Mode == domain.WorkflowModeRunning && s.State == domain.WorkflowRunning && !m.recoveryChecking {
		next, err := cloneWorkflowSnapshot(s)
		if err != nil {
			m.setStorageErrorLocked(err)
			return
		}
		advanceV2Passes(next)
		m.recomputeTaskStatesLocked(next)
		admitV2Children(next)
		m.recomputeTaskStatesLocked(next)
		m.refreshExecutionStateLocked(next)
		if !reflect.DeepEqual(s, next) {
			if err := m.commitControlLocked(next.ExecutionID, next); err != nil {
				return
			}
			s = next
		}
		if s.State == domain.WorkflowRunning {
			m.dispatchApprovedTasksLocked(s, approved)
		}
	}
	if s.Mode == domain.WorkflowModeRunning && s.State == domain.WorkflowRunning {
		for _, t := range s.Tasks {
			if t.State == domain.WorkflowTaskReady && !approved[s.Definition.Tasks[t.TaskID].Agent] {
				m.Notify()
				break
			}
		}
	}
	if s.State.Terminal() {
		m.active = nil
	}
}

// Name the exact barrier withholding a pending task, including through child
// admission. A phase wait remains pending and is not itself an attention hold.
func phaseWaitReason(s *domain.WorkflowSnapshot, id string) string {
	def := s.Definition
	owner := def.Tasks[id].Loop
	for _, scope := range def.LoopAncestry(owner) {
		node := def.ProjectTask(scope, id)
		g := def.ScopeGraph(scope)
		for _, c := range def.ControlTasks(scope) {
			cn := domain.ScopeNode{Task: c}
			if cn == node || g.Reachable(cn, false)[node] {
				d := currentControlDecision(s, c)
				if d == nil || d.Status != "released" {
					return "waiting_control:" + domain.RenderIterationPath(loopCurrentPath(s, scope)) + ":" + c
				}
			}
		}
	}
	return ""
}
