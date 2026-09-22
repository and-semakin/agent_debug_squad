// Package judge classifies completed workflow task attempts through an
// external decision model, returning a typed verdict with its confidence and
// the full probability distribution. The Judge interface names the role, not
// any provider: OpenRouter is one implementation selected by configuration.
package judge

import (
	"context"
	"encoding/json"
)

// QuestionType selects one of the decision primitives of the decision model.
type QuestionType string

const (
	// QuestionChoice picks one of the declared options and returns the
	// distribution across all of them. Workflow verdicts use this type.
	QuestionChoice QuestionType = "choice"
	// QuestionNoul answers a yes/no question with the probability that the
	// answer is yes.
	QuestionNoul QuestionType = "noul"
	// QuestionScore places the subject on a declared severity scale and
	// returns the distribution across its levels.
	QuestionScore QuestionType = "score"
)

// Question is one typed decision asked about the evidence in the request
// state. Criteria maps each option (or level, or the true/false pair for
// noul) to its human description; descriptions may be empty.
type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

// Request is one decision: freeform string evidence plus a single typed
// question asked about it. Model, when set, overrides the provider's
// configured default for this request.
type Request struct {
	Model        string
	State        map[string]string
	QuestionName string
	Question     Question
}

// Decision is the typed answer to a Request.
type Decision struct {
	// Choice is the selected option for choice questions and the selected
	// level for score questions; empty for noul.
	Choice string
	// Noul is the probability that the answer is yes, for noul questions.
	Noul float64
	// Confidence is the model's probability that the chosen answer is
	// correct; derived from the distribution for choice and score questions
	// and absent for noul.
	Confidence float64
	// Probabilities is the full distribution across the declared options.
	Probabilities map[string]float64
	// Model is the concrete model that served the decision; a routing alias
	// in the request resolves to a specific version here.
	Model string
	// Raw is the complete provider response, persisted unmodified for audit.
	Raw json.RawMessage
}

// Judge asks one decision of the configured model. Implementations must be
// safe for concurrent use; Decide must never be called while the caller holds
// scheduler locks.
type Judge interface {
	Decide(ctx context.Context, req Request) (Decision, error)
}
