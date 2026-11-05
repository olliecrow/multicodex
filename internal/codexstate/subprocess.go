package codexstate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrOutputLimit reports that a subprocess wrote more than its output budget.
var ErrOutputLimit = errors.New("subprocess output limit exceeded")

// CappedOutput collects at most a fixed number of subprocess output bytes. On
// overflow it calls stop and rejects later writes, so a child cannot grow
// memory use without bound.
type CappedOutput struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	exceeded bool
	stop     func()
}

func NewCappedOutput(limit int, stop func()) *CappedOutput {
	return &CappedOutput{limit: limit, stop: stop}
}

func (c *CappedOutput) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exceeded {
		return 0, ErrOutputLimit
	}
	if room := c.limit - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:room])
		c.exceeded = true
		if c.stop != nil {
			c.stop()
		}
		return room, ErrOutputLimit
	}
	return c.buf.Write(p)
}

func (c *CappedOutput) Exceeded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exceeded
}

func (c *CappedOutput) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

const versionOutputLimit = 64 << 10

var versionLinePattern = regexp.MustCompile(`^[A-Za-z0-9._+ -]{1,80}$`)

// CodexVersion runs a bounded `codex --version` without account-scoped
// environment. It returns only a version-shaped first line; errors carry fixed
// text and never raw subprocess output.
func CodexVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "--version")
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Env = SanitizedEnv(os.Environ(), "")
	out := NewCappedOutput(versionOutputLimit, cancel)
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	switch {
	case out.Exceeded():
		return "", errors.New("codex --version output exceeded 64 KiB")
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", errors.New("codex --version timed out")
	case err != nil:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("codex --version failed with exit code %d", exitErr.ExitCode())
		}
		return "", errors.New("codex --version could not be started")
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return "version output is empty", nil
	}
	if !versionLinePattern.MatchString(line) {
		return "", errors.New("codex --version output is not recognized")
	}
	return line, nil
}
