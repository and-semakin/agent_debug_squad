package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenCode2ManualAndOwnedRunIsolation(t *testing.T) {
	cfg := testConfig(t, "Manual", "Workflow", "Legacy")
	var mu sync.Mutex
	subscribers := map[chan string]bool{}
	sessions := map[string]string{}
	counter := 0
	emit := func(kind, sid string, data map[string]any) {
		data["sessionID"] = sid
		b, _ := json.Marshal(map[string]any{"type": kind, "data": data})
		mu.Lock()
		defer mu.Unlock()
		for ch := range subscribers {
			ch <- string(b)
		}
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "opencode" || p != "test-password" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/global/health":
			fmt.Fprint(w, `{"healthy":true,"version":"1.18.30"}`)
		case "/api/info":
			fmt.Fprint(w, `{"version":"2.1.4"}`)
		case "/api/config":
			fmt.Fprint(w, `[{"type":"document","info":{"snapshots":false}}]`)
		case "/api/location":
			json.NewEncoder(w).Encode(map[string]string{"directory": cfg.WorkspaceDir})
		case "/api/session/active":
			fmt.Fprint(w, `{"data":{}}`)
		case "/api/session":
			var body struct {
				Model struct {
					ID string `json:"id"`
				} `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			counter++
			sid := fmt.Sprintf("ses_%d", counter)
			sessions[sid] = body.Model.ID
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": sid, "location": map[string]string{"directory": cfg.WorkspaceDir}}})
		case "/api/event":
			ch := make(chan string, 100)
			mu.Lock()
			subscribers[ch] = true
			mu.Unlock()
			defer func() { mu.Lock(); delete(subscribers, ch); mu.Unlock() }()
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"data\":{}}\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case raw := <-ch:
					fmt.Fprintf(w, "data: %s\n\n", raw)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		default:
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) < 3 || parts[0] != "api" || parts[1] != "session" {
				w.WriteHeader(404)
				return
			}
			sid := parts[2]
			if len(parts) == 3 {
				json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": sid, "location": map[string]string{"directory": cfg.WorkspaceDir}}})
				return
			}
			switch parts[3] {
			case "model":
				w.WriteHeader(204)
			case "permission":
				fmt.Fprint(w, `{"data":[]}`)
			case "prompt":
				var body struct {
					ID string `json:"id"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": body.ID, "sessionID": sid}})
				emit("session.inbox.delivered", sid, map[string]any{"inboxID": body.ID})
				emit("session.step.started", sid, map[string]any{"assistantMessageID": "msg_" + sid})
				emit("session.execution.succeeded", sid, map[string]any{})
			case "message":
				json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "msg_" + sid, "type": "assistant", "content": []map[string]string{{"type": "text", "text": "answer"}}}})
			case "interrupt":
				fmt.Fprint(w, `{"interrupted":true}`)
			default:
				w.WriteHeader(404)
			}
		}
	}))
	defer backend.Close()
	for i := range cfg.Agents {
		cfg.Agents[i].Backend = "opencode2"
		cfg.Agents[i].StringOptions = map[string]string{"mode": "external", "base_url": backend.URL, "password": "test-password", "model": fmt.Sprintf("provider/model%d", i)}
	}
	cfg.Agents[2].Backend = "opencode1"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o, err := New(ctx, cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if err := o.PreflightAgents(ctx, []string{"Manual", "Workflow", "Legacy"}); err != nil {
		t.Fatal(err)
	}
	run, err := o.SubmitRun(ctx, "Manual", "manual", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(ctx, domain.OwnedRunOptions{RunID: "wrun_000001_000001", Agent: "Workflow", Message: "owned", StatePath: filepath.Join(t.TempDir(), "state.json"), OnDone: func(out domain.OwnedRunOutcome) { done <- out }}); err != nil {
		t.Fatal(err)
	}
	final, err := o.Wait(ctx, run.RunID, 5*time.Second)
	if err != nil || final.Status != domain.RunCompleted {
		t.Fatalf("manual %+v %v", final, err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	final, err = o.Wait(ctx, "wrun_000001_000001", time.Second)
	if err != nil || final.Status != domain.RunCompleted {
		t.Fatalf("owned %+v %v", final, err)
	}
	if err := o.WaitForWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sessions) != 2 {
		t.Fatal(sessions)
	}
	seen := map[string]bool{}
	for _, model := range sessions {
		seen[model] = true
	}
	if !seen["model0"] || !seen["model1"] {
		t.Fatal(sessions)
	}
}
