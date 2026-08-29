package domain

import "time"

type LogLevel string

const (
	LogLevelQuiet LogLevel = "quiet"
	LogLevelInfo  LogLevel = "info"
	LogLevelDebug LogLevel = "debug"
	LogLevelTrace LogLevel = "trace"
)

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

type RunPhase string

const (
	RunPhaseRunning            RunPhase = "running"
	RunPhaseWaitingForSubagent RunPhase = "waiting_for_subagent"
	RunPhaseCompleted          RunPhase = "completed"
	RunPhaseFailed             RunPhase = "failed"
	RunPhaseInterrupted        RunPhase = "interrupted"
)

type SubagentProgress struct {
	ID             string    `json:"id"`
	ParentID       string    `json:"parent_id,omitempty"`
	Status         string    `json:"status"`
	LastActivityAt time.Time `json:"last_activity_at"`
}

type RunProgress struct {
	Phase               RunPhase           `json:"phase"`
	LastActivityAt      time.Time          `json:"last_activity_at"`
	ChildLastActivityAt *time.Time         `json:"child_last_activity_at,omitempty"`
	Subagents           []SubagentProgress `json:"subagents,omitempty"`
}

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
	LogLevel     LogLevel        `json:"log_level"`
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

type AgentResetResult struct {
	Agent                    string      `json:"agent"`
	PreviousBackendSessionID string      `json:"previous_backend_session_id"`
	BackendSessionID         string      `json:"backend_session_id"`
	Status                   AgentStatus `json:"status"`
	ActiveRun                bool        `json:"active_run"`
	PreviousRunID            string      `json:"previous_run_id,omitempty"`
	Force                    bool        `json:"force"`
	State                    AgentState  `json:"state"`
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
	Progress    *RunProgress      `json:"progress,omitempty"`
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

type RunSink interface {
	StdoutLine(line string)
	StderrLine(line string)
	Err() error
}

type RunProgressSink interface {
	Progress(progress RunProgress)
}

type RunDiagnosticSink interface {
	DiagnosticLine(line string)
}

func ReportRunProgress(sink RunSink, progress RunProgress) {
	if progressSink, ok := sink.(RunProgressSink); ok {
		progressSink.Progress(progress)
	}
}

func ReportRunDiagnostic(sink RunSink, line string) {
	if diagnosticSink, ok := sink.(RunDiagnosticSink); ok {
		diagnosticSink.DiagnosticLine(line)
	}
}

type discardRunSink struct{}

func (discardRunSink) StdoutLine(string) {}
func (discardRunSink) StderrLine(string) {}
func (discardRunSink) Err() error        { return nil }

func DiscardRunSink() RunSink {
	return discardRunSink{}
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
