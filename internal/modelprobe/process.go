package modelprobe

import (
	"bufio"
	"context"
	"errors"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
	"github.com/and-semakin/agent_debug_squad/internal/procgroup"
	"io"
	"os/exec"
	"sync"
	"time"
)

var ErrLimit = errors.New("discovery_limit")

// Process owns pipes and the complete process group. No raw stderr leaves it.
type Process struct {
	cmd         *exec.Cmd
	In          io.WriteCloser
	Out         *bufio.Scanner
	total       int
	stop        context.CancelFunc
	once        sync.Once
	waitErr     error
	diagnostics *privateDiagnostics
}

func Start(ctx context.Context, command string, args, env []string, workspace string) (*Process, error) {
	resolved, issue := preflight.ResolveCommand(command, env, workspace)
	if issue != nil {
		return nil, errors.New("executable_unavailable")
	}
	owned, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(owned, resolved, args...)
	cmd.Dir = workspace
	cmd.Env = env
	diagnostics := &privateDiagnostics{}
	cmd.Stderr = diagnostics
	procgroup.Prepare(cmd)
	cmd.Cancel = func() error { procgroup.Kill(cmd); return nil }
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		cancel()
		return nil, err
	}
	p := &Process{cmd: cmd, In: in, Out: bufio.NewScanner(out), stop: cancel, diagnostics: diagnostics}
	p.Out.Buffer(make([]byte, 4096), MaxFrame+1)
	// Wait is called only after reading stdout (Close), as required by StdoutPipe.
	return p, nil
}
func (p *Process) Read() ([]byte, error) {
	if !p.Out.Scan() {
		if e := p.Out.Err(); e != nil {
			return nil, ErrLimit
		}
		return nil, io.EOF
	}
	b := append([]byte(nil), p.Out.Bytes()...)
	p.total += len(b) + 1
	if len(b) > MaxFrame || p.total > MaxBytes {
		return nil, ErrLimit
	}
	return b, nil
}
func (p *Process) Close() error {
	p.once.Do(func() { p.In.Close(); p.stop(); procgroup.Kill(p.cmd); p.waitErr = p.cmd.Wait() })
	return p.waitErr
}

// Collect bounds both whole output and individual lines, and always reaps descendants.
func Collect(ctx context.Context, command string, args, env []string, workspace string) ([]byte, error) {
	p, e := Start(ctx, command, args, env, workspace)
	if e != nil {
		return nil, e
	}
	defer p.Close()
	p.In.Close()
	var b []byte
	for {
		line, err := p.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return b, err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	// Child has closed stdout; wait briefly under context, including inherited descriptors.
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case e = <-done:
	case <-ctx.Done():
		p.stop()
		procgroup.Kill(p.cmd)
		e = <-done
	}
	p.once.Do(func() { p.stop(); procgroup.Kill(p.cmd); p.waitErr = e })
	if ctx.Err() != nil {
		return b, ctx.Err()
	}
	if e != nil {
		return b, &CommandFailure{Status: p.diagnostics.status()}
	}
	return b, nil
}
