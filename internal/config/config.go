package config

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"gopkg.in/yaml.v3"
)

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
	if !isAllowedHost(raw.Host) {
		return domain.SessionConfig{}, fmt.Errorf("non-loopback host %q is not allowed without auth in v1", raw.Host)
	}
	if raw.Port == 0 {
		raw.Port = 8080
	}
	if raw.WorkspaceDir == "" {
		return domain.SessionConfig{}, errors.New("workspace_dir is required")
	}

	defaults := domain.SessionDefaults{Yolo: true}
	if raw.Defaults.Yolo != nil {
		defaults.Yolo = *raw.Defaults.Yolo
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

		backend := strings.TrimSpace(a.Backend)
		if backend == "" {
			return domain.SessionConfig{}, fmt.Errorf("agent %q backend is required", name)
		}
		if strings.TrimSpace(a.StartupPrompt) == "" {
			return domain.SessionConfig{}, fmt.Errorf("agent %q startup_prompt is required", name)
		}

		spec := domain.AgentSpec{
			Name:          name,
			Backend:       backend,
			StartupPrompt: a.StartupPrompt,
			Options:       a.Options,
			StringOptions: map[string]string{},
			ListOptions:   map[string][]string{},
		}
		for key, value := range a.Options {
			switch typed := value.(type) {
			case string:
				spec.StringOptions[key] = typed
			case bool:
				if key != "yolo" {
					return domain.SessionConfig{}, fmt.Errorf("agent %q option %q has unsupported value type %T; expected string, bool yolo, or list of strings", name, key, value)
				}
				spec.Yolo = &typed
				spec.StringOptions[key] = strconv.FormatBool(typed)
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
			default:
				return domain.SessionConfig{}, fmt.Errorf("agent %q option %q has unsupported value type %T; expected string, bool yolo, or list of strings", name, key, value)
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
		Defaults:     defaults,
		Agents:       agents,
	}, nil
}

func isAllowedHost(host string) bool {
	switch strings.TrimSpace(strings.ToLower(host)) {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func stableSessionID(name, workspace string) string {
	sum := sha1.Sum([]byte(name + "\x00" + workspace))
	return "session_" + hex.EncodeToString(sum[:])[:12]
}
