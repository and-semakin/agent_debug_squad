package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/promptfmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

type Adapter struct {
	spec domain.AgentSpec
}

type StreamResult struct {
	Completed        bool
	Failed           bool
	FinalMessage     string
	ErrorMessage     string
	RawEvents        []string
	BackendSessionID string
}

const maxJSONLEventSize = 8 * 1024 * 1024

const incompleteTurnError = "codex turn did not complete"
const turnFailedError = "codex turn failed"

func New(spec domain.AgentSpec) *Adapter {
	return &Adapter{spec: spec}
}

func BuildEnv(spec domain.AgentSpec, environ []string) []string {
	source := map[string]string{}
	for _, item := range environ {
		key, value, ok := strings.Cut(item, "=")
		if ok && key != "" {
			source[key] = value
		}
	}

	var env []string
	for _, key := range spec.ListOptions["inherit_env"] {
		if value, ok := source[key]; ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, spec.ListOptions["env"]...)
	return env
}

func ParseJSONL(data []byte) (StreamResult, error) {
	var result StreamResult
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLEventSize)
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

		switch eventType, _ := event["type"].(string); eventType {
		case "thread.started":
			if result.BackendSessionID == "" {
				result.BackendSessionID = firstString(
					stringValue(event["thread_id"]),
					nestedString(event, "thread", "id"),
					stringValue(event["session_id"]),
				)
			}
		case "session.created":
			if result.BackendSessionID == "" {
				result.BackendSessionID = firstString(
					stringValue(event["session_id"]),
					stringValue(event["id"]),
					nestedString(event, "session", "id"),
				)
			}
		case "turn.completed":
			result.Completed = true
		case "turn.failed":
			result.Failed = true
			result.ErrorMessage = firstString(
				nestedString(event, "error", "message"),
				stringValue(event["message"]),
				turnFailedError,
			)
		case "item.completed":
			if text := assistantMessageText(event); text != "" {
				result.FinalMessage = text
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (a *Adapter) Init(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if state.Name == "" {
		state = domain.AgentState{
			Name:          spec.Name,
			Backend:       spec.Backend,
			StartupPrompt: spec.StartupPrompt,
			Status:        domain.AgentIdle,
			CreatedAt:     time.Now().UTC(),
		}
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest, sink domain.RunSink) (domain.RunResult, domain.AgentState, error) {
	if sink == nil {
		sink = domain.DiscardRunSink()
	}
	command := a.spec.StringOptions["command"]
	if command == "" {
		command = "codex"
	}

	message := run.Message
	if state.LastRunID == "" {
		message = promptfmt.WithStartupPrompt(a.startupPrompt(state), run.Message)
	}
	yolo := true
	if a.spec.Yolo != nil {
		yolo = *a.spec.Yolo
	}
	args := buildArgs(a.spec, state, message, yolo)

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = state.WorkspaceDir
	cmd.Env = BuildEnv(a.spec, os.Environ())

	stdout, stderr, err := runCommandStreaming(ctx, cmd, sink)
	state.Status = domain.AgentIdle
	result, resultErr := buildRunResult(stdout, stderr, err)
	if state.BackendSessionID == "" && result.BackendSessionID != "" {
		state.BackendSessionID = result.BackendSessionID
	}
	return result, state, resultErr
}

func buildArgs(spec domain.AgentSpec, state domain.AgentState, message string, yolo bool) []string {
	args := []string{"exec", "--json"}
	if yolo {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if model := spec.StringOptions["model"]; model != "" {
		args = append(args, "--model", model)
	}
	if reasoning := spec.StringOptions["reasoning"]; reasoning != "" {
		args = append(args, "-c", "model_reasoning_effort="+reasoning)
	}
	if state.BackendSessionID != "" {
		args = append(args, "resume", state.BackendSessionID)
	}
	args = append(args, message)
	return args
}

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
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
			mu.Unlock()
		}
	}
	wg.Add(2)
	go scan(stdoutPipe, "stdout")
	go scan(stderrPipe, "stderr")
	wg.Wait()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return stdout.Bytes(), stderr.Bytes(), scanErr
	}
	return stdout.Bytes(), stderr.Bytes(), waitErr
}

func buildRunResult(stdout []byte, stderr []byte, execErr error) (domain.RunResult, error) {
	if len(bytes.TrimSpace(stdout)) == 0 {
		if execErr != nil {
			return domain.RunResult{
				Stderr:       string(stderr),
				ErrorMessage: execErr.Error(),
			}, execErr
		}
		return domain.RunResult{
			Stderr:       string(stderr),
			ErrorMessage: incompleteTurnError,
		}, errors.New(incompleteTurnError)
	}

	parsed, parseErr := ParseJSONL(stdout)
	if parseErr != nil {
		return domain.RunResult{
			Stderr:           string(stderr),
			RawEvents:        parsed.RawEvents,
			BackendSessionID: parsed.BackendSessionID,
			ErrorMessage:     parseErr.Error(),
		}, parseErr
	}
	if parsed.Failed {
		return domain.RunResult{
			Stderr:           string(stderr),
			RawEvents:        parsed.RawEvents,
			BackendSessionID: parsed.BackendSessionID,
			ErrorMessage:     parsed.ErrorMessage,
		}, errors.New(parsed.ErrorMessage)
	}
	if execErr != nil {
		return domain.RunResult{
			Stderr:           string(stderr),
			RawEvents:        parsed.RawEvents,
			BackendSessionID: parsed.BackendSessionID,
			ErrorMessage:     execErr.Error(),
		}, execErr
	}
	if !parsed.Completed {
		return domain.RunResult{
			Stderr:           string(stderr),
			RawEvents:        parsed.RawEvents,
			BackendSessionID: parsed.BackendSessionID,
			ErrorMessage:     incompleteTurnError,
		}, errors.New(incompleteTurnError)
	}

	return domain.RunResult{
		FinalMessage:     parsed.FinalMessage,
		Stderr:           string(stderr),
		RawEvents:        parsed.RawEvents,
		BackendSessionID: parsed.BackendSessionID,
	}, nil
}

func (a *Adapter) startupPrompt(state domain.AgentState) string {
	if state.StartupPrompt != "" {
		return state.StartupPrompt
	}
	return a.spec.StartupPrompt
}

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Status = domain.AgentIdle
	return state, nil
}

func (a *Adapter) Reset(ctx context.Context, spec domain.AgentSpec, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	state.Name = spec.Name
	state.Backend = spec.Backend
	state.Model = spec.StringOptions["model"]
	state.StartupPrompt = spec.StartupPrompt
	state.BackendSessionID = ""
	state.Status = domain.AgentIdle
	state.LastRunID = ""
	state.LastError = nil
	return state, nil
}

func assistantMessageText(event map[string]any) string {
	item, ok := event["item"].(map[string]any)
	if !ok {
		return ""
	}
	if role, ok := item["role"].(string); ok && role != "assistant" {
		return ""
	}
	if text := obviousText(item); text != "" {
		return text
	}
	if message, ok := item["message"].(map[string]any); ok {
		if role, ok := message["role"].(string); ok && role != "assistant" {
			return ""
		}
		return obviousText(message)
	}
	return ""
}

func obviousText(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if text := stringValue(typed["text"]); text != "" {
			return text
		}
		if text := stringValue(typed["content"]); text != "" {
			return text
		}
		if content, ok := typed["content"].([]any); ok {
			for i := len(content) - 1; i >= 0; i-- {
				if text := obviousText(content[i]); text != "" {
					return text
				}
			}
		}
		if parts, ok := typed["parts"].([]any); ok {
			for i := len(parts) - 1; i >= 0; i-- {
				if text := obviousText(parts[i]); text != "" {
					return text
				}
			}
		}
	case []any:
		for i := len(typed) - 1; i >= 0; i-- {
			if text := obviousText(typed[i]); text != "" {
				return text
			}
		}
	case string:
		return typed
	}
	return ""
}

func nestedString(event map[string]any, objectKey string, stringKey string) string {
	obj, ok := event[objectKey].(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(obj[stringKey])
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
