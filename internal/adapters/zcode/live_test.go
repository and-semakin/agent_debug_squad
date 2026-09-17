package zcode

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// Opt-in only: uses a real account and a short Flash conversation. Never runs in CI.
func TestLiveZCode(t *testing.T) {
	if os.Getenv("SQUAD_ZCODE_LIVE") != "1" {
		t.Skip("set SQUAD_ZCODE_LIVE=1 for a billed Flash smoke test")
	}
	yolo := true
	spec := domain.AgentSpec{Name: "flash", Backend: "zcode", Yolo: &yolo, StringOptions: map[string]string{"model": "GLM-5.3-Flash", "reasoning": "low"}, ListOptions: map[string][]string{"inherit_env": {"HOME", "PATH", "TMPDIR", "SHELL", "ZCODE_HTTP_PROXY", "ZCODE_NO_PROXY", "ZCODE_AGENT_CA_CERT"}}}
	a := New(spec)
	state, err := a.Init(context.Background(), spec, domain.AgentState{WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for i, prompt := range []string{"Reply with exactly pong. Do not use tools.", "What was your previous reply? Repeat it exactly, without tools."} {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		result, next, err := a.Send(ctx, state, domain.RunRequest{RunID: []string{"live-first", "live-second"}[i], Message: prompt}, domain.DiscardRunSink())
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(result.FinalMessage) != "pong" {
			t.Fatalf("unexpected reply: %q", result.FinalMessage)
		}
		if i > 0 && next.BackendSessionID != state.BackendSessionID {
			t.Fatal("session changed")
		}
		state = next
		t.Logf("turn %d session %s: %s", i+1, state.BackendSessionID, result.FinalMessage)
	}
}

func TestLiveZCodeSubagent(t *testing.T) {
	if os.Getenv("SQUAD_ZCODE_LIVE_CHILD") != "1" {
		t.Skip("set SQUAD_ZCODE_LIVE_CHILD=1 for a billed Flash subagent smoke test")
	}
	spec := domain.AgentSpec{Name: "flash", Backend: "zcode", StringOptions: map[string]string{"model": "GLM-5.3-Flash", "reasoning": "low"}, ListOptions: map[string][]string{"inherit_env": {"HOME", "PATH", "TMPDIR", "SHELL", "ZCODE_HTTP_PROXY", "ZCODE_NO_PROXY", "ZCODE_AGENT_CA_CERT"}}}
	a := New(spec)
	state, err := a.Init(context.Background(), spec, domain.AgentState{WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 105*time.Second)
	defer cancel()
	sink := &recordingSink{}
	res, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "live-child", Message: "Use the Agent tool to launch exactly one foreground general-purpose subagent. Use the same GLM-5.3-Flash model as yourself, with low reasoning. Its entire task is: reply with exactly child-pong without using any tools. Wait for its reply, then respond with exactly parent-pong. Do not inspect or modify files, use other tools, or launch further agents."}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !sink.hasChild() {
		t.Fatalf("no subagent progress; reply: %s", res.FinalMessage)
	}
	if !strings.Contains(res.FinalMessage, "parent-pong") {
		t.Fatal(res.FinalMessage)
	}
	t.Log(res.FinalMessage)
}

func TestLiveZCodePermission(t *testing.T) {
	if os.Getenv("SQUAD_ZCODE_LIVE_PERMISSION") != "1" {
		t.Skip("set SQUAD_ZCODE_LIVE_PERMISSION=1 for a billed Flash permission test")
	}
	yolo := false
	spec := domain.AgentSpec{Name: "flash", Backend: "zcode", Yolo: &yolo, ListOptions: map[string][]string{"inherit_env": {"HOME", "PATH", "TMPDIR", "SHELL", "ZCODE_HTTP_PROXY", "ZCODE_NO_PROXY", "ZCODE_AGENT_CA_CERT"}}}
	a := New(spec)
	state, err := a.Init(context.Background(), spec, domain.AgentState{WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sink := &recordingSink{}
	done := make(chan error, 1)
	go func() {
		_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "live-permission", Message: "Use Bash exactly once to execute: printf permission-pong > permission-check.txt . Wait for approval if needed, then respond with exactly done. Do not use other tools."}, sink)
		done <- err
	}()
	approved := false
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if !approved {
				t.Fatal("no permission was requested")
			}
			data, err := os.ReadFile(state.WorkspaceDir + "/permission-check.txt")
			if err != nil || string(data) != "permission-pong" {
				t.Fatalf("%q %v", data, err)
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
			sink.mu.Lock()
			var id string
			if len(sink.progress) > 0 {
				p := sink.progress[len(sink.progress)-1]
				if len(p.PendingPermissions) > 0 {
					id = p.PendingPermissions[0].ID
				}
			}
			sink.mu.Unlock()
			if id != "" && !approved {
				if err = a.ReplyPermission(ctx, "live-permission", id, domain.PermissionReply{Reply: "once"}); err != nil {
					t.Fatal(err)
				}
				approved = true
			}
		}
	}
}

func TestLiveZCodeCancelChild(t *testing.T) {
	if os.Getenv("SQUAD_ZCODE_LIVE_CANCEL") != "1" {
		t.Skip("set SQUAD_ZCODE_LIVE_CANCEL=1 for a billed Flash cancellation test")
	}
	spec := domain.AgentSpec{Name: "flash", Backend: "zcode", ListOptions: map[string][]string{"inherit_env": {"HOME", "PATH", "TMPDIR", "SHELL", "ZCODE_HTTP_PROXY", "ZCODE_NO_PROXY", "ZCODE_AGENT_CA_CERT"}}}
	a := New(spec)
	state, err := a.Init(context.Background(), spec, domain.AgentState{WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sink := &recordingSink{}
	done := make(chan error, 1)
	go func() {
		_, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "live-cancel", Message: "Use Agent to launch one foreground subagent using your same GLM-5.3-Flash model with low reasoning. Ask it to respond with child-pong without tools. Wait for it, then respond parent-pong. Do not inspect files or use other tools."}, sink)
		done <- err
	}()
	for !sink.hasChild() {
		select {
		case err := <-done:
			t.Fatalf("ended before child observed: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	start := time.Now()
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("unbounded child cancellation")
	}
	t.Logf("child cancellation completed in %s", time.Since(start))
}
