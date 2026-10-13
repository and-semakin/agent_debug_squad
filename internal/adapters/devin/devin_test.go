package devin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// A subprocess fixture, not evidence about native Devin. It intentionally
// drops empty sessions on exit, like the tested official 3000.11.1 CLI.
func TestMain(m *testing.M) {
	if scenario := os.Getenv("SQUAD_DEVIN_FIXTURE"); scenario != "" {
		fixture(scenario)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixture(scenario string) {
	send := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	workspace, _ := os.Getwd()
	sid := ""
	mode := "accept-edits"
	model := "swe-2-high"
	reasoning, speed := "medium", "fast"
	options := func() any {
		option := func(id, category, current string, values ...string) map[string]any {
			choices := []map[string]string{}
			for _, v := range values {
				choices = append(choices, map[string]string{"value": v})
			}
			return map[string]any{"id": id, "category": category, "currentValue": current, "options": choices}
		}
		modes := []string{"normal", "bypass", "accept-edits", "ask", "plan", "smart"}
		if scenario == "no-normal" {
			modes = modes[1:]
		}
		result := []any{option("mode", "mode", mode, modes...), option("model", "model", model, "swe-2-high", "controlled", "no-high")}
		if model == "controlled" || model == "no-high" {
			levels := []string{"medium", "high", "max"}
			if model == "no-high" {
				levels = []string{"medium", "max"}
			}
			result = append(result, option("thought_level", "thought_level", reasoning, levels...))
			if scenario == "ambiguous-control" {
				result = append(result, option("other-thinking", "thought_level", reasoning, levels...))
			}
			result = append(result, option("speed", "model_config", speed, "standard", "fast"))
			// Another model_config category must not be mistaken for speed.
			result = append(result, option("unrelated", "model_config", "off", "off", "on"))
		}
		return result
	}
	update := func(kind, text string) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sid, "update": map[string]any{"sessionUpdate": kind, "content": map[string]string{"type": "text", "text": text}}}})
	}
	if len(os.Args) > 3 && os.Args[3] == "models" {
		fmt.Println(`{"families":[{"family_uid":"swe-2","variants":[{"model_uid":"swe-2-high","label":"SWE-2 High","max_context_tokens":262000}]}]}`)
		return
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), maxFrame)
	var promptID json.RawMessage
	for scan.Scan() {
		var m message
		if json.Unmarshal(scan.Bytes(), &m) != nil {
			return
		}
		log, _ := os.OpenFile(filepath.Join(workspace, "calls.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		log.Write(append(append([]byte{}, scan.Bytes()...), '\n'))
		log.Close()
		reply := func(v any) { send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": v}) }
		fail := func(code int) {
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": code, "message": "SECRET_NATIVE_ERROR https://u:pass@example.com/?token=x"}})
		}
		var p map[string]json.RawMessage
		json.Unmarshal(m.Params, &p)
		switch m.Method {
		case "initialize":
			if scenario == "malformed" {
				fmt.Println("not json SECRET_NATIVE_ERROR")
				return
			}
			if scenario == "oversized" {
				fmt.Println(strings.Repeat("x", maxFrame+1))
				return
			}
			if scenario == "stderr" {
				fmt.Fprint(os.Stderr, strings.Repeat("SECRET_NATIVE_ERROR", 100000))
			}
			if scenario == "auth" {
				fail(-32000)
				continue
			}
			if scenario == "out-of-order" {
				send(map[string]any{"jsonrpc": "2.0", "id": 999, "result": map[string]any{}})
				continue
			}
			reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": scenario != "incompatible", "sessionCapabilities": map[string]any{"list": map[string]any{}}}})
		case "session/new":
			sid = fmt.Sprintf("fixture-%d", os.Getpid())
			reply(map[string]any{"sessionId": sid, "configOptions": options()})
		case "session/list":
			paths, _ := filepath.Glob(filepath.Join(workspace, ".fixture-session-*"))
			sessions := []any{}
			for _, p := range paths {
				cwd := workspace
				if scenario == "foreign-workspace" {
					cwd = "/"
				}
				sessions = append(sessions, map[string]string{"sessionId": strings.TrimPrefix(filepath.Base(p), ".fixture-session-"), "cwd": cwd})
			}
			reply(map[string]any{"sessions": sessions})
		case "session/load":
			json.Unmarshal(p["sessionId"], &sid)
			b, e := os.ReadFile(filepath.Join(workspace, ".fixture-session-"+sid))
			if e != nil {
				fail(-32002)
				continue
			}
			if scenario == "locked" {
				fail(-32015)
				continue
			}
			update("user_message_chunk", string(b))
			update("agent_message_chunk", "HISTORICAL_ONLY")
			reply(map[string]any{"configOptions": options()})
		case "session/set_config_option":
			var id, v string
			json.Unmarshal(p["configId"], &id)
			json.Unmarshal(p["value"], &v)
			if scenario != "ignored-control" {
				switch id {
				case "mode":
					mode = v
					if scenario == "reset-control" {
						reasoning = "medium"
					}
				case "model":
					model = v
					reasoning = "medium"
					speed = "fast"
				case "thought_level":
					reasoning = v
				case "speed":
					speed = v
				}
			}
			reply(map[string]any{"configOptions": options()})
		case "session/prompt":
			promptID = m.ID
			os.WriteFile(filepath.Join(workspace, ".fixture-session-"+sid), p["prompt"], 0600)
			if scenario == "wait" {
				continue
			}
			if scenario == "permission" || scenario == "foreign-permission" || scenario == "duplicate-permission" {
				psid := sid
				if scenario == "foreign-permission" {
					psid = "foreign"
				}
				req := map[string]any{"jsonrpc": "2.0", "id": "native-permission", "method": "session/request_permission", "params": map[string]any{"sessionId": psid, "toolCall": map[string]any{"toolCallId": "tool-1", "title": "Run command", "_meta": map[string]string{"cognition.ai/editableCommand": "echo safe"}}, "options": []any{map[string]string{"optionId": "yes-once", "kind": "allow_once"}, map[string]string{"optionId": "no-once", "kind": "reject_once"}, map[string]string{"optionId": "persist", "kind": "allow_always"}}}}
				send(req)
				if scenario == "duplicate-permission" {
					send(req)
				}
				continue
			}
			if scenario == "unknown-callback" {
				send(map[string]any{"jsonrpc": "2.0", "id": "unknown", "method": "session/question", "params": map[string]any{}})
				continue
			}
			if scenario == "foreign-output" {
				sid = "foreign"
			}
			if scenario == "secret" {
				update("agent_message_chunk", "SECRET_")
				update("agent_message_chunk", "VALUE")
				reply(map[string]string{"stopReason": "end_turn"})
				continue
			}
			update("agent_thought_chunk", "THOUGHT_ONLY")
			if scenario != "empty" {
				update("agent_message_chunk", "FRESH_OK")
			}
			if scenario == "eof" {
				return
			}
			if scenario == "unsettled" {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sid, "update": map[string]string{"sessionUpdate": "tool_call", "toolCallId": "pending", "status": "in_progress"}}})
			}
			reason := "end_turn"
			if scenario == "refusal" || scenario == "max_tokens" || scenario == "cancelled" {
				reason = scenario
			}
			reply(map[string]string{"stopReason": reason})
			if scenario == "bad-exit" {
				os.Exit(7)
			}
		case "session/cancel":
			send(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]string{"stopReason": "cancelled"}})
		case "":
			if len(promptID) > 0 {
				update("agent_message_chunk", "FRESH_OK")
				send(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]string{"stopReason": "end_turn"}})
			}
		}
	}
}
func fixtureAdapter(t *testing.T, scenario string) (*Adapter, domain.AgentState) {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	spec := domain.AgentSpec{Name: "devin", Backend: "devin", StartupPrompt: "STARTUP_SENTINEL", StringOptions: map[string]string{"command": exe, "model": "swe-2-high"}, ListOptions: map[string][]string{"env": {"HOME=" + t.TempDir(), "XDG_CONFIG_HOME=" + t.TempDir(), "GORACE=atexit_sleep_ms=0", "SQUAD_DEVIN_FIXTURE=" + scenario}}}
	a := New(spec)
	t.Cleanup(func() { a.ClosePreparation() })
	return a, domain.AgentState{WorkspaceDir: dir}
}
func initFixture(t *testing.T, a *Adapter, s domain.AgentState) domain.AgentState {
	t.Helper()
	s, e := a.Init(context.Background(), a.spec, s)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func calls(t *testing.T, dir, method string) []message {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
	out := []message{}
	for _, line := range strings.Split(string(b), "\n") {
		var m message
		if json.Unmarshal([]byte(line), &m) == nil && m.Method == method {
			out = append(out, m)
		}
	}
	return out
}
func TestContinuityAndLazyReset(t *testing.T) {
	a, s := fixtureAdapter(t, "ok")
	s = initFixture(t, a, s)
	first := s.BackendSessionID
	if len(calls(t, s.WorkspaceDir, "session/prompt")) != 0 {
		t.Fatal("Init prompted")
	}
	for i := 0; i < 2; i++ {
		r, next, e := a.Send(context.Background(), s, domain.RunRequest{RunID: fmt.Sprint(i), Message: "hello"}, nil)
		if e != nil || r.FinalMessage != "FRESH_OK" || next.BackendSessionID != first {
			t.Fatalf("result=%+v error=%v", r, e)
		}
		s = next
	}
	ps := calls(t, s.WorkspaceDir, "session/prompt")
	if len(ps) != 2 || !strings.Contains(string(ps[0].Params), "STARTUP_SENTINEL") || strings.Contains(string(ps[1].Params), "STARTUP_SENTINEL") {
		t.Fatal("startup not once", ps)
	}
	reset, e := a.Reset(context.Background(), a.spec, s)
	if e != nil || reset.BackendSessionID != "" || len(calls(t, s.WorkspaceDir, "session/new")) != 1 {
		t.Fatal("reset not lazy")
	}
	reset = initFixture(t, a, reset)
	if reset.BackendSessionID == first {
		t.Fatal("reused identity")
	}
}
func TestProtocolFailures(t *testing.T) {
	for _, scenario := range []string{"malformed", "oversized", "out-of-order", "auth", "incompatible", "ignored-control", "foreign-output", "foreign-permission", "duplicate-permission", "unknown-callback", "empty", "eof", "bad-exit", "refusal", "max_tokens", "cancelled", "unsettled"} {
		t.Run(scenario, func(t *testing.T) {
			a, s := fixtureAdapter(t, scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, e := a.Init(ctx, a.spec, s)
			if e == nil {
				_, _, e = a.Send(ctx, s, domain.RunRequest{RunID: "failed", Message: "hello"}, nil)
			}
			if e == nil {
				t.Fatal("unexpected success")
			}
			if strings.Contains(e.Error(), "SECRET") || strings.Contains(e.Error(), "pass@") {
				t.Fatal("diagnostic leak")
			}
			if len(calls(t, s.WorkspaceDir, "session/prompt")) > 1 {
				t.Fatal("prompt replay")
			}
		})
	}
}
func TestStderrFloodAndCancellation(t *testing.T) {
	a, s := fixtureAdapter(t, "stderr")
	s = initFixture(t, a, s)
	if _, _, e := a.Send(context.Background(), s, domain.RunRequest{Message: "hello"}, nil); e != nil {
		t.Fatal(e)
	}
	a, s = fixtureAdapter(t, "wait")
	s = initFixture(t, a, s)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, e := a.Send(ctx, s, domain.RunRequest{Message: "hello"}, nil)
	if e == nil || time.Since(start) > 5*time.Second {
		t.Fatal("unbounded cancellation", e)
	}
	if len(calls(t, s.WorkspaceDir, "session/cancel")) != 1 {
		t.Fatal("missing native cancel")
	}
}
func TestMissingAndForeignSessionsDoNotRecreate(t *testing.T) {
	for _, scenario := range []string{"ok", "foreign-workspace", "locked"} {
		t.Run(scenario, func(t *testing.T) {
			a, s := fixtureAdapter(t, scenario)
			s = initFixture(t, a, s)
			_, _, e := a.Send(context.Background(), s, domain.RunRequest{Message: "first"}, nil)
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "ok" {
				os.Remove(filepath.Join(s.WorkspaceDir, ".fixture-session-"+s.BackendSessionID))
			}
			_, _, e = a.Send(context.Background(), s, domain.RunRequest{Message: "second"}, nil)
			if e == nil {
				t.Fatal("unexpected resume")
			}
			if len(calls(t, s.WorkspaceDir, "session/new")) != 1 || len(calls(t, s.WorkspaceDir, "session/prompt")) != 1 {
				t.Fatal("recreated/replayed")
			}
		})
	}
}

type captureSink struct {
	mu       sync.Mutex
	progress domain.RunProgress
	lines    []string
}

func (s *captureSink) StdoutLine(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, v)
}
func (s *captureSink) StderrLine(v string) {}
func (s *captureSink) Err() error          { return nil }
func (s *captureSink) Progress(p domain.RunProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = p
}
func TestPermissionDecisions(t *testing.T) {
	for _, decision := range []string{"once", "reject"} {
		t.Run(decision, func(t *testing.T) {
			a, s := fixtureAdapter(t, "permission")
			s = initFixture(t, a, s)
			sink := &captureSink{}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, _, e := a.Send(ctx, s, domain.RunRequest{RunID: "run", Message: "hello"}, sink); done <- e }()
			id := ""
			for i := 0; i < 200; i++ {
				sink.mu.Lock()
				if len(sink.progress.PendingPermissions) > 0 {
					id = sink.progress.PendingPermissions[0].ID
				}
				sink.mu.Unlock()
				if id != "" {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if id == "" {
				t.Fatal("no pending permission")
			}
			if a.ReplyPermission(ctx, "other", id, domain.PermissionReply{Reply: "once"}) == nil {
				t.Fatal("foreign reply")
			}
			if a.ReplyPermission(ctx, "run", id, domain.PermissionReply{Reply: "always"}) == nil {
				t.Fatal("persistent grant")
			}
			if e := a.ReplyPermission(ctx, "run", id, domain.PermissionReply{Reply: decision}); e != nil {
				t.Fatal(e)
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if a.ReplyPermission(ctx, "run", id, domain.PermissionReply{Reply: decision}) == nil {
				t.Fatal("stale reply")
			}
			b, _ := os.ReadFile(filepath.Join(s.WorkspaceDir, "calls.jsonl"))
			expected := "yes-once"
			if decision == "reject" {
				expected = "no-once"
			}
			if !strings.Contains(string(b), `"optionId":"`+expected+`"`) {
				t.Fatal("wrong native option")
			}
		})
	}
}
func TestSlashCommandsNeverReachPrompt(t *testing.T) {
	for _, command := range []string{"/cloud x", " /handoff x", "/loop x", "/login"} {
		a, s := fixtureAdapter(t, "ok")
		s = initFixture(t, a, s)
		if _, _, e := a.Send(context.Background(), s, domain.RunRequest{Message: command}, nil); e == nil {
			t.Fatal(command)
		}
		if len(calls(t, s.WorkspaceDir, "session/prompt")) != 0 {
			t.Fatal("slash submitted")
		}
	}
}

func TestUnavailableNormalModeFailsBeforePrompt(t *testing.T) {
	a, s := fixtureAdapter(t, "no-normal")
	no := false
	a.spec.Yolo = &no
	s = initFixture(t, a, s)
	_, _, e := a.Send(context.Background(), s, domain.RunRequest{Message: "never"}, nil)
	if e == nil || len(calls(t, s.WorkspaceDir, "session/prompt")) != 0 {
		t.Fatal("unavailable normal mode dispatched", e)
	}
}
func TestFailedFirstTurnDoesNotDuplicateStartup(t *testing.T) {
	a, s := fixtureAdapter(t, "refusal")
	s = initFixture(t, a, s)
	for i := 0; i < 2; i++ {
		_, _, e := a.Send(context.Background(), s, domain.RunRequest{Message: "hello"}, nil)
		if e == nil {
			t.Fatal("refusal succeeded")
		}
	}
	ps := calls(t, s.WorkspaceDir, "session/prompt")
	if len(ps) != 2 || strings.Contains(string(ps[1].Params), "STARTUP_SENTINEL") {
		t.Fatal("startup repeated after failed submitted turn")
	}
}
func TestExactModelMustBeOffered(t *testing.T) {
	a, s := fixtureAdapter(t, "ok")
	a.spec.StringOptions["model"] = "not-advertised-medium"
	s = initFixture(t, a, s)
	if _, _, e := a.Send(context.Background(), s, domain.RunRequest{Message: "never"}, nil); e == nil {
		t.Fatal("unknown variant accepted")
	}
	if len(calls(t, s.WorkspaceDir, "session/prompt")) > 0 {
		t.Fatal("model fallback")
	}
}

func TestChunkedSecretsNeverReachArtifacts(t *testing.T) {
	a, s := fixtureAdapter(t, "secret")
	a.spec.ListOptions["env"] = append(a.spec.ListOptions["env"], "API_TOKEN=SECRET_VALUE")
	s = initFixture(t, a, s)
	sink := &captureSink{}
	r, _, e := a.Send(context.Background(), s, domain.RunRequest{Message: "redact"}, sink)
	if e != nil || strings.Contains(r.FinalMessage, "SECRET") || !strings.Contains(r.FinalMessage, "redacted") {
		t.Fatal("unsafe final message", e)
	}
	for _, line := range sink.lines {
		if strings.Contains(line, "SECRET_") || strings.Contains(line, "VALUE") {
			t.Fatal("unsafe streamed chunk")
		}
	}
}

func TestIndependentAgentsAndAttemptsAllocateDistinctSessions(t *testing.T) {
	ids := map[string]bool{}
	workspace := t.TempDir()
	for i := 0; i < 4; i++ {
		a, s := fixtureAdapter(t, "ok")
		s.WorkspaceDir = workspace
		a.spec.Name = fmt.Sprintf("agent-or-attempt-%d", i)
		s = initFixture(t, a, s)
		if ids[s.BackendSessionID] {
			t.Fatal("native identity reused")
		}
		ids[s.BackendSessionID] = true
		if _, _, e := a.Send(context.Background(), s, domain.RunRequest{RunID: fmt.Sprint(i), Message: "hello"}, nil); e != nil {
			t.Fatal(e)
		}
	}
}
