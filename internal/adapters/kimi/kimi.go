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

const maxStreamJSONEventSize = 8 * 1024 * 1024

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
		if eventType, _ := event["type"].(string); eventType == "assistant" {
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

func (a *Adapter) Send(ctx context.Context, state domain.AgentState, run domain.RunRequest) (domain.RunResult, domain.AgentState, error) {
	command := a.spec.StringOptions["command"]
	if command == "" {
		command = "kimi"
	}

	cmd := exec.CommandContext(ctx, command, "-p", run.Message, "--output-format", "stream-json")
	cmd.Dir = state.WorkspaceDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.Output()
	state.Status = domain.AgentIdle
	if err != nil {
		return domain.RunResult{
			Stderr:       stderr.String(),
			ErrorMessage: err.Error(),
		}, state, err
	}

	parsed, parseErr := ParseStreamJSON(stdout)
	if parseErr != nil {
		return domain.RunResult{
			Stderr:       stderr.String(),
			RawEvents:    parsed.RawEvents,
			ErrorMessage: parseErr.Error(),
		}, state, parseErr
	}

	state.LastRunID = run.RunID
	return domain.RunResult{
		FinalMessage: parsed.FinalMessage,
		Stderr:       stderr.String(),
		RawEvents:    parsed.RawEvents,
	}, state, nil
}

func (a *Adapter) Recover(ctx context.Context, state domain.AgentState) (domain.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	state.Status = domain.AgentIdle
	return state, nil
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
