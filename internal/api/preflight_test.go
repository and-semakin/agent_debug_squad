package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
	"github.com/and-semakin/agent_debug_squad/internal/store"
	"github.com/and-semakin/agent_debug_squad/internal/workflow"
)

// TestWorkflowCreatePreflightRejectionIsStructured proves the 503 contract:
// concise aggregate, stable code, sanitized issues with official links, and
// agent attribution for a rejected admission.
func TestWorkflowCreatePreflightRejectionIsStructured(t *testing.T) {
	workspace := t.TempDir()
	cfg := domain.SessionConfig{
		SessionName:  "preflight-test",
		SessionID:    "session_preflight_test",
		WorkspaceDir: workspace,
		StateDirName: ".agent-debug-squad",
		Host:         "127.0.0.1",
		Port:         0,
		Agents: []domain.AgentSpec{
			{Name: "Reviewer", Backend: "fake", StartupPrompt: "You are Reviewer"},
			{Name: "Painter", Backend: "codex", StringOptions: map[string]string{
				"command": filepath.Join(workspace, "missing-codex"),
			}},
		},
		Workflow: &domain.WorkflowDefinition{
			Version:            1,
			Name:               "preflight-test",
			MaxParallel:        1,
			TaskTimeoutSeconds: 30,
			Tasks: map[string]domain.WorkflowTaskDefinition{
				"paint": {Agent: "Painter", Prompt: "Paint."},
			},
		},
	}
	o, err := orchestrator.New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.Close)
	manager := workflow.NewManager(cfg, store.New(cfg), o)
	srv := New(o, manager, cfg)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest("POST", "/workflows", strings.NewReader(`{"request_id":"req-preflight"}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		Error  string                  `json:"error"`
		Code   string                  `json:"code"`
		Issues []domain.PreflightIssue `json:"issues"`
	}
	decodeResponse(t, rr, &response)
	if response.Code != "backend_preflight_failed" || len(response.Issues) == 0 {
		t.Fatalf("unexpected response: %+v", response)
	}
	issue := response.Issues[0]
	if issue.Phase != "installation" || issue.Backend != "codex" || issue.Code != "not_found" {
		t.Fatalf("unexpected issue: %+v", issue)
	}
	if len(issue.Agents) != 1 || issue.Agents[0] != "Painter" {
		t.Fatalf("agents must be attributed: %+v", issue)
	}
	if len(issue.InstallationLinks) == 0 || issue.Message == "" {
		t.Fatalf("links and message are required: %+v", issue)
	}

	// Structural validity precedes preflight: a malformed request stays 400.
	bad := httptest.NewRecorder()
	srv.ServeHTTP(bad, httptest.NewRequest("POST", "/workflows", strings.NewReader(`{}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("missing request_id = %d, want 400", bad.Code)
	}

	// Repairing the installation makes the same request ID admissible: a
	// rejected admission consumed nothing.
	if err := os.WriteFile(filepath.Join(workspace, "missing-codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ok := httptest.NewRecorder()
	srv.ServeHTTP(ok, httptest.NewRequest("POST", "/workflows", strings.NewReader(`{"request_id":"req-preflight"}`)))
	if ok.Code != http.StatusAccepted {
		t.Fatalf("repaired resubmit status = %d, want 202; body=%s", ok.Code, ok.Body.String())
	}
}
