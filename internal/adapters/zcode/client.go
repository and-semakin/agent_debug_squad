package zcode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const maxFrame = 8 * 1024 * 1024
const rpcTimeout = 15 * time.Second

type wireMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}
type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type client struct {
	cmd     *exec.Cmd
	input   io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	next    int
	pending map[string]chan wireMessage
	err     error
	done    chan struct{}
	exited  chan struct{}
	inbox   chan wireMessage
	readers sync.WaitGroup
}

func startClient(cmd *exec.Cmd, stderr func(string)) (*client, error) {
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	errOut, err := cmd.StderrPipe()
	if err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	prepareProcess(cmd)
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	c := &client{cmd: cmd, input: in, pending: map[string]chan wireMessage{}, done: make(chan struct{}), exited: make(chan struct{}), inbox: make(chan wireMessage, 256)}
	c.readers.Add(2)
	go func() {
		defer c.readers.Done()
		scanner := bufio.NewScanner(errOut)
		scanner.Buffer(make([]byte, 65536), maxFrame)
		for scanner.Scan() {
			stderr(scanner.Text())
		}
		if scanner.Err() != nil {
			c.fail(errors.New("zcode stderr frame exceeded limit or stream failed"))
		}
	}()
	go func() {
		defer c.readers.Done()
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 65536), maxFrame)
		for scanner.Scan() {
			var msg wireMessage
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				c.fail(errors.New("malformed zcode protocol frame"))
				return
			}
			if len(msg.ID) > 0 && msg.Method == "" {
				c.mu.Lock()
				ch := c.pending[string(msg.ID)]
				c.mu.Unlock()
				if ch != nil {
					select {
					case ch <- msg:
					default:
					}
				}
			} else if msg.Method != "" {
				select {
				case c.inbox <- msg:
				case <-c.done:
					return
				default:
					c.fail(errors.New("zcode event queue overflow"))
					return
				}
			} else {
				c.fail(errors.New("invalid zcode protocol envelope"))
				return
			}
		}
		if scanner.Err() != nil {
			c.fail(errors.New("zcode protocol frame exceeded limit or stream failed"))
		} else {
			c.fail(errors.New("zcode app-server closed its output"))
		}
	}()
	go func() { c.readers.Wait(); _ = cmd.Wait(); close(c.exited) }()
	return c, nil
}
func (c *client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
		close(c.done)
	}
}
func (c *client) failure() error { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *client) write(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// Child pipe writes are small control frames or a bounded prompt. Closing the
	// process group on shutdown unblocks a peer that stops reading.
	err := json.NewEncoder(c.input).Encode(value)
	if err != nil {
		c.fail(fmt.Errorf("zcode protocol write: %w", err))
	}
	return err
}

// Host replies have no acknowledgement in App Server. Bound pipe submission;
// a failed submission closes this run's transport rather than retrying approval.
func (c *client) respond(ctx context.Context, value any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	written := make(chan error, 1)
	go func() { written <- c.write(value) }()
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
		c.fail(ctx.Err())
		return ctx.Err()
	case <-c.done:
		return c.failure()
	}
}

func (c *client) call(ctx context.Context, method string, params any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.next++
	id := fmt.Sprintf("squad-%d", c.next)
	raw, _ := json.Marshal(id)
	ch := make(chan wireMessage, 1)
	c.pending[string(raw)] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, string(raw)); c.mu.Unlock() }()
	// Writes also need a deadline when a broken server leaves stdin unread.
	written := make(chan error, 1)
	go func() { written <- c.write(map[string]any{"id": id, "method": method, "params": params}) }()
	select {
	case err := <-written:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.failure()
	}
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("zcode %s: %s", method, msg.Error.Message)
		}
		if result != nil {
			if err := json.Unmarshal(msg.Result, result); err != nil {
				return fmt.Errorf("zcode %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		// A final response may have arrived immediately before EOF.
		select {
		case msg := <-ch:
			if msg.Error != nil {
				return fmt.Errorf("zcode %s: %s", method, msg.Error.Message)
			}
			if result != nil {
				return json.Unmarshal(msg.Result, result)
			}
			return nil
		default:
			return c.failure()
		}
	}
}
func (c *client) close() {
	c.fail(errors.New("zcode client closed"))
	_ = c.input.Close()
	select {
	case <-c.exited:
	case <-time.After(500 * time.Millisecond):
	}
	// Kill the owned group even if the group leader already exited: tool children
	// can retain inherited descriptors and otherwise survive their parent.
	killProcess(c.cmd)
	select {
	case <-c.exited:
	case <-time.After(2 * time.Second):
	}
}
