package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func permissionEvent(id, session string) map[string]any {
	return map[string]any{"type": "permission.asked", "properties": map[string]any{
		"id": id, "sessionID": session, "permission": "external_directory",
		"patterns": []any{"/review/*"}, "always": []any{"/review/*"},
		"metadata": map[string]any{"filepath": "/review/candidates.md"},
		"tool":     map[string]any{"messageID": testMessageID, "callID": "call_read"}}}
}

func permissionHarness(t *testing.T, yolo *bool, handler http.HandlerFunc) (*permissionRun, *captureSink) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	sink := &captureSink{}
	a := newTestAdapter(domain.AgentSpec{Yolo: yolo, StringOptions: map[string]string{"base_url": server.URL}})
	tracker := newProgressTracker("root", testMessageID, sink)
	p := newPermissionRun(context.Background(), a, "run_1", tracker, sink)
	a.permissions = p
	t.Cleanup(p.close)
	return p, sink
}

func awaitPermission(t *testing.T, p *permissionRun, condition func([]domain.PermissionRequest) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		var pending []domain.PermissionRequest
		for _, v := range p.pending {
			pending = append(pending, v)
		}
		ok := condition(pending)
		p.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("permission state did not settle")
}

func TestPermissionsYoloOwnershipDuplicatesAndAudit(t *testing.T) {
	for _, setting := range []string{"default", "true", "false"} {
		t.Run(setting, func(t *testing.T) {
			var yolo *bool
			if setting != "default" {
				enabled := setting == "true"
				yolo = &enabled
			}
			var calls atomic.Int32
			p, sink := permissionHarness(t, yolo, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/permission/per_owned/reply" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				var reply domain.PermissionReply
				_ = json.NewDecoder(r.Body).Decode(&reply)
				if reply.Reply != "once" {
					t.Errorf("reply = %+v", reply)
				}
				calls.Add(1)
				_, _ = w.Write([]byte("true"))
			})
			if p.handleEvent(permissionEvent("per_foreign", "foreign")) {
				t.Fatal("tracked foreign session")
			}
			old := permissionEvent("per_old", "root")
			old["properties"].(map[string]any)["tool"] = map[string]any{"messageID": "old_turn"}
			if p.handleEvent(old) {
				t.Fatal("tracked old turn")
			}
			p.handleEvent(permissionEvent("per_owned", "root"))
			p.handleEvent(permissionEvent("per_owned", "root"))
			if setting == "false" {
				if calls.Load() != 0 {
					t.Fatal("manual permission was auto-approved")
				}
				progress := sink.lastProgress(t)
				if progress.Phase != domain.RunPhaseWaitingForPermission || len(progress.PendingPermissions) != 1 || !progress.NeedsPermissionReply() {
					t.Fatalf("progress=%+v", progress)
				}
				request := progress.PendingPermissions[0]
				if request.AskedAt.IsZero() || !strings.Contains(string(request.Metadata), "candidates.md") || len(request.Always) != 1 {
					t.Fatalf("details=%+v", request)
				}
				return
			}
			awaitPermission(t, p, func(v []domain.PermissionRequest) bool { return len(v) == 0 })
			p.handleEvent(permissionEvent("per_owned", "root"))
			if calls.Load() != 1 {
				t.Fatalf("calls=%d", calls.Load())
			}
			sink.mu.Lock()
			log := strings.Join(sink.stdout, "\n")
			sink.mu.Unlock()
			if !strings.Contains(log, `"source":"yolo"`) || !strings.Contains(log, `"reply":"once"`) {
				t.Fatalf("missing audit: %s", log)
			}
		})
	}
}

func TestPermissionsFailureVisibleAndManualRecovery(t *testing.T) {
	enabled := true
	var calls atomic.Int32
	p, sink := permissionHarness(t, &enabled, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "backend unavailable", 503)
			return
		}
		var reply domain.PermissionReply
		_ = json.NewDecoder(r.Body).Decode(&reply)
		if reply.Reply != "always" || reply.Message != "review files" {
			t.Errorf("reply=%+v", reply)
		}
		_, _ = w.Write([]byte("true"))
	})
	p.handleEvent(permissionEvent("per_owned", "root"))
	awaitPermission(t, p, func(v []domain.PermissionRequest) bool { return len(v) == 1 && v[0].AutoApproveError != "" })
	progress := sink.lastProgress(t)
	if !progress.NeedsPermissionReply() {
		t.Fatalf("error not actionable: %+v", progress)
	}
	p.handleEvent(permissionEvent("per_owned", "root"))
	if calls.Load() != 1 {
		t.Fatal("retried automatic failure")
	}
	if err := p.adapter.ReplyPermission(context.Background(), "run_1", "per_owned", domain.PermissionReply{Reply: "always", Message: "review files"}); err != nil {
		t.Fatal(err)
	}
	if progress := sink.lastProgress(t); progress.Phase != domain.RunPhaseRunning || len(progress.PendingPermissions) != 0 {
		t.Fatalf("progress=%+v", progress)
	}
}

func TestPermissionsDescendantsMultipleAndExternalReply(t *testing.T) {
	disabled := false
	p, sink := permissionHarness(t, &disabled, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected reply") })
	p.tracker.handleEvent(map[string]any{"type": "session.created", "properties": map[string]any{"sessionID": "child", "info": map[string]any{"parentID": "root"}}}, false, time.Now())
	p.handleEvent(permissionEvent("per_1", "root"))
	p.handleEvent(permissionEvent("per_2", "child"))
	p.handleEvent(permissionEvent("per_3", "root"))
	p.handleEvent(map[string]any{"type": "permission.replied", "properties": map[string]any{"sessionID": "root", "requestID": "per_1"}})
	if got := sink.lastProgress(t); got.Phase != domain.RunPhaseWaitingForPermission || len(got.PendingPermissions) != 2 {
		t.Fatalf("progress=%+v", got)
	}
	for id, session := range map[string]string{"per_2": "child", "per_3": "root"} {
		p.handleEvent(map[string]any{"type": "permission.replied", "properties": map[string]any{"sessionID": session, "requestID": id}})
	}
	if got := sink.lastProgress(t); got.Phase != domain.RunPhaseWaitingForSubagent {
		t.Fatalf("lost subagent wait: %+v", got)
	}
}

func TestPermissionRepliesSerializedAndCancelled(t *testing.T) {
	disabled := false
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	p, _ := permissionHarness(t, &disabled, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write([]byte("true"))
		case <-r.Context().Done():
		}
	})
	p.handleEvent(permissionEvent("per_owned", "root"))
	results := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			start.Done()
			start.Wait()
			results <- p.adapter.ReplyPermission(context.Background(), "run_1", "per_owned", domain.PermissionReply{Reply: "reject"})
		}()
	}
	<-entered
	close(release)
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, domain.ErrPermissionNotFound)) || (second == nil && errors.Is(first, domain.ErrPermissionNotFound))) {
		t.Fatalf("replies=%v,%v", first, second)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if err := p.adapter.ReplyPermission(context.Background(), "old_run", "per_owned", domain.PermissionReply{Reply: "once"}); !errors.Is(err, domain.ErrPermissionInactive) {
		t.Fatalf("stale=%v", err)
	}
	p.close()
	if err := p.adapter.ReplyPermission(context.Background(), "run_1", "per_owned", domain.PermissionReply{Reply: "once"}); !errors.Is(err, domain.ErrPermissionInactive) {
		t.Fatalf("closed=%v", err)
	}
}

func TestPermissionCloseCancelsInFlightAutoReply(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan struct{})
	p, _ := permissionHarness(t, nil, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(finished)
	})
	p.handleEvent(permissionEvent("per_owned", "root"))
	<-entered
	p.close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("backend reply not cancelled")
	}
}

func TestPermissionAutoReplyTimeoutRemainsActionable(t *testing.T) {
	p, sink := permissionHarness(t, nil, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	p.handleEvent(permissionEvent("per_timeout", "root"))
	deadline := time.Now().Add(permissionReplyTimeout + 2*time.Second)
	for time.Now().Before(deadline) {
		progress := sink.lastProgress(t)
		if progress.NeedsPermissionReply() {
			if !strings.Contains(progress.PendingPermissions[0].AutoApproveError, "deadline") {
				t.Fatalf("error=%+v", progress)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed-out auto approval was not exposed")
}

func TestPermissionAcknowledgedBeforeHTTPResponse(t *testing.T) {
	disabled := false
	entered := make(chan struct{})
	p, _ := permissionHarness(t, &disabled, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	})
	p.handleEvent(permissionEvent("per_owned", "root"))
	result := make(chan error, 1)
	go func() {
		result <- p.adapter.ReplyPermission(context.Background(), "run_1", "per_owned", domain.PermissionReply{Reply: "once"})
	}()
	<-entered
	p.handleEvent(map[string]any{"type": "permission.replied", "properties": map[string]any{"sessionID": "root", "requestID": "per_owned", "reply": "once"}})
	// OpenCode may emit session.idle before the reply HTTP response is received.
	p.close()
	if err := <-result; err != nil {
		t.Fatalf("acknowledged reply reported as failure: %v", err)
	}
}
