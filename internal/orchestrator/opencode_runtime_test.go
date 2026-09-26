package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

// A real child process implements just enough v1 protocol to exercise both
// orchestrator construction paths, without relying on an installed CLI/model.
func TestSquadOpenCodeHelper(t *testing.T) {
	if os.Getenv("SQUAD_MANAGED_HELPER") != "1" {
		return
	}
	wd, _ := os.Getwd()
	f, err := os.OpenFile(filepath.Join(wd, "launches"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(10)
	}
	fmt.Fprintln(f, os.Getpid())
	f.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(11)
	}
	var mu sync.Mutex
	sessions := 0
	messages := map[string]string{}
	models := []string{}
	streams := map[chan []string]bool{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, _ := r.BasicAuth()
		if pass != os.Getenv("OPENCODE_SERVER_PASSWORD") {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("x-opencode-directory") == "" {
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/global/health":
			fmt.Fprint(w, `{"healthy":true}`)
			return
		case "/config":
			fmt.Fprint(w, `{"snapshot":false}`)
			return
		case "/session":
			mu.Lock()
			sessions++
			id := sessions
			mu.Unlock()
			fmt.Fprintf(w, `{"id":"s%d"}`, id)
			return
		case "/event":
			ch := make(chan []string, 8)
			mu.Lock()
			streams[ch] = true
			mu.Unlock()
			defer func() { mu.Lock(); delete(streams, ch); mu.Unlock() }()
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"server.connected\"}\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case events := <-ch:
					for _, event := range events {
						fmt.Fprint(w, "data: "+event+"\n\n")
					}
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "session" {
			http.NotFound(w, r)
			return
		}
		id := parts[1]
		switch parts[2] {
		case "prompt_async":
			var body struct {
				MessageID string `json:"messageID"`
				Model     struct {
					ProviderID string `json:"providerID"`
					ModelID    string `json:"modelID"`
				} `json:"model"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			messages[id] = body.MessageID
			models = append(models, body.Model.ProviderID+"/"+body.Model.ModelID)
			_ = os.WriteFile(filepath.Join(wd, "models"), []byte(strings.Join(models, "\n")), 0600)
			update := fmt.Sprintf(`{"type":"message.part.updated","properties":{"sessionID":%q,"part":{"messageID":%q,"type":"text","text":"done"}}}`, id, body.MessageID)
			idle := fmt.Sprintf(`{"type":"session.idle","properties":{"sessionID":%q}}`, id)
			for ch := range streams {
				ch <- []string{update, idle}
			}
			mu.Unlock()
			w.WriteHeader(204)
		case "message":
			mu.Lock()
			msg := messages[id]
			mu.Unlock()
			fmt.Fprintf(w, `[{"info":{"id":"answer","role":"assistant","parentID":%q},"parts":[{"type":"text","text":"done"}]}]`, msg)
		default:
			http.NotFound(w, r)
		}
	})
	fmt.Println("opencode server listening on http://" + listener.Addr().String())
	_ = http.Serve(listener, handler)
	os.Exit(0)
}

func TestManagedOpenCodeSharedAcrossManualAndOwnedRuns(t *testing.T) {
	cfg := testConfig(t, "Manual", "Workflow")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "opencode")
	quoted := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec "+quoted+" -test.run=^TestSquadOpenCodeHelper$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SQUAD_MANAGED_HELPER", "1")
	cfg.MachineBackends.OpenCode = &domain.MachineBackendSettings{Command: script, InheritEnv: []string{"SQUAD_MANAGED_HELPER"}, ProxyURL: "http://proxy-user:proxy-secret@proxy.invalid"}
	for i := range cfg.Agents {
		cfg.Agents[i].Backend = "opencode"
		cfg.Agents[i].StringOptions = map[string]string{"model": "provider/model" + strconv.Itoa(i)}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.New(cfg)
	o, err := New(ctx, cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	run, err := o.SubmitRun(ctx, "Manual", "manual", nil)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := make(chan domain.OwnedRunOutcome, 1)
	if err := o.SubmitOwnedRun(ctx, domain.OwnedRunOptions{RunID: "wrun_000001_000001", Agent: "Workflow", Message: "owned", StatePath: filepath.Join(t.TempDir(), "state.json"), OnDone: func(out domain.OwnedRunOutcome) { outcomes <- out }}); err != nil {
		t.Fatal(err)
	}
	completed, err := o.Wait(ctx, run.RunID, 5*time.Second)
	if err != nil || completed.Status != domain.RunCompleted {
		t.Fatalf("manual: %+v %v", completed, err)
	}
	select {
	case <-outcomes:
	case <-time.After(5 * time.Second):
		t.Fatal("owned run timeout")
	}
	owned, err := o.Wait(ctx, "wrun_000001_000001", time.Second)
	if err != nil || owned.Status != domain.RunCompleted {
		if owned.Error != nil {
			t.Fatalf("owned: %+v err=%q %v", owned, *owned.Error, err)
		}
		t.Fatalf("owned: %+v %v", owned, err)
	}
	launches, _ := os.ReadFile(filepath.Join(cfg.WorkspaceDir, "launches"))
	if len(strings.Fields(string(launches))) != 1 {
		t.Fatalf("launched %s", launches)
	}
	models, _ := os.ReadFile(filepath.Join(cfg.WorkspaceDir, "models"))
	for _, model := range []string{"provider/model0", "provider/model1"} {
		if !strings.Contains(string(models), model) {
			t.Fatal("per-agent model lost", string(models))
		}
	}
	if err := filepath.Walk(st.SessionDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if strings.Contains(string(data), "proxy-secret") || strings.Contains(string(data), "proxy-user") {
			t.Errorf("machine secret persisted in %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := o.WaitForWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	o.Close()
}
