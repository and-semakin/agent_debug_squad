package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}
func boolPointer(v bool) *bool { return &v }

func TestProcessEnvironment(t *testing.T) {
	ambient := []string{"HOME=/home/test", "PATH=/bin", "HTTP_PROXY=http://old:oldpass@old.test", "http_proxy=http://lower.test", "ALL_PROXY=http://all.test", "NO_PROXY=old.test,localhost", "no_proxy=lower.test", "UNRELATED_SECRET=hidden", "API_KEY=providerkey", "OPENCODE_CONFIG_CONTENT={/*comment*/\"snapshot\":true,\"model\":\"provider/model\",\"server\":{\"port\":9999},}"}
	settings := domain.MachineBackendSettings{ProxyURL: "http://alice:privatepass@proxy.test:3128", NoProxy: "custom.test,old.test", InheritEnv: []string{"API_KEY", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "NO_PROXY", "no_proxy", "OPENCODE_CONFIG_CONTENT"}}
	env, redact, err := processEnvironment(settings, ambient, "generated-password")
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(env)
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		if m[k] != settings.ProxyURL {
			t.Fatalf("%s not overridden", k)
		}
	}
	for _, k := range []string{"UNRELATED_SECRET", "ALL_PROXY", "http_proxy"} {
		if _, ok := m[k]; ok {
			t.Fatalf("unexpected %s", k)
		}
	}
	if m["API_KEY"] != "providerkey" {
		t.Fatal("explicit inheritance lost")
	}
	if m["NO_PROXY"] != "old.test,localhost,lower.test,custom.test,127.0.0.1,::1" || m["no_proxy"] != m["NO_PROXY"] {
		t.Fatal(m["NO_PROXY"])
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(m["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["snapshot"] != false || cfg["model"] != "provider/model" || cfg["server"].(map[string]any)["port"] != float64(9999) {
		t.Fatal(cfg)
	}
	for _, secret := range []string{settings.ProxyURL, "alice", "privatepass", "oldpass", "generated-password"} {
		if strings.Contains(redact.Replace("error "+secret), secret) {
			t.Fatal("leak")
		}
	}
	env, _, err = processEnvironment(domain.MachineBackendSettings{Snapshot: boolPointer(true)}, ambient, "password")
	if err != nil {
		t.Fatal(err)
	}
	m = envMap(env)
	if m["HTTP_PROXY"] != "" || m["HTTPS_PROXY"] != "" || m["NO_PROXY"] != "localhost,127.0.0.1,::1" {
		t.Fatal("ambient proxy inherited")
	}
	if m["OPENCODE_CONFIG_CONTENT"] != `{"snapshot":true}` {
		t.Fatal(m["OPENCODE_CONFIG_CONTENT"])
	}
	env, _, err = processEnvironment(domain.MachineBackendSettings{InheritEnv: []string{"HTTP_PROXY"}}, ambient, "password")
	if err != nil || envMap(env)["HTTP_PROXY"] != "http://old:oldpass@old.test" {
		t.Fatal("explicit proxy inheritance lost")
	}
	for _, bad := range []string{"{broken secret", "null", "[]"} {
		_, _, err = processEnvironment(domain.MachineBackendSettings{InheritEnv: []string{"OPENCODE_CONFIG_CONTENT"}}, []string{"OPENCODE_CONFIG_CONTENT=" + bad}, "password")
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("bad inline config not rejected safely")
		}
	}
}

// Test binary doubles as a controllable, no-model serve process.
func TestRuntimeHelper(t *testing.T) {
	if os.Getenv("SQUAD_OPENCODE_HELPER") != "1" {
		return
	}
	if os.Getenv("HELPER_MODE") == "exit" {
		os.Exit(9)
	}
	if os.Getenv("HELPER_MODE") == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
	}
	if strings.Join(os.Args[len(os.Args)-6:], " ") != "serve --hostname 127.0.0.1 --port 0 --mdns=false" {
		os.Exit(10)
	}
	wd, _ := os.Getwd()
	_ = os.WriteFile(filepath.Join(wd, "helper-pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
	if os.Getenv("HELPER_MODE") == "hang" {
		time.Sleep(time.Hour)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(11)
	}
	var cfg map[string]any
	_ = json.Unmarshal([]byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), &cfg)
	if os.Getenv("HELPER_MODE") == "mismatch" {
		cfg["snapshot"] = true
	}
	var sessions atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "opencode" || pass != os.Getenv("OPENCODE_SERVER_PASSWORD") {
			w.WriteHeader(401)
			return
		}
		requested, _ := filepath.EvalSymlinks(r.Header.Get("x-opencode-directory"))
		actual, _ := filepath.EvalSymlinks(wd)
		if requested != actual {
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/global/health":
			fmt.Fprint(w, `{"healthy":true}`)
		case "/config":
			_ = json.NewEncoder(w).Encode(cfg)
		case "/session":
			fmt.Fprintf(w, `{"id":"session-%d"}`, sessions.Add(1))
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"server.connected\"}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/session/session-1/prompt_async":
			f, _ := os.OpenFile(filepath.Join(wd, "accepted"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			fmt.Fprintln(f, "accepted")
			f.Close()
			w.WriteHeader(204)
			w.(http.Flusher).Flush()
			go func() { time.Sleep(50 * time.Millisecond); os.Exit(20) }()
		case "/session/session-1":
			fmt.Fprintf(w, `{"id":"session-1","directory":%q}`, wd)
		default:
			http.NotFound(w, r)
		}
	})
	fmt.Println("diagnostic containing", os.Getenv("HTTP_PROXY"))
	fmt.Println("opencode server listening on http://" + listener.Addr().String())
	_ = http.Serve(listener, mux)
	os.Exit(0)
}

func helperRuntime(t *testing.T, mode string) *Runtime {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "opencode")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec "+quote(binary)+" -test.run=^TestRuntimeHelper$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := NewRuntime(context.Background(), dir, &domain.MachineBackendSettings{Command: script, ProxyURL: "http://alice:privatepass@proxy.test", InheritEnv: []string{"SQUAD_OPENCODE_HELPER", "HELPER_MODE"}})
	r.ambient = append(r.ambient, "HOME="+dir, "XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "SQUAD_OPENCODE_HELPER=1", "HELPER_MODE="+mode)
	r.startTimeout = 5 * time.Second
	r.stopTimeout = 200 * time.Millisecond
	t.Cleanup(r.Close)
	return r
}

func runtimeAgent(t *testing.T, r *Runtime, model string) *Adapter {
	t.Helper()
	a, err := r.Adapter(domain.AgentSpec{Name: model, Backend: "opencode", StringOptions: map[string]string{"model": model}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRuntimeReuseAndFailure(t *testing.T) {
	r := helperRuntime(t, "")
	const n = 8
	var wg sync.WaitGroup
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := runtimeAgent(t, r, fmt.Sprintf("provider/model%d", i))
			state, err := a.Init(context.Background(), a.spec, domain.AgentState{})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- state.BackendSessionID
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatal("sessions not independent", seen)
	}
	a := runtimeAgent(t, r, "provider/next")
	if a.promptBody("message", "id")["model"].(map[string]string)["modelID"] != "next" {
		t.Fatal("model lost")
	}
	state, err := a.Init(context.Background(), a.spec, domain.AgentState{BackendSessionID: "session-1"})
	if err != nil || state.BackendSessionID != "session-1" {
		t.Fatalf("recovery %v", err)
	}
	if _, err := a.Init(context.Background(), a.spec, domain.AgentState{BackendSessionID: "missing"}); err == nil {
		t.Fatal("missing session replaced")
	}
	pid := r.cmd.Process.Pid
	if _, err := a.Reset(context.Background(), a.spec, state); err != nil {
		t.Fatal(err)
	}
	if r.cmd.Process.Pid != pid {
		t.Fatal("reset replaced process")
	}
	_ = r.cmd.Process.Kill()
	<-r.done
	if _, _, err := a.Send(context.Background(), state, domain.RunRequest{}, nil); err == nil || !strings.Contains(err.Error(), "not replayed") {
		t.Fatalf("dead server: %v", err)
	}
	if r.cmd.Process.Pid != pid {
		t.Fatal("dead child restarted")
	}
}

func TestRuntimeStartupFailuresAndClose(t *testing.T) {
	for _, mode := range []string{"exit", "hang", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			r := helperRuntime(t, mode)
			r.startTimeout = 150 * time.Millisecond
			a := runtimeAgent(t, r, "model")
			if _, err := a.Init(context.Background(), a.spec, domain.AgentState{}); err == nil || strings.Contains(err.Error(), "privatepass") {
				t.Fatalf("unexpected error %v", err)
			}
			r.Close()
			if r.cmd != nil {
				select {
				case <-r.done:
				default:
					t.Fatal("child not reaped")
				}
			}
		})
	}
	r := helperRuntime(t, "")
	r.settings.Command = filepath.Join(t.TempDir(), "missing")
	if err := r.ensure(context.Background()); err == nil {
		t.Fatal("missing executable accepted")
	}
	r2 := helperRuntime(t, "hang")
	done := make(chan error, 1)
	go func() { done <- r2.ensure(context.Background()) }()
	time.Sleep(30 * time.Millisecond)
	r2.Close()
	if err := <-done; err == nil {
		t.Fatal("cancelled start accepted")
	}
	r3 := helperRuntime(t, "")
	if err := r3.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	r3.cancel()
	select {
	case <-r3.done:
	case <-time.After(time.Second):
		t.Fatal("owner cancellation did not stop server")
	}
}

func TestExternalVerificationNoMutationOrReplay(t *testing.T) {
	for _, configBody := range []string{`{"snapshot":false}`, `{"snapshot":true}`, `{}`, `not json`} {
		t.Run(configBody, func(t *testing.T) {
			var creates, prompts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/config" {
					if r.Method != "GET" {
						t.Error("config mutation")
					}
					fmt.Fprint(w, configBody)
					return
				}
				if r.URL.Path == "/session" {
					creates.Add(1)
					fmt.Fprint(w, `{"id":"existing"}`)
					return
				}
				prompts.Add(1)
				http.NotFound(w, r)
			}))
			defer server.Close()
			r := NewRuntime(context.Background(), t.TempDir(), &domain.MachineBackendSettings{Mode: "external", BaseURL: server.URL})
			defer r.Close()
			a := runtimeAgent(t, r, "model")
			_, err := a.Init(context.Background(), a.spec, domain.AgentState{})
			if configBody == `{"snapshot":false}` {
				if err != nil || creates.Load() != 1 {
					t.Fatalf("%v", err)
				}
			} else {
				if err == nil || creates.Load() != 0 {
					t.Fatal("unchecked config created session")
				}
				_, _, _ = a.Send(context.Background(), domain.AgentState{BackendSessionID: "existing"}, domain.RunRequest{}, nil)
				if prompts.Load() != 0 {
					t.Fatal("unchecked config submitted work")
				}
			}
			if r.cmd != nil {
				t.Fatal("external launched process")
			}
			r.Close()
			resp, err := http.Get(server.URL + "/config")
			if err != nil {
				t.Fatal("external server stopped")
			}
			resp.Body.Close()
		})
	}
}

func TestInstalledOpenCodeSmoke(t *testing.T) {
	if os.Getenv("SQUAD_OPENCODE_SMOKE") != "1" {
		t.Skip("set SQUAD_OPENCODE_SMOKE=1 for isolated no-model smoke")
	}
	command, err := exec.LookPath("opencode")
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []bool{false, true} {
		t.Run(strconv.FormatBool(snapshot), func(t *testing.T) {
			dir := t.TempDir()
			project := []byte(`{"$schema":"https://opencode.ai/config.json","snapshot":true,"default_agent":"plan","share":"disabled"}`)
			path := filepath.Join(dir, "opencode.json")
			if err := os.WriteFile(path, project, 0600); err != nil {
				t.Fatal(err)
			}
			r := NewRuntime(context.Background(), dir, &domain.MachineBackendSettings{Command: command, Snapshot: &snapshot, InheritEnv: []string{"OPENCODE_DISABLE_MODELS_FETCH", "OPENCODE_CONFIG_CONTENT"}})
			r.ambient = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_DATA_HOME=" + filepath.Join(dir, "data"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "XDG_STATE_HOME=" + filepath.Join(dir, "state"), "OPENCODE_DISABLE_MODELS_FETCH=true", `OPENCODE_CONFIG_CONTENT={"autoupdate":false,"disabled_providers":["anthropic","openai"]}`}
			defer r.Close()
			a := runtimeAgent(t, r, "test/model")
			state, err := a.Init(context.Background(), a.spec, domain.AgentState{})
			if err != nil {
				t.Fatal(err)
			}
			var cfg map[string]any
			if err := a.doJSON(context.Background(), http.MethodGet, "/config", nil, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg["snapshot"] != snapshot || cfg["default_agent"] != "plan" || cfg["autoupdate"] != false {
				t.Fatal("effective config not preserved")
			}
			if _, err := a.Recover(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(project) {
				t.Fatalf("project config modified: %s", after)
			}
			r.Close()
			restarted := NewRuntime(context.Background(), dir, &domain.MachineBackendSettings{Command: command, Snapshot: &snapshot, InheritEnv: []string{"OPENCODE_DISABLE_MODELS_FETCH", "OPENCODE_CONFIG_CONTENT"}})
			restarted.ambient = append([]string(nil), r.ambient...)
			defer restarted.Close()
			recovered := runtimeAgent(t, restarted, "test/model")
			if _, err := recovered.Init(context.Background(), recovered.spec, state); err != nil {
				t.Fatalf("new process recovery: %v", err)
			}
			restarted.Close()
			t.Logf("verified snapshot=%t, config preservation, session recovery and shutdown; no prompts", snapshot)
		})
	}
}

func TestConfigGuardPreventsNativeRewrites(t *testing.T) {
	dir := t.TempDir()
	env := []string{"HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config")}
	project := filepath.Join(dir, "opencode.jsonc")
	content := []byte(`{/* comment */ "snapshot":true}`)
	if err := os.WriteFile(project, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := guardConfigFiles(dir, env); err == nil || !strings.Contains(err.Error(), "$schema") {
		t.Fatal("schema-less config accepted")
	}
	after, _ := os.ReadFile(project)
	if string(after) != string(content) {
		t.Fatal("guard changed file")
	}
	if err := os.WriteFile(project, []byte(`{"$schema":"https://opencode.ai/config.json","snapshot":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := guardConfigFiles(dir, env); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(dir, "config", "opencode")
	_ = os.MkdirAll(global, 0700)
	_ = os.WriteFile(filepath.Join(global, "config"), []byte("legacy"), 0600)
	if err := guardConfigFiles(dir, env); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatal("legacy config accepted")
	}
}

func TestCloseEscalatesForUncooperativeChild(t *testing.T) {
	r := helperRuntime(t, "stubborn")
	if err := r.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	r.Close()
	if time.Since(started) < r.stopTimeout {
		t.Fatal("helper did not exercise kill escalation")
	}
	select {
	case <-r.done:
	default:
		t.Fatal("uncooperative child not reaped")
	}
	r.Close()
}

func TestAnnouncementRejectsForeignAddresses(t *testing.T) {
	c := make(chan string, 1)
	w := &announcementWriter{ready: c}
	for _, line := range []string{"http://localhost:1234", "http://0.0.0.0:1234", "http://127.0.0.1:0", "http://127.0.0.1:1234/path", "http://user@127.0.0.1:1234"} {
		_, _ = w.Write([]byte("opencode server listening on " + line + "\n"))
	}
	select {
	case <-c:
		t.Fatal("invalid endpoint accepted")
	default:
	}
	_, _ = w.Write([]byte(strings.Repeat("x", 8192) + "\nopencode server listen"))
	_, _ = w.Write([]byte("ing on http://127.0.0.1:1234\n"))
	select {
	case url := <-c:
		if url != "http://127.0.0.1:1234" {
			t.Fatal(url)
		}
	default:
		t.Fatal("valid split announcement lost")
	}
}

func TestProxyRedactionDoesNotCorruptProtocol(t *testing.T) {
	r := helperRuntime(t, "")
	if err := r.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := runtimeAgent(t, r, "model")
	sink := &captureSink{}
	safe := redactingSink{RunSink: sink, adapter: a}
	raw := `{"type":"message","text":"http://alice:privatepass@proxy.test privatepass"}`
	safe.StdoutLine(raw)
	safe.Progress(domain.RunProgress{Phase: domain.RunPhaseRunning})
	if len(sink.stdout) != 1 || strings.Contains(sink.stdout[0], "alice") || strings.Contains(sink.stdout[0], "privatepass") {
		t.Fatal("output leaked proxy")
	}
}

func TestChildDeathAfterPromptAcceptanceNeverReplays(t *testing.T) {
	r := helperRuntime(t, "")
	a := runtimeAgent(t, r, "model")
	state, err := a.Init(context.Background(), a.spec, domain.AgentState{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run_test", Message: "effect"}, nil); err == nil {
		t.Fatal("dead child reported success")
	}
	<-r.done
	if _, _, err := a.Send(ctx, state, domain.RunRequest{RunID: "run_second", Message: "next"}, nil); err == nil {
		t.Fatal("dead child restarted")
	}
	accepted, err := os.ReadFile(filepath.Join(r.workspace, "accepted"))
	if err != nil {
		t.Fatal(err)
	}
	if string(accepted) != "accepted\n" {
		t.Fatal("prompt repeated", string(accepted))
	}
}

func TestConfigChangeBlocksNextPrompt(t *testing.T) {
	var mismatch atomic.Bool
	var prompts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config":
			fmt.Fprintf(w, `{"snapshot":%t}`, mismatch.Load())
		case "/session":
			fmt.Fprint(w, `{"id":"session"}`)
		default:
			prompts.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	r := NewRuntime(context.Background(), t.TempDir(), &domain.MachineBackendSettings{Mode: "external", BaseURL: server.URL})
	defer r.Close()
	a := runtimeAgent(t, r, "model")
	state, err := a.Init(context.Background(), a.spec, domain.AgentState{})
	if err != nil {
		t.Fatal(err)
	}
	mismatch.Store(true)
	if _, _, err := a.Send(context.Background(), state, domain.RunRequest{}, nil); err == nil {
		t.Fatal("mismatch accepted")
	}
	if prompts.Load() != 0 {
		t.Fatal("prompt submitted after config changed")
	}
}

func TestExternalRecoveryWorkspaceContract(t *testing.T) {
	workspace := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, directory string
		valid           bool
	}{
		{"same", workspace, true}, {"symlink", alias, true}, {"different", t.TempDir(), false}, {"missing", filepath.Join(t.TempDir(), "absent"), false}, {"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-opencode-directory") != workspace {
					t.Error("workspace header lost")
				}
				if r.Method != "GET" {
					mutations.Add(1)
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/config":
					fmt.Fprint(w, `{"snapshot":false}`)
				case "/session/saved":
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "saved", "directory": tt.directory})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			owner := NewRuntime(context.Background(), workspace, &domain.MachineBackendSettings{Mode: "external", BaseURL: server.URL})
			defer owner.Close()
			a := runtimeAgent(t, owner, "model")
			state := domain.AgentState{Name: "agent", BackendSessionID: "saved"}
			initialized, err := a.Init(context.Background(), a.spec, state)
			if (err == nil) != tt.valid {
				t.Fatalf("Init: %v", err)
			}
			recovered, err := a.Recover(context.Background(), state)
			if (err == nil) != tt.valid {
				t.Fatalf("Recover: %v", err)
			}
			if initialized.BackendSessionID != "saved" || recovered.BackendSessionID != "saved" || mutations.Load() != 0 {
				t.Fatal("recovery changed identity or submitted work")
			}
		})
	}
}

func TestRuntimeAuthenticationContract(t *testing.T) {
	owner := NewRuntime(context.Background(), t.TempDir(), nil)
	defer owner.Close()
	for _, key := range []string{"username", "password"} {
		_, err := owner.Adapter(domain.AgentSpec{Backend: "opencode", StringOptions: map[string]string{key: "private-credential"}})
		if err == nil || strings.Contains(err.Error(), "private-credential") {
			t.Fatalf("%s: %v", key, err)
		}
	}
	if owner.cmd != nil {
		t.Fatal("validation launched a child")
	}
	for _, username := range []string{"", "reviewer"} {
		external := NewRuntime(context.Background(), t.TempDir(), &domain.MachineBackendSettings{Mode: "external", BaseURL: "http://127.0.0.1:4096"})
		a, err := external.Adapter(domain.AgentSpec{Backend: "opencode", StringOptions: map[string]string{"username": username, "password": "private-credential"}})
		if err != nil {
			t.Fatal(err)
		}
		req, err := a.newRequest(context.Background(), http.MethodGet, "/config", nil)
		if err != nil {
			t.Fatal(err)
		}
		user, pass, ok := req.BasicAuth()
		expected := username
		if expected == "" {
			expected = "opencode"
		}
		if !ok || user != expected || pass != "private-credential" {
			t.Fatal("external credentials not applied")
		}
		external.Close()
	}
}

func TestRedirectNeverForwardsAuthentication(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	owner := NewRuntime(context.Background(), t.TempDir(), &domain.MachineBackendSettings{Mode: "external", BaseURL: origin.URL})
	defer owner.Close()
	a, err := owner.Adapter(domain.AgentSpec{Backend: "opencode", StringOptions: map[string]string{"password": "private-credential"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.doJSON(context.Background(), http.MethodGet, "/config", nil, nil); err == nil {
		t.Fatal("redirect reported success")
	}
	if forwarded.Load() != 0 {
		t.Fatal("followed redirect")
	}
}

func TestInitiatingRequestCancellationIsTerminal(t *testing.T) {
	r := helperRuntime(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	first := r.ensure(ctx)
	if first == nil {
		t.Fatal("cancelled startup succeeded")
	}
	if r.ctx.Err() != nil {
		t.Fatal("request cancellation closed Squad context")
	}
	if r.cmd != nil {
		t.Fatal("already cancelled request started child")
	}
	if second := r.ensure(context.Background()); second != first {
		t.Fatalf("terminal failure not retained: %v", second)
	}
	if r.cmd != nil {
		t.Fatal("later request retried startup")
	}
}

func TestRequestCancellationDuringReadinessIsTerminal(t *testing.T) {
	r := helperRuntime(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- r.ensure(ctx) }()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
waiting:
	for {
		select {
		case <-deadline:
			t.Fatal("child did not start")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(r.workspace, "helper-pid")); err == nil {
				break waiting
			}
		}
	}
	cancel()
	var first error
	select {
	case first = <-result:
	case <-time.After(time.Second):
		t.Fatal("cancelled readiness did not return")
	}
	if first == nil || r.ctx.Err() != nil {
		t.Fatalf("wrong cancellation scope: %v", first)
	}
	select {
	case <-r.done:
	default:
		t.Fatal("child not reaped")
	}
	pid := r.cmd.Process.Pid
	if second := r.ensure(context.Background()); second != first {
		t.Fatalf("failure not retained: %v", second)
	}
	if r.cmd.Process.Pid != pid {
		t.Fatal("child restarted")
	}
}
