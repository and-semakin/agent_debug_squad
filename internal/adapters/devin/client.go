package devin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/procgroup"
)

const maxFrame = 8 << 20
const rpcTimeout = 30 * time.Second

type message struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Backend free-form errors may contain secrets. Never return their message.
func (e *rpcError) Error() string {
	if e.Code == -32000 {
		return "devin: authentication required; run devin auth login separately"
	}
	switch e.Code {
	case -32015:
		return "devin: saved session is locked by another process"
	case -32002:
		return "devin: saved session unavailable; explicit reset required"
	case -32800:
		return "devin: native request cancelled"
	}
	return "devin: RPC request failed"
}

type client struct {
	cmd       *exec.Cmd
	in        *os.File
	out       *os.File
	stderr    *os.File
	frames    chan message
	failure   chan error
	stop      chan struct{}
	exited    chan error
	readers   sync.WaitGroup
	writeMu   sync.Mutex
	seq       int
	closeOnce sync.Once
	closeErr  error
}

func startClient(cmd *exec.Cmd) (*client, error) {
	ir, iw, e := os.Pipe()
	if e != nil {
		return nil, errors.New("devin: input pipe unavailable")
	}
	or, ow, e := os.Pipe()
	if e != nil {
		ir.Close()
		iw.Close()
		return nil, errors.New("devin: output pipe unavailable")
	}
	er, ew, e := os.Pipe()
	if e != nil {
		ir.Close()
		iw.Close()
		or.Close()
		ow.Close()
		return nil, errors.New("devin: diagnostic pipe unavailable")
	}
	cmd.Stdin = ir
	cmd.Stdout = ow
	cmd.Stderr = ew
	procgroup.Prepare(cmd)
	if e = cmd.Start(); e != nil {
		ir.Close()
		iw.Close()
		or.Close()
		ow.Close()
		er.Close()
		ew.Close()
		return nil, errors.New("devin: process start failed")
	}
	ir.Close()
	ow.Close()
	ew.Close()
	c := &client{cmd: cmd, in: iw, out: or, stderr: er, frames: make(chan message, 32), failure: make(chan error, 1), stop: make(chan struct{}), exited: make(chan error, 1)}
	c.readers.Add(2)
	go func() { defer c.readers.Done(); _, _ = io.Copy(io.Discard, er) }()
	go func() {
		defer c.readers.Done()
		s := bufio.NewScanner(or)
		s.Buffer(make([]byte, 64*1024), maxFrame)
		for s.Scan() {
			select {
			case <-c.stop:
				continue
			default:
			}
			var m message
			if json.Unmarshal(s.Bytes(), &m) != nil || m.JSONRPC != "2.0" {
				c.fail(errors.New("devin: malformed protocol frame"))
				return
			}
			select {
			case c.frames <- m:
			case <-c.stop:
				// Keep draining during graceful shutdown; a native writer
				// must not block just because the turn reader has stopped.
			}
		}
		if s.Err() != nil {
			c.fail(errors.New("devin: invalid or oversized protocol stream"))
		} else {
			c.fail(io.EOF)
		}
	}()
	go func() { c.exited <- cmd.Wait() }()
	return c, nil
}
func (c *client) fail(e error) {
	select {
	case c.failure <- e:
	default:
	}
}
func (c *client) write(ctx context.Context, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return errors.New("devin: cannot encode request")
	}
	if len(b) > maxFrame {
		return errors.New("devin: request exceeds frame limit")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.in.SetWriteDeadline(deadline)
	if _, e = c.in.Write(append(b, '\n')); e != nil {
		return errors.New("devin: protocol write failed")
	}
	return nil
}
func (c *client) notify(ctx context.Context, method string, params any) error {
	return c.write(ctx, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (c *client) call(ctx context.Context, method string, params any, handle func(message) error) (json.RawMessage, error) {
	c.seq++
	id := c.seq
	if e := c.write(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); e != nil {
		return nil, e
	}
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var m message
		// Drain already-read frames before observing EOF: completion can precede it.
		select {
		case m = <-c.frames:
		default:
			select {
			case m = <-c.frames:
			case e := <-c.failure:
				select {
				case m = <-c.frames:
					c.fail(e)
				default:
					return nil, e
				}
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if m.Method != "" {
			if handle != nil {
				if e := handle(m); e != nil {
					return nil, e
				}
			} else if len(m.ID) > 0 {
				return nil, errors.New("devin: unsupported startup interaction")
			}
			continue
		}
		var got int
		if json.Unmarshal(m.ID, &got) != nil || got != id {
			return nil, errors.New("devin: unexpected RPC response")
		}
		if m.Error != nil {
			return nil, m.Error
		}
		if len(m.Result) == 0 {
			return nil, errors.New("devin: missing RPC result")
		}
		return m.Result, nil
	}
}
func (c *client) close() error {
	c.closeOnce.Do(func() {
		close(c.stop)
		c.in.Close()
		select {
		case exitErr := <-c.exited:
			if exitErr != nil {
				c.closeErr = errors.New("devin: process exited unsuccessfully during cleanup")
			}
		case <-time.After(2 * time.Second):
			c.closeErr = errors.New("devin: forced process teardown; native cleanup uncertain")
			procgroup.Kill(c.cmd)
			select {
			case <-c.exited:
			case <-time.After(time.Second):
				c.closeErr = errors.New("devin: process cleanup incomplete")
			}
		}
		// Descendants may retain descriptors even after a clean leader exit.
		procgroup.Kill(c.cmd)
		c.out.Close()
		c.stderr.Close()
		done := make(chan struct{})
		go func() { c.readers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			c.closeErr = errors.New("devin: reader cleanup incomplete")
		}
	})
	return c.closeErr
}
