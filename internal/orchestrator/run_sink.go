package orchestrator

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

const progressPersistenceInterval = time.Second

type runSink struct {
	store                   *store.Store
	run                     domain.RunRecord
	logger                  *log.Logger
	logLevel                domain.LogLevel
	mu                      sync.Mutex
	err                     error
	progress                domain.RunProgress
	lastProgressPersistedAt time.Time
}

func newRunSink(st *store.Store, run domain.RunRecord, logger *log.Logger, logLevel domain.LogLevel) *runSink {
	if logger == nil {
		logger = log.Default()
	}
	sink := &runSink{store: st, run: run, logger: logger, logLevel: normalizedLogLevel(logLevel)}
	if run.Progress != nil {
		sink.progress = *run.Progress
		sink.lastProgressPersistedAt = run.Progress.LastActivityAt
	}
	return sink
}

func (s *runSink) Progress(progress domain.RunProgress) {
	s.mu.Lock()
	if progress.LastActivityAt.Before(s.progress.LastActivityAt) {
		progress.LastActivityAt = s.progress.LastActivityAt
	}
	s.progress = progress
	s.lastProgressPersistedAt = progress.LastActivityAt
	s.mu.Unlock()
	if err := s.store.UpdateRunProgress(s.run.RunID, progress); err != nil {
		s.recordError(fmt.Errorf("update run progress: %w", err))
	}
}

func (s *runSink) ProgressSnapshot() domain.RunProgress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRunProgress(s.progress)
}

func (s *runSink) StdoutLine(line string) {
	s.write("stdout", line)
}

func (s *runSink) StderrLine(line string) {
	s.write("stderr", line)
}

func (s *runSink) DiagnosticLine(line string) {
	if logLevelEnabled(s.logLevel, domain.LogLevelDebug) {
		s.logger.Printf("run=%s agent=%s diagnostic=%s", s.run.RunID, s.run.Agent, line)
	}
	if _, err := s.store.AppendRunDiagnostics(s.run, line); err != nil {
		s.recordError(fmt.Errorf("write diagnostic stream: %w", err))
	}
}

func (s *runSink) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *runSink) write(stream string, line string) {
	logStream := stream == "stderr" && logLevelEnabled(s.logLevel, domain.LogLevelDebug) ||
		logLevelEnabled(s.logLevel, domain.LogLevelTrace)
	if logStream {
		s.logger.Printf("run=%s agent=%s stream=%s %s", s.run.RunID, s.run.Agent, stream, line)
	}
	var err error
	if stream == "stderr" {
		_, err = s.store.AppendRunStderr(s.run, line)
	} else {
		_, err = s.store.AppendRunEvents(s.run, line)
	}
	if err != nil {
		s.recordError(fmt.Errorf("write %s stream: %w", stream, err))
	}
	s.touchActivity(time.Now().UTC())
}

func (s *runSink) touchActivity(at time.Time) {
	s.mu.Lock()
	if s.progress.Phase == "" {
		s.mu.Unlock()
		return
	}
	s.progress.LastActivityAt = at
	shouldPersist := s.lastProgressPersistedAt.IsZero() || at.Sub(s.lastProgressPersistedAt) >= progressPersistenceInterval
	if shouldPersist {
		s.lastProgressPersistedAt = at
	}
	progress := cloneRunProgress(s.progress)
	s.mu.Unlock()
	if shouldPersist {
		if err := s.store.UpdateRunProgress(s.run.RunID, progress); err != nil {
			s.recordError(fmt.Errorf("update run activity: %w", err))
		}
	}
}

func (s *runSink) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func cloneRunProgress(progress domain.RunProgress) domain.RunProgress {
	cloned := progress
	cloned.Subagents = append([]domain.SubagentProgress(nil), progress.Subagents...)
	if progress.ChildLastActivityAt != nil {
		at := *progress.ChildLastActivityAt
		cloned.ChildLastActivityAt = &at
	}
	return cloned
}

func normalizedLogLevel(level domain.LogLevel) domain.LogLevel {
	if level == "" {
		return domain.LogLevelInfo
	}
	return level
}

func logLevelEnabled(configured, required domain.LogLevel) bool {
	rank := func(level domain.LogLevel) int {
		switch normalizedLogLevel(level) {
		case domain.LogLevelQuiet:
			return 0
		case domain.LogLevelInfo:
			return 1
		case domain.LogLevelDebug:
			return 2
		case domain.LogLevelTrace:
			return 3
		default:
			return 1
		}
	}
	return rank(configured) >= rank(required)
}
