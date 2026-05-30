# Agent Debug Squad Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the v1 Go service that starts one YAML-defined debugging squad session, exposes REST endpoints, persists run artifacts, and executes agent turns through backend adapters.

**Architecture:** Implement a single Go binary with a narrow adapter boundary. The orchestrator owns session state, run lifecycle, per-agent locking, and filesystem persistence; adapters hide backend-specific CLI/API behavior. Storage is JSON/JSONL/text under `<workspace_dir>/.agent-debug-squad/sessions/<session_id>/`.

**Tech Stack:** Go 1.22+, standard `net/http`, `os/exec`, `encoding/json`, `gopkg.in/yaml.v3`, table-driven Go tests, no database.

---

## File Structure

- Create `go.mod`: Go module and YAML dependency.
- Create `cmd/agent-debug-squad/main.go`: CLI entrypoint for `serve --config`.
- Create `internal/config/config.go`: YAML schema, validation, defaults, normalized config.
- Create `internal/config/config_test.go`: config parser tests.
- Create `internal/domain/types.go`: shared domain types for agents, runs, statuses, adapters, and transcript events.
- Create `internal/store/store.go`: file-backed state store.
- Create `internal/store/store_test.go`: state store persistence and recovery tests.
- Create `internal/adapters/adapter.go`: adapter factory and shared adapter helpers.
- Create `internal/adapters/fake/fake.go`: deterministic fake adapter used by tests and local smoke config.
- Create `internal/adapters/codex/codex.go`: Codex command builder, environment whitelist, JSONL parser, and adapter.
- Create `internal/adapters/codex/codex_test.go`: Codex env and JSONL parser tests.
- Create `internal/adapters/kimi/kimi.go`: Kimi command builder, stream-json parser, and adapter.
- Create `internal/adapters/kimi/kimi_test.go`: Kimi parser tests.
- Create `internal/adapters/opencode/opencode.go`: OpenCode HTTP adapter using an existing `opencode serve` base URL.
- Create `internal/orchestrator/orchestrator.go`: session initialization, run submission, worker lifecycle, locking, recovery.
- Create `internal/orchestrator/orchestrator_test.go`: lifecycle, concurrency, failure, recovery tests.
- Create `internal/api/server.go`: HTTP handlers and JSON responses.
- Create `internal/api/server_test.go`: REST contract tests with fake adapters.
- Create `examples/squad.yaml`: runnable fake-adapter config for local smoke tests.
- Create `README.md`: usage, API examples, storage layout.

## Task 1: Module, Domain Types, And Config Parser

**Files:**
- Create: `go.mod`
- Create: `internal/domain/types.go`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Create the Go module**

Create `go.mod`:

```go
module github.com/andrey/agent-debug-squad

go 1.22

require gopkg.in/yaml.v3 v3.0.1
```

- [ ] **Step 2: Define shared domain types**

Create `internal/domain/types.go`:

```go
package domain

import "time"

type AgentStatus string

const (
	AgentIdle    AgentStatus = "idle"
	AgentRunning AgentStatus = "running"
	AgentFailed  AgentStatus = "failed"
)

type RunStatus string

const (
	RunQueued      RunStatus = "queued"
	RunRunning     RunStatus = "running"
	RunCompleted   RunStatus = "completed"
	RunFailed      RunStatus = "failed"
	RunInterrupted RunStatus = "interrupted"
)

type AgentSpec struct {
	Name          string            `json:"name"`
	Backend       string            `json:"backend"`
	StartupPrompt string            `json:"startup_prompt"`
	Options       map[string]any    `json:"options,omitempty"`
	StringOptions map[string]string `json:"-"`
	ListOptions   map[string][]string `json:"-"`
}

type SessionConfig struct {
	SessionName  string      `json:"session_name"`
	SessionID    string      `json:"session_id"`
	WorkspaceDir string      `json:"workspace_dir"`
	StateDirName string      `json:"state_dir_name"`
	Host         string      `json:"host"`
	Port         int         `json:"port"`
	Agents       []AgentSpec `json:"agents"`
}

type AgentState struct {
	Name             string      `json:"name"`
	Backend          string      `json:"backend"`
	StartupPrompt    string      `json:"startup_prompt"`
	WorkspaceDir      string      `json:"workspace_dir"`
	BackendSessionID  string      `json:"backend_session_id"`
	Status           AgentStatus `json:"status"`
	CreatedAt        time.Time   `json:"created_at"`
	LastRunID         string      `json:"last_run_id"`
	LastError         *string     `json:"last_error"`
}

type RunRecord struct {
	RunID       string            `json:"run_id"`
	Agent       string            `json:"agent"`
	Status      RunStatus         `json:"status"`
	Message     string            `json:"message,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
	OutputPath  *string           `json:"output_path"`
	Error       *string           `json:"error"`
}

type RunRequest struct {
	RunID    string            `json:"run_id"`
	Agent    string            `json:"agent"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type RunResult struct {
	FinalMessage string
	Stderr       string
	RawEvents    []string
	ErrorMessage  string
}

type TranscriptEvent struct {
	Type       string            `json:"type"`
	RunID      string            `json:"run_id"`
	Agent      string            `json:"agent,omitempty"`
	To         string            `json:"to,omitempty"`
	Text       string            `json:"text,omitempty"`
	OutputPath string            `json:"output_path,omitempty"`
	Status     RunStatus         `json:"status,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	At         time.Time         `json:"at"`
}
```

- [ ] **Step 3: Write failing config parser tests**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigAppliesDefaultsAndParsesOptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
session_name: debug-quorum
workspace_dir: ` + dir + `
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: Review carefully.
    options:
      command: codex
      model: openai/gpt-5.5
      inherit_env:
        - OPENAI_API_KEY
        - CODEX_HOME
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionName != "debug-quorum" {
		t.Fatalf("SessionName = %q", cfg.SessionName)
	}
	if cfg.StateDirName != ".agent-debug-squad" {
		t.Fatalf("StateDirName = %q", cfg.StateDirName)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("Host = %q", cfg.Host)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d", cfg.Port)
	}
	if cfg.SessionID == "" {
		t.Fatal("SessionID is empty")
	}
	got := cfg.Agents[0].StringOptions["command"]
	if got != "codex" {
		t.Fatalf("command option = %q", got)
	}
	env := cfg.Agents[0].ListOptions["inherit_env"]
	if len(env) != 2 || env[0] != "OPENAI_API_KEY" || env[1] != "CODEX_HOME" {
		t.Fatalf("inherit_env = %#v", env)
	}
}

func TestLoadConfigRejectsDuplicateAgentNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	yaml := `
workspace_dir: ` + dir + `
agents:
  - name: Reviewer
    backend: codex
    startup_prompt: One.
  - name: Reviewer
    backend: kimi
    startup_prompt: Two.
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected duplicate agent error")
	}
}
```

- [ ] **Step 4: Run tests and verify they fail**

Run:

```bash
go test ./internal/config
```

Expected: FAIL because `Load` is undefined.

- [ ] **Step 5: Implement config parser**

Create `internal/config/config.go`:

```go
package config

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"gopkg.in/yaml.v3"
)

type rawConfig struct {
	SessionName  string            `yaml:"session_name"`
	WorkspaceDir string            `yaml:"workspace_dir"`
	StateDirName string            `yaml:"state_dir_name"`
	Host         string            `yaml:"host"`
	Port         int               `yaml:"port"`
	Agents       []rawAgent        `yaml:"agents"`
}

type rawAgent struct {
	Name          string         `yaml:"name"`
	Backend       string         `yaml:"backend"`
	StartupPrompt string         `yaml:"startup_prompt"`
	Options       map[string]any `yaml:"options"`
}

func Load(path string) (domain.SessionConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.SessionConfig{}, err
	}
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return domain.SessionConfig{}, err
	}
	if raw.StateDirName == "" {
		raw.StateDirName = ".agent-debug-squad"
	}
	if raw.Host == "" {
		raw.Host = "127.0.0.1"
	}
	if raw.Port == 0 {
		raw.Port = 8080
	}
	if raw.WorkspaceDir == "" {
		return domain.SessionConfig{}, errors.New("workspace_dir is required")
	}
	workspace, err := filepath.Abs(raw.WorkspaceDir)
	if err != nil {
		return domain.SessionConfig{}, err
	}
	if len(raw.Agents) == 0 {
		return domain.SessionConfig{}, errors.New("at least one agent is required")
	}
	seen := map[string]bool{}
	agents := make([]domain.AgentSpec, 0, len(raw.Agents))
	for _, a := range raw.Agents {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return domain.SessionConfig{}, errors.New("agent name is required")
		}
		if seen[name] {
			return domain.SessionConfig{}, fmt.Errorf("duplicate agent name %q", name)
		}
		seen[name] = true
		if strings.TrimSpace(a.Backend) == "" {
			return domain.SessionConfig{}, fmt.Errorf("agent %q backend is required", name)
		}
		if strings.TrimSpace(a.StartupPrompt) == "" {
			return domain.SessionConfig{}, fmt.Errorf("agent %q startup_prompt is required", name)
		}
		spec := domain.AgentSpec{
			Name:          name,
			Backend:       strings.TrimSpace(a.Backend),
			StartupPrompt: a.StartupPrompt,
			Options:       a.Options,
			StringOptions: map[string]string{},
			ListOptions:   map[string][]string{},
		}
		for key, value := range a.Options {
			switch typed := value.(type) {
			case string:
				spec.StringOptions[key] = typed
			case []any:
				items := make([]string, 0, len(typed))
				for _, item := range typed {
					s, ok := item.(string)
					if !ok {
						return domain.SessionConfig{}, fmt.Errorf("agent %q option %q must contain only strings", name, key)
					}
					items = append(items, s)
				}
				spec.ListOptions[key] = items
			}
		}
		agents = append(agents, spec)
	}
	sessionName := raw.SessionName
	if sessionName == "" {
		sessionName = "default"
	}
	return domain.SessionConfig{
		SessionName:  sessionName,
		SessionID:    stableSessionID(sessionName, workspace),
		WorkspaceDir: workspace,
		StateDirName: raw.StateDirName,
		Host:         raw.Host,
		Port:         raw.Port,
		Agents:       agents,
	}, nil
}

func stableSessionID(name, workspace string) string {
	sum := sha1.Sum([]byte(name + "\x00" + workspace))
	return "session_" + hex.EncodeToString(sum[:])[:12]
}
```

- [ ] **Step 6: Run config tests**

Run:

```bash
go test ./internal/config
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod internal/domain/types.go internal/config/config.go internal/config/config_test.go
git commit -m "feat: add config parser and domain types"
```

## Task 2: File-Backed State Store

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write failing state store tests**

Create `internal/store/store_test.go`:

```go
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

func TestStoreWritesAgentRunOutputAndTranscript(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionName:  "debug",
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
	}
	s := New(cfg)

	state := domain.AgentState{
		Name:            "Reviewer",
		Backend:         "codex",
		StartupPrompt:   "Review.",
		WorkspaceDir:     root,
		BackendSessionID: "thread_1",
		Status:          domain.AgentIdle,
		CreatedAt:       time.Unix(10, 0).UTC(),
	}
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAgentState(state); err != nil {
		t.Fatal(err)
	}
	run := domain.RunRecord{
		RunID:     "run_000001",
		Agent:     "Reviewer",
		Status:    domain.RunRunning,
		Message:   "Check this.",
		CreatedAt: time.Unix(20, 0).UTC(),
	}
	if err := s.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	out, err := s.WriteAgentOutput(run, "Final answer")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTranscript(domain.TranscriptEvent{
		Type:       "agent_result",
		RunID:      run.RunID,
		Agent:      "Reviewer",
		OutputPath: out,
		Status:     domain.RunCompleted,
		At:         time.Unix(30, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(s.SessionDir(), "config.json")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !contains(string(data), "Final answer") {
		t.Fatalf("output file missing final answer: %q", string(data))
	}
	loaded, err := s.LoadAgentState("Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BackendSessionID != "thread_1" {
		t.Fatalf("BackendSessionID = %q", loaded.BackendSessionID)
	}
	events, err := s.ReadTranscript()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "agent_result" {
		t.Fatalf("events = %#v", events)
	}
}

func TestMarkInterruptedUpdatesActiveRuns(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad"}
	s := New(cfg)
	if err := s.SaveRun(domain.RunRecord{RunID: "run_1", Agent: "A", Status: domain.RunRunning, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRun(domain.RunRecord{RunID: "run_2", Agent: "B", Status: domain.RunQueued, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkActiveRunsInterrupted(); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListRuns()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(runs)
	for _, run := range runs {
		if run.Status != domain.RunInterrupted {
			t.Fatalf("expected all runs interrupted: %s", encoded)
		}
	}
}

func contains(s, part string) bool {
	return len(part) == 0 || (len(s) >= len(part) && (s == part || contains(s[1:], part) || s[:len(part)] == part))
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internal/store
```

Expected: FAIL because `New` is undefined.

- [ ] **Step 3: Implement the state store**

Create `internal/store/store.go`:

```go
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

type Store struct {
	cfg domain.SessionConfig
}

func New(cfg domain.SessionConfig) *Store {
	return &Store{cfg: cfg}
}

func (s *Store) SessionDir() string {
	return filepath.Join(s.cfg.WorkspaceDir, s.cfg.StateDirName, "sessions", s.cfg.SessionID)
}

func (s *Store) SaveConfig(cfg domain.SessionConfig) error {
	return writeJSONAtomic(filepath.Join(s.SessionDir(), "config.json"), cfg)
}

func (s *Store) SaveAgentState(state domain.AgentState) error {
	return writeJSONAtomic(filepath.Join(s.SessionDir(), "agents", state.Name, "state.json"), state)
}

func (s *Store) LoadAgentState(name string) (domain.AgentState, error) {
	var state domain.AgentState
	err := readJSON(filepath.Join(s.SessionDir(), "agents", name, "state.json"), &state)
	return state, err
}

func (s *Store) SaveRun(run domain.RunRecord) error {
	return writeJSONAtomic(filepath.Join(s.SessionDir(), "runs", run.RunID, "run.json"), run)
}

func (s *Store) LoadRun(runID string) (domain.RunRecord, error) {
	var run domain.RunRecord
	err := readJSON(filepath.Join(s.SessionDir(), "runs", runID, "run.json"), &run)
	return run, err
}

func (s *Store) ListRuns() ([]domain.RunRecord, error) {
	root := filepath.Join(s.SessionDir(), "runs")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	runs := make([]domain.RunRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run, err := s.LoadRun(entry.Name())
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	return runs, nil
}

func (s *Store) WriteAgentOutput(run domain.RunRecord, finalMessage string) (string, error) {
	completed := ""
	if run.CompletedAt != nil {
		completed = run.CompletedAt.Format(time.RFC3339)
	}
	started := ""
	if run.StartedAt != nil {
		started = run.StartedAt.Format(time.RFC3339)
	}
	body := fmt.Sprintf("Agent: %s\nRun: %s\nStarted: %s\nCompleted: %s\n\n%s\n", run.Agent, run.RunID, started, completed, finalMessage)
	path := filepath.Join(s.SessionDir(), "runs", run.RunID, run.Agent+".txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(body), 0o600)
}

func (s *Store) WriteRunStderr(run domain.RunRecord, text string) (string, error) {
	path := filepath.Join(s.SessionDir(), "runs", run.RunID, run.Agent+".stderr.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(text), 0o600)
}

func (s *Store) AppendTranscript(event domain.TranscriptEvent) error {
	path := filepath.Join(s.SessionDir(), "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func (s *Store) ReadTranscript() ([]domain.TranscriptEvent, error) {
	path := filepath.Join(s.SessionDir(), "transcript.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []domain.TranscriptEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event domain.TranscriptEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}

func (s *Store) MarkActiveRunsInterrupted() error {
	runs, err := s.ListRuns()
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.Status == domain.RunQueued || run.Status == domain.RunRunning {
			now := time.Now().UTC()
			msg := "server restarted while run was active"
			run.Status = domain.RunInterrupted
			run.CompletedAt = &now
			run.Error = &msg
			if err := s.SaveRun(run); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
```

- [ ] **Step 4: Run store tests**

Run:

```bash
go test ./internal/store
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat: add file-backed state store"
```

## Task 3: Adapter Interface, Fake Adapter, And Codex Environment Whitelist

**Files:**
- Create: `internal/adapters/adapter.go`
- Create: `internal/adapters/fake/fake.go`
- Create: `internal/adapters/codex/codex.go`
- Test: `internal/adapters/codex/codex_test.go`

- [ ] **Step 1: Write failing Codex env tests**

Create `internal/adapters/codex/codex_test.go`:

```go
package codex

import (
	"os"
	"testing"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

func TestBuildEnvOnlyIncludesWhitelistedVariables(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "secret")
	t.Setenv("CODEX_HOME", "/tmp/codex-home")
	t.Setenv("SHOULD_NOT_PASS", "nope")
	spec := domain.AgentSpec{
		Name:        "Reviewer",
		Backend:     "codex",
		ListOptions: map[string][]string{"inherit_env": []string{"OPENAI_API_KEY", "CODEX_HOME", "MISSING"}},
	}
	env := BuildEnv(spec, os.Environ())
	got := map[string]string{}
	for _, item := range env {
		key, value, ok := splitEnv(item)
		if ok {
			got[key] = value
		}
	}
	if got["OPENAI_API_KEY"] != "secret" {
		t.Fatalf("OPENAI_API_KEY not inherited: %#v", got)
	}
	if got["CODEX_HOME"] != "/tmp/codex-home" {
		t.Fatalf("CODEX_HOME not inherited: %#v", got)
	}
	if _, ok := got["SHOULD_NOT_PASS"]; ok {
		t.Fatalf("unexpected env inherited: %#v", got)
	}
}

func TestParseJSONLFindsCompletionAndFinalMessage(t *testing.T) {
	input := []byte(`{"type":"item.completed","item":{"type":"assistant_message","text":"First"}}
{"type":"item.completed","item":{"type":"assistant_message","text":"Final answer"}}
{"type":"turn.completed"}
`)
	result, err := ParseJSONL(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed {
		t.Fatal("expected completed")
	}
	if result.FinalMessage != "Final answer" {
		t.Fatalf("FinalMessage = %q", result.FinalMessage)
	}
}

func TestParseJSONLDetectsFailure(t *testing.T) {
	input := []byte(`{"type":"turn.failed","error":{"message":"boom"}}` + "\n")
	result, err := ParseJSONL(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Failed || result.ErrorMessage != "boom" {
		t.Fatalf("result = %#v", result)
	}
}
```

- [ ] **Step 2: Run adapter tests and verify they fail**

Run:

```bash
go test ./internal/adapters/codex
```

Expected: FAIL because `BuildEnv` and `ParseJSONL` are undefined.

- [ ] **Step 3: Define adapter interface**

Create `internal/adapters/adapter.go`:

```go
package adapters

import (
	"context"
	"fmt"

	"github.com/andrey/agent-debug-squad/internal/adapters/codex"
	"github.com/andrey/agent-debug-squad/internal/adapters/fake"
	"github.com/andrey/agent-debug-squad/internal/domain"
)

type AgentAdapter interface {
	Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error)
	Send(ctx context.Context, state domain.AgentState, run domain.RunRequest) (domain.RunResult, domain.AgentState, error)
	Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error)
}

func New(spec domain.AgentSpec) (AgentAdapter, error) {
	switch spec.Backend {
	case "fake":
		return fake.New(spec), nil
	case "codex":
		return codex.New(spec), nil
	case "opencode", "kimi":
		return nil, fmt.Errorf("backend %q is wired in a later implementation task", spec.Backend)
	default:
		return nil, fmt.Errorf("unknown backend %q", spec.Backend)
	}
}
```

- [ ] **Step 4: Add fake adapter**

Create `internal/adapters/fake/fake.go`:

```go
package fake

import (
	"context"
	"fmt"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

type Adapter struct {
	spec domain.AgentSpec
}

func New(spec domain.AgentSpec) *Adapter {
	return &Adapter{spec: spec}
}

func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if state.Name == "" {
		state = domain.AgentState{
			Name:            spec.Name,
			Backend:         spec.Backend,
			StartupPrompt:   spec.StartupPrompt,
			BackendSessionID: "fake_" + spec.Name,
			Status:          domain.AgentIdle,
			CreatedAt:       time.Now().UTC(),
		}
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest) (domain.RunResult, domain.AgentState, error) {
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

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	state.Status = domain.AgentIdle
	return state, nil
}
```

- [ ] **Step 5: Implement Codex env and JSONL parsing**

Create `internal/adapters/codex/codex.go`:

```go
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

type Adapter struct {
	spec domain.AgentSpec
}

type StreamResult struct {
	Completed    bool
	Failed       bool
	FinalMessage string
	ErrorMessage string
	RawEvents    []string
}

func New(spec domain.AgentSpec) *Adapter {
	return &Adapter{spec: spec}
}

func BuildEnv(spec domain.AgentSpec, environ []string) []string {
	source := map[string]string{}
	for _, item := range environ {
		key, value, ok := splitEnv(item)
		if ok {
			source[key] = value
		}
	}
	var out []string
	for _, key := range spec.ListOptions["inherit_env"] {
		if value, ok := source[key]; ok {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func splitEnv(item string) (string, string, bool) {
	key, value, ok := strings.Cut(item, "=")
	return key, value, ok && key != ""
}

func ParseJSONL(data []byte) (StreamResult, error) {
	var result StreamResult
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		result.RawEvents = append(result.RawEvents, line)
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return result, err
		}
		switch event["type"] {
		case "turn.completed":
			result.Completed = true
		case "turn.failed":
			result.Failed = true
			result.ErrorMessage = nestedString(event, "error", "message")
		case "item.completed":
			if text := nestedString(event, "item", "text"); text != "" {
				result.FinalMessage = text
			}
		}
	}
	return result, scanner.Err()
}

func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if state.Name == "" {
		state = domain.AgentState{Name: spec.Name, Backend: spec.Backend, StartupPrompt: spec.StartupPrompt, Status: domain.AgentIdle, CreatedAt: time.Now().UTC()}
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest) (domain.RunResult, domain.AgentState, error) {
	command := a.spec.StringOptions["command"]
	if command == "" {
		command = "codex"
	}
	args := []string{"exec", "--json"}
	if state.BackendSessionID != "" {
		args = append(args, "resume", state.BackendSessionID)
	}
	args = append(args, run.Message)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = state.WorkspaceDir
	cmd.Env = BuildEnv(a.spec, os.Environ())
	out, err := cmd.CombinedOutput()
	parsed, parseErr := ParseJSONL(out)
	if parseErr != nil {
		return domain.RunResult{Stderr: string(out), ErrorMessage: parseErr.Error()}, state, parseErr
	}
	state.Status = domain.AgentIdle
	state.LastRunID = run.RunID
	if err != nil {
		return domain.RunResult{Stderr: string(out), ErrorMessage: err.Error(), RawEvents: parsed.RawEvents}, state, err
	}
	if parsed.Failed {
		return domain.RunResult{Stderr: string(out), ErrorMessage: parsed.ErrorMessage, RawEvents: parsed.RawEvents}, state, nil
	}
	return domain.RunResult{FinalMessage: parsed.FinalMessage, Stderr: string(out), RawEvents: parsed.RawEvents}, state, nil
}

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	state.Status = domain.AgentIdle
	return state, nil
}

func nestedString(event map[string]any, objectKey, stringKey string) string {
	obj, ok := event[objectKey].(map[string]any)
	if !ok {
		return ""
	}
	value, _ := obj[stringKey].(string)
	return value
}
```

- [ ] **Step 6: Run adapter tests**

Run:

```bash
go test ./internal/adapters/...
```

Expected: PASS for fake and Codex packages.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/adapter.go internal/adapters/fake/fake.go internal/adapters/codex/codex.go internal/adapters/codex/codex_test.go
git commit -m "feat: add adapter interface and codex env whitelist"
```

## Task 4: Orchestrator Run Lifecycle And Recovery

**Files:**
- Create: `internal/orchestrator/orchestrator.go`
- Test: `internal/orchestrator/orchestrator_test.go`

- [ ] **Step 1: Write failing orchestrator tests**

Create `internal/orchestrator/orchestrator_test.go`:

```go
package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/store"
)

func TestSubmitRunCompletesAndWritesOutput(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
		Agents: []domain.AgentSpec{{
			Name:          "Reviewer",
			Backend:       "fake",
			StartupPrompt: "Review.",
		}},
	}
	o, err := New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	run, err := o.SubmitRun(context.Background(), "Reviewer", "hello", map[string]string{"reason": "test"})
	if err != nil {
		t.Fatal(err)
	}
	final, err := o.Wait(context.Background(), run.RunID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != domain.RunCompleted {
		t.Fatalf("status = %s", final.Status)
	}
	if final.OutputPath == nil || *final.OutputPath == "" {
		t.Fatalf("missing output path: %#v", final)
	}
}

func TestSubmitRunRejectsConcurrentRunForSameAgent(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{
		SessionID:    "session_test",
		WorkspaceDir: root,
		StateDirName: ".agent-debug-squad",
		Agents: []domain.AgentSpec{{
			Name:          "Reviewer",
			Backend:       "fake",
			StartupPrompt: "Review.",
		}},
	}
	o, err := New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.SubmitRun(context.Background(), "Reviewer", "first", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.SubmitRun(context.Background(), "Reviewer", "second", nil); err != ErrAgentBusy {
		t.Fatalf("expected ErrAgentBusy, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internal/orchestrator
```

Expected: FAIL because `New` is undefined.

- [ ] **Step 3: Implement orchestrator**

Create `internal/orchestrator/orchestrator.go`:

```go
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/andrey/agent-debug-squad/internal/adapters"
	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/store"
)

var ErrAgentBusy = errors.New("agent already has an active run")
var ErrAgentNotFound = errors.New("agent not found")
var ErrRunNotFound = errors.New("run not found")

type Orchestrator struct {
	cfg      domain.SessionConfig
	store    *store.Store
	mu       sync.Mutex
	agents   map[string]*agentRuntime
	waiters  map[string]chan struct{}
	runSeq   int
}

type agentRuntime struct {
	spec    domain.AgentSpec
	state   domain.AgentState
	adapter adapters.AgentAdapter
	busy    bool
}

func New(ctx context.Context, cfg domain.SessionConfig, st *store.Store) (*Orchestrator, error) {
	if err := st.SaveConfig(cfg); err != nil {
		return nil, err
	}
	if err := st.MarkActiveRunsInterrupted(); err != nil {
		return nil, err
	}
	o := &Orchestrator{cfg: cfg, store: st, agents: map[string]*agentRuntime{}, waiters: map[string]chan struct{}{}}
	for _, spec := range cfg.Agents {
		adapter, err := adapters.New(spec)
		if err != nil {
			return nil, err
		}
		state, loadErr := st.LoadAgentState(spec.Name)
		if loadErr != nil {
			state = domain.AgentState{Name: spec.Name, Backend: spec.Backend, StartupPrompt: spec.StartupPrompt, WorkspaceDir: cfg.WorkspaceDir, Status: domain.AgentIdle, CreatedAt: time.Now().UTC()}
		}
		state.WorkspaceDir = cfg.WorkspaceDir
		state, err = adapter.Init(ctx, spec, state)
		if err != nil {
			return nil, err
		}
		if err := st.SaveAgentState(state); err != nil {
			return nil, err
		}
		o.agents[spec.Name] = &agentRuntime{spec: spec, state: state, adapter: adapter}
	}
	return o, nil
}

func (o *Orchestrator) Agents() []domain.AgentState {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]domain.AgentState, 0, len(o.agents))
	for _, agent := range o.agents {
		out = append(out, agent.state)
	}
	return out
}

func (o *Orchestrator) SubmitRun(ctx context.Context, agentName, message string, metadata map[string]string) (domain.RunRecord, error) {
	o.mu.Lock()
	agent, ok := o.agents[agentName]
	if !ok {
		o.mu.Unlock()
		return domain.RunRecord{}, ErrAgentNotFound
	}
	if agent.busy {
		o.mu.Unlock()
		return domain.RunRecord{}, ErrAgentBusy
	}
	o.runSeq++
	runID := fmt.Sprintf("run_%06d", o.runSeq)
	now := time.Now().UTC()
	run := domain.RunRecord{RunID: runID, Agent: agentName, Status: domain.RunQueued, Message: message, Metadata: metadata, CreatedAt: now}
	agent.busy = true
	waiter := make(chan struct{})
	o.waiters[runID] = waiter
	o.mu.Unlock()

	if err := o.store.SaveRun(run); err != nil {
		o.release(agentName, runID)
		return domain.RunRecord{}, err
	}
	_ = o.store.AppendTranscript(domain.TranscriptEvent{Type: "facilitator_message", RunID: runID, To: agentName, Text: message, Metadata: metadata, At: now})
	go o.runWorker(ctx, agentName, run)
	return run, nil
}

func (o *Orchestrator) Run(runID string) (domain.RunRecord, error) {
	run, err := o.store.LoadRun(runID)
	if err != nil {
		return domain.RunRecord{}, ErrRunNotFound
	}
	return run, nil
}

func (o *Orchestrator) Runs() ([]domain.RunRecord, error) {
	return o.store.ListRuns()
}

func (o *Orchestrator) Transcript() ([]domain.TranscriptEvent, error) {
	return o.store.ReadTranscript()
}

func (o *Orchestrator) Wait(ctx context.Context, runID string, timeout time.Duration) (domain.RunRecord, error) {
	o.mu.Lock()
	waiter := o.waiters[runID]
	o.mu.Unlock()
	if waiter == nil {
		return o.Run(runID)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-waiter:
		return o.Run(runID)
	case <-ctx.Done():
		return o.Run(runID)
	case <-timer.C:
		return o.Run(runID)
	}
}

func (o *Orchestrator) runWorker(ctx context.Context, agentName string, run domain.RunRecord) {
	started := time.Now().UTC()
	run.Status = domain.RunRunning
	run.StartedAt = &started
	_ = o.store.SaveRun(run)

	o.mu.Lock()
	agent := o.agents[agentName]
	state := agent.state
	adapter := agent.adapter
	o.mu.Unlock()

	result, nextState, err := adapter.Send(ctx, state, domain.RunRequest{RunID: run.RunID, Agent: agentName, Message: run.Message, Metadata: run.Metadata})
	completed := time.Now().UTC()
	run.CompletedAt = &completed
	if err != nil || result.ErrorMessage != "" {
		run.Status = domain.RunFailed
		msg := result.ErrorMessage
		if msg == "" {
			msg = err.Error()
		}
		run.Error = &msg
		_, _ = o.store.WriteRunStderr(run, result.Stderr)
	} else {
		run.Status = domain.RunCompleted
		path, writeErr := o.store.WriteAgentOutput(run, result.FinalMessage)
		if writeErr != nil {
			run.Status = domain.RunFailed
			msg := writeErr.Error()
			run.Error = &msg
		} else {
			run.OutputPath = &path
		}
	}
	nextState.Status = domain.AgentIdle
	nextState.LastRunID = run.RunID
	_ = o.store.SaveRun(run)
	_ = o.store.SaveAgentState(nextState)
	_ = o.store.AppendTranscript(domain.TranscriptEvent{Type: "agent_result", RunID: run.RunID, Agent: agentName, OutputPath: value(run.OutputPath), Status: run.Status, At: completed})
	o.mu.Lock()
	agent.state = nextState
	agent.busy = false
	waiter := o.waiters[run.RunID]
	delete(o.waiters, run.RunID)
	o.mu.Unlock()
	if waiter != nil {
		close(waiter)
	}
}

func (o *Orchestrator) release(agentName, runID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if agent := o.agents[agentName]; agent != nil {
		agent.busy = false
	}
	if waiter := o.waiters[runID]; waiter != nil {
		close(waiter)
		delete(o.waiters, runID)
	}
}

func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
```

- [ ] **Step 4: Run orchestrator tests**

Run:

```bash
go test ./internal/orchestrator
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "feat: add orchestrator run lifecycle"
```

## Task 5: REST API Contract

**Files:**
- Create: `internal/api/server.go`
- Test: `internal/api/server_test.go`

- [ ] **Step 1: Write failing REST tests**

Create `internal/api/server_test.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/orchestrator"
	"github.com/andrey/agent-debug-squad/internal/store"
)

func TestRunEndpointReturnsRunIDAndStatus(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad", Agents: []domain.AgentSpec{{Name: "Reviewer", Backend: "fake", StartupPrompt: "Review."}}}
	o, err := orchestrator.New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	server := New(o, cfg)
	body := bytes.NewBufferString(`{"message":"hello","metadata":{"reason":"test"}}`)
	req := httptest.NewRequest(http.MethodPost, "/agents/Reviewer/runs", body)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var response domain.RunRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RunID == "" || response.Agent != "Reviewer" {
		t.Fatalf("response = %#v", response)
	}
}

func TestBusyAgentReturnsConflict(t *testing.T) {
	root := t.TempDir()
	cfg := domain.SessionConfig{SessionID: "session_test", WorkspaceDir: root, StateDirName: ".agent-debug-squad", Agents: []domain.AgentSpec{{Name: "Reviewer", Backend: "fake", StartupPrompt: "Review."}}}
	o, err := orchestrator.New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	server := New(o, cfg)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/agents/Reviewer/runs", bytes.NewBufferString(`{"message":"hello"}`))
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if i == 1 && rec.Code != http.StatusConflict {
			t.Fatalf("second status = %d body = %s", rec.Code, rec.Body.String())
		}
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internal/api
```

Expected: FAIL because `New` is undefined.

- [ ] **Step 3: Implement REST handlers**

Create `internal/api/server.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
	"github.com/andrey/agent-debug-squad/internal/orchestrator"
)

type Server struct {
	mux *http.ServeMux
	o   *orchestrator.Orchestrator
	cfg domain.SessionConfig
}

type runRequest struct {
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata"`
}

func New(o *orchestrator.Orchestrator, cfg domain.SessionConfig) *Server {
	s := &Server{mux: http.NewServeMux(), o: o, cfg: cfg}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}) })
	s.mux.HandleFunc("GET /session", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, s.cfg) })
	s.mux.HandleFunc("GET /agents", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, s.o.Agents()) })
	s.mux.HandleFunc("GET /runs", func(w http.ResponseWriter, r *http.Request) {
		runs, err := s.o.Runs()
		if err != nil { writeError(w, http.StatusInternalServerError, err); return }
		writeJSON(w, http.StatusOK, runs)
	})
	s.mux.HandleFunc("GET /transcript", func(w http.ResponseWriter, r *http.Request) {
		events, err := s.o.Transcript()
		if err != nil { writeError(w, http.StatusInternalServerError, err); return }
		writeJSON(w, http.StatusOK, events)
	})
	s.mux.HandleFunc("GET /runs/{run_id}", func(w http.ResponseWriter, r *http.Request) {
		run, err := s.o.Run(r.PathValue("run_id"))
		if err != nil { writeError(w, http.StatusNotFound, err); return }
		writeJSON(w, http.StatusOK, run)
	})
	s.mux.HandleFunc("POST /agents/{name}/runs", s.createRun)
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}
	run, err := s.o.SubmitRun(r.Context(), r.PathValue("name"), req.Message, req.Metadata)
	if err != nil {
		if errors.Is(err, orchestrator.ErrAgentBusy) { writeError(w, http.StatusConflict, err); return }
		if errors.Is(err, orchestrator.ErrAgentNotFound) { writeError(w, http.StatusNotFound, err); return }
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if r.URL.Query().Get("wait") == "true" {
		timeout := 60 * time.Second
		if raw := r.URL.Query().Get("timeout_seconds"); raw != "" {
			if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
				timeout = time.Duration(seconds) * time.Second
			}
		}
		final, _ := s.o.Wait(r.Context(), run.RunID, timeout)
		writeJSON(w, http.StatusAccepted, final)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
```

- [ ] **Step 4: Run REST tests**

Run:

```bash
go test ./internal/api
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "feat: expose REST API"
```

## Task 6: CLI Entrypoint And Local Smoke Config

**Files:**
- Create: `cmd/agent-debug-squad/main.go`
- Create: `examples/squad.yaml`
- Create: `README.md`

- [ ] **Step 1: Write CLI entrypoint**

Create `cmd/agent-debug-squad/main.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/andrey/agent-debug-squad/internal/api"
	"github.com/andrey/agent-debug-squad/internal/config"
	"github.com/andrey/agent-debug-squad/internal/orchestrator"
	"github.com/andrey/agent-debug-squad/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return errors.New("usage: agent-debug-squad serve --config squad.yaml")
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to squad YAML config")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("--config is required")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	st := store.New(cfg)
	orch, err := orchestrator.New(context.Background(), cfg, st)
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("agent-debug-squad listening on http://%s", addr)
	return http.ListenAndServe(addr, api.New(orch, cfg))
}
```

- [ ] **Step 2: Add fake smoke config**

Create `examples/squad.yaml`:

```yaml
session_name: local-fake-squad
workspace_dir: .
state_dir_name: .agent-debug-squad
host: 127.0.0.1
port: 8080

agents:
  - name: Reviewer
    backend: fake
    startup_prompt: |
      You review debugging hypotheses and look for missing evidence.

  - name: Skeptic
    backend: fake
    startup_prompt: |
      You challenge assumptions and propose alternative root causes.
```

- [ ] **Step 3: Add README usage**

Create `README.md`:

```markdown
# Agent Debug Squad

Local REST service for coordinating a YAML-defined squad of coding agents.

## Run

```bash
go run ./cmd/agent-debug-squad serve --config examples/squad.yaml
```

## API

```bash
curl http://127.0.0.1:8080/agents

curl -sS -X POST http://127.0.0.1:8080/agents/Reviewer/runs \
  -H 'Content-Type: application/json' \
  -d '{"message":"Review this hypothesis.","metadata":{"reason":"smoke"}}'
```

Each final agent response is written under:

```text
<workspace_dir>/.agent-debug-squad/sessions/<session_id>/runs/<run_id>/<agent_name>.txt
```

## Codex environment whitelist

For Codex agents, pass only explicit variables into child processes:

```yaml
options:
  command: codex
  inherit_env:
    - OPENAI_API_KEY
    - CODEX_HOME
```
```

- [ ] **Step 4: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Run local smoke server**

Run:

```bash
go run ./cmd/agent-debug-squad serve --config examples/squad.yaml
```

Expected: server logs `agent-debug-squad listening on http://127.0.0.1:8080`.

In another terminal, run:

```bash
curl -sS http://127.0.0.1:8080/agents
curl -sS -X POST 'http://127.0.0.1:8080/agents/Reviewer/runs?wait=true&timeout_seconds=2' \
  -H 'Content-Type: application/json' \
  -d '{"message":"hello"}'
```

Expected: second response has `"status":"completed"` and a non-empty `output_path`.

- [ ] **Step 6: Commit**

```bash
git add cmd/agent-debug-squad/main.go examples/squad.yaml README.md
git commit -m "feat: add CLI server entrypoint"
```

## Task 7: Kimi And OpenCode Adapters With Parser And HTTP Tests

**Files:**
- Create: `internal/adapters/kimi/kimi.go`
- Test: `internal/adapters/kimi/kimi_test.go`
- Create: `internal/adapters/opencode/opencode.go`
- Test: `internal/adapters/opencode/opencode_test.go`
- Modify: `internal/adapters/adapter.go`

- [ ] **Step 1: Write Kimi parser tests**

Create `internal/adapters/kimi/kimi_test.go`:

```go
package kimi

import "testing"

func TestParseStreamJSONUsesLastAssistantMessage(t *testing.T) {
	input := []byte(`{"type":"assistant","message":{"content":"First"}}
{"type":"tool","name":"read_file"}
{"type":"assistant","message":{"content":"Final"}}
`)
	result, err := ParseStreamJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "Final" {
		t.Fatalf("FinalMessage = %q", result.FinalMessage)
	}
}
```

- [ ] **Step 2: Implement Kimi adapter and parser**

Create `internal/adapters/kimi/kimi.go`:

```go
package kimi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

type Adapter struct{ spec domain.AgentSpec }

type StreamResult struct {
	FinalMessage string
	RawEvents    []string
}

func New(spec domain.AgentSpec) *Adapter { return &Adapter{spec: spec} }

func ParseStreamJSON(data []byte) (StreamResult, error) {
	var result StreamResult
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" { continue }
		result.RawEvents = append(result.RawEvents, line)
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil { return result, err }
		if event["type"] == "assistant" {
			if msg, ok := event["message"].(map[string]any); ok {
				if content, ok := msg["content"].(string); ok && content != "" {
					result.FinalMessage = content
				}
			}
		}
	}
	return result, scanner.Err()
}

func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if state.Name == "" {
		state = domain.AgentState{Name: spec.Name, Backend: spec.Backend, StartupPrompt: spec.StartupPrompt, Status: domain.AgentIdle, CreatedAt: time.Now().UTC()}
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest) (domain.RunResult, domain.AgentState, error) {
	command := a.spec.StringOptions["command"]
	if command == "" { command = "kimi" }
	cmd := exec.CommandContext(ctx, command, "-p", run.Message, "--output-format", "stream-json")
	cmd.Dir = state.WorkspaceDir
	out, err := cmd.CombinedOutput()
	parsed, parseErr := ParseStreamJSON(out)
	if parseErr != nil { return domain.RunResult{Stderr: string(out), ErrorMessage: parseErr.Error()}, state, parseErr }
	state.Status = domain.AgentIdle
	state.LastRunID = run.RunID
	if err != nil { return domain.RunResult{Stderr: string(out), ErrorMessage: err.Error(), RawEvents: parsed.RawEvents}, state, err }
	return domain.RunResult{FinalMessage: parsed.FinalMessage, Stderr: string(out), RawEvents: parsed.RawEvents}, state, nil
}

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	state.Status = domain.AgentIdle
	return state, nil
}
```

- [ ] **Step 3: Write OpenCode HTTP adapter tests**

Create `internal/adapters/opencode/opencode_test.go`:

```go
package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

func TestSendPostsMessageToSessionAndExtractsFinalText(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"info": map[string]any{"id": "msg_1"},
			"parts": []map[string]any{
				{"type": "text", "text": "Final OpenCode answer"},
			},
		})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{
			"base_url": server.URL,
			"model":    "anthropic/claude-sonnet-4.5",
			"agent":    "build",
		},
	}
	adapter := New(spec)
	state := domain.AgentState{Name: "Skeptic", BackendSessionID: "session_123", WorkspaceDir: t.TempDir()}
	result, nextState, err := adapter.Send(context.Background(), state, domain.RunRequest{RunID: "run_1", Agent: "Skeptic", Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/session/session_123/message" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotBody["model"] != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("body = %#v", gotBody)
	}
	if result.FinalMessage != "Final OpenCode answer" {
		t.Fatalf("FinalMessage = %q", result.FinalMessage)
	}
	if nextState.LastRunID != "run_1" {
		t.Fatalf("LastRunID = %q", nextState.LastRunID)
	}
}

func TestInitCreatesSessionWhenMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "created_session"})
	}))
	defer server.Close()

	spec := domain.AgentSpec{
		Name:          "Skeptic",
		Backend:       "opencode",
		StartupPrompt: "Challenge assumptions.",
		StringOptions: map[string]string{"base_url": server.URL},
	}
	state, err := New(spec).Init(context.Background(), spec, domain.AgentState{})
	if err != nil {
		t.Fatal(err)
	}
	if state.BackendSessionID != "created_session" {
		t.Fatalf("BackendSessionID = %q", state.BackendSessionID)
	}
}
```

- [ ] **Step 4: Implement OpenCode HTTP adapter**

Create `internal/adapters/opencode/opencode.go`:

```go
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/andrey/agent-debug-squad/internal/domain"
)

type Adapter struct{ spec domain.AgentSpec }

func New(spec domain.AgentSpec) *Adapter { return &Adapter{spec: spec} }

func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if state.Name == "" {
		state = domain.AgentState{Name: spec.Name, Backend: spec.Backend, StartupPrompt: spec.StartupPrompt, Status: domain.AgentIdle, CreatedAt: time.Now().UTC()}
	}
	if state.BackendSessionID == "" {
		id, err := a.createSession(ctx)
		if err != nil {
			return state, err
		}
		state.BackendSessionID = id
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest) (domain.RunResult, domain.AgentState, error) {
	if state.BackendSessionID == "" {
		err := errors.New("opencode backend_session_id is empty")
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}
	body := map[string]any{
		"model": a.spec.StringOptions["model"],
		"agent": a.spec.StringOptions["agent"],
		"parts": []map[string]any{{"type": "text", "text": run.Message}},
	}
	var response messageResponse
	if err := a.postJSON(ctx, "/session/"+state.BackendSessionID+"/message", body, &response); err != nil {
		return domain.RunResult{ErrorMessage: err.Error()}, state, err
	}
	state.Status = domain.AgentIdle
	state.LastRunID = run.RunID
	return domain.RunResult{FinalMessage: response.finalText()}, state, nil
}

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	state.Status = domain.AgentIdle
	return state, nil
}

type sessionResponse struct {
	ID string `json:"id"`
}

type messageResponse struct {
	Info  map[string]any `json:"info"`
	Parts []part         `json:"parts"`
}

type part struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (r messageResponse) finalText() string {
	var out []string
	for _, part := range r.Parts {
		if part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}

func (a *Adapter) createSession(ctx context.Context) (string, error) {
	var response sessionResponse
	body := map[string]any{"title": a.spec.Name}
	if err := a.postJSON(ctx, "/session", body, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", errors.New("opencode create session response did not include id")
	}
	return response.ID, nil
}

func (a *Adapter) postJSON(ctx context.Context, path string, body any, out any) error {
	baseURL := strings.TrimRight(a.spec.StringOptions["base_url"], "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:4096"
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if password := a.spec.StringOptions["password"]; password != "" {
		username := a.spec.StringOptions["username"]
		if username == "" {
			username = "opencode"
		}
		req.SetBasicAuth(username, password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("opencode HTTP %d for %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 5: Wire Kimi and OpenCode in factory**

Modify `internal/adapters/adapter.go` switch:

```go
case "kimi":
	return kimi.New(spec), nil
case "opencode":
	return opencode.New(spec), nil
```

Add imports:

```go
import (
	"context"
	"fmt"

	"github.com/andrey/agent-debug-squad/internal/adapters/codex"
	"github.com/andrey/agent-debug-squad/internal/adapters/fake"
	"github.com/andrey/agent-debug-squad/internal/adapters/kimi"
	"github.com/andrey/agent-debug-squad/internal/adapters/opencode"
	"github.com/andrey/agent-debug-squad/internal/domain"
)
```

- [ ] **Step 6: Run adapter tests**

Run:

```bash
go test ./internal/adapters/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/adapter.go internal/adapters/kimi/kimi.go internal/adapters/kimi/kimi_test.go internal/adapters/opencode/opencode.go internal/adapters/opencode/opencode_test.go
git commit -m "feat: add kimi and opencode adapters"
```

## Task 8: Final Verification And Spec Coverage

**Files:**
- Modify only if verification finds gaps.

- [ ] **Step 1: Run full unit test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run fake end-to-end smoke test**

Start server:

```bash
go run ./cmd/agent-debug-squad serve --config examples/squad.yaml
```

In another terminal:

```bash
curl -sS 'http://127.0.0.1:8080/health'
curl -sS 'http://127.0.0.1:8080/agents'
curl -sS -X POST 'http://127.0.0.1:8080/agents/Reviewer/runs?wait=true&timeout_seconds=2' \
  -H 'Content-Type: application/json' \
  -d '{"message":"Read the last run and respond.","metadata":{"reason":"final-smoke"}}'
curl -sS 'http://127.0.0.1:8080/runs'
curl -sS 'http://127.0.0.1:8080/transcript'
```

Expected:

- `/health` returns `{"status":"ok"}`.
- `/agents` includes `Reviewer` and `Skeptic`.
- `POST /agents/Reviewer/runs` returns `completed`.
- `/runs` includes the completed run and `output_path`.
- `/transcript` includes `facilitator_message` and `agent_result`.

- [ ] **Step 3: Verify storage artifacts**

Run:

```bash
find .agent-debug-squad -type f | sort
```

Expected files include:

```text
.agent-debug-squad/sessions/<session_id>/config.json
.agent-debug-squad/sessions/<session_id>/agents/Reviewer/state.json
.agent-debug-squad/sessions/<session_id>/runs/run_000001/Reviewer.txt
.agent-debug-squad/sessions/<session_id>/runs/run_000001/run.json
.agent-debug-squad/sessions/<session_id>/transcript.jsonl
```

- [ ] **Step 4: Confirm Codex env whitelist test covers spec change**

Run:

```bash
go test ./internal/adapters/codex -run TestBuildEnvOnlyIncludesWhitelistedVariables -v
```

Expected: PASS.

- [ ] **Step 5: Commit final verification fixes if any**

If files changed during verification:

```bash
git status --short
git add README.md examples/squad.yaml cmd/agent-debug-squad/main.go internal/api/server.go internal/orchestrator/orchestrator.go internal/store/store.go internal/adapters/codex/codex.go internal/adapters/kimi/kimi.go internal/adapters/opencode/opencode.go
git commit -m "test: complete agent debug squad verification"
```

If no files changed, do not create an empty commit.
