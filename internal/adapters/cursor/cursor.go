package cursor

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

	"github.com/andrey/agent-debug-squad/internal/adapters/promptfmt"
	"github.com/andrey/agent-debug-squad/internal/domain"
)

const maxJSONLEventSize = 8 * 1024 * 1024
const incompleteTurnError = "cursor turn did not complete"
const turnFailedError = "cursor turn failed"

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

func ParseStreamJSON(data []byte) (StreamResult, error) {
	var result StreamResult
	var assistantText strings.Builder
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

		if sessionID := stringValue(event["session_id"]); sessionID != "" && result.BackendSessionID == "" {
			result.BackendSessionID = sessionID
		}

		eventType := stringValue(event["type"])
		switch eventType {
		case "assistant":
			if text := assistantMessageText(event); text != "" {
				assistantText.WriteString(text)
			}
		case "result":
			subtype := stringValue(event["subtype"])
			isError, _ := event["is_error"].(bool)
			if isError || (subtype != "" && subtype != "success") {
				result.Failed = true
				result.ErrorMessage = firstString(
					stringValue(event["result"]),
					nestedString(event, "error", "message"),
					stringValue(event["message"]),
					turnFailedError,
				)
				continue
			}
			if subtype == "success" {
				result.Completed = true
				result.FinalMessage = stringValue(event["result"])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if result.Completed && result.FinalMessage == "" {
		result.FinalMessage = assistantText.String()
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
			Model:         spec.StringOptions["model"],
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
		command = "cursor-agent"
	}

	message := run.Message
	if state.LastRunID == "" {
		message = promptfmt.WithStartupPrompt(a.startupPrompt(state), run.Message)
	}
	yolo := true
	if a.spec.Yolo != nil {
		yolo = *a.spec.Yolo
	}

	cmd := exec.CommandContext(ctx, command, buildArgs(a.spec, state, message, yolo)...)
	cmd.Dir = state.WorkspaceDir
	cmd.Env = BuildEnv(a.spec, os.Environ())

	stdout, stderr, execErr := runCommandStreaming(ctx, cmd, sink)
	state.Status = domain.AgentIdle
	result, resultErr := buildRunResult(stdout, stderr, execErr)
	if result.BackendSessionID != "" {
		state.BackendSessionID = result.BackendSessionID
	}
	return result, state, resultErr
}

func buildArgs(spec domain.AgentSpec, state domain.AgentState, message string, yolo bool) []string {
	args := []string{
		"--print",
		"--trust",
		"--output-format", "stream-json",
		"--stream-partial-output",
	}
	if yolo {
		args = append(args, "--force")
	}
	if model := spec.StringOptions["model"]; model != "" {
		args = append(args, "--model", model)
	}
	if mode := spec.StringOptions["mode"]; mode != "" {
		args = append(args, "--mode", mode)
	}
	if sandbox := spec.StringOptions["sandbox"]; sandbox != "" {
		args = append(args, "--sandbox", sandbox)
	}
	if state.BackendSessionID != "" {
		args = append(args, "--resume", state.BackendSessionID)
	}
	return append(args, message)
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
			return domain.RunResult{Stderr: string(stderr), ErrorMessage: execErr.Error()}, execErr
		}
		return domain.RunResult{Stderr: string(stderr), ErrorMessage: incompleteTurnError}, errors.New(incompleteTurnError)
	}

	parsed, parseErr := ParseStreamJSON(stdout)
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
	message, ok := event["message"].(map[string]any)
	if !ok {
		return ""
	}
	if role := stringValue(message["role"]); role != "" && role != "assistant" {
		return ""
	}
	return obviousText(message["content"])
}

func obviousText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var text strings.Builder
		for _, item := range typed {
			text.WriteString(obviousText(item))
		}
		return text.String()
	case map[string]any:
		if kind := stringValue(typed["type"]); kind != "" && kind != "text" && kind != "output_text" {
			return ""
		}
		return stringValue(typed["text"])
	default:
		return ""
	}
}

func nestedString(event map[string]any, objectKey string, stringKey string) string {
	object, ok := event[objectKey].(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(object[stringKey])
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
