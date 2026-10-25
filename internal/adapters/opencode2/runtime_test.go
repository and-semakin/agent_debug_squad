package opencode2

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRuntimeChild(t *testing.T) {
	if os.Getenv("SQUAD_V2_HELPER") != "1" {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(2)
	}
	fmt.Println("server listening on http://" + ln.Addr().String())
	http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, _ := r.BasicAuth()
		if password != os.Getenv("OPENCODE_SERVER_PASSWORD") {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/api/info" {
			json.NewEncoder(w).Encode(map[string]string{"version": "2.1.4"})
			return
		}
		w.WriteHeader(404)
	}))
	os.Exit(0)
}
func helperRuntime(t *testing.T) *Runtime {
	t.Helper()
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "opencode")
	body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'opencode v2.1.4'; exit 0; fi\nexec '" + strings.ReplaceAll(binary, "'", "'\\''") + "' -test.run=^TestRuntimeChild$ -- \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	r := NewRuntimeWithEnv(context.Background(), dir, &domain.MachineBackendSettings{Command: script, InheritEnv: []string{"SQUAD_V2_HELPER"}}, []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "SQUAD_V2_HELPER=1"})
	t.Cleanup(r.Close)
	return r
}
func TestRuntimeConcurrentOwnershipAndClose(t *testing.T) {
	r := helperRuntime(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.EnsureReady(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	r.mu.Lock()
	pid := r.cmd.Process.Pid
	endpoint := r.endpoint
	r.mu.Unlock()
	if pid == 0 || endpoint == "" {
		t.Fatal("missing owned process")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled later caller cannot kill an already healthy shared owner.
	_ = r.EnsureReady(cancelled)
	r.mu.Lock()
	same := r.cmd.Process.Pid == pid
	r.mu.Unlock()
	if !same {
		t.Fatal("runtime replaced")
	}
	r.Close()
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("not reaped")
	}
	if err := r.EnsureReady(context.Background()); err == nil {
		t.Fatal("closed runtime restarted")
	}
}
func TestFailedStartupLatches(t *testing.T) {
	r := helperRuntime(t)
	r.settings.Command = filepath.Join(t.TempDir(), "missing")
	if err := r.EnsureReady(context.Background()); err == nil {
		t.Fatal("missing executable passed")
	}
	if !r.FailureLatched() {
		t.Fatal("failure not latched")
	}
	r.settings.Command = "opencode"
	if err := r.EnsureReady(context.Background()); err == nil || r.cmd != nil {
		t.Fatal("failure retried")
	}
}
func TestEnvironmentPreservesConfigAndRedacts(t *testing.T) {
	env, redact, err := processEnvironment(domain.MachineBackendSettings{ProxyURL: "http://user:secret@proxy.invalid", InheritEnv: []string{"OPENCODE_CONFIG_CONTENT"}}, []string{"HOME=/tmp", "UNRELATED=private", "OPENCODE_CONFIG_CONTENT={\"snapshot\":true,\"agents\":{\"build\":{}}}"}, "auth-secret")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "UNRELATED") || !strings.Contains(joined, `"snapshots":false`) || !strings.Contains(joined, `"agents"`) {
		t.Fatal(joined)
	}
	if strings.Contains(redact.Replace("http://user:secret@proxy.invalid auth-secret"), "secret") {
		t.Fatal("credentials leaked")
	}
}
