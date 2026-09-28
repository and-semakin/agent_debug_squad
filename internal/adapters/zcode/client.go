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

	"github.com/and-semakin/agent_debug_squad/internal/procgroup"
)

const maxFrame = 8 * 1024 * 1024
const rpcTimeout = 15 * time.Second

// The duplex client services reverse requests throughout startup,
// create/resume, subscribe, send, polling, and shutdown: a dedicated
// dispatcher consumes inbound requests/notifications so an ordinary pending
// RPC never blocks host policy. Queues are bounded and overflow fails
// visibly; a completion or permission event is never silently dropped.
const (
	// eventQueueBound bounds session events awaiting the run loop.
	eventQueueBound = 256
	// requestQueueBound bounds inbound host requests awaiting dispatch.
	requestQueueBound = 64
)

type wireMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
	// seq is the transport-local order of a queued reverse request; it is not
	// part of the wire format.
	seq uint64
}
type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *wireError) Error() string { return e.Message }

// wireProbeError preserves the wire error code for the startup probes and
// any other typed handling.
type wireProbeError = wireError

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
	events  chan wireMessage
	// requests carries inbound host requests/notifications to the dispatcher.
	requests    chan wireMessage
	handler     func(wireMessage)
	handlerOnce sync.Once
	readers     sync.WaitGroup
	// inboundSeq counts queued reverse requests and dispatchedSeq tracks how
	// many the dispatcher has finished. A response that arrived after N
	// queued requests is only delivered once the dispatcher caught up, so a
	// bootstrap association is always reconciled against the create/resume
	// result it preceded.
	inboundSeq    uint64
	dispatchedSeq uint64
	dispatchStep  chan struct{}
}

// setHandler installs the reverse-request dispatcher target. It must be
// called before the first RPC so create/resume preferences are serviced.
func (c *client) setHandler(handler func(wireMessage)) {
	c.handlerOnce.Do(func() {
		c.handler = handler
		go c.dispatchLoop()
	})
}

// dispatchLoop services inbound requests for the life of the transport, never
// waiting behind an ordinary RPC. Overflow fails the run visibly instead of
// dropping a control event.
func (c *client) dispatchLoop() {
	for {
		select {
		case msg := <-c.requests:
			if c.handler != nil {
				c.handler(msg)
			}
			c.mu.Lock()
			c.dispatchedSeq = msg.seq
			c.mu.Unlock()
			select {
			case c.dispatchStep <- struct{}{}:
			default:
			}
		case <-c.done:
			return
		}
	}
}

// awaitDispatched blocks until every reverse request queued before the given
// sequence has been dispatched, or the context ends. It bounds response
// delivery, never request handling.
func (c *client) awaitDispatched(ctx context.Context, seq uint64) error {
	for {
		c.mu.Lock()
		dispatched := c.dispatchedSeq
		c.mu.Unlock()
		if dispatched >= seq {
			return nil
		}
		select {
		case <-c.dispatchStep:
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return c.failure()
		}
	}
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
	procgroup.Prepare(cmd)
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	c := &client{cmd: cmd, input: in, pending: map[string]chan wireMessage{}, done: make(chan struct{}), exited: make(chan struct{}), events: make(chan wireMessage, eventQueueBound), requests: make(chan wireMessage, requestQueueBound), dispatchStep: make(chan struct{}, 1)}
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
				msg.seq = c.inboundSeq
				c.mu.Unlock()
				if ch != nil {
					select {
					case ch <- msg:
					default:
					}
				}
			} else if msg.Method == "session/event" || msg.Method == "interaction/requestPermission" {
				// Turn events and permission requests share one FIFO so the
				// run loop observes the wire order: a permission for a turn is
				// never evaluated before its turn.started event.
				select {
				case c.events <- msg:
				case <-c.done:
					return
				}
			} else if msg.Method != "" {
				// Inbound requests and notifications go to the dispatcher; an
				// overfull queue is a hard failure, never a dropped request.
				c.mu.Lock()
				c.inboundSeq++
				msg.seq = c.inboundSeq
				c.mu.Unlock()
				select {
				case c.requests <- msg:
				case <-c.done:
					return
				default:
					c.fail(errors.New("zcode host request queue overflow"))
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
		if err := c.awaitDispatched(ctx, msg.seq); err != nil {
			return err
		}
		return callResult(method, msg, result)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		// A final response may have arrived immediately before EOF.
		select {
		case msg := <-ch:
			if err := c.awaitDispatched(ctx, msg.seq); err != nil {
				return err
			}
			return callResult(method, msg, result)
		default:
			return c.failure()
		}
	}
}

// callResult converts a response into either the decoded result or the typed
// wire error so probes and eligibility logic can inspect codes safely.
func callResult(method string, msg wireMessage, result any) error {
	if msg.Error != nil {
		return &wireProbeError{Code: msg.Error.Code, Message: msg.Error.Message}
	}
	if result != nil {
		if err := json.Unmarshal(msg.Result, result); err != nil {
			return fmt.Errorf("zcode %s result: %w", method, err)
		}
	}
	return nil
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
	procgroup.Kill(c.cmd)
	select {
	case <-c.exited:
	case <-time.After(2 * time.Second):
	}
}
