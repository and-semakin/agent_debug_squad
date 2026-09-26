package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// A model-free OpenCode server exercises the real adapter/orchestrator/API path.
func permissionAPIServer(t *testing.T, yolo bool, failReply bool) (*Server, chan struct{}, *atomic.Int32) {
	t.Helper()
	prompt := make(chan string, 1)
	ask := make(chan struct{})
	resolved := make(chan struct{}, 2)
	var calls atomic.Int32
	var promptID atomic.Value
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config":
			_, _ = w.Write([]byte(`{"snapshot":false}`))
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"test"}`))
		case "/session":
			_, _ = w.Write([]byte(`{"id":"ses_test"}`))
		case "/session/ses_test/prompt_async":
			var body struct {
				MessageID string `json:"messageID"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			promptID.Store(body.MessageID)
			prompt <- body.MessageID
			w.WriteHeader(204)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			emit := func(v any) { raw, _ := json.Marshal(v); fmt.Fprintf(w, "data: %s\n\n", raw); w.(http.Flusher).Flush() }
			emit(map[string]any{"type": "server.connected"})
			var id string
			select {
			case id = <-prompt:
			case <-r.Context().Done():
				return
			}
			emit(map[string]any{"type": "message.updated", "properties": map[string]any{"sessionID": "ses_test", "info": map[string]any{"id": id, "role": "user"}}})
			emit(map[string]any{"type": "message.updated", "properties": map[string]any{"sessionID": "ses_test", "info": map[string]any{"id": "msg_answer", "role": "assistant", "parentID": id}}})
			select {
			case <-ask:
			case <-r.Context().Done():
				return
			}
			emit(map[string]any{"type": "permission.asked", "properties": map[string]any{"id": "per_test", "sessionID": "ses_test", "permission": "external_directory", "patterns": []string{"/review/*"}, "tool": map[string]any{"messageID": "msg_answer", "callID": "read_1"}}})
			select {
			case <-resolved:
			case <-r.Context().Done():
				return
			}
			emit(map[string]any{"type": "permission.replied", "properties": map[string]any{"requestID": "per_test", "sessionID": "ses_test", "reply": "once"}})
			emit(map[string]any{"type": "session.idle", "properties": map[string]any{"sessionID": "ses_test"}})
		case "/permission/per_test/reply":
			calls.Add(1)
			if failReply {
				http.Error(w, "backend unavailable", 503)
				return
			}
			_, _ = w.Write([]byte("true"))
			resolved <- struct{}{}
		case "/session/ses_test/message":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"info": map[string]any{"id": "msg_answer", "role": "assistant", "parentID": promptID.Load()}, "parts": []any{map[string]any{"type": "text", "text": "Review complete"}}}})
		case "/session/ses_test/abort":
			_, _ = w.Write([]byte("true"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	srv := newTestServerWithConfig(t, func(cfg *domain.SessionConfig) {
		cfg.Defaults.Yolo = yolo
		cfg.Agents[0].Backend = "opencode"
		cfg.Agents[0].StringOptions = map[string]string{"mode": "external", "base_url": backend.URL, "timeout_seconds": "10"}
	}, "Reviewer")
	t.Cleanup(func() { _, _ = srv.orchestrator.ResetAgent(context.Background(), "Reviewer", true) })
	return srv, ask, &calls
}

func TestPermissionAPILongPollAndReply(t *testing.T) {
	for _, kind := range []string{"create", "get", "auto_failure"} {
		t.Run(kind, func(t *testing.T) {
			srv, ask, calls := permissionAPIServer(t, kind == "auto_failure", kind == "auto_failure")
			var accepted domain.RunRecord
			if kind != "create" {
				rr := httptest.NewRecorder()
				srv.ServeHTTP(rr, newJSONRequest(t, "POST", "/agents/Reviewer/runs", runPayload{Message: "review"}))
				decodeResponse(t, rr, &accepted)
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				rr := httptest.NewRecorder()
				if kind == "create" {
					srv.ServeHTTP(rr, newJSONRequest(t, "POST", "/agents/Reviewer/runs?wait=true&timeout_seconds=20", runPayload{Message: "review"}))
				} else {
					srv.ServeHTTP(rr, httptest.NewRequest("GET", "/runs/"+accepted.RunID+"?wait=true&timeout_seconds=20", nil))
				}
				done <- rr
			}()
			close(ask)
			var rr *httptest.ResponseRecorder
			select {
			case rr = <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("permission did not wake long poll")
			}
			var run domain.RunRecord
			decodeResponse(t, rr, &run)
			if run.Status != domain.RunRunning || run.Progress == nil || run.Progress.Phase != domain.RunPhaseWaitingForPermission || len(run.Progress.PendingPermissions) != 1 {
				t.Fatalf("run=%+v body=%s", run, rr.Body.String())
			}
			endpoint := "/runs/" + run.RunID + "/permissions/per_test/reply"
			if kind == "auto_failure" {
				if calls.Load() != 1 || run.Progress.PendingPermissions[0].AutoApproveError == "" {
					t.Fatalf("missing auto failure: %s", rr.Body.String())
				}
				failure := httptest.NewRecorder()
				srv.ServeHTTP(failure, httptest.NewRequest("POST", endpoint, strings.NewReader(`{"reply":"once"}`)))
				if failure.Code != 502 {
					t.Fatalf("status=%d", failure.Code)
				}
				return
			}
			if calls.Load() != 0 {
				t.Fatal("manual run auto-approved")
			}
			foreign := httptest.NewRecorder()
			srv.ServeHTTP(foreign, httptest.NewRequest("POST", "/runs/"+run.RunID+"/permissions/per_foreign/reply", strings.NewReader(`{"reply":"once"}`)))
			if foreign.Code != 404 || calls.Load() != 0 {
				t.Fatalf("foreign=%d calls=%d", foreign.Code, calls.Load())
			}
			replied := httptest.NewRecorder()
			srv.ServeHTTP(replied, httptest.NewRequest("POST", endpoint, strings.NewReader(`{"reply":"once"}`)))
			if replied.Code != 200 || calls.Load() != 1 {
				t.Fatalf("reply=%s status=%d", replied.Body.String(), replied.Code)
			}
			final, err := srv.orchestrator.Wait(context.Background(), run.RunID, 2*time.Second)
			if err != nil || final.Status != domain.RunCompleted {
				t.Fatalf("run did not finish: %+v, %v", final, err)
			}
			stale := httptest.NewRecorder()
			srv.ServeHTTP(stale, httptest.NewRequest("POST", endpoint, strings.NewReader(`{"reply":"once"}`)))
			if stale.Code != 409 {
				t.Fatalf("stale=%d", stale.Code)
			}
		})
	}
}

func TestPermissionAPIYoloCompletesWithoutIntervention(t *testing.T) {
	srv, ask, calls := permissionAPIServer(t, true, false)
	close(ask)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, newJSONRequest(t, "POST", "/agents/Reviewer/runs?wait=true&timeout_seconds=5", runPayload{Message: "review"}))
	var run domain.RunRecord
	decodeResponse(t, rr, &run)
	if run.Status != domain.RunCompleted || calls.Load() != 1 {
		t.Fatalf("body=%s calls=%d", rr.Body.String(), calls.Load())
	}
	if run.Progress != nil && len(run.Progress.PendingPermissions) != 0 {
		t.Fatal("terminal run kept pending permissions")
	}
}

func TestPermissionAPIRejectsInvalidBodies(t *testing.T) {
	srv := newTestServer(t, "Reviewer")
	for _, body := range []string{`{}`, `null`, `{"reply":"yes"}`, `{"reply":"once","typo":true}`, `{"reply":"once"} {}`, `{`} {
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, httptest.NewRequest("POST", "/runs/run_missing/permissions/per_1/reply", strings.NewReader(body)))
		if rr.Code != 400 {
			t.Errorf("body=%s status=%d", body, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest("POST", "/runs/run_missing/permissions/per_1/reply", strings.NewReader(`{"reply":"once"}`)))
	if rr.Code != 404 {
		t.Fatalf("missing run status=%d", rr.Code)
	}
}

func TestPermissionAPIForceResetInvalidatesPendingRequest(t *testing.T) {
	srv, ask, calls := permissionAPIServer(t, false, false)
	close(ask)
	created := httptest.NewRecorder()
	srv.ServeHTTP(created, newJSONRequest(t, "POST", "/agents/Reviewer/runs?wait=true&timeout_seconds=5", runPayload{Message: "review"}))
	var run domain.RunRecord
	decodeResponse(t, created, &run)
	if run.Progress == nil || !run.Progress.NeedsPermissionReply() {
		t.Fatalf("missing wait: %s", created.Body.String())
	}
	reset := httptest.NewRecorder()
	srv.ServeHTTP(reset, httptest.NewRequest("POST", "/agents/Reviewer/reset?force=true", nil))
	if reset.Code != 200 {
		t.Fatalf("reset=%d %s", reset.Code, reset.Body.String())
	}
	final, err := srv.orchestrator.Run(context.Background(), run.RunID)
	if err != nil || final.Status != domain.RunInterrupted || len(final.Progress.PendingPermissions) != 0 {
		t.Fatalf("final=%+v %v", final, err)
	}
	replied := httptest.NewRecorder()
	srv.ServeHTTP(replied, httptest.NewRequest("POST", "/runs/"+run.RunID+"/permissions/per_test/reply", strings.NewReader(`{"reply":"once"}`)))
	if replied.Code != 409 || calls.Load() != 0 {
		t.Fatalf("stale reply=%d calls=%d", replied.Code, calls.Load())
	}
}
