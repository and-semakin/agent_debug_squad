package opencode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// Runtime owns at most one serve process. Share it across every adapter in a
// Squad, including workflow attempts. A failed owner is intentionally not retried.
type Runtime struct {
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	workspace    string
	settings     domain.MachineBackendSettings
	ambient      []string
	password     string
	redactor     *strings.Replacer
	cmd          *exec.Cmd
	done         chan struct{}
	endpoint     string
	failure      error
	closed       bool
	startTimeout time.Duration
	stopTimeout  time.Duration
}

func NewRuntime(ctx context.Context, workspace string, settings *domain.MachineBackendSettings) *Runtime {
	ctx, cancel := context.WithCancel(ctx)
	r := &Runtime{ctx: ctx, cancel: cancel, workspace: workspace, ambient: os.Environ(), startTimeout: 30 * time.Second, stopTimeout: 3 * time.Second}
	if settings != nil {
		r.settings = *settings
		r.settings.InheritEnv = append([]string(nil), settings.InheritEnv...)
		if settings.Snapshot != nil {
			snapshot := *settings.Snapshot
			r.settings.Snapshot = &snapshot
		}
		r.settings.Declared = make(map[string]bool, len(settings.Declared))
		for k, v := range settings.Declared {
			r.settings.Declared[k] = v
		}
	}
	return r
}

func (r *Runtime) Adapter(spec domain.AgentSpec) (*Adapter, error) {
	if err := config.ValidateOpenCodeAgent(spec); err != nil {
		return nil, err
	}
	s := r.settings
	if value := spec.StringOptions["mode"]; value != "" {
		s.Mode = value
	}
	if value, present := spec.StringOptions["base_url"]; present {
		s.BaseURL = value
		s.Declared = make(map[string]bool, len(r.settings.Declared)+1)
		for k, v := range r.settings.Declared {
			s.Declared[k] = v
		}
		s.Declared["base_url"] = true
	}
	if err := config.ValidateOpenCodeSettings(s); err != nil {
		return nil, err
	}
	if s.Mode == "" {
		s.Mode = "managed"
	}
	if s.Mode == "managed" && (spec.StringOptions["username"] != "" || spec.StringOptions["password"] != "") {
		return nil, errors.New("opencode managed authentication is owned by Squad; username/password require external mode")
	}
	a := New(spec)
	a.runtime = r
	a.settings = s
	a.workspace = r.workspace
	return a, nil
}

func (r *Runtime) ensure(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return errors.New("opencode runtime is closed")
	}
	if r.failure != nil {
		return r.failure
	}
	if r.cmd != nil {
		select {
		case <-r.done:
			return errors.New("owned opencode server exited; restart Squad explicitly; work was not replayed")
		default:
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		r.failure = fmt.Errorf("opencode startup cancelled; restart Squad explicitly: %w", err)
		return r.failure
	}
	startCtx, cancel := context.WithTimeout(r.ctx, r.startTimeout)
	defer cancel()
	// Caller cancellation aborts initialization, but subsequent prompts do not
	// own the server lifetime.
	stopCaller := context.AfterFunc(ctx, cancel)
	defer stopCaller()
	r.failure = r.start(startCtx)
	if r.failure != nil {
		r.stopLocked()
		return r.failure
	}
	go func() { <-r.ctx.Done(); r.Close() }()
	return nil
}

func (r *Runtime) start(ctx context.Context) error {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return errors.New("opencode server credential generation failed")
	}
	r.password = hex.EncodeToString(token)
	env, redactor, err := processEnvironment(r.settings, r.ambient, r.password)
	if err != nil {
		return err
	}
	r.redactor = redactor
	if err := guardConfigFiles(r.workspace, env); err != nil {
		return err
	}
	command := r.settings.Command
	if command == "" {
		command = "opencode"
	}
	cmd := exec.Command(command, "serve", "--hostname", "127.0.0.1", "--port", "0", "--mdns=false")
	cmd.Dir = r.workspace
	cmd.Env = env
	prepareProcess(cmd)
	announced := make(chan string, 1)
	cmd.Stdout = &announcementWriter{ready: announced}
	cmd.Stderr = io.Discard
	// Bound exec's pipe-copy wait even if a descendant inherits stdout.
	cmd.WaitDelay = r.stopTimeout
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("opencode startup cancelled: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return errors.New("cannot launch opencode serve; check opencode.command, executable permissions and workspace")
	}
	r.cmd = cmd
	r.done = make(chan struct{})
	go func() {
		_ = cmd.Wait()
		// Clean up descendants in the process group even when the serve
		// leader exits first (for example a background plugin installer).
		signalProcess(cmd, true)
		close(r.done)
	}()
	select {
	case r.endpoint = <-announced:
	case <-r.done:
		return errors.New("opencode serve exited before readiness (child output suppressed)")
	case <-ctx.Done():
		return fmt.Errorf("opencode readiness failed: %w", ctx.Err())
	}
	probe := New(domain.AgentSpec{StringOptions: map[string]string{"base_url": r.endpoint, "password": r.password}})
	probe.workspace = r.workspace
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var health struct {
			Healthy bool `json:"healthy"`
		}
		reqCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := probe.doJSON(reqCtx, http.MethodGet, "/global/health", nil, &health)
		cancel()
		if err == nil && health.Healthy {
			return nil
		}
		select {
		case <-r.done:
			return errors.New("opencode serve exited during readiness")
		case <-ctx.Done():
			return fmt.Errorf("opencode readiness failed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// Close is idempotent and never contacts or signals an external process.
func (r *Runtime) Close() {
	r.cancel() // interrupt startup before waiting for the startup mutex
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.stopLocked()
}

func (r *Runtime) stopLocked() {
	if r.cmd == nil {
		return
	}
	select {
	case <-r.done:
		return
	default:
	}
	signalProcess(r.cmd, false)
	timer := time.NewTimer(r.stopTimeout)
	defer timer.Stop()
	select {
	case <-r.done:
		return
	case <-timer.C:
	}
	signalProcess(r.cmd, true)
	<-r.done
}

// announcementWriter discards all diagnostics. It retains only a bounded line
// and accepts only the exact v1 serve announcement for a loopback address.
type announcementWriter struct {
	pending  []byte
	dropping bool
	ready    chan string
}

func (w *announcementWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			i = len(p)
		}
		if !w.dropping {
			if len(w.pending)+i > 4096 {
				w.pending = nil
				w.dropping = true
			} else {
				w.pending = append(w.pending, p[:i]...)
			}
		}
		if i == len(p) {
			break
		}
		if !w.dropping {
			line := strings.TrimSuffix(string(w.pending), "\r")
			const prefix = "opencode server listening on "
			if strings.HasPrefix(line, prefix) {
				endpoint := strings.TrimPrefix(line, prefix)
				u, err := url.Parse(endpoint)
				if err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" {
					port, err := strconv.Atoi(u.Port())
					if err == nil && port > 0 && port <= 65535 {
						select {
						case w.ready <- endpoint:
						default:
						}
					}
				}
			}
		}
		w.pending = nil
		w.dropping = false
		p = p[i+1:]
	}
	return n, nil
}
