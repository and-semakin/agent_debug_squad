package judge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// DefaultKeyPath returns the default OpenRouter API key location in the
// user's home directory — deliberately outside any workspace, because the
// workflow state directory resolves inside WorkspaceDir and a key there
// could land in a git repository.
func DefaultKeyPath(homeDir string) string {
	return filepath.Join(homeDir, ".agent-debug-squad", "openrouter-api-key")
}

// LoadAPIKey reads the bearer key from a one-line file: the bare token with
// no prefix or quoting, at most one trailing newline, surrounding whitespace
// trimmed.
func LoadAPIKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("openrouter api key file %s does not exist; create it containing the bare token on a single line", path)
		}
		return "", fmt.Errorf("read openrouter api key file %s: %w", path, err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("openrouter api key file %s is empty", path)
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", fmt.Errorf("openrouter api key file %s must contain the bare token on a single line", path)
	}
	return key, nil
}

// Setup resolves the judge for one server session. It returns nil — and
// expects no key — when neither a judge section nor workflow verdict tasks
// require one. The verdict-task rule keeps purely static workflows usable
// without an OpenRouter key or network access. The judge proxy resolves from
// the session judge section first; machineProxyURL (the machine backend
// settings file's judge.proxy_url) applies only as a default below it.
func Setup(cfg *domain.JudgeConfig, workflow *domain.WorkflowDefinition, homeDir, machineProxyURL string) (Judge, error) {
	if cfg == nil && !workflowDeclaresVerdicts(workflow) {
		return nil, nil
	}
	resolved := domain.JudgeConfig{}
	if cfg != nil {
		resolved = *cfg
	}
	if resolved.ProxyURL == "" {
		resolved.ProxyURL = strings.TrimSpace(machineProxyURL)
	}
	if resolved.Provider == "" {
		resolved.Provider = domain.JudgeProviderOpenRouter
	}
	if resolved.Provider != domain.JudgeProviderOpenRouter {
		return nil, fmt.Errorf("judge: unsupported provider %q (supported: %s)", resolved.Provider, domain.JudgeProviderOpenRouter)
	}
	if resolved.Model == "" {
		resolved.Model = domain.DefaultJudgeModel
	}
	if resolved.TimeoutSeconds <= 0 {
		resolved.TimeoutSeconds = domain.DefaultJudgeTimeoutSeconds
	}
	keyPath := resolved.APIKeyFile
	if keyPath == "" {
		keyPath = DefaultKeyPath(homeDir)
	}
	key, err := LoadAPIKey(keyPath)
	if err != nil {
		return nil, err
	}
	return NewOpenRouter(OpenRouterConfig{
		APIKey:   key,
		Model:    resolved.Model,
		ProxyURL: resolved.ProxyURL,
		Timeout:  time.Duration(resolved.TimeoutSeconds) * time.Second,
	})
}

func workflowDeclaresVerdicts(def *domain.WorkflowDefinition) bool {
	if def == nil {
		return false
	}
	for _, task := range def.Tasks {
		if len(task.Verdicts) > 0 {
			return true
		}
	}
	return false
}
