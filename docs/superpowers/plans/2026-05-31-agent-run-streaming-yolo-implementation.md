# Agent Run Streaming And YOLO Defaults Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream agent stdout/stderr to run artifacts and server logs while making Codex/Kimi YOLO mode the default with per-agent opt-out.

**Architecture:** Add config-level defaults and a computed per-agent YOLO value in the domain layer. Add a run-scoped sink owned by the orchestrator/store boundary, pass it into adapters, and let CLI adapters tee stdout/stderr lines while still accumulating buffers for existing parsers. Keep OpenCode request/response behavior unchanged except for an explicit warning when YOLO is requested but unsupported.

**Tech Stack:** Go 1.22, stdlib `os/exec`, `bufio.Scanner`, file-backed store, existing REST/orchestrator architecture.

---

## File Structure

- Modify `internal/domain/types.go`: add `SessionDefaults`, `Yolo` fields, `RunSink` interface, and `DiscardRunSink`.
- Modify `internal/config/config.go`: parse top-level `defaults.yolo` and boolean/string `options.yolo`.
- Modify `internal/config/config_test.go`: cover default YOLO and agent override behavior.
- Modify `internal/store/store.go`: add append-only artifact helpers for `.events.jsonl` and `.stderr.log`.
- Modify `internal/store/store_test.go`: cover append helpers.
- Create `internal/orchestrator/run_sink.go`: implement file+log sink and sink error handling.
- Create `internal/orchestrator/run_sink_test.go`: cover file writes, logs, and write errors.
- Modify `internal/adapters/adapter.go`: extend `Send` signature with `domain.RunSink`.
- Modify `internal/orchestrator/orchestrator.go`: pass sink into adapters, check sink errors, preserve existing final output behavior.
- Modify `internal/adapters/fake/fake.go`: accept the new sink parameter.
- Modify `internal/adapters/codex/codex.go`: stream stdout/stderr and add YOLO flag.
- Modify `internal/adapters/codex/codex_test.go`: cover streaming and YOLO args.
- Modify `internal/adapters/kimi/kimi.go`: stream stdout/stderr and add YOLO flag.
- Modify `internal/adapters/kimi/kimi_test.go`: cover streaming and YOLO args.
- Modify `internal/adapters/opencode/opencode.go`: log unsupported YOLO warning.
- Modify `internal/adapters/opencode/opencode_test.go`: cover warning path if practical.
- Modify `configs/code-review-squad.yaml`: add `defaults.yolo: true`.
- Modify `docs/code-review-squad.md`: document streaming artifacts and YOLO default.
- Modify `README.md`: mention streaming artifact files and YOLO defaults.

---

### Task 1: Domain And Config YOLO Defaults

**Files:**
- Modify: `internal/domain/types.go`
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Add these tests to `internal/config/config_test.go`:

```go
func TestLoadDefaultsYoloDefaultsToTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
workspace_dir: ` + dir + `
agents:
  - name: Reviewer
    backend: fake
    startup_prompt: Review.
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Defaults.Yolo {
		t.Fatalf("Defaults.Yolo = false, want true")
	}
	if cfg.Agents[0].Yolo != nil {
		t.Fatalf("agent Yolo = %v, want nil inherited default", cfg.Agents[0].Yolo)
	}
	if !cfg.AgentYolo(cfg.Agents[0]) {
		t.Fatalf("AgentYolo(defaulted agent) = false, want true")
	}
}

func TestLoadParsesDefaultsYoloAndAgentOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
workspace_dir: ` + dir + `
defaults:
  yolo: false
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review.
    options:
      yolo: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.Yolo {
		t.Fatalf("Defaults.Yolo = true, want false")
	}
	if cfg.Agents[0].Yolo == nil || !*cfg.Agents[0].Yolo {
		t.Fatalf("agent Yolo = %v, want true override", cfg.Agents[0].Yolo)
	}
	if cfg.Agents[0].StringOptions["yolo"] != "true" {
		t.Fatalf("StringOptions[yolo] = %q, want true", cfg.Agents[0].StringOptions["yolo"])
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```sh
go test ./internal/config -run 'TestLoad(Default|ParsesDefaultsYolo)' -count=1
```

Expected: compile failure because `SessionConfig.Defaults` and `AgentSpec.Yolo` do not exist.

- [ ] **Step 3: Add domain fields and helper**

In `internal/domain/types.go`, add:

```go
type SessionDefaults struct {
	Yolo bool `json:"yolo"`
}
```

Update `AgentSpec`:

```go
type AgentSpec struct {
	Name          string              `json:"name"`
	Backend       string              `json:"backend"`
	StartupPrompt string              `json:"startup_prompt"`
	Options       map[string]any      `json:"options,omitempty"`
	Yolo          *bool               `json:"yolo,omitempty"`
	StringOptions map[string]string   `json:"-"`
	ListOptions   map[string][]string `json:"-"`
}
```

Update `SessionConfig`:

```go
type SessionConfig struct {
	SessionName  string          `json:"session_name"`
	SessionID    string          `json:"session_id"`
	WorkspaceDir string          `json:"workspace_dir"`
	StateDirName string          `json:"state_dir_name"`
	Host         string          `json:"host"`
	Port         int             `json:"port"`
	Defaults     SessionDefaults `json:"defaults"`
	Agents       []AgentSpec     `json:"agents"`
}
```

Add helper:

```go
func (cfg SessionConfig) AgentYolo(spec AgentSpec) bool {
	if spec.Yolo != nil {
		return *spec.Yolo
	}
	return cfg.Defaults.Yolo
}
```

- [ ] **Step 4: Parse defaults and yolo options**

In `internal/config/config.go`, update `rawConfig`:

```go
type rawConfig struct {
	SessionName  string      `yaml:"session_name"`
	WorkspaceDir string      `yaml:"workspace_dir"`
	StateDirName string      `yaml:"state_dir_name"`
	Host         string      `yaml:"host"`
	Port         int         `yaml:"port"`
	Defaults     rawDefaults `yaml:"defaults"`
	Agents       []rawAgent  `yaml:"agents"`
}

type rawDefaults struct {
	Yolo *bool `yaml:"yolo"`
}
```

Before returning config:

```go
defaults := domain.SessionDefaults{Yolo: true}
	if raw.Defaults.Yolo != nil {
		defaults.Yolo = *raw.Defaults.Yolo
	}
```

Inside option parsing switch, add a bool case:

```go
case bool:
	if key != "yolo" {
		return domain.SessionConfig{}, fmt.Errorf("agent %q option %q has unsupported value type %T; expected string, bool yolo, or list of strings", name, key, value)
	}
	spec.Yolo = &typed
	spec.StringOptions[key] = strconv.FormatBool(typed)
```

Add `strconv` import.

Set `Defaults: defaults` in the returned `domain.SessionConfig`.

- [ ] **Step 5: Run targeted config tests**

Run:

```sh
go test ./internal/config -run 'TestLoad(Default|ParsesDefaultsYolo)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run full config tests**

Run:

```sh
go test ./internal/config -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/domain/types.go internal/config/config.go internal/config/config_test.go
git commit -m "Add yolo defaults to config"
```

---

### Task 2: Run Sink Storage And Logging

**Files:**
- Modify: `internal/domain/types.go`
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`
- Create: `internal/orchestrator/run_sink.go`
- Test: `internal/orchestrator/run_sink_test.go`

- [ ] **Step 1: Add sink interface to domain**

In `internal/domain/types.go`, add:

```go
type RunSink interface {
	StdoutLine(line string)
	StderrLine(line string)
	Err() error
}

type discardRunSink struct{}

func (discardRunSink) StdoutLine(string) {}
func (discardRunSink) StderrLine(string) {}
func (discardRunSink) Err() error        { return nil }

func DiscardRunSink() RunSink {
	return discardRunSink{}
}
```

- [ ] **Step 2: Write failing store append tests**

Add to `internal/store/store_test.go`:

```go
func TestStoreAppendsRunEventsAndStderr(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
	}
	s := New(cfg)
	run := domain.RunRecord{RunID: "run_000001", Agent: "Reviewer"}

	eventsPath, err := s.AppendRunEvents(run, "line one")
	if err != nil {
		t.Fatalf("AppendRunEvents(first) error = %v", err)
	}
	if _, err := s.AppendRunEvents(run, "line two"); err != nil {
		t.Fatalf("AppendRunEvents(second) error = %v", err)
	}
	stderrPath, err := s.AppendRunStderr(run, "err one")
	if err != nil {
		t.Fatalf("AppendRunStderr(error) = %v", err)
	}

	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("ReadFile(eventsPath) error = %v", err)
	}
	if string(events) != "line one\nline two\n" {
		t.Fatalf("events = %q", string(events))
	}
	stderr, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("ReadFile(stderrPath) error = %v", err)
	}
	if string(stderr) != "err one\n" {
		t.Fatalf("stderr = %q", string(stderr))
	}
}
```

- [ ] **Step 3: Run store test and verify failure**

Run:

```sh
go test ./internal/store -run TestStoreAppendsRunEventsAndStderr -count=1
```

Expected: compile failure because append methods do not exist.

- [ ] **Step 4: Implement append helpers**

Add to `internal/store/store.go`:

```go
func (s *Store) AppendRunEvents(run domain.RunRecord, line string) (string, error) {
	return s.appendRunArtifactLine(run, ".events.jsonl", line)
}

func (s *Store) AppendRunStderr(run domain.RunRecord, line string) (string, error) {
	return s.appendRunArtifactLine(run, ".stderr.log", line)
}

func (s *Store) appendRunArtifactLine(run domain.RunRecord, suffix string, line string) (string, error) {
	path, err := s.runArtifactPath(run.RunID, run.Agent, suffix)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		return "", err
	}
	return path, file.Sync()
}
```

- [ ] **Step 5: Write failing run sink test**

Create `internal/orchestrator/run_sink_test.go`:

```go
package orchestrator

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/store"
)

func TestRunSinkWritesArtifactsAndLogs(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
	st := store.New(cfg)
	run := domain.RunRecord{RunID: "run_000001", Agent: "Reviewer"}

	var logs bytes.Buffer
	sink := newRunSink(st, run, log.New(&logs, "", 0))
	sink.StdoutLine(`{"type":"turn.started"}`)
	sink.StderrLine("warning")

	if err := sink.Err(); err != nil {
		t.Fatalf("sink.Err() = %v", err)
	}

	eventsPath := filepath.Join(root, ".agent-debug-squad", "sessions", "session_test", "runs", "run_000001", "Reviewer.events.jsonl")
	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("ReadFile(events) error = %v", err)
	}
	if string(events) != "{\"type\":\"turn.started\"}\n" {
		t.Fatalf("events = %q", string(events))
	}
	if !strings.Contains(logs.String(), `run=run_000001 agent=Reviewer stream=stdout {"type":"turn.started"}`) {
		t.Fatalf("logs = %q, want stdout line", logs.String())
	}
}
```

- [ ] **Step 6: Run run sink test and verify failure**

Run:

```sh
go test ./internal/orchestrator -run TestRunSinkWritesArtifactsAndLogs -count=1
```

Expected: compile failure because `newRunSink` does not exist.

- [ ] **Step 7: Implement run sink**

Create `internal/orchestrator/run_sink.go`:

```go
package orchestrator

import (
	"fmt"
	"log"
	"sync"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/store"
)

type runSink struct {
	store  *store.Store
	run    domain.RunRecord
	logger *log.Logger
	mu     sync.Mutex
	err    error
}

func newRunSink(st *store.Store, run domain.RunRecord, logger *log.Logger) *runSink {
	if logger == nil {
		logger = log.Default()
	}
	return &runSink{store: st, run: run, logger: logger}
}

func (s *runSink) StdoutLine(line string) {
	s.write("stdout", line)
}

func (s *runSink) StderrLine(line string) {
	s.write("stderr", line)
}

func (s *runSink) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *runSink) write(stream string, line string) {
	s.logger.Printf("run=%s agent=%s stream=%s %s", s.run.RunID, s.run.Agent, stream, line)
	var err error
	if stream == "stderr" {
		_, err = s.store.AppendRunStderr(s.run, line)
	} else {
		_, err = s.store.AppendRunEvents(s.run, line)
	}
	if err != nil {
		s.mu.Lock()
		if s.err == nil {
			s.err = fmt.Errorf("write %s stream: %w", stream, err)
		}
		s.mu.Unlock()
	}
}
```

- [ ] **Step 8: Run sink/store tests**

Run:

```sh
go test ./internal/store ./internal/orchestrator -run 'Test(StoreAppendsRunEventsAndStderr|RunSinkWritesArtifactsAndLogs)' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```sh
git add internal/domain/types.go internal/store/store.go internal/store/store_test.go internal/orchestrator/run_sink.go internal/orchestrator/run_sink_test.go
git commit -m "Add run streaming sink"
```

---

### Task 3: Adapter Boundary And Orchestrator Wiring

**Files:**
- Modify: `internal/adapters/adapter.go`
- Modify: `internal/adapters/fake/fake.go`
- Modify: `internal/orchestrator/orchestrator.go`
- Test: `internal/orchestrator/orchestrator_test.go`

- [ ] **Step 1: Update adapter interface**

Change `internal/adapters/adapter.go`:

```go
type AgentAdapter interface {
	Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
	Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error)
	Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error)
}
```

- [ ] **Step 2: Update fake adapter**

Change `internal/adapters/fake/fake.go`:

```go
func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error) {
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	sink.StdoutLine(fmt.Sprintf("%s received run %s", state.Name, run.RunID))
	select {
	case <-ctx.Done():
		return domain.RunResult{}, state, ctx.Err()
	case <-time.After(50 * time.Millisecond):
	}
	state.LastRunID = run.RunID
	state.Status = domain.AgentIdle
	return domain.RunResult{
		FinalMessage: fmt.Sprintf("%s received: %s", state.Name, run.Message),
	}, state, nil
}
```

- [ ] **Step 3: Wire sink into orchestrator**

In `internal/orchestrator/orchestrator.go`, before adapter send:

```go
sink := newRunSink(o.store, run, log.Default())
result, newState, sendErr := adapter.Send(ctx, state, domain.RunRequest{
	RunID:    run.RunID,
	Agent:    run.Agent,
	Message:  run.Message,
	Metadata: cloneMetadata(run.Metadata),
}, sink)
if sinkErr := sink.Err(); sinkErr != nil && sendErr == nil {
	sendErr = sinkErr
}
```

Add `log` import.

- [ ] **Step 4: Update remaining adapter method signatures temporarily**

Update Codex, Kimi, and OpenCode signatures to accept the sink and discard it until their tasks:

```go
func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error) {
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	// existing implementation
}
```

- [ ] **Step 5: Add orchestrator artifact test**

Add to `internal/orchestrator/orchestrator_test.go`:

```go
func TestRunWorkerWritesStreamingEvents(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "Reviewer")
	s := store.New(cfg)
	o, err := New(ctx, cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	run, err := o.SubmitRun(ctx, "Reviewer", "stream please", nil)
	if err != nil {
		t.Fatalf("SubmitRun() error = %v", err)
	}
	if _, err := o.Wait(ctx, run.RunID, time.Second); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	path := filepath.Join(cfg.WorkspaceDir, cfg.StateDirName, "sessions", cfg.SessionID, "runs", run.RunID, run.Agent+".events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(events) error = %v", err)
	}
	if !strings.Contains(string(data), "Reviewer received run run_000001") {
		t.Fatalf("events = %q, want fake stream line", string(data))
	}
}
```

- [ ] **Step 6: Run tests**

Run:

```sh
go test ./internal/adapters ./internal/adapters/fake ./internal/orchestrator -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/adapters/adapter.go internal/adapters/fake/fake.go internal/adapters/codex/codex.go internal/adapters/kimi/kimi.go internal/adapters/opencode/opencode.go internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "Wire run sink into adapters"
```

---

### Task 4: Codex Streaming And YOLO

**Files:**
- Modify: `internal/adapters/codex/codex.go`
- Test: `internal/adapters/codex/codex_test.go`

- [ ] **Step 1: Add Codex arg builder tests**

Add to `internal/adapters/codex/codex_test.go`:

```go
func TestBuildArgsAddsYoloFlagByDefault(t *testing.T) {
	spec := domain.AgentSpec{Name: "Reviewer", Backend: "codex"}
	state := domain.AgentState{}
	args := buildArgs(spec, state, "hello", true)
	if !containsString(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("args = %#v, want yolo flag", args)
	}
}

func TestBuildArgsOmitsYoloFlagWhenDisabled(t *testing.T) {
	spec := domain.AgentSpec{Name: "Reviewer", Backend: "codex"}
	disabled := false
	spec.Yolo = &disabled
	state := domain.AgentState{}
	args := buildArgs(spec, state, "hello", false)
	if containsString(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("args = %#v, want no yolo flag", args)
	}
}
```

Add helper if missing:

```go
func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Add streaming command test**

Add:

```go
func TestSendStreamsStdoutAndStderr(t *testing.T) {
	script, promptPath := codexCommandScript(t, `{"type":"item.completed","item":{"type":"message","role":"assistant","text":"done"}}
{"type":"turn.completed"}`)
	sink := &recordingSink{}
	spec := domain.AgentSpec{Name: "Reviewer", Backend: "codex", StartupPrompt: "Review.", StringOptions: map[string]string{"command": script}}
	state := domain.AgentState{Name: "Reviewer", WorkspaceDir: t.TempDir()}

	result, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Reviewer", Message: "hello"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "done" {
		t.Fatalf("FinalMessage = %q, want done", result.FinalMessage)
	}
	if len(sink.stdout) == 0 {
		t.Fatalf("stdout sink empty")
	}
	if gotPrompt := readTextFile(t, promptPath); !strings.Contains(gotPrompt, "hello") {
		t.Fatalf("prompt = %q, want hello", gotPrompt)
	}
}

type recordingSink struct {
	stdout []string
	stderr []string
}

func (s *recordingSink) StdoutLine(line string) { s.stdout = append(s.stdout, line) }
func (s *recordingSink) StderrLine(line string) { s.stderr = append(s.stderr, line) }
func (s *recordingSink) Err() error             { return nil }
```

- [ ] **Step 3: Run Codex tests and verify failures**

Run:

```sh
go test ./internal/adapters/codex -run 'Test(BuildArgs|SendStreams)' -count=1
```

Expected: compile failure for `buildArgs` and old send behavior until implemented.

- [ ] **Step 4: Implement arg builder**

In `internal/adapters/codex/codex.go`, extract arg construction:

```go
func buildArgs(spec domain.AgentSpec, state domain.AgentState, message string, yolo bool) []string {
	args := []string{"exec", "--json"}
	if yolo {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if state.BackendSessionID != "" {
		args = append(args, "resume", state.BackendSessionID)
	}
	args = append(args, message)
	return args
}
```

In `Send`, replace inline args construction with:

```go
message := run.Message
if state.LastRunID == "" {
	message = promptfmt.WithStartupPrompt(a.startupPrompt(state), run.Message)
}
yolo := true
if a.spec.Yolo != nil {
	yolo = *a.spec.Yolo
}
args := buildArgs(a.spec, state, message, yolo)
```

- [ ] **Step 5: Implement streaming process helper**

Add helper in `codex.go`:

```go
func runCommandStreaming(ctx context.Context, cmd *exec.Cmd, sink domain.RunSink) ([]byte, []byte, error) {
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var wg sync.WaitGroup
	var scanErr error
	var mu sync.Mutex
	scan := func(r io.Reader, stream string) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLEventSize)
		for scanner.Scan() {
			line := scanner.Text()
			if stream == "stderr" {
				stderr.WriteString(line + "\n")
				sink.StderrLine(line)
			} else {
				stdout.WriteString(line + "\n")
				sink.StdoutLine(line)
			}
		}
		if err := scanner.Err(); err != nil {
			mu.Lock()
			if scanErr == nil {
				scanErr = err
			}
			mu.Unlock()
		}
	}
	wg.Add(2)
	go scan(stdoutPipe, "stdout")
	go scan(stderrPipe, "stderr")
	waitErr := cmd.Wait()
	wg.Wait()
	if scanErr != nil {
		return stdout.Bytes(), stderr.Bytes(), scanErr
	}
	return stdout.Bytes(), stderr.Bytes(), waitErr
}
```

Add imports `io` and `sync`.

Use it in `Send`:

```go
stdout, stderr, err := runCommandStreaming(ctx, cmd, sink)
```

- [ ] **Step 6: Run Codex tests**

Run:

```sh
go test ./internal/adapters/codex -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/adapters/codex/codex.go internal/adapters/codex/codex_test.go
git commit -m "Stream codex run output"
```

---

### Task 5: Kimi Streaming And YOLO

**Files:**
- Modify: `internal/adapters/kimi/kimi.go`
- Test: `internal/adapters/kimi/kimi_test.go`

- [ ] **Step 1: Add Kimi arg builder tests**

Add to `internal/adapters/kimi/kimi_test.go`:

```go
func TestBuildArgsAddsYoloByDefault(t *testing.T) {
	args := buildArgs("hello", true)
	if !containsString(args, "--yolo") {
		t.Fatalf("args = %#v, want --yolo", args)
	}
}

func TestBuildArgsOmitsYoloWhenDisabled(t *testing.T) {
	args := buildArgs("hello", false)
	if containsString(args, "--yolo") {
		t.Fatalf("args = %#v, want no --yolo", args)
	}
}
```

- [ ] **Step 2: Add Kimi streaming test**

Add:

```go
func TestSendStreamsStdoutAndStderr(t *testing.T) {
	script, _ := kimiCommandScript(t, `{"type":"assistant","message":{"content":"Final answer"}}
`)
	sink := &recordingSink{}
	spec := domain.AgentSpec{Name: "Advocat", Backend: "kimi", StartupPrompt: "Defend.", StringOptions: map[string]string{"command": script}}
	state := domain.AgentState{Name: "Advocat", WorkspaceDir: t.TempDir()}

	result, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Advocat", Message: "hello"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "Final answer" {
		t.Fatalf("FinalMessage = %q, want Final answer", result.FinalMessage)
	}
	if len(sink.stdout) == 0 {
		t.Fatalf("stdout sink empty")
	}
}
```

Reuse the same `recordingSink` shape as Codex if it is not already present in the Kimi test file.

- [ ] **Step 3: Run Kimi tests and verify failure**

Run:

```sh
go test ./internal/adapters/kimi -run 'Test(BuildArgs|SendStreams)' -count=1
```

Expected: compile failure for `buildArgs` and old send signature until implemented.

- [ ] **Step 4: Implement Kimi arg builder**

In `internal/adapters/kimi/kimi.go`, add:

```go
func buildArgs(prompt string, yolo bool) []string {
	args := []string{"-p", prompt, "--output-format", "stream-json"}
	if yolo {
		args = append(args, "--yolo")
	}
	return args
}
```

In `Send`:

```go
yolo := true
if a.spec.Yolo != nil {
	yolo = *a.spec.Yolo
}
args := buildArgs(promptfmt.WithStartupPrompt(a.startupPrompt(state), run.Message), yolo)
cmd := exec.CommandContext(ctx, command, args...)
```

- [ ] **Step 5: Add streaming helper**

Implement the same `runCommandStreaming` helper as Codex, using `maxStreamJSONEventSize` for scanner buffer.

Use:

```go
stdout, stderr, err := runCommandStreaming(ctx, cmd, sink)
```

Then reuse existing `buildRunResult(stdout, stderr)`.

- [ ] **Step 6: Run Kimi tests**

Run:

```sh
go test ./internal/adapters/kimi -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/adapters/kimi/kimi.go internal/adapters/kimi/kimi_test.go
git commit -m "Stream kimi run output"
```

---

### Task 6: OpenCode YOLO Warning

**Files:**
- Modify: `internal/adapters/opencode/opencode.go`
- Test: `internal/adapters/opencode/opencode_test.go`

- [ ] **Step 1: Add warning test**

Add this test using the package-level `logger` variable introduced in Step 3:

```go
func TestSendLogsUnsupportedYoloWarning(t *testing.T) {
	var logs bytes.Buffer
	previous := logger
	logger = log.New(&logs, "", 0)
	t.Cleanup(func() { logger = previous })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"parts": []map[string]any{{"type": "text", "text": "ok"}},
		})
	}))
	defer server.Close()

	enabled := true
	spec := domain.AgentSpec{
		Name:          "Critic",
		Backend:       "opencode",
		StartupPrompt: "Review.",
		Yolo:          &enabled,
		StringOptions: map[string]string{"base_url": server.URL},
	}
	state := domain.AgentState{Name: "Critic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir(), LastRunID: "run_previous"}

	_, _, err := New(spec).Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Critic", Message: "hello"}, domain.DiscardRunSink())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "backend=opencode yolo=true unsupported") {
		t.Fatalf("logs = %q, want unsupported yolo warning", logs.String())
	}
}
```

- [ ] **Step 2: Run test and verify failure**

Run:

```sh
go test ./internal/adapters/opencode -run TestSendLogsUnsupportedYoloWarning -count=1
```

Expected: compile failure or assertion failure until logger/warning exists.

- [ ] **Step 3: Implement warning**

In `internal/adapters/opencode/opencode.go`, add:

```go
var logger = log.Default()
```

Add `log` import.

At the top of `Send`, after backend session validation:

```go
if a.spec.Yolo != nil && *a.spec.Yolo {
	logger.Printf("agent=%s backend=opencode yolo=true unsupported by opencode HTTP adapter", state.Name)
}
```

- [ ] **Step 4: Run OpenCode tests**

Run:

```sh
go test ./internal/adapters/opencode -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/adapters/opencode/opencode.go internal/adapters/opencode/opencode_test.go
git commit -m "Warn on unsupported opencode yolo"
```

---

### Task 7: Project Config And Documentation

**Files:**
- Modify: `configs/code-review-squad.yaml`
- Modify: `docs/code-review-squad.md`
- Modify: `README.md`

- [ ] **Step 1: Update project YAML**

Add near the top of `configs/code-review-squad.yaml`:

```yaml
defaults:
  yolo: true
```

Do not add `options.yolo: true` to every agent because the default covers it. Add explicit `options.yolo: false` only for agents that must not run in YOLO mode.

- [ ] **Step 2: Update docs**

In `docs/code-review-squad.md`, add:

````markdown
During a run, intermediate CLI output is written as it arrives:

```text
.agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.events.jsonl
.agent-review-artifacts/sessions/<session_id>/runs/<run_id>/<agent>.stderr.log
```

The same lines are also emitted to the `agent-debug-squad` server log with `run`, `agent`, and `stream` fields.

YOLO mode is enabled by default through `defaults.yolo: true`. Codex uses `--dangerously-bypass-approvals-and-sandbox`; Kimi uses `--yolo`. Set `options.yolo: false` on an agent to opt out.
````

In `README.md`, add the same shorter note under output layout.

- [ ] **Step 3: Run config and markdown smoke checks**

Run:

```sh
go test ./internal/config -count=1
git diff --check
```

Expected: PASS and no whitespace errors.

- [ ] **Step 4: Commit**

```sh
git add configs/code-review-squad.yaml docs/code-review-squad.md README.md
git commit -m "Document streaming artifacts and yolo defaults"
```

---

### Task 8: Full Verification

**Files:**
- No source changes unless verification exposes a defect.

- [ ] **Step 1: Run full test suite**

Run:

```sh
go test ./...
```

Expected: all packages PASS.

- [ ] **Step 2: Build/install smoke test**

Run:

```sh
go install ./cmd/agent-debug-squad
agent-debug-squad
```

Expected:

```text
missing command
Usage:
  agent-debug-squad serve --config squad.yaml
```

- [ ] **Step 3: Run fake-backend runtime smoke**

Run:

```sh
agent-debug-squad serve --config examples/squad.yaml
```

In another shell:

```sh
curl -sS -X POST 'http://127.0.0.1:8080/agents/Reviewer/runs?wait=true&timeout_seconds=5' \
  -H 'Content-Type: application/json' \
  -d '{"message":"stream smoke"}'
```

Expected:

- run completes
- `.events.jsonl` exists for the fake run
- server log contains `stream=stdout`

- [ ] **Step 4: Final status**

Run:

```sh
git status --short --branch
git log --oneline -8
```

Expected: clean working tree, with the task commits present.
