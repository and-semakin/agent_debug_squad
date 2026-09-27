package domain

import "time"

// WorkflowLoopProgress is present only in schema 4. A child can have a planned
// path without having consumed its first pass. Closed is a durable, once-only
// marker consumed by Advance; it survives holds and process restarts.
type WorkflowLoopProgress struct {
	Admitted          bool   `json:"admitted"`
	Entered           bool   `json:"entered"`
	IterationsStarted int    `json:"iterations_started"`
	Closed            bool   `json:"closed"`
	CloseReason       string `json:"close_reason,omitempty"`
	DecisionID        string `json:"decision_id,omitempty"`
}

type WorkflowTaskSkip struct {
	IterationPath []IterationEntry `json:"iteration_path"`
	Reason        string           `json:"reason"`
	DecisionID    string           `json:"decision_id"`
}

// Decisions are immutable evidence except for an explicitly audited release of
// a held action. A manual revision creates another ID and retires the old hold.
type WorkflowControlDecision struct {
	ID              string           `json:"id"`
	TaskID          string           `json:"task_id"`
	Attempt         int              `json:"attempt"`
	OutcomeRevision int              `json:"outcome_revision"`
	IterationPath   []IterationEntry `json:"iteration_path"`
	Verdict         AttemptVerdict   `json:"verdict"`
	MappedAction    string           `json:"mapped_action"`
	EffectiveAction string           `json:"effective_action"`
	Status          string           `json:"status"` // held, released, closed
	ResolvedBy      string           `json:"resolved_by,omitempty"`
	At              time.Time        `json:"at"`
}

// Each closed pass retains the final child paths that determine its outputs.
// Selection follows these links instead of searching attempts by a prefix.
type WorkflowLoopHistory struct {
	Loop          string           `json:"loop"`
	IterationPath []IterationEntry `json:"iteration_path"`
	WorkflowLoopProgress
	ExtendedIterations int                         `json:"extended_iterations,omitempty"`
	FinalChildren      map[string][]IterationEntry `json:"final_children,omitempty"`
}

func (s *WorkflowSnapshot) LegacyLoops() bool {
	return s.Definition.Version != 2 && len(s.Definition.Loops) > 0
}

func (s WorkflowLoopState) Settled() bool { return s == WorkflowLoopDone || s == WorkflowLoopSkipped }
