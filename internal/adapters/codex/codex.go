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
	return env
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

		switch eventType, _ := event["type"].(string); eventType {
		case "turn.completed":
			result.Completed = true
		case "turn.failed":
			result.Failed = true
			result.ErrorMessage = firstString(
				nestedString(event, "error", "message"),
				stringValue(event["message"]),
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
	state.Status = domain.AgentIdle
	state.LastRunID = run.RunID
	if parseErr != nil {
		return domain.RunResult{
			Stderr:       string(out),
			RawEvents:    parsed.RawEvents,
			ErrorMessage: parseErr.Error(),
		}, state, parseErr
	}
	if parsed.Failed {
		return domain.RunResult{
			Stderr:       string(out),
			RawEvents:    parsed.RawEvents,
			ErrorMessage: parsed.ErrorMessage,
		}, state, nil
	}
	if err != nil {
		return domain.RunResult{
			Stderr:       string(out),
			RawEvents:    parsed.RawEvents,
			ErrorMessage: err.Error(),
		}, state, err
	}

	return domain.RunResult{
		FinalMessage: parsed.FinalMessage,
		Stderr:       string(out),
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
