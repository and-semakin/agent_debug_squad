package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultOpenRouterEndpoint is the OpenRouter Decisions API. It is alpha and
// may change without deprecation; it is isolated behind the Judge interface
// and every raw response is persisted for audit.
const DefaultOpenRouterEndpoint = "https://openrouter.ai/api/alpha/decisions"

const (
	defaultOpenRouterTimeout     = 30 * time.Second
	defaultOpenRouterMaxAttempts = 3
	defaultOpenRouterBackoff     = 500 * time.Millisecond
	maxErrorBodySnippet          = 512
)

// OpenRouterConfig configures the OpenRouter decision provider. Endpoint,
// MaxAttempts, and Backoff exist for tests; production uses the defaults.
type OpenRouterConfig struct {
	APIKey   string
	Model    string
	ProxyURL string
	Timeout  time.Duration
	// Endpoint overrides the Decisions API URL (tests).
	Endpoint string
	// MaxAttempts is the total number of attempts including the first
	// (tests).
	MaxAttempts int
	// Backoff is the base delay between retries (tests).
	Backoff time.Duration
}

// OpenRouter implements Judge against the OpenRouter Decisions API. It
// authenticates every request with the configured bearer key, routes them
// through the configured HTTP proxy when set, and retries transient
// transport failures a bounded number of times.
type OpenRouter struct {
	cfg    OpenRouterConfig
	client *http.Client
}

func NewOpenRouter(cfg OpenRouterConfig) (*OpenRouter, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openrouter judge: api key is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("openrouter judge: model is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultOpenRouterTimeout
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultOpenRouterEndpoint
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultOpenRouterMaxAttempts
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = defaultOpenRouterBackoff
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.ProxyURL != "" {
		parsed, err := url.Parse(cfg.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("openrouter judge: parse proxy_url: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &OpenRouter{cfg: cfg, client: &http.Client{Transport: transport}}, nil
}

type openRouterPayload struct {
	Model     string              `json:"model"`
	State     map[string]string   `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type openRouterAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Score         string             `json:"score"`
	Noul          *float64           `json:"noul"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

type openRouterResponse struct {
	Model   string                      `json:"model"`
	Answers map[string]openRouterAnswer `json:"answers"`
}

func (o *OpenRouter) Decide(ctx context.Context, req Request) (Decision, error) {
	switch QuestionType(req.Question.Type) {
	case QuestionChoice, QuestionNoul, QuestionScore:
	default:
		return Decision{}, fmt.Errorf("openrouter judge: unsupported question type %q", req.Question.Type)
	}
	if strings.TrimSpace(req.QuestionName) == "" {
		return Decision{}, errors.New("openrouter judge: question name is required")
	}
	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = o.cfg.Model
	}
	payload, err := json.Marshal(openRouterPayload{
		Model:     model,
		State:     req.State,
		Questions: map[string]Question{req.QuestionName: req.Question},
	})
	if err != nil {
		return Decision{}, fmt.Errorf("openrouter judge: encode request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= o.cfg.MaxAttempts; attempt++ {
		decision, retriable, err := o.attemptOnce(ctx, payload, req.QuestionName)
		if err == nil {
			return decision, nil
		}
		lastErr = err
		if !retriable || attempt == o.cfg.MaxAttempts {
			break
		}
		wait := o.cfg.Backoff * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Decision{}, ctx.Err()
		case <-timer.C:
		}
	}
	return Decision{}, lastErr
}

func (o *OpenRouter) attemptOnce(ctx context.Context, payload []byte, questionName string) (Decision, bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, o.cfg.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, o.cfg.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return Decision{}, false, fmt.Errorf("openrouter judge: build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := o.client.Do(request)
	if err != nil {
		// Network-level failures are transient until proven otherwise.
		return Decision{}, true, fmt.Errorf("openrouter judge: request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Decision{}, true, fmt.Errorf("openrouter judge: read response: %w", err)
	}
	if response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusRequestTimeout {
		return Decision{}, true, fmt.Errorf("openrouter judge: transient status %d: %s", response.StatusCode, snippet(body))
	}
	if response.StatusCode != http.StatusOK {
		return Decision{}, false, fmt.Errorf("openrouter judge: status %d: %s", response.StatusCode, snippet(body))
	}
	decision, err := parseOpenRouterResponse(body, questionName)
	if err != nil {
		return Decision{}, false, err
	}
	return decision, false, nil
}

func parseOpenRouterResponse(body []byte, questionName string) (Decision, error) {
	var parsed openRouterResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Decision{}, fmt.Errorf("openrouter judge: decode response: %w", err)
	}
	answer, ok := parsed.Answers[questionName]
	if !ok {
		return Decision{}, fmt.Errorf("openrouter judge: response has no answer for question %q", questionName)
	}
	decision := Decision{
		Probabilities: answer.Probabilities,
		Model:         parsed.Model,
		Raw:           append(json.RawMessage(nil), body...),
	}
	// Choice and score answers both select one declared criterion; noul
	// answers only carry the yes-probability.
	switch strings.TrimSpace(answer.Choice) {
	case "":
		decision.Choice = strings.TrimSpace(answer.Score)
	default:
		decision.Choice = strings.TrimSpace(answer.Choice)
	}
	if answer.Noul != nil {
		decision.Noul = *answer.Noul
	}
	if answer.Confidence != nil {
		decision.Confidence = *answer.Confidence
	}
	return decision, nil
}

func snippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > maxErrorBodySnippet {
		return text[:maxErrorBodySnippet] + "..."
	}
	return text
}
