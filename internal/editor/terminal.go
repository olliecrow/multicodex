package editor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type Attachment struct {
	cmd           *exec.Cmd
	pty           *os.File
	ptyFD         int
	terminal      *vt.SafeEmulator
	emulatorMu    sync.Mutex
	ptyWriteMu    sync.Mutex
	done          chan struct{}
	updates       chan struct{}
	responsesDone chan struct{}
	responseMu    sync.Mutex
	responseErr   error
	inputPipe     io.WriteCloser
	inputQueue    chan terminalInput
	inputDone     chan struct{}
	inputMu       sync.Mutex
	inputClosed   bool
	closing       chan struct{}
	closeOnce     sync.Once
}

type terminalInput struct {
	kind string
	key  tea.KeyPressMsg
	text string
}

const maxTerminalPaste = 1 << 20

func attachWindowPTY(ctx context.Context, host Host, controlPath, instanceID string, window Window, width, height int) (*Attachment, error) {
	if width < 1 || height < 1 {
		return nil, errors.New("terminal size must be positive")
	}
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return nil, err
	}
	if err := validateID(window.ID, "window identifier"); err != nil {
		return nil, err
	}
	args, err := tmuxAttachArgs(instanceID, window)
	if err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	if host.ID == localHostID {
		cmd = exec.CommandContext(ctx, "tmux", args...)
	} else {
		if err := validateSSHAlias(host.SSHAlias); err != nil {
			return nil, err
		}
		if controlPath == "" || !filepath.IsAbs(controlPath) {
			return nil, errors.New("remote terminal is missing its private SSH control path")
		}
		sshArgs := append([]string{"-tt"}, sshConnectionOptions("no", "", controlPath)...)
		sshArgs = append(sshArgs, host.SSHAlias)
		remoteArgs := append([]string{"env", "-u", "TMUX", "-u", "TMUX_TMPDIR", "tmux"}, args...)
		for _, arg := range remoteArgs {
			sshArgs = append(sshArgs, quoteRemoteShellArg(arg))
		}
		cmd = exec.CommandContext(ctx, "ssh", sshArgs...)
	}
	cmd.Env = replaceEnvironment(sanitizedCommandEnvironment(os.Environ(), cmd.Path), "TERM", "xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		if host.ID == localHostID {
			return nil, errors.New("attach to local tmux window")
		}
		return nil, fmt.Errorf("attach to tmux window on SSH host %q", host.Name)
	}
	if err := syscall.SetNonblock(int(ptmx.Fd()), true); err != nil {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		return nil, errors.New("prepare non-blocking terminal attachment")
	}
	terminal := vt.NewSafeEmulator(width, height)
	// Tmux is the only scrollback owner. Keep the renderer's private history at
	// one line so sustained output does not duplicate the long tmux scrollback.
	terminal.SetScrollbackSize(1)
	inputPipe, ok := terminal.InputPipe().(io.WriteCloser)
	if !ok {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		return nil, errors.New("prepare terminal input pipe")
	}
	a := &Attachment{
		cmd: cmd, pty: ptmx, ptyFD: int(ptmx.Fd()), terminal: terminal,
		done: make(chan struct{}), updates: make(chan struct{}, 1), responsesDone: make(chan struct{}),
		inputPipe: inputPipe, inputQueue: make(chan terminalInput, 16), inputDone: make(chan struct{}), closing: make(chan struct{}),
	}
	go a.runInput()
	go func() {
		buffer := make([]byte, 32<<10)
		poll := []unix.PollFd{{Fd: int32(a.ptyFD), Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}
		for {
			select {
			case <-a.closing:
				close(a.updates)
				close(a.done)
				return
			default:
			}
			ready, pollErr := unix.Poll(poll, 250)
			if pollErr != nil && !errors.Is(pollErr, syscall.EINTR) {
				break
			}
			if ready == 0 || pollErr != nil {
				continue
			}
			n, readErr := ptmx.Read(buffer)
			if n > 0 {
				a.emulatorMu.Lock()
				_, _ = terminal.Write(buffer[:n])
				a.emulatorMu.Unlock()
				select {
				case a.updates <- struct{}{}:
				default:
				}
			}
			if readErr != nil {
				if errors.Is(readErr, syscall.EAGAIN) || errors.Is(readErr, syscall.EWOULDBLOCK) {
					continue
				}
				break
			}
		}
		close(a.updates)
		close(a.done)
	}()
	go func() {
		_, copyErr := io.Copy(attachmentPTYWriter{attachment: a}, terminal)
		a.responseMu.Lock()
		a.responseErr = copyErr
		a.responseMu.Unlock()
		close(a.responsesDone)
		if copyErr != nil {
			select {
			case <-a.closing:
			default:
				_ = a.Close()
			}
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = a.Close()
		case <-a.closing:
		}
	}()
	return a, nil
}

func tmuxAttachArgs(instanceID string, window Window) ([]string, error) {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return nil, err
	}
	if err := validateID(window.ID, "window identifier"); err != nil {
		return nil, err
	}
	if window.Adopted {
		if validateTmuxSessionName(window.Session) != nil || !tmuxIDPattern.MatchString(window.TmuxSessionID) {
			return nil, errors.New("refuse to attach to an unsafe adopted tmux session")
		}
		return []string{"-L", "default", "attach-session", "-t", window.TmuxSessionID}, nil
	}
	session := "mce-" + window.ID
	if window.Session != session {
		return nil, errors.New("refuse to attach to a non-deterministic tmux session")
	}
	return []string{"-L", "mce-" + instanceID[:12], "attach-session", "-t", session}, nil
}

func quoteRemoteShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (a *Attachment) Resize(width, height int) error {
	if width < 1 || height < 1 {
		return nil
	}
	a.emulatorMu.Lock()
	a.terminal.Resize(width, height)
	a.emulatorMu.Unlock()
	a.ptyWriteMu.Lock()
	err := pty.Setsize(a.pty, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	a.ptyWriteMu.Unlock()
	if err != nil {
		return errors.New("resize attached terminal")
	}
	return nil
}

func (a *Attachment) SendKey(key tea.KeyPressMsg) error {
	if isDirectTextKey(key) {
		return a.enqueueInput(terminalInput{kind: "text", text: key.Text})
	}
	if sequence, ok := modifiedKeySequence(key); ok {
		return a.enqueueInput(terminalInput{kind: "raw", text: sequence})
	}
	return a.enqueueInput(terminalInput{kind: "key", key: key})
}

func isDirectTextKey(key tea.KeyPressMsg) bool {
	return key.Text != "" && key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModHyper|tea.ModSuper) == 0
}

func (a *Attachment) Paste(text string) error {
	if len(text) > maxTerminalPaste {
		return fmt.Errorf("terminal paste exceeds %d MiB", maxTerminalPaste>>20)
	}
	return a.enqueueInput(terminalInput{kind: "paste", text: safeTerminalPaste(text)})
}

func safeTerminalPaste(text string) string {
	return strings.Map(func(value rune) rune {
		if value == '\t' || value == '\n' || value == '\r' {
			return value
		}
		if value < ' ' || value == '\x7f' || value >= '\x80' && value <= '\x9f' {
			return -1
		}
		return value
	}, text)
}

func (a *Attachment) SendFocus(focused bool) error {
	sequence := "\x1b[O"
	if focused {
		sequence = "\x1b[I"
	}
	return a.enqueueInput(terminalInput{kind: "raw", text: sequence})
}

func (a *Attachment) SendMouse(event tea.MouseMsg, x, y int) error {
	sequence, ok := terminalMouseSequence(event, x, y)
	if !ok {
		return nil
	}
	return a.enqueueInput(terminalInput{kind: "raw", text: sequence})
}

func terminalMouseSequence(event tea.MouseMsg, x, y int) (string, bool) {
	if x < 0 || y < 0 {
		return "", false
	}
	mouse := event.Mouse()
	code, final := 0, 'M'
	switch event.(type) {
	case tea.MouseClickMsg:
		switch mouse.Button {
		case tea.MouseLeft:
			code = 0
		case tea.MouseMiddle:
			code = 1
		case tea.MouseRight:
			code = 2
		default:
			return "", false
		}
	case tea.MouseReleaseMsg:
		final = 'm'
		switch mouse.Button {
		case tea.MouseLeft:
			code = 0
		case tea.MouseMiddle:
			code = 1
		case tea.MouseRight:
			code = 2
		default:
			return "", false
		}
	case tea.MouseWheelMsg:
		switch mouse.Button {
		case tea.MouseWheelUp:
			code = 64
		case tea.MouseWheelDown:
			code = 65
		case tea.MouseWheelLeft:
			code = 66
		case tea.MouseWheelRight:
			code = 67
		default:
			return "", false
		}
	default:
		return "", false
	}
	if mouse.Mod&tea.ModShift != 0 {
		code += 4
	}
	if mouse.Mod&(tea.ModAlt|tea.ModMeta) != 0 {
		code += 8
	}
	if mouse.Mod&tea.ModCtrl != 0 {
		code += 16
	}
	return fmt.Sprintf("\x1b[<%d;%d;%d%c", code, x+1, y+1, final), true
}

func (a *Attachment) enqueueInput(input terminalInput) error {
	a.inputMu.Lock()
	defer a.inputMu.Unlock()
	if a.inputClosed {
		return errors.New("terminal is disconnected")
	}
	select {
	case a.inputQueue <- input:
		return nil
	default:
		return errors.New("terminal input is busy; retry")
	}
}

func (a *Attachment) runInput() {
	defer close(a.inputDone)
	for {
		select {
		case <-a.closing:
			return
		case input := <-a.inputQueue:
			a.emulatorMu.Lock()
			switch input.kind {
			case "text":
				a.terminal.SendText(input.text)
			case "raw":
				_, _ = io.WriteString(a.inputPipe, input.text)
			case "key":
				a.terminal.SendKey(vt.KeyPressEvent(input.key))
			case "paste":
				a.terminal.Paste(input.text)
			}
			a.emulatorMu.Unlock()
		}
	}
}

func (a *Attachment) Render(width, height int) string {
	a.emulatorMu.Lock()
	defer a.emulatorMu.Unlock()
	return safeTerminalRender(a.terminal, width, height)
}

func (a *Attachment) CursorPosition() (int, int) {
	a.emulatorMu.Lock()
	defer a.emulatorMu.Unlock()
	position := a.terminal.CursorPosition()
	return position.X, position.Y
}

func (a *Attachment) Done() <-chan struct{} { return a.done }

func (a *Attachment) Updates() <-chan struct{} { return a.updates }

func (a *Attachment) inputError() error {
	a.responseMu.Lock()
	defer a.responseMu.Unlock()
	return a.responseErr
}

func (a *Attachment) Close() error {
	a.closeOnce.Do(func() {
		a.inputMu.Lock()
		a.inputClosed = true
		close(a.closing)
		a.inputMu.Unlock()
		_ = a.inputPipe.Close()
		<-a.inputDone
		a.ptyWriteMu.Lock()
		_ = a.pty.Close()
		a.ptyWriteMu.Unlock()
		select {
		case <-a.done:
		case <-time.After(500 * time.Millisecond):
			if a.cmd.Process != nil {
				_ = a.cmd.Process.Kill()
			}
		}
		select {
		case <-a.responsesDone:
		case <-time.After(500 * time.Millisecond):
		}
		waitDone := make(chan error, 1)
		go func() { waitDone <- a.cmd.Wait() }()
		select {
		case <-waitDone:
		case <-time.After(250 * time.Millisecond):
			if a.cmd.Process != nil {
				_ = a.cmd.Process.Kill()
			}
			<-waitDone
		}
		a.emulatorMu.Lock()
		_ = a.terminal.Close()
		a.emulatorMu.Unlock()
	})
	return nil
}

type attachmentPTYWriter struct {
	attachment *Attachment
}

func (w attachmentPTYWriter) Write(p []byte) (int, error) {
	a := w.attachment
	a.ptyWriteMu.Lock()
	defer a.ptyWriteMu.Unlock()
	written := 0
	for written < len(p) {
		select {
		case <-a.closing:
			return written, os.ErrClosed
		default:
		}
		n, err := unix.Write(a.ptyFD, p[written:])
		if n > 0 {
			written += n
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return written, err
		}
		poll := []unix.PollFd{{Fd: int32(a.ptyFD), Events: unix.POLLOUT | unix.POLLHUP | unix.POLLERR}}
		if _, err := unix.Poll(poll, 250); err != nil && !errors.Is(err, syscall.EINTR) {
			return written, err
		}
	}
	return written, nil
}

func safeTerminalRender(terminal *vt.SafeEmulator, width, height int) string {
	var out strings.Builder
	for y := 0; y < height; y++ {
		var previous uv.Style
		for x := 0; x < width; {
			cell := terminal.CellAt(x, y)
			if cell == nil || cell.Width == 0 {
				out.WriteString((&uv.Style{}).Diff(&previous))
				previous = uv.Style{}
				out.WriteByte(' ')
				x++
				continue
			}
			out.WriteString(cell.Style.Diff(&previous))
			previous = cell.Style
			content := strings.Map(func(r rune) rune {
				if r == '\x1b' || r == 0x7f || r >= 0x80 && r <= 0x9f || r < 0x20 && r != '\t' {
					return -1
				}
				return r
			}, cell.Content)
			if content == "" {
				content = " "
			}
			out.WriteString(content)
			x += max(1, cell.Width)
		}
		out.WriteString(ansi.ResetStyle)
		if y+1 < height {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func modifiedKeySequence(key tea.KeyPressMsg) (string, bool) {
	mod := 1
	if key.Mod&tea.ModShift != 0 {
		mod++
	}
	if key.Mod&tea.ModAlt != 0 {
		mod += 2
	}
	if key.Mod&tea.ModCtrl != 0 {
		mod += 4
	}
	if key.Mod&(tea.ModMeta|tea.ModHyper|tea.ModSuper) != 0 {
		mod += 8
	}
	if mod == 1 {
		return "", false
	}
	parameter := strconv.Itoa(mod)
	if key.Text != "" && key.Mod&(tea.ModShift|tea.ModMeta|tea.ModHyper|tea.ModSuper) != 0 {
		return "\x1b[" + strconv.Itoa(int(key.Code)) + ";" + parameter + "u", true
	}
	switch key.Code {
	case tea.KeyEnter:
		return "\x1b[13;" + parameter + "u", true
	case tea.KeyTab:
		if key.Mod == tea.ModShift {
			return "\x1b[Z", true
		}
		return "\x1b[9;" + parameter + "u", true
	case tea.KeyUp:
		return "\x1b[1;" + parameter + "A", true
	case tea.KeyDown:
		return "\x1b[1;" + parameter + "B", true
	case tea.KeyRight:
		return "\x1b[1;" + parameter + "C", true
	case tea.KeyLeft:
		return "\x1b[1;" + parameter + "D", true
	case tea.KeyHome:
		return "\x1b[1;" + parameter + "H", true
	case tea.KeyEnd:
		return "\x1b[1;" + parameter + "F", true
	case tea.KeyInsert:
		return "\x1b[2;" + parameter + "~", true
	case tea.KeyDelete:
		return "\x1b[3;" + parameter + "~", true
	case tea.KeyPgUp:
		return "\x1b[5;" + parameter + "~", true
	case tea.KeyPgDown:
		return "\x1b[6;" + parameter + "~", true
	case tea.KeyBackspace:
		return "\x1b[127;" + parameter + "u", true
	case tea.KeyEscape:
		return "\x1b[27;" + parameter + "u", true
	case tea.KeyF1:
		return "\x1b[1;" + parameter + "P", true
	case tea.KeyF2:
		return "\x1b[1;" + parameter + "Q", true
	case tea.KeyF3:
		return "\x1b[1;" + parameter + "R", true
	case tea.KeyF4:
		return "\x1b[1;" + parameter + "S", true
	case tea.KeyF5:
		return "\x1b[15;" + parameter + "~", true
	case tea.KeyF6:
		return "\x1b[17;" + parameter + "~", true
	case tea.KeyF7:
		return "\x1b[18;" + parameter + "~", true
	case tea.KeyF8:
		return "\x1b[19;" + parameter + "~", true
	case tea.KeyF9:
		return "\x1b[20;" + parameter + "~", true
	case tea.KeyF10:
		return "\x1b[21;" + parameter + "~", true
	case tea.KeyF11:
		return "\x1b[23;" + parameter + "~", true
	case tea.KeyF12:
		return "\x1b[24;" + parameter + "~", true
	}
	return "", false
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}
