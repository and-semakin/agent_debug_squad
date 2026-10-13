package devin

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestConcurrentPermissionsAndCancelledReplies(t *testing.T) {
	reader, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &turn{ctx: ctx, c: &client{in: writer}, runID: "owned", sessionID: "native", sink: &captureSink{}, pending: map[string]approval{}, seen: map[string]bool{}, live: true, clean: func(s string) string { return s }}
	a := New(domain.AgentSpec{})
	a.active = run
	request := func(id string) message {
		b, _ := json.Marshal(map[string]any{"sessionId": "native", "toolCall": map[string]string{"toolCallId": id}, "options": []any{map[string]string{"optionId": "yes-" + id, "kind": "allow_once"}, map[string]string{"optionId": "no-" + id, "kind": "reject_once"}}})
		wire, _ := json.Marshal(id)
		return message{ID: wire, Method: "session/request_permission", Params: b}
	}
	if e = run.permission(request("first")); e != nil {
		t.Fatal(e)
	}
	if e = run.permission(request("second")); e != nil {
		t.Fatal(e)
	}
	responses := make(chan message, 2)
	go func() {
		scan := bufio.NewScanner(reader)
		for i := 0; i < 2 && scan.Scan(); i++ {
			var m message
			json.Unmarshal(scan.Bytes(), &m)
			responses <- m
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, decision := range []string{"once", "reject"} {
		wg.Add(1)
		go func(i int, d string) {
			defer wg.Done()
			id := "devin-1"
			if i == 1 {
				id = "devin-2"
			}
			errs <- a.ReplyPermission(ctx, "owned", id, domain.PermissionReply{Reply: d})
		}(i, decision)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	got := map[string]string{}
	for i := 0; i < 2; i++ {
		m := <-responses
		var result struct {
			Outcome struct {
				ID string `json:"optionId"`
			} `json:"outcome"`
		}
		json.Unmarshal(m.Result, &result)
		got[string(m.ID)] = result.Outcome.ID
	}
	if got[`"first"`] != "yes-first" || got[`"second"`] != "no-second" {
		t.Fatal("crossed native decisions", got)
	}
	if e = run.permission(request("third")); e != nil {
		t.Fatal(e)
	}
	cancel()
	if e = a.ReplyPermission(context.Background(), "owned", "devin-3", domain.PermissionReply{Reply: "once"}); e != domain.ErrPermissionInactive {
		t.Fatal("cancelled permission accepted", e)
	}
}
