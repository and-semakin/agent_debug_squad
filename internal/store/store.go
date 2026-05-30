package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	return filepath.Join(s.cfg.WorkspaceDir, s.cfg.StateDirName, "sessions", s.cfg.SessionID)
}

func (s *Store) SaveConfig(cfg domain.SessionConfig) error {
	return writeJSONAtomic(filepath.Join(s.SessionDir(), "config.json"), cfg)
}

func (s *Store) SaveAgentState(state domain.AgentState) error {
	return writeJSONAtomic(filepath.Join(s.SessionDir(), "agents", state.Name, "state.json"), state)
}

func (s *Store) LoadAgentState(name string) (domain.AgentState, error) {
	var state domain.AgentState
	err := readJSON(filepath.Join(s.SessionDir(), "agents", name, "state.json"), &state)
	return state, err
}

func (s *Store) SaveRun(run domain.RunRecord) error {
	return writeJSONAtomic(filepath.Join(s.runDir(), run.RunID, "run.json"), run)
}

func (s *Store) LoadRun(runID string) (domain.RunRecord, error) {
	var run domain.RunRecord
	err := readJSON(filepath.Join(s.runDir(), runID, "run.json"), &run)
	return run, err
}

func (s *Store) ListRuns() ([]domain.RunRecord, error) {
	entries, err := os.ReadDir(s.runDir())
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
	path := filepath.Join(s.runDir(), run.RunID, run.Agent+".txt")
	body := fmt.Sprintf(
		"Agent: %s\nRun: %s\nStarted: %s\nCompleted: %s\n\n%s\n",
		run.Agent,
		run.RunID,
		formatTime(run.StartedAt),
		formatTime(run.CompletedAt),
		finalMessage,
	)
	return path, writeFile(path, []byte(body))
}

func (s *Store) WriteRunStderr(run domain.RunRecord, text string) (string, error) {
	path := filepath.Join(s.runDir(), run.RunID, run.Agent+".stderr.log")
	return path, writeFile(path, []byte(text))
}

func (s *Store) AppendTranscript(event domain.TranscriptEvent) error {
	path := filepath.Join(s.SessionDir(), "transcript.jsonl")
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
	path := filepath.Join(s.SessionDir(), "transcript.jsonl")
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

func (s *Store) runDir() string {
	return filepath.Join(s.SessionDir(), "runs")
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
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
	return os.Rename(tmpPath, path)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
