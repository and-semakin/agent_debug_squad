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
	Name          string              `json:"name"`
	Backend       string              `json:"backend"`
	StartupPrompt string              `json:"startup_prompt"`
	Options       map[string]any      `json:"options,omitempty"`
	Yolo          *bool               `json:"yolo,omitempty"`
	StringOptions map[string]string   `json:"-"`
	ListOptions   map[string][]string `json:"-"`
}

type SessionDefaults struct {
	Yolo bool `json:"yolo"`
}

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

func (cfg SessionConfig) AgentYolo(spec AgentSpec) bool {
	if spec.Yolo != nil {
		return *spec.Yolo
	}
	return cfg.Defaults.Yolo
}

type AgentState struct {
	Name             string      `json:"name"`
	Backend          string      `json:"backend"`
	Model            string      `json:"model,omitempty"`
	StartupPrompt    string      `json:"startup_prompt"`
	WorkspaceDir     string      `json:"workspace_dir"`
	BackendSessionID string      `json:"backend_session_id"`
	Status           AgentStatus `json:"status"`
	CreatedAt        time.Time   `json:"created_at"`
	LastRunID        string      `json:"last_run_id"`
	LastError        *string     `json:"last_error"`
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
	FinalMessage     string
	Stderr           string
	RawEvents        []string
	ErrorMessage     string
	BackendSessionID string
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
