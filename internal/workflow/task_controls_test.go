package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/judge"
)

func queueDefinition(n int) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{Version: 2, Name: "queue", MaxParallel: 3, TaskTimeoutSeconds: 60,
		Loops: map[string]domain.WorkflowLoopDefinition{"queue": {MaxIterations: n}, "reviews": {Parent: "queue", MaxIterations: 1}, "deep": {Parent: "reviews", MaxIterations: 1}},
		Tasks: map[string]domain.WorkflowTaskDefinition{
			"select":      {Agent: "a1", Prompt: "Select next MR", Loop: "queue", Verdicts: map[string]string{"more": "Review next", "empty": "Finished", "human": "Ask human"}, Control: map[string]string{"more": "proceed", "empty": "break", "human": "needs_attention"}},
			"review":      {Agent: "a2", Prompt: "Review", Loop: "reviews", Needs: []string{"select"}},
			"hidden":      {Agent: "a3", Prompt: "Independent deep review", Loop: "deep"},
			"consolidate": {Agent: "a4", Prompt: "Consolidate", Loop: "queue", Needs: []string{"review"}, Verdicts: map[string]string{"next": "Next MR", "done": "Finished"}, Control: map[string]string{"next": "continue", "done": "break"}},
			"report":      {Agent: "a5", Prompt: "Report queue outcome", Needs: []string{"select"}},
		}}
}
func queueFixture(t *testing.T, n int) (*judgedFixture, chan string) {
	fx := newJudgedFixture(t, queueDefinition(n), "a1", "a2", "a3", "a4", "a5")
	q := make(chan string, 20)
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) { return decisionFor(<-q, .95), nil })
	if _, _, err := fx.m.Create(context.Background(), "queue"); err != nil {
		t.Fatal(err)
	}
	fx.pump()
	return fx, q
}
func settleQueueControl(t *testing.T, fx *judgedFixture, q chan string, id, value string) {
	t.Helper()
	before := len(loopSnapshot(t, fx.managerFixture).Decisions)
	q <- value
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, id), value)
	if !fx.pumpUntil(2*time.Second, func() bool { return len(loopSnapshot(t, fx.managerFixture).Decisions) > before }) {
		t.Fatal("control did not settle")
	}
}

func TestQueueHeadBreakSkipsEntireUnadmittedSubtree(t *testing.T) {
	fx, q := queueFixture(t, 3)
	if got := len(fx.exec.liveRunIDs()); got != 1 {
		t.Fatalf("hidden roots escaped head gate: %d", got)
	}
	settleQueueControl(t, fx, q, "select", "empty")
	s := loopSnapshot(t, fx.managerFixture)
	if s.SchemaVersion != 4 || len(s.Decisions) != 1 || s.Loops["queue"].IterationsStarted != 1 {
		t.Fatalf("decision/budget: %+v", s.Loops)
	}
	for _, id := range []string{"review", "hidden", "consolidate"} {
		task := s.Tasks[id]
		if len(task.Attempts) != 0 || len(task.Skips) != 1 || task.State != domain.WorkflowTaskSkipped {
			t.Fatalf("%s must skip without backend attempts: %+v", id, task)
		}
	}
	for _, name := range []string{"reviews", "deep"} {
		l := s.Loops[name]
		if l.Entered || l.IterationsStarted != 0 || !l.Closed {
			t.Fatalf("planned skip consumed budget: %+v", l)
		}
	}
	if p := domain.RenderIterationPath(s.Tasks["hidden"].Skips[0].IterationPath); p != "queue=1/reviews=1/deep=1" {
		t.Fatal(p)
	}
	if got := driveToTerminal(t, fx.managerFixture); got.State != domain.WorkflowSucceeded || got.TaskCounts.Skipped != 3 {
		t.Fatalf("skip-only completion: %+v", got)
	}
}

func TestQueuePhaseBarrierWaitsForHiddenDescendant(t *testing.T) {
	fx, q := queueFixture(t, 2)
	settleQueueControl(t, fx, q, "select", "more")
	fx.pump()
	if len(fx.exec.liveRunIDs()) != 2 {
		t.Fatalf("parallel reviews were not admitted: %+v", fx.exec.liveRunIDs())
	}
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "review"), "review")
	fx.pump()
	if len(loopSnapshot(t, fx.managerFixture).Tasks["consolidate"].Attempts) != 0 {
		t.Fatal("outer control escaped unfinished child")
	}
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "hidden"), "hidden")
	fx.pump()
	settleQueueControl(t, fx, q, "consolidate", "next")
	fx.pump()
	settleQueueControl(t, fx, q, "select", "empty")
	s := loopSnapshot(t, fx.managerFixture)
	if len(s.Tasks["review"].Attempts) != 1 || len(s.Tasks["review"].Skips) != 1 {
		t.Fatalf("wrong final review history: %+v", s.Tasks["review"])
	}
	out := outcomeAt(&s, "review", loopCurrentPath(&s, "reviews"))
	if out.Status != "skipped" || out.ResultPath != "" || out.Attempt != 0 {
		t.Fatalf("stale success substituted: %+v", out)
	}
	historical := nestedPreviousIterationOutcomes(&s, "queue", []domain.IterationEntry{{Loop: "queue", Iteration: 1}})
	for _, e := range historical {
		if e.TaskID == "hidden" && (e.Status != "succeeded" || e.IterationPath[0].Iteration != 1) {
			t.Fatalf("historical final context lost: %+v", e)
		}
	}
	if view := findTaskView(mustView2(t, fx.managerFixture), "review"); view.Result != nil || !strings.Contains(view.ResultUnavailableReason, "lcd_3") {
		t.Fatalf("view used old result: %+v", view)
	}
}

func TestSkippedRequiredOutputBlocksReportEvenWhenTolerated(t *testing.T) {
	def := queueDefinition(1)
	r := def.Tasks["report"]
	r.Needs = []string{"review"}
	def.Tasks["report"] = r
	review := def.Tasks["review"]
	review.AllowedToFail = true
	def.Tasks["review"] = review
	fx := newJudgedFixture(t, def, "a1", "a2", "a3", "a4", "a5")
	q := make(chan string, 1)
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) { return decisionFor(<-q, .95), nil })
	if _, _, e := fx.m.Create(context.Background(), "skip"); e != nil {
		t.Fatal(e)
	}
	fx.pump()
	settleQueueControl(t, fx, q, "select", "empty")
	fx.pump()
	v := mustView2(t, fx.managerFixture)
	if v.State != domain.WorkflowFailed || findTaskView(v, "report").BlockedReason != "dependency_skipped:review:lcd_1" {
		t.Fatalf("required skipped dependency: %+v", v)
	}
}

func TestStopReleasesAttentionAtomicallyAndPreventsOverride(t *testing.T) {
	fx, q := queueFixture(t, 3)
	settleQueueControl(t, fx, q, "select", "human")
	if _, e := fx.m.Pause("wf_000001"); e != nil {
		t.Fatal(e)
	}
	if _, _, e := fx.m.StopLoop("wf_000001", "queue", StopRequest{RequestID: "stop"}); e != nil {
		t.Fatal(e)
	}
	s := loopSnapshot(t, fx.managerFixture)
	d := s.Decisions[0]
	if d.MappedAction != "needs_attention" || d.EffectiveAction != "proceed" || d.Status != "released" || d.ResolvedBy != "stop" {
		t.Fatalf("stop audit: %+v", d)
	}
	if len(s.Tasks["review"].Attempts) != 0 {
		t.Fatal("paused suffix dispatched")
	}
	if _, _, e := fx.m.OverrideVerdict("wf_000001", "select", 1, OverrideVerdictRequest{RequestID: "late", Verdict: "empty"}); !errors.Is(e, ErrVerdictConflict) {
		t.Fatalf("stop-resolved override: %v", e)
	}
	if _, e := fx.m.Resume(context.Background(), "wf_000001"); e != nil {
		t.Fatal(e)
	}
	fx.pump()
	for _, id := range []string{"review", "hidden"} {
		fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, id), "ok")
	}
	fx.pump()
	settleQueueControl(t, fx, q, "consolidate", "next")
	if got := driveToTerminal(t, fx.managerFixture); got.State != domain.WorkflowSucceeded {
		t.Fatalf("graceful stop: %+v", got)
	}
	if l := loopSnapshot(t, fx.managerFixture).Loops["queue"]; l.IterationsStarted != 1 {
		t.Fatal("stop entered another pass")
	}
}

func TestControlSaveFailurePublishesNoDecisionOrSkip(t *testing.T) {
	fx, q := queueFixture(t, 1)
	settleQueueControl(t, fx, q, "select", "human")
	before := loopSnapshot(t, fx.managerFixture)
	fx.st.mu.Lock()
	fx.st.saveErr = errors.New("disk unavailable")
	fx.st.mu.Unlock()
	_, _, err := fx.m.OverrideVerdict("wf_000001", "select", 1, OverrideVerdictRequest{RequestID: "override", Verdict: "empty"})
	if err == nil {
		t.Fatal("expected failed save")
	}
	fx.m.mu.Lock()
	active, _ := cloneWorkflowSnapshot(fx.m.active.snapshot)
	fx.m.mu.Unlock()
	if !reflect.DeepEqual(active.Decisions, before.Decisions) || len(active.Tasks["hidden"].Skips) != 0 || active.Tasks["select"].LastAttempt().Verdict.Value != "human" {
		t.Fatal("partial decision adopted")
	}
	after := loopSnapshot(t, fx.managerFixture)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed commit changed durable state")
	}
}

func TestContinueCapSkipsOnceAndExtensionNeverResurrectsSuffix(t *testing.T) {
	def := queueDefinition(1)
	sel := def.Tasks["select"]
	sel.Control["empty"] = "continue"
	def.Tasks["select"] = sel
	fx := newJudgedFixture(t, def, "a1", "a2", "a3", "a4", "a5")
	q := make(chan string, 3)
	fx.judge.setRespond(func(judge.Request) (judge.Decision, error) { return decisionFor(<-q, .95), nil })
	if _, _, e := fx.m.Create(context.Background(), "continue"); e != nil {
		t.Fatal(e)
	}
	fx.pump()
	settleQueueControl(t, fx, q, "select", "empty")
	fx.pump()
	s := loopSnapshot(t, fx.managerFixture)
	if s.State != domain.WorkflowNeedsAttention || !s.Loops["queue"].Closed {
		t.Fatalf("cap not held: %+v", s.Loops)
	}
	for i := 0; i < 5; i++ {
		fx.pump()
	}
	if len(loopSnapshot(t, fx.managerFixture).Tasks["hidden"].Skips) != 1 {
		t.Fatal("duplicate skip")
	}
	if _, _, e := fx.m.ExtendLoop("wf_000001", "queue", ExtendRequest{RequestID: "overflow", AddIterations: math.MaxInt}); !errors.Is(e, ErrInvalidRequest) {
		t.Fatalf("overflow accepted: %v", e)
	}
	if _, _, e := fx.m.ExtendLoop("wf_000001", "queue", ExtendRequest{RequestID: "extend", AddIterations: 1}); e != nil {
		t.Fatal(e)
	}
	fx.pump()
	s = loopSnapshot(t, fx.managerFixture)
	if s.Loops["queue"].Iteration != 2 || len(s.Tasks["hidden"].Attempts) != 0 || len(s.Tasks["hidden"].Skips) != 1 {
		t.Fatal("extension resurrected skipped pass")
	}
	bytes, _ := json.Marshal(outcomeAt(&s, "hidden", s.Tasks["hidden"].Skips[0].IterationPath))
	for _, key := range []string{"\"attempt\"", "\"run_id\"", "\"result_path\"", "\"verdict\""} {
		if strings.Contains(string(bytes), key) {
			t.Fatalf("skip carries fabricated field: %s", bytes)
		}
	}
}

func TestAllProceedNaturalExhaustion(t *testing.T) {
	for _, policy := range []string{"", "succeed"} {
		t.Run("policy_"+policy, func(t *testing.T) {
			def := conditionedLoopDefinition(1, policy)
			td := def.Tasks["review"]
			for v := range td.Control {
				td.Control[v] = "proceed"
			}
			def.Tasks["review"] = td
			fx, q := newConditionedFixture(t, def)
			if _, _, e := fx.m.Create(context.Background(), "all-proceed"); e != nil {
				t.Fatal(e)
			}
			runIteration(t, fx, q, "review_passed")
			fx.pump()
			v := mustView2(t, fx.managerFixture)
			if policy == "" {
				if v.State != domain.WorkflowNeedsAttention || !hasReason(v, "loop_exhausted") {
					t.Fatalf("natural cap should hold: %+v", v)
				}
			} else if v.Loops[0].State != domain.WorkflowLoopDone {
				t.Fatalf("natural succeed cap: %+v", v.Loops)
			}
		})
	}
}

func TestDecisionFencesToleratedPrefixWhilePaused(t *testing.T) {
	for _, verdict := range []string{"review_passed", "needs_human"} {
		t.Run(verdict, func(t *testing.T) {
			def := conditionedLoopDefinition(2, "")
			p := def.Tasks["implement"]
			p.AllowedToFail = true
			def.Tasks["implement"] = p
			c := def.Tasks["review"]
			c.Control["review_passed"] = "proceed"
			def.Tasks["review"] = c
			fx, q := newConditionedFixture(t, def)
			if _, _, e := fx.m.Create(context.Background(), "fence"); e != nil {
				t.Fatal(e)
			}
			fx.pump()
			fx.exec.releaseFailure(mustFindRunForTask(t, fx.managerFixture, "implement"), "tolerated")
			fx.pump()
			if _, e := fx.m.Pause("wf_000001"); e != nil {
				t.Fatal(e)
			}
			q <- verdict
			fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "review"), "classify")
			if !fx.pumpUntil(time.Second, func() bool { return len(loopSnapshot(t, fx.managerFixture).Decisions) == 1 }) {
				t.Fatal("decision missing")
			}
			if _, _, e := fx.m.RetryTask(context.Background(), "wf_000001", "implement", RetryRequest{RequestID: "retry", ExpectedAttempt: 1}); !errors.Is(e, ErrRetryConflict) {
				t.Fatalf("decision failed to fence prefix: %v", e)
			}
		})
	}
}

func TestV2PreflightRetainsSkippedRearmableAgents(t *testing.T) {
	fx, q := queueFixture(t, 2)
	settleQueueControl(t, fx, q, "select", "empty")
	s := loopSnapshot(t, fx.managerFixture)
	for _, name := range runnableAgents(&s) {
		if name == "a2" || name == "a3" || name == "a4" {
			t.Fatalf("permanently skipped agent included: %v", runnableAgents(&s))
		}
	}
	// A continue hold at cap can be extended; its skipped agents must still be
	// checked on recovery/resume. Use the same complete decision transaction.
	def := queueDefinition(1)
	sel := def.Tasks["select"]
	sel.Control["empty"] = "continue"
	def.Tasks["select"] = sel
	snapshot := s
	snapshot.Definition = def
	l := snapshot.Loops["queue"]
	l.State = domain.WorkflowLoopNeedsAttention
	l.CloseReason = "continue"
	agents := runnableAgents(&snapshot)
	for _, want := range []string{"a2", "a3", "a4"} {
		found := false
		for _, name := range agents {
			found = found || name == want
		}
		if !found {
			t.Fatalf("rearmable skipped agent omitted: %v", agents)
		}
	}
}

func TestV2SubmissionPreflightAndThresholdIdentity(t *testing.T) {
	def := queueDefinition(1)
	fx := newJudgedFixture(t, def, "a1", "a2", "a3", "a4", "a5")
	fx.exec.brokenAgent = "a3"
	if _, _, e := fx.m.Create(context.Background(), "preflight"); e == nil {
		t.Fatal("broken future reviewer admitted")
	}
	ids, e := fx.st.ListWorkflowExecutions()
	if e != nil {
		t.Fatal(e)
	}
	if len(ids) != 0 {
		t.Fatal("failed preflight accepted execution")
	}
	fx.exec.brokenAgent = ""
	if _, _, e := fx.m.Create(context.Background(), "preflight"); e != nil {
		t.Fatal(e)
	}
	if s := loopSnapshot(t, fx.managerFixture); s.Definition.ConfidenceThreshold != 0 || HashWorkflowDefinition(s.Definition, s.Agents) != s.DefinitionHash {
		t.Fatal("default threshold altered saved identity")
	}
}

func TestMiddleControlActionsPreservePrefix(t *testing.T) {
	for _, action := range []string{"break", "continue", "needs_attention"} {
		t.Run(action, func(t *testing.T) {
			def := conditionedLoopDefinition(1, "")
			r := def.Tasks["review"]
			r.Control["review_passed"] = action
			def.Tasks["review"] = r
			def.Tasks["tail"] = domain.WorkflowTaskDefinition{Agent: "a4", Prompt: "Tail", Loop: "refine", Needs: []string{"review"}}
			fx := newJudgedFixture(t, def, "a1", "a2", "a3", "a4")
			q := make(chan string, 2)
			fx.judge.setRespond(func(judge.Request) (judge.Decision, error) { return decisionFor(<-q, .95), nil })
			if _, _, e := fx.m.Create(context.Background(), "middle"); e != nil {
				t.Fatal(e)
			}
			runIteration(t, fx, q, "review_passed")
			fx.pump()
			s := loopSnapshot(t, fx.managerFixture)
			if s.Tasks["implement"].State != domain.WorkflowTaskSucceeded || len(s.Tasks["implement"].Attempts) != 1 {
				t.Fatal("prefix lost")
			}
			if len(s.Tasks["tail"].Attempts) != 0 {
				t.Fatal("suffix backend ran")
			}
			if action == "needs_attention" {
				if len(s.Tasks["tail"].Skips) != 0 || s.Tasks["tail"].State != domain.WorkflowTaskPending {
					t.Fatal("attention skipped suffix")
				}
				if _, _, e := fx.m.OverrideVerdict("wf_000001", "review", 1, OverrideVerdictRequest{RequestID: "continue", Verdict: "issues_found"}); e != nil {
					t.Fatal(e)
				}
				s = loopSnapshot(t, fx.managerFixture)
				if len(s.Decisions) != 2 || s.Decisions[0].ResolvedBy != "override" {
					t.Fatal("override revision lost")
				}
			}
			if len(s.Tasks["tail"].Skips) != 1 || s.Loops["refine"].IterationsStarted != 1 {
				t.Fatal("middle action did not close suffix")
			}
		})
	}
}

func TestJudgeControlCommitFailureKeepsJudgingOutcome(t *testing.T) {
	fx, q := queueFixture(t, 1)
	fx.exec.releaseSuccess(mustFindRunForTask(t, fx.managerFixture, "select"), "empty")
	fx.pump()
	fx.st.mu.Lock()
	fx.st.saveErr = errors.New("judge commit unavailable")
	fx.st.mu.Unlock()
	q <- "empty"
	if !fx.pumpUntil(time.Second, func() bool { fx.m.mu.Lock(); defer fx.m.mu.Unlock(); return fx.m.storageErr != nil }) {
		t.Fatal("save failure not observed")
	}
	fx.m.mu.Lock()
	s, _ := cloneWorkflowSnapshot(fx.m.active.snapshot)
	fx.m.mu.Unlock()
	if s.Tasks["select"].LastAttempt().State != domain.WorkflowAttemptJudging || len(s.Decisions) != 0 || len(s.Tasks["hidden"].Skips) != 0 {
		t.Fatal("judge published partial successful control")
	}
	disk := loopSnapshot(t, fx.managerFixture)
	if len(disk.Decisions) != 0 || disk.Tasks["select"].LastAttempt().State != domain.WorkflowAttemptJudging {
		t.Fatal("failed judge save changed disk")
	}
}

func TestV2NewPhaseChecksBackendsBeforeReservation(t *testing.T) {
	fx, q := queueFixture(t, 1)
	fx.exec.mu.Lock()
	fx.exec.brokenAgent = "a3"
	fx.exec.mu.Unlock()
	settleQueueControl(t, fx, q, "select", "more")
	fx.pump()
	s := loopSnapshot(t, fx.managerFixture)
	if s.State != domain.WorkflowNeedsAttention || s.BackendPreflight == nil {
		t.Fatalf("new phase skipped batch check: %+v", s.AttentionReasons)
	}
	if len(s.Tasks["review"].Attempts) != 0 || len(s.Tasks["hidden"].Attempts) != 0 {
		t.Fatal("failed batch check reserved work")
	}
	fx.exec.mu.Lock()
	fx.exec.brokenAgent = ""
	fx.exec.mu.Unlock()
	if _, e := fx.m.Resume(context.Background(), "wf_000001"); e != nil {
		t.Fatal(e)
	}
	fx.pump()
	if len(fx.exec.liveRunIDs()) != 2 {
		t.Fatal("repaired backend did not release phase")
	}
}
