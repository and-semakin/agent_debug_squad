package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/store"
)

// buildBinary compiles the real CLI binary for subprocess tests.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agent-debug-squad")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	return bin
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

type subprocess struct {
	bin        string
	cfgPath    string
	sessionDir string
	stdout     *bytes.Buffer
	stderrLog  *bytes.Buffer
	cmd        *exec.Cmd
	controlURL chan string
	execution  chan string
	exited     chan error
	childLog   string
}

func workflowConfig(t *testing.T, tasks string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	port := freePort(t)
	body := fmt.Sprintf(`session_name: subprocess-test
workspace_dir: %s
state_dir_name: .agent-debug-squad
host: 127.0.0.1
port: %d
defaults:
  yolo: true
agents:
  - name: alpha
    backend: fake
    startup_prompt: You are alpha.
    options:
      delay_ms: "40"
workflow:
  version: 1
  name: subprocess-test
  max_parallel: 2
  task_timeout_seconds: 30
  tasks:
%s`, dir, port, indent(tasks))
	path := filepath.Join(dir, "squad.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sessionDir := filepath.Join(dir, ".agent-debug-squad", "sessions")
	return path, sessionDir, port
}

func indent(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}

// startRun launches the run subprocess and streams its stderr to find the
// one-shot announcement.
func startRun(t *testing.T, bin, cfgPath, requestID string) *subprocess {
	t.Helper()
	cmd := exec.Command(bin, "run", "--config", cfgPath, "--request-id", requestID)
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout bytes.Buffer
	var stderrLog bytes.Buffer
	cmd.Stdout = &stdout
	stderrPath := filepath.Join(t.TempDir(), "child-stderr.log")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("stderr file: %v", err)
	}
	cmd.Stderr = stderrFile
	sp := &subprocess{
		childLog:   stderrPath,
		bin:        bin,
		cfgPath:    cfgPath,
		stdout:     &stdout,
		stderrLog:  &stderrLog,
		cmd:        cmd,
		controlURL: make(chan string, 1),
		execution:  make(chan string, 1),
		exited:     make(chan error, 1),
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start run: %v", err)
	}
	go func() { sp.exited <- cmd.Wait() }()
	// Tail the child's stderr file for the announcement and keep a copy for
	// failure reports; a file cannot lose buffered output at Wait time.
	go func() {
		url := ""
		id := ""
		for {
			data, _ := os.ReadFile(sp.childLog)
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "control URL: ") {
					url = strings.TrimPrefix(line, "control URL: ")
				}
				if strings.HasPrefix(line, "one-shot run: execution ") {
					rest := strings.TrimPrefix(line, "one-shot run: execution ")
					if idx := strings.Index(rest, " "); idx > 0 {
						id = rest[:idx]
					}
				}
			}
			if url != "" && id != "" {
				sp.stderrLog.Write(data)
				sp.controlURL <- url
				sp.execution <- id
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return sp
}

func waitAnnouncement(t *testing.T, sp *subprocess) (string, string) {
	t.Helper()
	select {
	case url := <-sp.controlURL:
		return url, <-sp.execution
	case <-time.After(30 * time.Second):
		t.Fatalf("run never announced its control URL; stderr:\n%s", sp.stderrLog.String())
		return "", ""
	}
}

func waitExit(t *testing.T, sp *subprocess, timeout time.Duration) int {
	t.Helper()
	select {
	case err := <-sp.exited:
		code := 0
		if err != nil {
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run exited unexpectedly: %v", err)
			}
			code = exitErr.ExitCode()
		}
		return code
	case <-time.After(timeout):
		_ = sp.cmd.Process.Kill()
		t.Fatalf("run did not exit within %s; stderr:\n%s", timeout, sp.stderrLog.String())
		return -1
	}
}

func decodeSummary(t *testing.T, raw string) map[string]any {
	t.Helper()
	var summary map[string]any
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("decode summary %q: %v", raw, err)
	}
	return summary
}

func summaryState(t *testing.T, raw string) string {
	t.Helper()
	summary := decodeSummary(t, raw)
	state, _ := summary["workflow_state"].(string)
	return state
}

func assertOwnershipReacquirable(t *testing.T, sessionDir string) {
	t.Helper()
	sessionPaths, err := filepath.Glob(filepath.Join(sessionDir, "*"))
	if err != nil || len(sessionPaths) == 0 {
		t.Fatalf("no session directories under %s: %v", sessionDir, err)
	}
	for _, dir := range sessionPaths {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		ownership, err := store.AcquireSessionOwnership(dir)
		if err != nil {
			t.Fatalf("session ownership was not released (%s): %v", dir, err)
		}
		_ = ownership.Release()
	}
}

func TestRunSubprocessSucceededAndReplay(t *testing.T) {
	bin := buildBinary(t)
	cfgPath, sessionDir, _ := workflowConfig(t, `
a:
  agent: alpha
  prompt: Do the work.
`)
	sp := startRun(t, bin, cfgPath, "req-sub")
	code := waitExit(t, sp, 60*time.Second)
	if code != 0 {
		time.Sleep(500 * time.Millisecond)
		t.Fatalf("exit = %d, want 0; stderr:\n%s\nstdout:\n%s", code, sp.stderrLog.String(), sp.stdout.String())
	}
	summary := decodeSummary(t, sp.stdout.String())
	if summary["workflow_state"] != "succeeded" || summary["exit_code"].(float64) != 0 {
		t.Fatalf("summary: %v", summary)
	}
	if summary["summary_persisted"] != true || summary["cleanup"].(map[string]any)["status"] != "complete" {
		t.Fatalf("summary teardown fields: %v", summary)
	}
	assertOwnershipReacquirable(t, sessionDir)

	// Replay exits 0 again and replaces only the derived summary.
	replay := startRun(t, bin, cfgPath, "req-sub")
	code = waitExit(t, replay, 60*time.Second)
	if code != 0 {
		t.Fatalf("replay exit = %d; stderr:\n%s", code, replay.stderrLog.String())
	}
	if summaryState(t, replay.stdout.String()) != "succeeded" {
		t.Fatalf("replay summary: %s", replay.stdout.String())
	}
}

func TestRunSubprocessFailedExitsOne(t *testing.T) {
	bin := buildBinary(t)
	cfgPath, _, _ := workflowConfig(t, `
a:
  agent: alpha
  prompt: Do the work.
  timeout_seconds: 1
`)
	// Slow the fake backend down via the config: 1s timeout must hit.
	data, _ := os.ReadFile(cfgPath)
	body := strings.Replace(string(data), `delay_ms: "40"`, `delay_ms: "3000"`, 1)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	sp := startRun(t, bin, cfgPath, "req-failed")
	code := waitExit(t, sp, 60*time.Second)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr:\n%s", code, sp.stderrLog.String())
	}
	if summaryState(t, sp.stdout.String()) != "failed" {
		t.Fatalf("summary: %s", sp.stdout.String())
	}
}

func TestRunSubprocessAPICancelExitsThree(t *testing.T) {
	bin := buildBinary(t)
	cfgPath, sessionDir, _ := workflowConfig(t, `
a:
  agent: alpha
  prompt: Do the work.
`)
	data, _ := os.ReadFile(cfgPath)
	body := strings.Replace(string(data), `delay_ms: "40"`, `delay_ms: "8000"`, 1)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	sp := startRun(t, bin, cfgPath, "req-cancel")
	url, execID := waitAnnouncement(t, sp)

	// A foreign mutation must be rejected while the run is live.
	resp, err := http.Post(url+"/workflows", "application/json", strings.NewReader(`{"request_id":"req-foreign"}`))
	if err != nil {
		t.Fatalf("foreign submission: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("foreign submission status = %d, want 409", resp.StatusCode)
	}

	// Cancelling the selected execution finishes the process with 3.
	resp, err = http.Post(url+"/workflows/"+execID+"/cancel", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d", resp.StatusCode)
	}
	code := waitExit(t, sp, 60*time.Second)
	if code != 3 {
		if data, err := os.ReadFile(sp.childLog); err == nil {
			t.Logf("child stderr file:\n%s", string(data))
		}
		t.Fatalf("exit = %d, want 3; stderr:\n%s", code, sp.stderrLog.String())
	}
	if summaryState(t, sp.stdout.String()) != "cancelled" {
		t.Fatalf("summary: %s", sp.stdout.String())
	}
	assertOwnershipReacquirable(t, sessionDir)
}

func TestRunSubprocessSignalExits130(t *testing.T) {
	bin := buildBinary(t)
	cfgPath, _, _ := workflowConfig(t, `
a:
  agent: alpha
  prompt: Do the work.
`)
	data, _ := os.ReadFile(cfgPath)
	body := strings.Replace(string(data), `delay_ms: "40"`, `delay_ms: "8000"`, 1)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	sp := startRun(t, bin, cfgPath, "req-signal")
	waitAnnouncement(t, sp)
	if err := sp.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal: %v", err)
	}
	code := waitExit(t, sp, 60*time.Second)
	if code != 130 {
		time.Sleep(500 * time.Millisecond)
		t.Fatalf("exit = %d, want 130; stderr:\n%s\nstdout:\n%s", code, sp.stderrLog.String(), sp.stdout.String())
	}
	summary := decodeSummary(t, sp.stdout.String())
	if summary["workflow_state"] != "cancelled" {
		t.Fatalf("workflow state: %v", summary["workflow_state"])
	}
	if signal, ok := summary["triggering_signal"].(string); !ok || signal != "SIGINT" {
		t.Fatalf("triggering signal: %v", summary["triggering_signal"])
	}
}
