package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/olliecrow/multicodex/internal/codexstate"
)

const (
	defaultCommand          = "codex"
	defaultNotificationSize = 256
	// Allow worst-case JSON escaping of the documented 4 MiB generation prompt.
	maxMessageBytes = 32 * 1024 * 1024
	shutdownTimeout = 2 * time.Second
	// writeQueueSize bounds outbound messages waiting for the single writer.
	writeQueueSize = 8
)

type ErrorSanitizer func(method string, code int, message string) error

type Config struct {
	Command        []string
	GlobalArgs     []string
	BaseEnv        []string
	CodexHome      string
	ActiveProfile  string
	ClientName     string
	ClientVersion  string
	CaptureEvents  bool
	ErrorSanitizer ErrorSanitizer
}

type Event struct {
	Method        string
	Params        json.RawMessage
	ServerRequest bool
}

// Client owns one App Server connection. A single writer goroutine sends
// every outbound message from a bounded queue. Canceling a request after its
// message is queued ends the connection, because a partly written message
// would corrupt the stream; other pending requests then fail with safe errors.
type Client struct {
	mu sync.Mutex

	config  Config
	cmd     *exec.Cmd
	stdin   io.Closer
	stdout  io.Closer
	started bool

	pending map[int]chan message
	nextID  int

	writes     chan outbound
	writerDone chan struct{}
	// beforeWrite lets tests observe that a write is in progress.
	beforeWrite func()

	events   chan Event
	done     chan struct{}
	stopping chan struct{}
	stopOnce sync.Once
	failErr  error
	doneErr  error
}

type outbound struct {
	payload []byte
	// result receives the write outcome; nil for fire-and-forget responses.
	result chan error
}

type request struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *wireError      `json:"error,omitempty"`
}

type message struct {
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

type initializeParams struct {
	ClientInfo   clientInfo     `json:"clientInfo"`
	Capabilities map[string]any `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func New(config Config) *Client {
	if len(config.Command) == 0 {
		config.Command = []string{defaultCommand}
	}
	client := &Client{
		config:     config,
		pending:    make(map[int]chan message),
		writes:     make(chan outbound, writeQueueSize),
		writerDone: make(chan struct{}),
		done:       make(chan struct{}),
		stopping:   make(chan struct{}),
	}
	if config.CaptureEvents {
		client.events = make(chan Event, defaultNotificationSize)
	}
	return client
}

func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		return errors.New("app-server already started")
	}

	args := append([]string{}, c.config.Command[1:]...)
	args = append(args, c.config.GlobalArgs...)
	args = append(args, "app-server")
	cmd := exec.Command(c.config.Command[0], args...)
	baseEnv := c.config.BaseEnv
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	env := codexstate.SanitizedEnv(baseEnv, strings.TrimSpace(c.config.CodexHome))
	if profile := strings.TrimSpace(c.config.ActiveProfile); profile != "" {
		env = append(env, "MULTICODEX_ACTIVE_PROFILE="+profile)
	}
	cmd.Env = env
	// Descendants stay in this group, so Close can stop them with the child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open app-server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("start codex app-server: %w", err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	c.started = true
	go c.writeLoop(stdin)
	go c.readLoop(stdout)
	return nil
}

func (c *Client) Initialize(ctx context.Context) error {
	name := strings.TrimSpace(c.config.ClientName)
	if name == "" {
		name = "multicodex"
	}
	version := strings.TrimSpace(c.config.ClientVersion)
	if version == "" {
		version = "unknown"
	}
	var result map[string]any
	if err := c.Request(ctx, "initialize", initializeParams{
		ClientInfo:   clientInfo{Name: name, Version: version},
		Capabilities: map[string]any{},
	}, &result); err != nil {
		return err
	}
	return c.Notify(ctx, "initialized", map[string]any{})
}

func (c *Client) Request(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	if c.cmd == nil {
		c.mu.Unlock()
		return errors.New("app-server is not running")
	}
	c.nextID++
	id := c.nextID
	response := make(chan message, 1)
	c.pending[id] = response
	done := c.done
	c.mu.Unlock()

	payload, err := encodeMessage(request{
		ID:     json.RawMessage(fmt.Sprintf("%d", id)),
		Method: method,
		Params: params,
	})
	if err == nil {
		err = c.send(ctx, payload)
	}
	if err != nil {
		c.removePending(id)
		return fmt.Errorf("send app-server request %s: %w", method, err)
	}

	select {
	case msg, ok := <-response:
		if !ok {
			return fmt.Errorf("app-server request %s aborted: %w", method, c.Err())
		}
		if msg.Error != nil {
			if sanitize := c.config.ErrorSanitizer; sanitize != nil {
				return sanitize(method, msg.Error.Code, msg.Error.Message)
			}
			return safeRPCError(method, msg.Error)
		}
		if out != nil {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return fmt.Errorf("decode app-server %s response: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		return fmt.Errorf("app-server %s canceled: %w", method, ctx.Err())
	case <-done:
		c.removePending(id)
		return fmt.Errorf("app-server %s failed: %w", method, c.Err())
	}
}

func (c *Client) Notify(ctx context.Context, method string, params any) error {
	payload, err := encodeMessage(request{Method: method, Params: params})
	if err == nil {
		err = c.send(ctx, payload)
	}
	if err != nil {
		return fmt.Errorf("send app-server notification %s: %w", method, err)
	}
	return nil
}

// send queues one encoded message for the writer and waits for its outcome.
// Cancellation before queueing writes nothing. Cancellation after queueing
// ends the connection so no later message follows a partial write.
func (c *Client) send(ctx context.Context, payload []byte) error {
	c.mu.Lock()
	running := c.cmd != nil
	c.mu.Unlock()
	if !running {
		return errors.New("app-server is not running")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result := make(chan error, 1)
	select {
	case c.writes <- outbound{payload: payload, result: result}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.stopping:
		return errors.New("app-server connection is closed")
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		c.fail(errors.New("app-server write canceled"))
		return ctx.Err()
	case <-c.stopping:
		return errors.New("app-server connection is closed")
	}
}

func encodeMessage(value request) ([]byte, error) {
	value.JSONRPC = "2.0"
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func (c *Client) writeLoop(stdin io.Writer) {
	defer close(c.writerDone)
	for {
		select {
		case item := <-c.writes:
			if c.beforeWrite != nil {
				c.beforeWrite()
			}
			_, err := stdin.Write(item.payload)
			if item.result != nil {
				item.result <- err
			}
			if err != nil {
				c.fail(errors.New("app-server input closed"))
				return
			}
		case <-c.stopping:
			return
		}
	}
}

func (c *Client) Events() <-chan Event {
	return c.events
}

func (c *Client) Done() <-chan struct{} {
	return c.done
}

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.doneErr != nil {
		return c.doneErr
	}
	return errors.New("app-server exited")
}

func (c *Client) Close() error {
	c.mu.Lock()
	started := c.started
	c.mu.Unlock()
	if !started {
		return nil
	}
	c.fail(errors.New("app-server closed"))
	select {
	case <-c.done:
		return nil
	case <-time.After(shutdownTimeout):
		return errors.New("timeout waiting for app-server shutdown")
	}
}

func (c *Client) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	for scanner.Scan() {
		var msg message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Method != "" {
			serverRequest := len(msg.ID) != 0
			if serverRequest {
				if err := c.denyServerRequest(msg.ID); err != nil {
					c.terminate(err)
					return
				}
			}
			if !c.emit(Event{Method: msg.Method, Params: msg.Params, ServerRequest: serverRequest}) {
				c.terminate(errors.New("app-server event delivery stopped"))
				return
			}
			continue
		}
		id, ok := integerID(msg.ID)
		if !ok {
			continue
		}
		c.mu.Lock()
		response := c.pending[id]
		if response != nil {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if response != nil {
			response <- msg
			close(response)
		}
	}

	streamErr := scanner.Err()
	if streamErr == nil {
		streamErr = errors.New("app-server stream closed")
	}
	c.terminate(streamErr)
}

// denyServerRequest queues a fixed denial. A peer that floods requests faster
// than the writer drains them ends the connection.
func (c *Client) denyServerRequest(id json.RawMessage) error {
	payload, err := encodeMessage(request{
		ID: id,
		Error: &wireError{
			Code:    -32000,
			Message: "client does not permit server requests",
		},
	})
	if err != nil {
		return errors.New("encode app-server request denial")
	}
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case c.writes <- outbound{payload: payload}:
		return nil
	case <-c.stopping:
		return errors.New("app-server connection is closed")
	case <-timer.C:
		return errors.New("app-server sent server requests faster than they could be denied")
	}
}

func (c *Client) emit(event Event) bool {
	if c.events == nil {
		return true
	}
	select {
	case c.events <- event:
		return true
	case <-c.stopping:
		return false
	}
}

func (c *Client) terminate(err error) {
	c.fail(err)
	c.finish()
}

// fail records the first connection error, stops delivery, closes both pipes,
// and kills the child process group. Closing stdout unblocks the read loop
// when a descendant still holds the write end.
func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.failErr == nil {
		c.failErr = err
	}
	cmd := c.cmd
	stdin := c.stdin
	stdout := c.stdout
	c.stdout = nil
	c.mu.Unlock()
	c.stop()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil && cmd.Process.Pid > 1 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if stdout != nil {
		_ = stdout.Close()
	}
}

func (c *Client) stop() {
	c.stopOnce.Do(func() { close(c.stopping) })
}

// finish releases the connection. Only the read loop calls it, so the events
// channel is closed by its only sender.
func (c *Client) finish() {
	c.mu.Lock()
	if c.cmd == nil {
		c.mu.Unlock()
		return
	}
	for id, response := range c.pending {
		delete(c.pending, id)
		close(response)
	}
	cmd := c.cmd
	c.cmd = nil
	c.stdin = nil
	c.mu.Unlock()

	_ = cmd.Wait()
	<-c.writerDone

	c.mu.Lock()
	c.doneErr = c.failErr
	c.mu.Unlock()
	if c.events != nil {
		close(c.events)
	}
	close(c.done)
}

func (c *Client) removePending(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func integerID(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var id int
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, false
	}
	return id, true
}

func safeRPCError(method string, rpcErr *wireError) error {
	message := fmt.Sprintf("app-server %s failed with RPC code %d", method, rpcErr.Code)
	lower := strings.ToLower(rpcErr.Message)
	switch {
	case strings.Contains(lower, "token_expired"), strings.Contains(lower, "authentication token is expired"):
		message += ": authentication expired; sign in again"
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "invalid token"):
		message += ": authentication rejected; sign in again"
	}
	return errors.New(message)
}
