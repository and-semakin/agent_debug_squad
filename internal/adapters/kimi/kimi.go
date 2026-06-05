package kimi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/andrey/agent-debug-squad/internal/adapters/promptfmt"
	"github.com/andrey/agent-debug-squad/internal/domain"
)

const maxStreamJSONEventSize = 8 * 1024 * 1024
const missingAssistantMessageError = "kimi stream did not include assistant message"

type Adapter struct {
	spec domain.AgentSpec
}

type StreamResult struct {
	FinalMessage string
	RawEvents    []string
}

func New(spec domain.AgentSpec) *Adapter {
	return &Adapter{spec: spec}
}

func ParseStreamJSON(data []byte) (StreamResult, error) {
	var result StreamResult
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamJSONEventSize)
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
		if isAssistantEvent(event) {
			if text := assistantText(event); text != "" {
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
		command = "kimi"
	}

	args := buildArgs(promptfmt.WithStartupPrompt(a.startupPrompt(state), run.Message))

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = state.WorkspaceDir

	stdout, stderr, err := runCommandStreaming(ctx, cmd, sink)
	state.Status = domain.AgentIdle
	if err != nil {
		return domain.RunResult{
			Stderr:       string(stderr),
			ErrorMessage: err.Error(),
		}, state, err
	}

	result, resultErr := buildRunResult(stdout, stderr)
	if resultErr != nil {
		return result, state, resultErr
	}

	state.LastRunID = run.RunID
	return result, state, nil
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

func buildArgs(prompt string) []string {
	return []string{"-p", prompt, "--output-format", "stream-json"}
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
		scanner.Buffer(make([]byte, 0, 64*1024), maxStreamJSONEventSize)
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

func buildRunResult(stdout []byte, stderr []byte) (domain.RunResult, error) {
	parsed, parseErr := ParseStreamJSON(stdout)
	if parseErr != nil {
		return domain.RunResult{
			Stderr:       string(stderr),
			RawEvents:    parsed.RawEvents,
			ErrorMessage: parseErr.Error(),
		}, parseErr
	}
	if parsed.FinalMessage == "" {
		err := errors.New(missingAssistantMessageError)
		return domain.RunResult{
			Stderr:       string(stderr),
			RawEvents:    parsed.RawEvents,
			ErrorMessage: err.Error(),
		}, err
	}
	return domain.RunResult{
		FinalMessage: parsed.FinalMessage,
		Stderr:       string(stderr),
		RawEvents:    parsed.RawEvents,
	}, nil
}

func isAssistantEvent(event map[string]any) bool {
	if eventType, _ := event["type"].(string); eventType == "assistant" {
		return true
	}
	role, _ := event["role"].(string)
	return role == "assistant"
}

func assistantText(event map[string]any) string {
	if text := obviousText(event); text != "" {
		return text
	}
	if message, ok := event["message"].(map[string]any); ok {
		return obviousText(message)
	}
	return ""
}

func obviousText(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if text := stringValue(typed["content"]); text != "" {
			return text
		}
		if text := stringValue(typed["text"]); text != "" {
			return text
		}
		if message, ok := typed["message"].(map[string]any); ok {
			if text := obviousText(message); text != "" {
				return text
			}
		}
		if parts, ok := typed["parts"].([]any); ok {
			return obviousText(parts)
		}
		if content, ok := typed["content"].([]any); ok {
			return obviousText(content)
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

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
