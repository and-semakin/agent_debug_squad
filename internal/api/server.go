package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/orchestrator"
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
	s.mux.HandleFunc("GET /runs", s.handleRuns)
	s.mux.HandleFunc("GET /runs/{run_id}", s.handleRun)
	s.mux.HandleFunc("GET /transcript", s.handleTranscript)
	s.mux.HandleFunc("POST /agents/{name}/runs", s.handleCreateRun)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.orchestrator.Agents())
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
	run, err := s.orchestrator.Run(r.Context(), r.PathValue("run_id"))
	if errors.Is(err, orchestrator.ErrRunNotFound) {
		writeError(w, http.StatusNotFound, err)
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

	if r.URL.Query().Get("wait") == "true" {
		waitCtx := context.Background()
		if deadline, ok := r.Context().Deadline(); ok {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithDeadline(waitCtx, deadline)
			defer cancel()
		}
		waited, waitErr := s.orchestrator.Wait(waitCtx, run.RunID, timeoutFromQuery(r))
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

type createRunRequest struct {
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata"`
}

func timeoutFromQuery(r *http.Request) time.Duration {
	value := r.URL.Query().Get("timeout_seconds")
	if value == "" {
		return 0
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
