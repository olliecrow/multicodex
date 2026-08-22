package editor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestHostProtocolRejectsUnknownFieldsAndMethods(t *testing.T) {
	home := privateTestHome(t)
	service, err := NewHostService(home, testInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- RunHostProtocol(context.Background(), service, server, server) }()
	reader := bufio.NewReader(client)
	requests := []struct {
		line string
		want string
	}{
		{"{\"id\":1,\"method\":\"inspect_project\",\"params\":{\"path\":\"/tmp\",\"extra\":true}}\n", "invalid editor host protocol parameters"},
		{"{\"id\":2,\"method\":\"unknown\"}\n", "unknown editor host protocol method"},
	}
	for _, request := range requests {
		if _, err := io.WriteString(client, request.line); err != nil {
			t.Fatal(err)
		}
		response, err := reader.ReadString('\n')
		if err != nil || !strings.Contains(response, request.want) {
			t.Fatalf("unexpected protocol response %q: %v", response, err)
		}
	}
	_ = client.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHostProtocolCancelsActiveRequestOnDisconnect(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	var once sync.Once
	service := &HostService{runner: runnerFunc(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})}
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- RunHostProtocol(context.Background(), service, server, server) }()
	request := fmt.Sprintf("{\"id\":1,\"method\":\"inspect_project\",\"params\":{\"path\":%q}}\n", t.TempDir())
	if _, err := io.WriteString(client, request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("host request did not start")
	}
	_ = client.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("host request was not canceled after transport EOF")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("host protocol did not stop after transport EOF")
	}
}

func TestSSHControlPathRejectsFilesAndCleansExactSockets(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "mce-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	hostID := mustID(t)
	instanceID := mustID(t)
	t.Cleanup(func() { _ = cleanupSSHControlPaths(home, instanceID) })
	path, err := prepareSSHControlPath(home, instanceID, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSSHControlPath(home, instanceID, hostID); err == nil {
		t.Fatal("expected a non-socket SSH control path to be rejected")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	unixListener := listener.(*net.UnixListener)
	unixListener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cleanupSSHControlPath(home, instanceID, hostID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SSH control socket remains: %v", err)
	}
}

func TestSSHRuntimeDirectoryRejectsSymlinkAndPrunesEmptyRoot(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	link := filepath.Join(parent, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := secureOwnedPrivateRuntimeDir(link); err == nil {
		t.Fatal("accepted a symlinked SSH runtime directory")
	}
	empty := filepath.Join(parent, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptySSHRuntimeRoot(empty); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(empty); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty SSH runtime root remains: %v", err)
	}
}

func TestSSHControlPathEscapesOpenSSHPercentTokens(t *testing.T) {
	path := "/tmp/mce-%h-%C.sock"
	option := sshControlPathOption(path)
	if strings.Contains(strings.ReplaceAll(option, "%%", ""), "%") {
		t.Fatalf("SSH control option contains an unescaped percent token: %q", option)
	}
	if got := strings.ReplaceAll(option, "%%", "%"); got != path {
		t.Fatalf("SSH control option resolves to %q, want %q", got, path)
	}
}

func TestSSHControlPathSupportsADeepDefaultStyleHome(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "mce-long-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	componentLength := 50 - len(root) - 1
	if componentLength < 1 {
		t.Fatalf("temporary root is too long for this boundary test: %q", root)
	}
	home := filepath.Join(root, strings.Repeat("h", componentLength))
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	hostID := mustID(t)
	instanceID := mustID(t)
	t.Cleanup(func() { _ = cleanupSSHControlPaths(home, instanceID) })
	oldPath := filepath.Join(home, "editor", "ssh", instanceID, hostID+".sock")
	if len(oldPath) <= 100 {
		t.Fatalf("test path does not exercise the old socket limit: %d", len(oldPath))
	}
	path, err := prepareSSHControlPath(home, instanceID, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) > 80 {
		t.Fatalf("runtime SSH control path is still too long: %d", len(path))
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		t.Fatalf("control socket still depends on the long home: %q", path)
	}
	if got := filepath.Base(filepath.Dir(path)); got != instanceID[:12] {
		t.Fatalf("control directory = %q", got)
	}
	if got := strings.TrimSuffix(filepath.Base(path), ".sock"); got != hostID[:12] {
		t.Fatalf("control socket = %q", got)
	}
}

func TestSSHConnectionOptionsBoundDeadConnections(t *testing.T) {
	options := strings.Join(sshConnectionOptions("auto", "no", "/tmp/editor-%h.sock"), " ")
	for _, required := range []string{
		"BatchMode=yes", "ClearAllForwardings=yes", "ForwardAgent=no", "ForwardX11=no",
		"Tunnel=no", "PermitLocalCommand=no",
		"ConnectTimeout=8", "ServerAliveInterval=5", "ServerAliveCountMax=3",
		"ControlMaster=auto", "ControlPersist=no", "ControlPath=/tmp/editor-%%h.sock",
	} {
		if !strings.Contains(options, required) {
			t.Errorf("SSH options omit %q: %s", required, options)
		}
	}
}

func TestHostRequestTimeoutsFinishBeforeClientDeadlines(t *testing.T) {
	for method, want := range map[string]time.Duration{
		"hello": 8 * time.Second, "snapshot": 8 * time.Second, "touch_window": 8 * time.Second,
		"copy_mode": 8 * time.Second, "doctor": 8 * time.Second, "list_tmux_sessions": 8 * time.Second,
		"delete_window": 25 * time.Second, "delete_workspace": 25 * time.Second, "adopt_tmux_session": 25 * time.Second,
		"create_workspace": 2 * time.Minute, "put_attachment": 30 * time.Second,
	} {
		if got := hostRequestTimeout(method); got != want {
			t.Errorf("%s timeout = %s, want %s", method, got, want)
		}
	}
}

func TestProtocolErrorIsLengthBounded(t *testing.T) {
	got := safeProtocolError(errors.New(strings.Repeat("x", 1000)))
	if len(got) != 300 {
		t.Fatalf("safe error length = %d", len(got))
	}
}

func TestSupportedTmuxVersions(t *testing.T) {
	for _, value := range []string{"tmux 3.2", "tmux 3.2a", "tmux 3.4", "tmux 4.0"} {
		if !supportedTmuxVersion(value) {
			t.Errorf("expected %q to be supported", value)
		}
	}
	for _, value := range []string{"", "tmux 2.9", "tmux 3.1c", "unknown"} {
		if supportedTmuxVersion(value) {
			t.Errorf("expected %q to be rejected", value)
		}
	}
}

func TestBuildIdentityRejectsUnverifiableDevelopmentBuilds(t *testing.T) {
	if got := buildIdentity("0.1.0-dev", "", false); got != "" {
		t.Fatalf("unverifiable development identity = %q", got)
	}
	if got := buildIdentity("0.1.0-dev", "abc123", true); got != "" {
		t.Fatalf("modified development identity = %q", got)
	}
	if got := buildIdentity("0.1.0-dev", "abc123", false); got != "0.1.0-dev@abc123" {
		t.Fatalf("clean development identity = %q", got)
	}
	if got := buildIdentity("v1.2.3", "", false); got != "v1.2.3" {
		t.Fatalf("release identity = %q", got)
	}
	if got := buildIdentity("v1.2.3", "abc123", false); got != "v1.2.3" {
		t.Fatalf("release identity with VCS metadata = %q", got)
	}
}

func TestHostCallDeadlineInterruptsBlockedRequestWrite(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	callGate := make(chan struct{}, 1)
	callGate <- struct{}{}
	client := &HostClient{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout), callGate: callGate}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = client.Call(ctx, "blocked", struct {
		Data string `json:"data"`
	}{Data: strings.Repeat("x", 1<<20)}, nil)
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("blocked request deadline = %v after %s", err, time.Since(started))
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHostCallDeadlineIncludesTransportQueueWait(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	callGate := make(chan struct{}, 1)
	callGate <- struct{}{}
	client := &HostClient{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout), callGate: callGate}
	firstContext, cancelFirst := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelFirst()
	firstDone := make(chan error, 1)
	go func() { firstDone <- client.Call(firstContext, "first", nil, nil) }()
	waitUntil(t, time.Second, func() bool { return len(callGate) == 0 })
	secondContext, cancelSecond := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelSecond()
	started := time.Now()
	err = client.Call(secondContext, "second", nil, nil)
	if err == nil || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("queued request deadline = %v after %s", err, time.Since(started))
	}
	if !errors.Is(err, errHostRequestCanceled) || errors.Is(err, errHostTransport) {
		t.Fatalf("queued request was classified as a transport failure: %v", err)
	}
	<-firstDone
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHostClientCloseIsConcurrentAndReapsCommand(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	callGate := make(chan struct{}, 1)
	callGate <- struct{}{}
	client := &HostClient{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout), callGate: callGate}
	var closes sync.WaitGroup
	for range 16 {
		closes.Add(1)
		go func() {
			defer closes.Done()
			if err := client.Close(); err != nil {
				t.Errorf("close editor host: %v", err)
			}
		}()
	}
	closes.Wait()
	if cmd.ProcessState == nil {
		t.Fatal("editor host command was not reaped through Cmd.Wait")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := client.Call(ctx, "hello", nil, nil); !errors.Is(err, errHostTransport) {
		t.Fatalf("call after close = %v", err)
	}
}

func TestHostCallCancellationKillsOwnedGrandchild(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "grandchild.pid")
	processContext, cancelProcess := context.WithCancel(context.Background())
	cmd := exec.CommandContext(processContext, "sh", "-c", "read request; sleep 30 & echo $! > "+quotePOSIX(pidPath)+"; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	callGate := make(chan struct{}, 1)
	callGate <- struct{}{}
	client := &HostClient{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout), callGate: callGate, cancel: cancelProcess}
	callContext, cancelCall := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() { callDone <- client.Call(callContext, "blocked", nil, nil) }()
	var grandchild int
	waitUntil(t, time.Second, func() bool {
		contents, readErr := os.ReadFile(pidPath)
		if readErr != nil {
			return false
		}
		grandchild, readErr = strconv.Atoi(strings.TrimSpace(string(contents)))
		return readErr == nil && grandchild > 0
	})
	cancelCall()
	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("canceled host call succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled host call did not stop")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		return errors.Is(syscall.Kill(grandchild, 0), syscall.ESRCH)
	})
}
