package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func legacyLoopSnapshot() domain.WorkflowSnapshot {
	d := loopReviewDefinition(2)
	d.Version = 1
	s := domain.WorkflowSnapshot{SchemaVersion: 2, ExecutionID: "wf_000001", Definition: d, RequestID: "legacy", State: domain.WorkflowRunning, Mode: domain.WorkflowModeRunning, Loops: newLoopExecutions(d), Tasks: map[string]*domain.WorkflowTaskExecution{}}
	for id, td := range d.Tasks {
		s.Tasks[id] = &domain.WorkflowTaskExecution{TaskID: id, Agent: td.Agent, State: domain.WorkflowTaskPending}
	}
	return s
}

func TestLegacyActiveLoopsRefuseStartupWithoutMutation(t *testing.T) {
	for _, kind := range []string{"fixed", "conditioned", "nested"} {
		t.Run(kind, func(t *testing.T) {
			fx := newManagerFixture(t, loopReviewDefinition(2), "a1", "a2", "a3")
			s := legacyLoopSnapshot()
			if kind == "conditioned" {
				l := s.Definition.Loops["refine"]
				l.UntilTask = "review"
				l.OnVerdict = map[string]string{"clean": "break", "issues": "continue"}
				s.Definition.Loops["refine"] = l
			}
			if kind == "nested" {
				s.Definition.Loops["outer"] = domain.WorkflowLoopDefinition{MaxIterations: 1}
				l := s.Definition.Loops["refine"]
				l.Parent = "outer"
				s.Definition.Loops["refine"] = l
				s.Loops = newLoopExecutions(s.Definition)
			}
			if e := fx.st.SaveWorkflowSnapshot(&s); e != nil {
				t.Fatal(e)
			}
			dir, _ := fx.st.WorkflowDir(s.ExecutionID)
			before, _ := os.ReadFile(filepath.Join(dir, "workflow.json"))
			e := fx.m.Start(context.Background())
			if e == nil || !strings.Contains(e.Error(), "previous binary") {
				t.Fatalf("legacy activation: %v", e)
			}
			after, _ := os.ReadFile(filepath.Join(dir, "workflow.json"))
			if string(before) != string(after) {
				t.Fatal("refusal changed authoritative state")
			}
			if len(fx.exec.liveRunIDs()) != 0 {
				t.Fatal("legacy work dispatched")
			}
		})
	}
}

func TestLegacyTerminalHistoryReplaysBeforeMutationGuard(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(2), "a1", "a2", "a3")
	s := legacyLoopSnapshot()
	s.State = domain.WorkflowSucceeded
	s.Loops["refine"].State = domain.WorkflowLoopDone
	for _, task := range s.Tasks {
		task.State = domain.WorkflowTaskSucceeded
	}
	s.Agents = resolvedAgents(fx.m.cfg, s.Definition)
	s.DefinitionHash = HashWorkflowDefinition(s.Definition, s.Agents)
	s.Controls = []domain.WorkflowControlEvent{{Type: "loop_stop", RequestID: "stop", Loop: "refine"}, {Type: "loop_extend", RequestID: "extend", Loop: "refine", Detail: "1"}, {Type: "verdict_override", RequestID: "override", TaskID: "review", Attempt: 1, Detail: "clean"}}
	s.RetryRequests = map[string]domain.WorkflowRetryRecord{"retry": {TaskID: "review", Attempt: 2}}
	if e := fx.st.SaveWorkflowSnapshot(&s); e != nil {
		t.Fatal(e)
	}
	before, _ := json.Marshal(s)
	if v, _, e := fx.m.StopLoop(s.ExecutionID, "refine", StopRequest{RequestID: "stop"}); e != nil || v.ExecutionSupported == nil || *v.ExecutionSupported {
		t.Fatalf("legacy replay: %+v %v", v, e)
	}
	if _, _, e := fx.m.ExtendLoop(s.ExecutionID, "refine", ExtendRequest{RequestID: "extend", AddIterations: 1}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := fx.m.OverrideVerdict(s.ExecutionID, "review", 1, OverrideVerdictRequest{RequestID: "override", Verdict: "clean"}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := fx.m.RetryTask(context.Background(), s.ExecutionID, "review", RetryRequest{RequestID: "retry", ExpectedAttempt: 1}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := fx.m.StopLoop(s.ExecutionID, "refine", StopRequest{RequestID: "new"}); !errors.Is(e, ErrLoopControlConflict) {
		t.Fatalf("mutation: %v", e)
	}
	if _, e := fx.m.Resume(context.Background(), s.ExecutionID); e == nil {
		t.Fatal("legacy resume accepted")
	}
	after, e := fx.st.LoadWorkflowSnapshot(s.ExecutionID)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(after)
	if string(before) != string(raw) {
		t.Fatal("read-only request changed history")
	}
	// Hash/replay lookup precedes new-definition acceptance. A converted v2
	// request conflicts with the old request id instead of activating history.
	if _, _, e := fx.m.Create(context.Background(), "legacy"); !errors.Is(e, ErrDefinitionChanged) {
		t.Fatalf("converted replay: %v", e)
	}
	fx.m.cfg.Workflow = &s.Definition
	if _, created, e := fx.m.Create(context.Background(), "legacy"); e != nil || created {
		t.Fatalf("legacy identity replay: %v %v", created, e)
	}
}

func TestUnknownTerminalSchemaBlocksStartup(t *testing.T) {
	fx := newManagerFixture(t, loopReviewDefinition(1), "a1", "a2", "a3")
	s := legacyLoopSnapshot()
	s.State = domain.WorkflowSucceeded
	s.SchemaVersion = 99
	if e := fx.st.SaveWorkflowSnapshot(&s); e != nil {
		t.Fatal(e)
	}
	if e := fx.m.Start(context.Background()); e == nil || !strings.Contains(e.Error(), "unsupported schema") {
		t.Fatalf("unknown terminal schema accepted: %v", e)
	}
}

func TestV2DefinitionIdentityAndLooplessSchema(t *testing.T) {
	d := queueDefinition(2)
	base := HashWorkflowDefinition(d, nil)
	other := queueDefinition(2)
	sel := other.Tasks["select"]
	sel.Control["empty"] = "continue"
	other.Tasks["select"] = sel
	if HashWorkflowDefinition(other, nil) == base {
		t.Fatal("control action omitted from identity")
	}
	d.Version = 1
	if HashWorkflowDefinition(d, nil) == base {
		t.Fatal("version omitted from identity")
	}
	a, b := queueDefinition(2), queueDefinition(2)
	tdef := b.Tasks["select"]
	tdef.Control = map[string]string{"human": "needs_attention", "empty": "break", "more": "proceed"}
	b.Tasks["select"] = tdef
	if HashWorkflowDefinition(a, nil) != HashWorkflowDefinition(b, nil) {
		t.Fatal("map order changed identity")
	}
	flat := chainDefinition()
	flat.Version = 2
	fx := newManagerFixture(t, flat, "a1", "a2", "a3")
	if _, _, e := fx.m.Create(context.Background(), "flat-v2"); e != nil {
		t.Fatal(e)
	}
	s := loopSnapshot(t, fx)
	if s.SchemaVersion != 4 {
		t.Fatal("flat v2 needs schema4")
	}
}

func TestPausedClosedPassRecoversAndAdvancesOnce(t *testing.T) {
	def := queueDefinition(2)
	sel := def.Tasks["select"]
	sel.Control["empty"] = "continue"
	def.Tasks["select"] = sel
	fx2 := newJudgedFixture(t, def, "a1", "a2", "a3", "a4", "a5")
	if _, _, e := fx2.m.Create(context.Background(), "closed"); e != nil {
		t.Fatal(e)
	}
	fx2.pump()
	if _, e := fx2.m.Pause("wf_000001"); e != nil {
		t.Fatal(e)
	}
	fx2.exec.releaseSuccess(mustFindRunForTask(t, fx2.managerFixture, "select"), "empty")
	fx2.pump()
	if _, _, e := fx2.m.OverrideVerdict("wf_000001", "select", 1, OverrideVerdictRequest{RequestID: "close", Verdict: "empty"}); e != nil {
		t.Fatal(e)
	}
	saved := loopSnapshot(t, fx2.managerFixture)
	if !saved.Loops["queue"].Closed || saved.Loops["queue"].Iteration != 1 {
		t.Fatal("paused close advanced")
	}
	restarted := restartJudged(t, fx2)
	if e := restarted.m.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = restarted.m.Stop(ctx)
	}()
	if _, e := restarted.m.Resume(context.Background(), "wf_000001"); e != nil {
		t.Fatal(e)
	}
	if !restarted.pumpUntil(2*time.Second, func() bool { return loopSnapshot(t, restarted.managerFixture).Loops["queue"].Iteration == 2 }) {
		t.Fatal("recovery did not advance")
	}
	after := loopSnapshot(t, restarted.managerFixture)
	if !reflect.DeepEqual(saved.Decisions, after.Decisions) || len(after.Tasks["hidden"].Skips) != 1 || after.Loops["reviews"].Entered {
		t.Fatal("recovery changed close history or admitted suffix")
	}
}
