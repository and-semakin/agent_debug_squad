package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func controlSnapshot() domain.WorkflowSnapshot {
	path := []domain.IterationEntry{{Loop: "l", Iteration: 1}}
	return domain.WorkflowSnapshot{SchemaVersion: 4, Definition: domain.WorkflowDefinition{Version: 2, Name: "test", MaxParallel: 1, TaskTimeoutSeconds: 60, Loops: map[string]domain.WorkflowLoopDefinition{"l": {MaxIterations: 2}}, Tasks: map[string]domain.WorkflowTaskDefinition{
		"head": {Agent: "head", Prompt: "p", Loop: "l", Verdicts: map[string]string{"stop": "stop", "go": "go"}, Control: map[string]string{"stop": "break", "go": "proceed"}}, "body": {Agent: "body", Prompt: "p", Loop: "l", Needs: []string{"head"}}}},
		Loops:       map[string]*domain.WorkflowLoopExecution{"l": {Iteration: 1, IterationPath: path, State: domain.WorkflowLoopDone, WorkflowLoopProgress: &domain.WorkflowLoopProgress{Entered: true, Admitted: true, IterationsStarted: 1, Closed: true, CloseReason: "break", DecisionID: "lcd_1"}}},
		Tasks:       map[string]*domain.WorkflowTaskExecution{"head": {TaskID: "head", State: domain.WorkflowTaskSucceeded, Attempts: []domain.WorkflowAttempt{{Attempt: 1, Iteration: 1, IterationPath: path, State: domain.WorkflowAttemptSucceeded, Verdict: &domain.AttemptVerdict{Value: "stop"}}}}, "body": {TaskID: "body", State: domain.WorkflowTaskSkipped, Skips: []domain.WorkflowTaskSkip{{IterationPath: path, Reason: "loop_break", DecisionID: "lcd_1"}}}},
		Decisions:   []domain.WorkflowControlDecision{{ID: "lcd_1", TaskID: "head", Attempt: 1, OutcomeRevision: 1, IterationPath: path, Verdict: domain.AttemptVerdict{Value: "stop"}, MappedAction: "break", EffectiveAction: "break", Status: "closed"}},
		LoopHistory: []domain.WorkflowLoopHistory{{Loop: "l", IterationPath: path, WorkflowLoopProgress: domain.WorkflowLoopProgress{Entered: true, Admitted: true, IterationsStarted: 1, Closed: true, CloseReason: "break", DecisionID: "lcd_1"}}},
	}
}

func TestSchema4RejectsPartialControlCommits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.WorkflowSnapshot)
	}{
		{"missing loops", func(s *domain.WorkflowSnapshot) { delete(s.Loops, "l") }},
		{"missing progress", func(s *domain.WorkflowSnapshot) { s.Loops["l"].WorkflowLoopProgress = nil }},
		{"wrong schema", func(s *domain.WorkflowSnapshot) { s.SchemaVersion = 3 }},
		{"wrong path", func(s *domain.WorkflowSnapshot) { s.Loops["l"].IterationPath = nil }},
		{"wrong budget", func(s *domain.WorkflowSnapshot) { s.Loops["l"].IterationsStarted = 0 }},
		{"unsafe decision", func(s *domain.WorkflowSnapshot) { s.Decisions[0].ID = "lcd:1" }},
		{"orphan decision", func(s *domain.WorkflowSnapshot) { s.Decisions[0].Attempt = 2 }},
		{"missing decision", func(s *domain.WorkflowSnapshot) { s.Decisions = nil }},
		{"missing skip", func(s *domain.WorkflowSnapshot) { s.Tasks["body"].Skips = nil }},
		{"orphan skip", func(s *domain.WorkflowSnapshot) { s.Tasks["body"].Skips[0].DecisionID = "lcd_9" }},
		{"attempt in skip", func(s *domain.WorkflowSnapshot) {
			s.Tasks["body"].Attempts = append(s.Tasks["body"].Attempts, s.Tasks["head"].Attempts[0])
		}},
		{"missing history", func(s *domain.WorkflowSnapshot) { s.LoopHistory = nil }},
		{"duplicate history", func(s *domain.WorkflowSnapshot) { s.LoopHistory = append(s.LoopHistory, s.LoopHistory[0]) }},
		{"released break", func(s *domain.WorkflowSnapshot) { s.Decisions[0].Status = "released" }},
	}
	valid := controlSnapshot()
	if e := validateWorkflowSnapshot(&valid); e != nil {
		t.Fatal(e)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := controlSnapshot()
			tc.mutate(&s)
			if e := validateWorkflowSnapshot(&s); e == nil {
				t.Fatal("accepted damaged snapshot")
			}
		})
	}
	data, e := json.Marshal(valid)
	if e != nil {
		t.Fatal(e)
	}
	var round domain.WorkflowSnapshot
	if e = json.Unmarshal(data, &round); e != nil {
		t.Fatal(e)
	}
	if e = validateWorkflowSnapshot(&round); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "\"control\":null") {
		t.Fatal("unexpected null control")
	}
}
