package zcode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

type recordingSink struct {
	mu          sync.Mutex
	lines       []string
	progress    []domain.RunProgress
	diagnostics []string
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
func (s *recordingSink) DiagnosticLine(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diagnostics = append(s.diagnostics, v)
}
func (s *recordingSink) hasPermission() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.progress {
		if p.Phase == domain.RunPhaseWaitingForPermission && len(p.PendingPermissions) > 0 {
			return true
		}
	}
	return false
}
func (s *recordingSink) hasChild() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.progress {
		if len(p.Subagents) > 0 {
			return true
		}
	}
	return false
}

func fakeAdapter(t *testing.T, scenario string) (*Adapter, domain.AgentState) {
	t.Helper()
	spec := domain.AgentSpec{Name: "test", Backend: "zcode", StartupPrompt: "startup marker", ListOptions: map[string][]string{"env": {"GO_WANT_ZCODE_HELPER=1"}}}
	a := New(spec)
	a.command = func() *exec.Cmd { return exec.Command(os.Args[0], "-test.run=^TestProtocolHelper$", "--", scenario) }
	state, err := a.Init(context.Background(), spec, domain.AgentState{WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return a, state
}
func TestLifecycle(t *testing.T) {
	a, state := fakeAdapter(t, "success")
	sink := &recordingSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		res, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if res.FinalMessage != "pong" || next.BackendSessionID != "session" {
			t.Fatalf("%+v %+v", res, next)
		}
		state = next
	}
	recovered, err := a.Recover(ctx, state)
	if err != nil || recovered.BackendSessionID != "session" {
		t.Fatal(recovered, err)
	}
	reset, err := a.Reset(ctx, a.spec, state)
	if err != nil || reset.BackendSessionID != "" {
		t.Fatal(reset, err)
	}
	if len(sink.diagnostics) != 2 {
		t.Fatal("missing model discovery")
	}
}
func TestFailures(t *testing.T) {
	for _, scenario := range []string{"eof", "malformed", "oversized", "failed", "empty", "rejected", "unsupported", "wrong-turn"} {
		t.Run(scenario, func(t *testing.T) {
			a, state := fakeAdapter(t, scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			res, next, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
			if err == nil || res.FinalMessage != "" {
				t.Fatalf("accepted incomplete turn: %+v %v", res, err)
			}
			if scenario != "malformed" && scenario != "oversized" && next.BackendSessionID != "session" {
				t.Fatal("lost session ID")
			}
		})
	}
}
func TestCancellation(t *testing.T) {
	a, state := fakeAdapter(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("unbounded cancellation")
	}
	if err = a.ReplyPermission(context.Background(), "run", "p", domain.PermissionReply{Reply: "once"}); !errors.Is(err, domain.ErrPermissionInactive) {
		t.Fatal(err)
	}
}
func TestSubagents(t *testing.T) {
	a, state := fakeAdapter(t, "child")
	state.BackendSessionID = "session"
	sink := &recordingSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !sink.hasChild() {
		t.Fatal("missing child progress")
	}
	foundNested := false
	for _, p := range sink.progress {
		for _, child := range p.Subagents {
			if child.ID == "old" {
				t.Fatal("historical child leaked")
			}
			if child.ID == "grandchild" && child.ParentID == "child" {
				foundNested = true
			}
		}
	}
	if !foundNested {
		t.Fatal("nested child missing")
	}
}
func TestPermissions(t *testing.T) {
	for _, decision := range []string{"once", "always", "reject"} {
		t.Run(decision, func(t *testing.T) {
			a, state := fakeAdapter(t, "permission")
			yolo := false
			a.spec.Yolo = &yolo
			sink := &recordingSink{}
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, sink)
				done <- err
			}()
			for !sink.hasPermission() {
				select {
				case err := <-done:
					t.Fatalf("ended before permission: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err := a.ReplyPermission(ctx, "foreign", "p", domain.PermissionReply{Reply: "once"}); !errors.Is(err, domain.ErrPermissionInactive) {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results <- a.ReplyPermission(ctx, "run", "p", domain.PermissionReply{Reply: decision})
				}()
			}
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				}
			}
			if success != 1 {
				t.Fatalf("%d approvals succeeded", success)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := a.ReplyPermission(ctx, "run", "p", domain.PermissionReply{Reply: decision}); !errors.Is(err, domain.ErrPermissionInactive) {
				t.Fatal(err)
			}
		})
	}
}
func TestEnvironment(t *testing.T) {
	a, state := fakeAdapter(t, "env")
	a.spec.ListOptions["env"] = append(a.spec.ListOptions["env"], "ZCODE_HTTP_PROXY=http://proxy.example:8080")
	t.Setenv("DO_NOT_INHERIT", "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run", Message: "ping"}, nil); err != nil {
		t.Fatal(err)
	}
}

// A separate Go test process exercises real pipes, bidirectional callbacks,
// response/event interleaving, and shutdown without Node or a paid account.
func TestProtocolHelper(t *testing.T) {
	if os.Getenv("GO_WANT_ZCODE_HELPER") != "1" {
		return
	}
	scenario := os.Args[len(os.Args)-1]
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), maxFrame)
	emit := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	sent := false
	fresh := true
	input := ""
	mode := ""
	event := func(kind, turn string, payload any) {
		emit(map[string]any{"method": "session/event", "params": map[string]any{"type": kind, "sessionId": "session", "turnId": turn, "payload": payload}})
	}
	complete := func() {
		event("turn.completed", "turn", map[string]any{"inputId": input, "response": "pong", "resultType": "success"})
	}
	for scanner.Scan() {
		var m wireMessage
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(3)
		}
		result := any(map[string]any{})
		if m.Method == "" {
			if string(m.ID) != "\"host-permission\"" {
				os.Exit(4)
			}
			var answer map[string]any
			_ = json.Unmarshal(m.Result, &answer)
			if answer["decision"] != "allow" && answer["decision"] != "deny" {
				os.Exit(5)
			}
			complete()
			continue
		}
		switch m.Method {
		case "squad/bootstrap":
			if scenario == "malformed" {
				fmt.Println("invalid")
				continue
			}
			if scenario == "oversized" {
				fmt.Println(strings.Repeat("x", maxFrame+1))
				continue
			}
		case "session/create", "session/resume":
			fresh = m.Method == "session/create"
			result = map[string]any{"session": map[string]string{"sessionId": "session"}, "settings": map[string]any{"model": map[string]any{"available": []string{"Flash"}}}}
		case "session/setMode":
			var p struct{ Mode string }
			_ = json.Unmarshal(m.Params, &p)
			mode = p.Mode
		case "session/subagents":
			items := []map[string]string{}
			var params struct{ SessionID string }
			_ = json.Unmarshal(m.Params, &params)
			if scenario == "child" && params.SessionID == "session" {
				items = append(items, map[string]string{"childSessionId": "old", "status": "success"})
			}
			if scenario == "child" && sent && params.SessionID == "child" {
				items = append(items, map[string]string{"childSessionId": "grandchild", "status": "waiting"})
			}
			if scenario == "child" && sent && params.SessionID == "session" {
				items = append(items, map[string]string{"childSessionId": "child", "status": "running"})
			}
			result = map[string]any{"running": items, "ended": map[string]any{"items": []any{}}}
		case "session/send":
			var p struct {
				Content        string
				InputID        string
				ModelSelection struct {
					ModelID string
					Options struct{ ReasoningLevel string }
				}
			}
			_ = json.Unmarshal(m.Params, &p)
			input = p.InputID
			if strings.Contains(p.Content, "startup marker") != fresh || p.ModelSelection.ModelID != "GLM-5.3-Flash" || p.ModelSelection.Options.ReasoningLevel != "low" {
				os.Exit(6)
			}
			if scenario == "permission" && mode != "build" || scenario != "permission" && mode != "yolo" {
				os.Exit(7)
			}
			if scenario == "env" && (os.Getenv("DO_NOT_INHERIT") != "" || os.Getenv("ZCODE_HTTP_PROXY") != "http://proxy.example:8080") {
				os.Exit(8)
			}
			emit(map[string]any{"id": m.ID, "result": map[string]bool{"accepted": scenario != "rejected"}})
			sent = true
			if scenario == "success" {
				event("turn.started", "previous", map[string]string{"inputId": "previous"})
				event("turn.completed", "previous", map[string]string{"resultType": "success", "response": "stale"})
			}
			event("turn.started", "turn", map[string]string{"inputId": input})
			switch scenario {
			case "eof":
				os.Exit(0)
			case "hang":
				continue
			case "failed":
				event("turn.failed", "turn", map[string]any{"error": map[string]string{"message": "failed"}})
			case "empty":
				event("turn.completed", "turn", map[string]string{"resultType": "success", "response": ""})
			case "wrong-turn":
				event("turn.completed", "foreign-turn", map[string]string{"resultType": "success", "response": "stale"})
				os.Exit(0)
			case "unsupported":
				emit(map[string]any{"id": "question", "method": "interaction/requestUserInput", "params": map[string]string{"sessionId": "session"}})
			case "permission":
				emit(map[string]any{"id": "host-permission", "method": "interaction/requestPermission", "params": map[string]any{"requestId": "p", "sessionId": "session", "turnId": "turn", "toolName": "Bash", "input": map[string]string{"command": "echo test"}, "options": []any{map[string]any{"kind": "allow_once", "response": map[string]string{"decision": "allow"}}, map[string]any{"kind": "allow_always", "response": map[string]string{"decision": "allow"}}}}})
			default:
				complete()
			}
			continue
		case "session/close":
			emit(map[string]any{"id": m.ID, "result": map[string]bool{"closed": true}})
			os.Exit(0)
		}
		emit(map[string]any{"id": m.ID, "result": result})
	}
	os.Exit(0)
}

func TestUnsupportedAlwaysAndCancelledReply(t *testing.T) {
	r := &activeRun{pending: map[string]approval{"p": {options: []permissionOption{{Kind: "allow_once", Response: json.RawMessage(`{"decision":"allow"}`)}}}}}
	err := r.reply(replyJob{ctx: context.Background(), id: "p", reply: domain.PermissionReply{Reply: "always"}})
	if !errors.Is(err, domain.ErrPermissionReplyInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = r.reply(replyJob{ctx: ctx, id: "p", reply: domain.PermissionReply{Reply: "once"}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(r.pending) != 1 {
		t.Fatal("failed reply consumed permission")
	}
}

func TestRejectConcurrentSend(t *testing.T) {
	a, state := fakeAdapter(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "first", Message: "ping"}, nil)
		done <- err
	}()
	deadline := time.After(3 * time.Second)
	for {
		a.mu.Lock()
		active := a.active != nil
		a.mu.Unlock()
		if active {
			break
		}
		select {
		case <-deadline:
			t.Fatal("run never started")
		case <-time.After(time.Millisecond):
		}
	}
	if _, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "second"}, nil); err == nil {
		t.Fatal("concurrent Send accepted")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
