package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	dir, err := s.sessionDir()
	if err != nil {
		return ""
	}
	return dir
}

func (s *Store) SaveConfig(cfg domain.SessionConfig) error {
	sessionDir, err := s.sessionDir()
	if err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(sessionDir, "config.json"), cfg)
}

func (s *Store) SaveAgentState(state domain.AgentState) error {
	path, err := s.agentStatePath(state.Name)
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, state)
}

func (s *Store) LoadAgentState(name string) (domain.AgentState, error) {
	var state domain.AgentState
	path, err := s.agentStatePath(name)
	if err != nil {
		return state, err
	}
	err = readJSON(path, &state)
	return state, err
}

func (s *Store) SaveRun(run domain.RunRecord) error {
	path, err := s.runPath(run.RunID)
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, run)
}

func (s *Store) LoadRun(runID string) (domain.RunRecord, error) {
	var run domain.RunRecord
	path, err := s.runPath(runID)
	if err != nil {
		return run, err
	}
	err = readJSON(path, &run)
	return run, err
}

func (s *Store) ListRuns() ([]domain.RunRecord, error) {
	runDir, err := s.runDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(runDir)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.RunRecord{}, nil
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
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].RunID < runs[j].RunID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
	return runs, nil
}

func (s *Store) WriteAgentOutput(run domain.RunRecord, finalMessage string) (string, error) {
	path, err := s.runArtifactPath(run.RunID, run.Agent, ".txt")
	if err != nil {
		return "", err
	}
	body := fmt.Sprintf(
		"Agent: %s\nRun: %s\nStarted: %s\nCompleted: %s\n\n%s\n",
		run.Agent,
		run.RunID,
		formatTime(run.StartedAt),
		formatTime(run.CompletedAt),
		finalMessage,
	)
	return path, writeFileAtomic(path, []byte(body))
}

func (s *Store) WriteRunStderr(run domain.RunRecord, text string) (string, error) {
	path, err := s.runArtifactPath(run.RunID, run.Agent, ".stderr.log")
	if err != nil {
		return "", err
	}
	return path, writeFileAtomic(path, []byte(text))
}

func (s *Store) AppendRunEvents(run domain.RunRecord, line string) (string, error) {
	return s.appendRunArtifactLine(run, ".events.jsonl", line)
}

func (s *Store) AppendRunStderr(run domain.RunRecord, line string) (string, error) {
	return s.appendRunArtifactLine(run, ".stderr.log", line)
}

func (s *Store) appendRunArtifactLine(run domain.RunRecord, suffix string, line string) (string, error) {
	path, err := s.runArtifactPath(run.RunID, run.Agent, suffix)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return path, err
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		return path, err
	}
	return path, file.Sync()
}

func (s *Store) AppendTranscript(event domain.TranscriptEvent) error {
	sessionDir, err := s.sessionDir()
	if err != nil {
		return err
	}
	path := filepath.Join(sessionDir, "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (s *Store) ReadTranscript() ([]domain.TranscriptEvent, error) {
	sessionDir, err := s.sessionDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(sessionDir, "transcript.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.TranscriptEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	events := []domain.TranscriptEvent{}
	for {
		var event domain.TranscriptEvent
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func (s *Store) MarkActiveRunsInterrupted() error {
	runs, err := s.ListRuns()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	message := "interrupted on startup"
	for _, run := range runs {
		if run.Status != domain.RunQueued && run.Status != domain.RunRunning {
			continue
		}
		run.Status = domain.RunInterrupted
		run.CompletedAt = &now
		run.Error = &message
		if err := s.SaveRun(run); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) sessionDir() (string, error) {
	stateDirName, err := safePathElement("state_dir_name", s.cfg.StateDirName)
	if err != nil {
		return "", err
	}
	sessionID, err := safePathElement("session_id", s.cfg.SessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.cfg.WorkspaceDir, stateDirName, "sessions", sessionID), nil
}

func (s *Store) agentStatePath(name string) (string, error) {
	sessionDir, err := s.sessionDir()
	if err != nil {
		return "", err
	}
	agentName, err := safePathElement("agent name", name)
	if err != nil {
		return "", err
	}
	return filepath.Join(sessionDir, "agents", agentName, "state.json"), nil
}

func (s *Store) runDir() (string, error) {
	sessionDir, err := s.sessionDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sessionDir, "runs"), nil
}

func (s *Store) runPath(runID string) (string, error) {
	runDir, err := s.runDir()
	if err != nil {
		return "", err
	}
	safeRunID, err := safePathElement("run_id", runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(runDir, safeRunID, "run.json"), nil
}

func (s *Store) runArtifactPath(runID, agentName, suffix string) (string, error) {
	runDir, err := s.runDir()
	if err != nil {
		return "", err
	}
	safeRunID, err := safePathElement("run_id", runID)
	if err != nil {
		return "", err
	}
	safeAgentName, err := safePathElement("agent name", agentName)
	if err != nil {
		return "", err
	}
	return filepath.Join(runDir, safeRunID, safeAgentName+suffix), nil
}

func safePathElement(label, value string) (string, error) {
	if value == "" || value == "." || value == ".." {
		return "", fmt.Errorf("unsafe %s %q", label, value)
	}
	if filepath.IsAbs(value) || strings.Contains(value, "/") || strings.ContainsRune(value, filepath.Separator) {
		return "", fmt.Errorf("unsafe %s %q", label, value)
	}
	return value, nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
