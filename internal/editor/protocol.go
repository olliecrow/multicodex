package editor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/olliecrow/multicodex/internal/buildinfo"
)

const (
	maxProtocolMessage                 = 24 << 20
	incompatibleEditorHostMessage      = "editor and host use incompatible multicodex protocols; update every machine, then quit and reopen only the outer editor"
	unverifiableEditorHostBuildMessage = "remote editor connections require clean verifiable multicodex builds"
)

var (
	errHostRequestCanceled = errors.New("editor host request canceled")
	errHostTransport       = errors.New("editor host transport failed")
)

type protocolRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type protocolResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type helloResult struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	Identity string `json:"identity,omitempty"`
}

func RunHostProtocol(ctx context.Context, service *HostService, in io.Reader, out io.Writer) error {
	requestContext, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), maxProtocolMessage)
	lines := make(chan []byte)
	readerDone := make(chan error, 1)
	go func() {
		var readErr error
		defer func() {
			readerDone <- readErr
			cancelRequests()
			close(lines)
		}()
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-requestContext.Done():
				return
			}
		}
		if scanner.Err() != nil {
			readErr = errors.New("read editor host protocol request")
		}
	}()
	writer := bufio.NewWriter(out)
	for {
		var raw []byte
		select {
		case err := <-readerDone:
			return err
		case line, ok := <-lines:
			if !ok {
				return <-readerDone
			}
			raw = line
		case <-ctx.Done():
			return nil
		}
		var request protocolRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return errors.New("invalid editor host protocol request")
		}
		response := protocolResponse{ID: request.ID}
		result, err := dispatchHostRequest(requestContext, service, request)
		if err != nil {
			response.Error = safeProtocolError(err)
		} else if result != nil {
			response.Result, err = json.Marshal(result)
			if err != nil {
				response.Error = "encode editor host response"
			}
		}
		line, err := json.Marshal(response)
		if err != nil {
			return errors.New("encode editor host protocol response")
		}
		if len(line) > maxProtocolMessage {
			return errors.New("editor host protocol response is too large")
		}
		if _, err := writer.Write(append(line, '\n')); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func dispatchHostRequest(parent context.Context, service *HostService, request protocolRequest) (any, error) {
	ctx, cancel := context.WithTimeout(parent, hostRequestTimeout(request.Method))
	defer cancel()

	switch request.Method {
	case "hello":
		return helloResult{Protocol: hostProtocol, Version: buildinfo.Current(), Identity: editorBuildIdentity()}, nil
	case "snapshot":
		return service.Snapshot(ctx)
	case "inspect_project":
		var params struct {
			Path string `json:"path"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.InspectProject(ctx, params.Path)
	case "create_workspace":
		var params CreateWorkspaceRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.CreateWorkspace(ctx, params)
	case "create_window":
		var params CreateWindowRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.CreateWindow(ctx, params)
	case "open_project_window":
		var params OpenProjectWindowRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.OpenProjectWindow(ctx, params)
	case "list_tmux_sessions":
		var params ListTmuxSessionsRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.ListTmuxSessions(ctx, params)
	case "adopt_tmux_session":
		var params AdoptTmuxSessionRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.AdoptTmuxSession(ctx, params)
	case "rename_workspace":
		var params RenameRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, service.RenameWorkspace(ctx, params)
	case "rename_window":
		var params RenameRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, service.RenameWindow(ctx, params)
	case "put_attachment":
		var params PutAttachmentRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.PutAttachment(ctx, params)
	case "touch_window":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, service.TouchWindow(ctx, params.ID)
	case "copy_mode":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, service.CopyMode(ctx, params.ID)
	case "delete_window":
		var params DeleteRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.DeleteWindow(ctx, params)
	case "delete_workspace":
		var params DeleteRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return service.DeleteWorkspace(ctx, params)
	case "cleanup":
		return service.Cleanup(ctx)
	case "doctor":
		return service.Doctor(ctx), nil
	default:
		return nil, errors.New("unknown editor host protocol method")
	}
}

func hostRequestTimeout(method string) time.Duration {
	timeout := 30 * time.Second
	switch method {
	case "hello", "snapshot", "touch_window", "copy_mode", "doctor", "list_tmux_sessions":
		timeout = 8 * time.Second
	case "delete_window", "delete_workspace", "adopt_tmux_session":
		timeout = 25 * time.Second
	case "create_workspace":
		timeout = 2 * time.Minute
	}
	return timeout
}

func decodeParams(raw json.RawMessage, value any) error {
	if len(raw) == 0 {
		return errors.New("missing editor host protocol parameters")
	}
	decoder := json.NewDecoder(bytesReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid editor host protocol parameters")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid editor host protocol parameters")
	}
	return nil
}

func safeProtocolError(err error) string {
	return safeClientText(err.Error(), 300)
}

type byteReader struct {
	b []byte
}

func bytesReader(b []byte) *byteReader { return &byteReader{b: b} }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

type HostClient struct {
	host      Host
	instance  string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	scanner   *bufio.Scanner
	nextID    uint64
	callGate  chan struct{}
	closeOnce sync.Once
	cancel    context.CancelFunc
}

func StartHostClient(ctx context.Context, executable, multicodexHome, instanceID string, host Host) (*HostClient, error) {
	return startHostClient(ctx, executable, multicodexHome, instanceID, host, true)
}

func startIsolatedHostClient(ctx context.Context, executable, multicodexHome, instanceID string, host Host) (*HostClient, error) {
	return startHostClient(ctx, executable, multicodexHome, instanceID, host, false)
}

func startHostClient(ctx context.Context, executable, multicodexHome, instanceID string, host Host, sharedSSH bool) (*HostClient, error) {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return nil, err
	}
	processContext, cancelProcess := context.WithCancel(context.Background())
	var cmd *exec.Cmd
	if host.ID == localHostID {
		cmd = editorCommandContext(processContext, executable, "__editor-host", "--instance", instanceID)
	} else {
		if err := validateSSHAlias(host.SSHAlias); err != nil {
			cancelProcess()
			return nil, err
		}
		var options []string
		if sharedSSH {
			controlPath, err := prepareSSHControlPath(multicodexHome, instanceID, host.ID)
			if err != nil {
				cancelProcess()
				return nil, err
			}
			options = sshConnectionOptions("auto", "no", controlPath)
		} else {
			// Cleanup opens a separate SSH connection. Its timeout cannot close
			// the shared protocol or terminal connection.
			options = sshConnectionOptions("no", "no", "none")
		}
		args := append([]string{"-T"}, options...)
		args = append(args, host.SSHAlias, "multicodex", "__editor-host", "--instance", instanceID)
		cmd = editorCommandContext(processContext, "ssh", args...)
	}
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
		cancelProcess()
		return nil, errors.New("prepare editor host connection")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelProcess()
		return nil, errors.New("prepare editor host connection")
	}
	cmd.Stderr = &limitedWriter{remaining: 64 << 10}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		cancelProcess()
		return nil, connectionStartError(host)
	}
	callGate := make(chan struct{}, 1)
	callGate <- struct{}{}
	client := &HostClient{host: host, instance: instanceID, cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout), callGate: callGate, cancel: cancelProcess}
	client.scanner.Buffer(make([]byte, 64<<10), maxProtocolMessage)
	var hello helloResult
	helloCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Call(helloCtx, "hello", nil, &hello); err != nil {
		client.Close()
		return nil, connectionHandshakeError(host)
	}
	if err := validateHostHello(host, hello, editorBuildIdentity()); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func validateHostHello(host Host, hello helloResult, clientIdentity string) error {
	if hello.Protocol != hostProtocol {
		return errors.New(incompatibleEditorHostMessage)
	}
	if host.ID != localHostID && (clientIdentity == "" || hello.Identity == "") {
		return errors.New(unverifiableEditorHostBuildMessage)
	}
	return nil
}

func editorBuildIdentity() string {
	revision, modified := "", false
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	return buildIdentity(buildinfo.Current(), revision, modified)
}

func buildIdentity(version, revision string, modified bool) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if !strings.HasSuffix(version, "-dev") {
		return version
	}
	if revision != "" && !modified {
		return version + "@" + revision
	}
	return ""
}

func prepareSSHControlPath(_ string, instanceID, hostID string) (string, error) {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return "", err
	}
	if err := validateID(hostID, "host identifier"); err != nil {
		return "", err
	}
	paths := []string{sshControlRuntimeRoot(), filepath.Join(sshControlRuntimeRoot(), instanceID[:12])}
	for _, path := range paths {
		if err := ensureOwnedPrivateRuntimeDir(path); err != nil {
			return "", err
		}
	}
	controlPath := filepath.Join(paths[len(paths)-1], hostID[:12]+".sock")
	if len(controlPath) > 80 {
		return "", errors.New("editor SSH control path is too long")
	}
	if info, err := os.Lstat(controlPath); err == nil && info.Mode()&os.ModeSocket == 0 {
		return "", errors.New("editor SSH control path is not a socket")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("inspect editor SSH control path")
	}
	return controlPath, nil
}

func sshControlRuntimeRoot() string {
	return filepath.Join("/tmp", "multicodex-editor-"+strconv.Itoa(os.Getuid()))
}

func ensureOwnedPrivateRuntimeDir(path string) error {
	if err := ensurePrivateDir(path); err != nil {
		return err
	}
	return secureOwnedPrivateRuntimeDir(path)
}

func secureOwnedPrivateRuntimeDir(path string) error {
	if err := secureExistingDir(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("inspect private editor runtime directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return errors.New("editor runtime directory is not owned by the current user")
	}
	return nil
}

// OpenSSH expands percent tokens in ControlPath values. Doubling each percent
// keeps the private path identical to the path that lifecycle cleanup owns.
func sshControlPathOption(path string) string {
	return strings.ReplaceAll(path, "%", "%%")
}

func sshConnectionOptions(controlMaster, controlPersist, controlPath string) []string {
	options := []string{
		"-o", "BatchMode=yes",
		"-o", "ClearAllForwardings=yes",
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
		"-o", "Tunnel=no",
		"-o", "PermitLocalCommand=no",
		"-o", "ConnectTimeout=8",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=3",
		"-o", "ControlMaster=" + controlMaster,
	}
	if controlPersist != "" {
		options = append(options, "-o", "ControlPersist="+controlPersist)
	}
	return append(options, "-o", "ControlPath="+sshControlPathOption(controlPath))
}

func cleanupSSHControlPath(_ string, instanceID, hostID string) error {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return err
	}
	if err := validateID(hostID, "host identifier"); err != nil {
		return err
	}
	runtimeRoot := sshControlRuntimeRoot()
	if _, err := os.Lstat(runtimeRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return errors.New("inspect editor SSH runtime directory")
	}
	if err := secureOwnedPrivateRuntimeDir(runtimeRoot); err != nil {
		return err
	}
	directory := filepath.Join(runtimeRoot, instanceID[:12])
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return errors.New("inspect editor SSH directory")
	}
	if err := secureOwnedPrivateRuntimeDir(directory); err != nil {
		return err
	}
	path := filepath.Join(directory, hostID[:12]+".sock")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("inspect editor SSH control socket")
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("refuse to remove a non-socket editor SSH control path")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("remove editor SSH control socket")
	}
	return nil
}

func cleanupSSHControlPaths(_ string, instanceID string) error {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return err
	}
	runtimeRoot := sshControlRuntimeRoot()
	if _, err := os.Lstat(runtimeRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return errors.New("inspect editor SSH runtime directory")
	}
	if err := secureOwnedPrivateRuntimeDir(runtimeRoot); err != nil {
		return err
	}
	directory := filepath.Join(runtimeRoot, instanceID[:12])
	_, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return removeEmptySSHRuntimeRoot(runtimeRoot)
	}
	if err != nil {
		return errors.New("inspect editor SSH directory")
	}
	if err := secureOwnedPrivateRuntimeDir(directory); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return errors.New("inspect editor SSH control paths")
	}
	for _, entry := range entries {
		name := entry.Name()
		shortID := strings.TrimSuffix(name, ".sock")
		if !strings.HasSuffix(name, ".sock") || len(shortID) != 12 || !idPattern.MatchString(shortID+strings.Repeat("0", 12)) {
			continue
		}
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			return errors.New("refuse to remove an unsafe editor SSH control path")
		}
		if err := os.Remove(path); err != nil {
			return errors.New("remove editor SSH control socket")
		}
	}
	if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil // Preserve a non-empty directory that contains anything unexpected.
	}
	return removeEmptySSHRuntimeRoot(runtimeRoot)
}

func removeEmptySSHRuntimeRoot(runtimeRoot string) error {
	if err := os.Remove(runtimeRoot); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) {
		return errors.New("remove empty editor SSH runtime directory")
	}
	return nil
}

func (c *HostClient) Call(ctx context.Context, method string, params, result any) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("%w: request timed out", errHostRequestCanceled)
	case <-c.callGate:
	}
	defer func() { c.callGate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: request timed out", errHostRequestCanceled)
	}
	if c.stdin == nil || c.cmd == nil || c.cmd.Process == nil {
		return fmt.Errorf("%w: connection closed", errHostTransport)
	}
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	defer func() {
		close(done)
		<-watcherDone
	}()
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			if c.stdin != nil {
				_ = c.stdin.Close()
			}
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			select {
			case <-done:
				return
			case <-timer.C:
				if c.cancel != nil {
					c.cancel()
				} else if c.cmd != nil && c.cmd.Process != nil {
					_ = c.cmd.Process.Kill()
				}
			}
		case <-done:
		}
	}()
	c.nextID++
	request := protocolRequest{ID: c.nextID, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encode editor host request: %w", err)
		}
		request.Params = b
	}
	line, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode editor host request: %w", err)
	}
	if len(line) > maxProtocolMessage {
		return errors.New("editor host request is too large")
	}
	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: request timed out", errHostTransport)
		}
		return fmt.Errorf("%w: connection closed", errHostTransport)
	}
	if !c.scanner.Scan() {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: request timed out", errHostTransport)
		}
		return fmt.Errorf("%w: connection closed", errHostTransport)
	}
	var response protocolResponse
	if err := json.Unmarshal(c.scanner.Bytes(), &response); err != nil || response.ID != request.ID {
		return fmt.Errorf("%w: invalid response", errHostTransport)
	}
	if response.Error != "" {
		return errors.New(safeClientText(response.Error, 300))
	}
	if result != nil {
		if len(response.Result) == 0 {
			return errors.New("editor host response is missing a result")
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return errors.New("invalid editor host result")
		}
	}
	return nil
}

func (c *HostClient) Close() error {
	c.closeOnce.Do(func() {
		<-c.callGate
		defer func() { c.callGate <- struct{}{} }()
		if c.stdin != nil {
			_ = c.stdin.Close()
			c.stdin = nil
		}
		if c.cmd != nil && c.cmd.Process != nil {
			waited := make(chan error, 1)
			go func() { waited <- c.cmd.Wait() }()
			select {
			case <-waited:
			case <-time.After(time.Second):
				if c.cancel != nil {
					c.cancel()
				} else {
					_ = c.cmd.Process.Kill()
				}
				<-waited
			}
		}
		if c.cancel != nil {
			c.cancel()
		}
	})
	return nil
}

func connectionStartError(host Host) error {
	if host.ID == localHostID {
		return errors.New("start local editor host")
	}
	return fmt.Errorf("connect to SSH host %q: verify the configured alias and install multicodex on that host", host.Name)
}

func connectionHandshakeError(host Host) error {
	if host.ID == localHostID {
		return errors.New("local editor host did not start correctly")
	}
	return fmt.Errorf("SSH host %q did not start the multicodex editor host; verify the connection and installed multicodex command", host.Name)
}
