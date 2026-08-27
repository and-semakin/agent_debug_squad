package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/orchestrator"
)

const (
	defaultWaitTimeout       = 60 * time.Second
	defaultStatusWaitTimeout = 30 * time.Second
)

type Server struct {
	orchestrator *orchestrator.Orchestrator
	cfg          domain.SessionConfig
	mux          *http.ServeMux
}

func New(o *orchestrator.Orchestrator, cfg domain.SessionConfig) *Server {
	s := &Server{
		orchestrator: o,
		cfg:          cfg,
		mux:          http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /session", s.handleSession)
	s.mux.HandleFunc("GET /agents", s.handleAgents)
	s.mux.HandleFunc("GET /agents/{name}", s.handleAgent)
	s.mux.HandleFunc("GET /runs", s.handleRuns)
	s.mux.HandleFunc("GET /runs/{run_id}", s.handleRun)
	s.mux.HandleFunc("GET /transcript", s.handleTranscript)
	s.mux.HandleFunc("POST /agents/{name}/runs", s.handleCreateRun)
	s.mux.HandleFunc("POST /agents/{name}/reset", s.handleResetAgent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, redactedSessionConfig(s.cfg))
}

func redactedSessionConfig(cfg domain.SessionConfig) domain.SessionConfig {
	redacted := cfg
	redacted.Agents = append([]domain.AgentSpec(nil), cfg.Agents...)
	for i := range redacted.Agents {
		redacted.Agents[i].Options = redactedOptions(cfg.Agents[i].Options)
	}
	return redacted
}

func redactedOptions(options map[string]any) map[string]any {
	if options == nil {
		return nil
	}
	redacted := make(map[string]any, len(options))
	for key, value := range options {
		switch {
		case isSensitiveOptionKey(key):
			redacted[key] = "[REDACTED]"
		case strings.EqualFold(key, "env"):
			redacted[key] = redactedEnvValues(value)
		default:
			redacted[key] = redactedOptionValue(value)
		}
	}
	return redacted
}

func isSensitiveOptionKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(key))
	for _, marker := range []string{"password", "passwd", "token", "secret", "api_key", "apikey", "authorization", "credential"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func redactedEnvValues(value any) any {
	redact := func(item string) string {
		name, _, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return "[REDACTED]"
		}
		return name + "=[REDACTED]"
	}
	switch values := value.(type) {
	case []any:
		out := make([]any, len(values))
		for i, item := range values {
			if text, ok := item.(string); ok {
				out[i] = redact(text)
			} else {
				out[i] = "[REDACTED]"
			}
		}
		return out
	case []string:
		out := make([]string, len(values))
		for i, item := range values {
			out[i] = redact(item)
		}
		return out
	default:
		return "[REDACTED]"
	}
}

func redactedOptionValue(value any) any {
	switch typed := value.(type) {
	case string:
		parsed, err := url.Parse(typed)
		if err == nil && parsed.User != nil {
			parsed.User = url.User("[REDACTED]")
			return parsed.String()
		}
		return typed
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactedOptionValue(item)
		}
		return out
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			if redacted, ok := redactedOptionValue(item).(string); ok {
				out[i] = redacted
			}
		}
		return out
	case map[string]any:
		return redactedOptions(typed)
	default:
		return typed
	}
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.orchestrator.Agents())
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.orchestrator.Agent(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, orchestrator.ErrAgentNotFound)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.orchestrator.Runs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	wait := r.URL.Query().Get("wait") == "true"
	timeout := defaultStatusWaitTimeout
	if wait {
		var err error
		timeout, err = timeoutFromQuery(r, defaultStatusWaitTimeout)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	var run domain.RunRecord
	var err error
	if wait {
		run, err = s.orchestrator.Wait(r.Context(), r.PathValue("run_id"), timeout)
	} else {
		run, err = s.orchestrator.Run(r.Context(), r.PathValue("run_id"))
	}
	if errors.Is(err, orchestrator.ErrWaitTimeout) {
		writeJSON(w, http.StatusOK, run)
		return
	}
	if errors.Is(err, orchestrator.ErrRunNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if isUnsafePathError(err) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	events, err := s.orchestrator.Transcript(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var body createRunRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		writeError(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}

	wait := r.URL.Query().Get("wait") == "true"
	timeout := defaultWaitTimeout
	if wait {
		var err error
		timeout, err = timeoutFromQuery(r, defaultWaitTimeout)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	run, err := s.orchestrator.SubmitRun(r.Context(), r.PathValue("name"), body.Message, body.Metadata)
	if errors.Is(err, orchestrator.ErrAgentNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if errors.Is(err, orchestrator.ErrAgentBusy) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if wait {
		waited, waitErr := s.orchestrator.Wait(r.Context(), run.RunID, timeout)
		if waitErr == nil || errors.Is(waitErr, orchestrator.ErrWaitTimeout) {
			run = waited
		} else if errors.Is(waitErr, orchestrator.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, waitErr)
			return
		} else {
			writeError(w, http.StatusInternalServerError, waitErr)
			return
		}
	}

	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) handleResetAgent(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"

	result, err := s.orchestrator.ResetAgent(r.Context(), r.PathValue("name"), force)
	if errors.Is(err, orchestrator.ErrAgentNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if errors.Is(err, orchestrator.ErrAgentBusy) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if errors.Is(err, orchestrator.ErrResetTimeout) {
		writeError(w, http.StatusGatewayTimeout, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type createRunRequest struct {
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata"`
}

func timeoutFromQuery(r *http.Request, defaultTimeout time.Duration) (time.Duration, error) {
	value := r.URL.Query().Get("timeout_seconds")
	if value == "" {
		return defaultTimeout, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0, errors.New("timeout_seconds must be a positive integer")
	}
	return time.Duration(seconds) * time.Second, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("json encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func isUnsafePathError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unsafe ")
}
