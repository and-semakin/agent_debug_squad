package orchestrator

import (
	"fmt"
	"log"
	"sync"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

type runSink struct {
	store  *store.Store
	run    domain.RunRecord
	logger *log.Logger
	mu     sync.Mutex
	err    error
}

func newRunSink(st *store.Store, run domain.RunRecord, logger *log.Logger) *runSink {
	if logger == nil {
		logger = log.Default()
	}
	return &runSink{store: st, run: run, logger: logger}
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
		s.mu.Lock()
		if s.err == nil {
			s.err = fmt.Errorf("write %s stream: %w", stream, err)
		}
		s.mu.Unlock()
	}
}
