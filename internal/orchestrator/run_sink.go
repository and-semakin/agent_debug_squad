package orchestrator

import (
	"fmt"
	"log"
	"sync"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

type runSink struct {
	store    *store.Store
	run      domain.RunRecord
	logger   *log.Logger
	mu       sync.Mutex
	err      error
	progress domain.RunProgress
}

func newRunSink(st *store.Store, run domain.RunRecord, logger *log.Logger) *runSink {
	if logger == nil {
		logger = log.Default()
	}
	sink := &runSink{store: st, run: run, logger: logger}
	if run.Progress != nil {
		sink.progress = *run.Progress
	}
	return sink
}

func (s *runSink) Progress(progress domain.RunProgress) {
	s.mu.Lock()
	s.progress = progress
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

func (s *runSink) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *runSink) write(stream string, line string) {
	s.logger.Printf("run=%s agent=%s stream=%s %s", s.run.RunID, s.run.Agent, stream, line)
	var err error
	if stream == "stderr" {
		_, err = s.store.AppendRunStderr(s.run, line)
	} else {
		_, err = s.store.AppendRunEvents(s.run, line)
	}
	if err != nil {
		s.recordError(fmt.Errorf("write %s stream: %w", stream, err))
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
