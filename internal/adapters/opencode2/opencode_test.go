package opencode2

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingSink struct {
	mu       sync.Mutex
	lines    []string
	progress []domain.RunProgress
}

func (s *recordingSink) StdoutLine(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, v)
}
func (s *recordingSink) StderrLine(v string) { s.StdoutLine(v) }
func (s *recordingSink) Err() error          { return nil }
func (s *recordingSink) Progress(p domain.RunProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = append(s.progress, p)
}

type fixture struct {
	t           *testing.T
	workspace   string
	server      *httptest.Server
	events      chan string
	prompts     atomic.Int32
	replies     atomic.Int32
	mu          sync.Mutex
	bodies      []map[string]any
	interrupted []string
	mode        string
}

func newFixture(t *testing.T, mode string) (*fixture, *Adapter) {
	t.Helper()
	f := &fixture{t: t, workspace: t.TempDir(), events: make(chan string, 100), mode: mode}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	rt := NewRuntime(context.Background(), f.workspace, &domain.MachineBackendSettings{Mode: "external", BaseURL: f.server.URL})
	t.Cleanup(rt.Close)
	a, err := rt.Adapter(domain.AgentSpec{Name: "worker", Backend: "opencode2", StartupPrompt: "startup", StringOptions: map[string]string{"model": "fixture/model", "agent": "build", "password": "secret", "timeout_seconds": "3"}})
	if err != nil {
		t.Fatal(err)
	}
	return f, a
}
func (f *fixture) event(kind, sid string, d map[string]any) {
	if d == nil {
		d = map[string]any{}
	}
	d["sessionID"] = sid
	b, _ := json.Marshal(map[string]any{"type": kind, "data": d})
	f.mu.Lock()
	ch := f.events
	f.mu.Unlock()
	ch <- string(b)
}
func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u != "opencode" || p != "secret" {
		http.Error(w, "unauthorized", 401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/info":
		fmt.Fprint(w, `{"version":"2.1.4"}`)
	case "/api/location":
		json.NewEncoder(w).Encode(map[string]string{"directory": f.workspace})
	case "/api/config":
		fmt.Fprint(w, `[{"type":"document","info":{"snapshots":true}},{"type":"document","info":{"snapshots":false}}]`)
	case "/api/session":
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "ses_root", "location": map[string]string{"directory": f.workspace}}})
	case "/api/session/ses_root":
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "ses_root", "location": map[string]string{"directory": f.workspace}}})
	case "/api/session/active":
		fmt.Fprint(w, `{"data":{}}`)
	case "/api/session/ses_root/model", "/api/session/ses_root/agent":
		w.WriteHeader(204)
	case "/api/session/ses_root/permission", "/api/session/ses_child/permission", "/api/session/ses_nested/permission":
		fmt.Fprint(w, `{"data":[]}`)
	case "/api/event":
		events := make(chan string, 100)
		f.mu.Lock()
		f.events = events
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"data\":{}}\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case e := <-events:
				if e == "disconnect" {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", e)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	case "/api/session/ses_root/prompt":
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			f.t.Error(err)
			return
		}
		f.prompts.Add(1)
		f.mu.Lock()
		f.bodies = append(f.bodies, b)
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": b["id"], "sessionID": "ses_root"}})
		f.event("session.execution.succeeded", "ses_root", nil) // stale completion before our input is delivered
		f.event("session.inbox.delivered", "ses_root", map[string]any{"inboxID": b["id"]})
		f.event("session.step.started", "ses_root", map[string]any{"assistantMessageID": "msg_answer"})
		if f.mode == "disconnect" {
			f.mu.Lock()
			ch := f.events
			f.mu.Unlock()
			ch <- "disconnect"
			return
		}
		f.event("session.created", "ses_child", map[string]any{"parentID": "ses_root"})
		f.event("session.created", "ses_nested", map[string]any{"parentID": "ses_child"})
		f.event("session.text.delta", "ses_nested", map[string]any{"delta": "child"})
		if f.mode == "permission" || f.mode == "cancel" {
			f.event("permission.asked", "ses_other", map[string]any{"id": "foreign", "action": "bash", "resources": []string{"bad"}})
			f.event("permission.asked", "ses_nested", map[string]any{"id": "request", "action": "bash", "resources": []string{"pwd"}, "save": []string{"*"}})
			f.event("permission.asked", "ses_nested", map[string]any{"id": "request", "action": "bash", "resources": []string{"pwd"}})
			return
		}
		f.finish()
	case "/api/session/ses_nested/permission/request/reply":
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		if b["decision"] != "once" {
			f.t.Errorf("decision %q", b["decision"])
		}
		f.replies.Add(1)
		w.WriteHeader(204)
		f.finish()
	case "/api/session/ses_root/message/msg_answer":
		fmt.Fprint(w, `{"data":{"id":"msg_answer","type":"assistant","content":[{"type":"reasoning","text":"private reasoning"},{"type":"text","text":"answer"}]}}`)
	default:
		if strings.HasSuffix(r.URL.Path, "/interrupt") {
			f.mu.Lock()
			f.interrupted = append(f.interrupted, r.URL.Path)
			f.mu.Unlock()
			fmt.Fprint(w, `{"interrupted":true}`)
			return
		}
		http.NotFound(w, r)
	}
}
func (f *fixture) finish() {
	f.event("session.execution.succeeded", "ses_nested", nil)
	f.event("session.execution.succeeded", "ses_child", nil)
	f.event("session.execution.succeeded", "ses_root", nil)
}
func TestTurnsAndDescendants(t *testing.T) {
	f, a := newFixture(t, "normal")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, err := a.Init(ctx, a.spec, domain.AgentState{})
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	for i := 0; i < 2; i++ {
		r, next, e := a.Send(ctx, state, domain.RunRequest{RunID: fmt.Sprint(i), Message: "question"}, sink)
		if e != nil || r.FinalMessage != "answer" {
			t.Fatalf("%+v %v", r, e)
		}
		state = next
		state.LastRunID = "previous"
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(f.bodies[0]["text"].(string), "startup") || strings.Contains(f.bodies[1]["text"].(string), "startup") {
		t.Fatal(f.bodies)
	}
	found := false
	for _, p := range sink.progress {
		if len(p.Subagents) == 2 && p.ChildLastActivityAt != nil && p.Phase == domain.RunPhaseWaitingForSubagent {
			found = true
		}
	}
	if !found {
		t.Fatal("missing nested progress")
	}
}
func TestPermissionIsolationAndManualReply(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			f, a := newFixture(t, "permission")
			yolo := !manual
			a.spec.Yolo = &yolo
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			state, e := a.Init(ctx, a.spec, domain.AgentState{})
			if e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			sink := &recordingSink{}
			go func() { _, _, e := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "go"}, sink); done <- e }()
			if manual {
				for {
					sink.mu.Lock()
					pending := false
					for _, p := range sink.progress {
						if len(p.PendingPermissions) > 0 {
							pending = true
						}
					}
					sink.mu.Unlock()
					if pending {
						break
					}
					select {
					case e := <-done:
						t.Fatalf("early completion %v", e)
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(time.Millisecond):
					}
				}
				if err := a.ReplyPermission(ctx, "run", "foreign", domain.PermissionReply{Reply: "once"}); err == nil {
					t.Fatal("foreign reply accepted")
				}
				if err := a.ReplyPermission(ctx, "run", "request", domain.PermissionReply{Reply: "always"}); err == nil {
					t.Fatal("project-wide always accepted")
				}
				if err := a.ReplyPermission(ctx, "run", "request", domain.PermissionReply{Reply: "once"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if f.replies.Load() != 1 {
				t.Fatal(f.replies.Load())
			}
			if err := a.ReplyPermission(ctx, "run", "request", domain.PermissionReply{Reply: "once"}); err == nil {
				t.Fatal("stale reply accepted")
			}
		})
	}
}
func TestDisconnectDoesNotReplay(t *testing.T) {
	f, a := newFixture(t, "disconnect")
	state, e := a.Init(context.Background(), a.spec, domain.AgentState{})
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = a.Send(context.Background(), state, domain.RunRequest{RunID: "run", Message: "go"}, nil)
	if e == nil || f.prompts.Load() != 1 {
		t.Fatalf("%v %d", e, f.prompts.Load())
	}
}
func TestCancellationInterruptsOwnedDescendants(t *testing.T) {
	f, a := newFixture(t, "cancel")
	no := false
	a.spec.Yolo = &no
	state, e := a.Init(context.Background(), a.spec, domain.AgentState{})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := &recordingSink{}
	done := make(chan error, 1)
	go func() { _, _, e := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "go"}, sink); done <- e }()
	deadline := time.After(3 * time.Second)
	for {
		sink.mu.Lock()
		n := 0
		if len(sink.progress) > 0 {
			n = len(sink.progress[len(sink.progress)-1].Subagents)
		}
		sink.mu.Unlock()
		if n == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no child activity")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancel succeeded")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.interrupted) != 3 {
		t.Fatal(f.interrupted)
	}
	if f.replies.Load() != 0 {
		t.Fatal("manual request auto approved")
	}
}
