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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/olliecrow/multicodex/internal/buildinfo"
)

const maxProtocolMessage = 24 << 20

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
	timeout := 30 * time.Second
	if request.Method == "create_workspace" {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	switch request.Method {
	case "hello":
		return helloResult{Protocol: hostProtocol, Version: buildinfo.Version, Identity: editorBuildIdentity()}, nil
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
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return nil, err
	}
	processContext, cancelProcess := context.WithCancel(context.Background())
	var cmd *exec.Cmd
	if host.ID == localHostID {
		cmd = exec.CommandContext(processContext, executable, "__editor-host", "--instance", instanceID)
	} else {
		if err := validateSSHAlias(host.SSHAlias); err != nil {
			cancelProcess()
			return nil, err
		}
		controlPath, err := prepareSSHControlPath(multicodexHome, instanceID, host.ID)
		if err != nil {
			cancelProcess()
			return nil, err
		}
		args := append([]string{"-T"}, sshConnectionOptions("auto", "no", controlPath)...)
		args = append(args, host.SSHAlias, "multicodex", "__editor-host", "--instance", instanceID)
		cmd = exec.CommandContext(processContext, "ssh", args...)
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
	if hello.Protocol != hostProtocol || hello.Version != buildinfo.Version {
		client.Close()
		return nil, errors.New("editor host uses an incompatible multicodex version")
	}
	if host.ID != localHostID {
		identity := editorBuildIdentity()
		if identity == "" || hello.Identity != identity {
			client.Close()
			return nil, errors.New("editor host must use the same release or clean source revision")
		}
	}
	return client, nil
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
	return buildIdentity(buildinfo.Version, revision, modified)
}

func buildIdentity(version, revision string, modified bool) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if revision != "" && !modified {
		return version + "@" + revision
	}
	if !strings.HasSuffix(version, "-dev") {
		return version
	}
	return ""
}

func prepareSSHControlPath(multicodexHome, instanceID, hostID string) (string, error) {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return "", err
	}
	if err := validateID(hostID, "host identifier"); err != nil {
		return "", err
	}
	paths := []string{
		filepath.Join(multicodexHome, "editor"),
		filepath.Join(multicodexHome, "editor", "ssh"),
		filepath.Join(multicodexHome, "editor", "ssh", instanceID),
	}
	for _, path := range paths {
		if err := ensurePrivateDir(path); err != nil {
			return "", err
		}
	}
	controlPath := filepath.Join(paths[len(paths)-1], hostID+".sock")
	if len(controlPath) > 100 {
		return "", errors.New("MULTICODEX_HOME path is too long for a reliable SSH control socket")
	}
	if info, err := os.Lstat(controlPath); err == nil && info.Mode()&os.ModeSocket == 0 {
		return "", errors.New("editor SSH control path is not a socket")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("inspect editor SSH control path")
	}
	return controlPath, nil
}

// OpenSSH expands percent tokens in ControlPath values. Doubling each percent
// keeps the private path identical to the path that lifecycle cleanup owns.
func sshControlPathOption(path string) string {
	return strings.ReplaceAll(path, "%", "%%")
}

func sshConnectionOptions(controlMaster, controlPersist, controlPath string) []string {
	options := []string{
		"-o", "BatchMode=yes",
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

func cleanupSSHControlPath(multicodexHome, instanceID, hostID string) error {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return err
	}
	if err := validateID(hostID, "host identifier"); err != nil {
		return err
	}
	path := filepath.Join(multicodexHome, "editor", "ssh", instanceID, hostID+".sock")
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

func cleanupSSHControlPaths(multicodexHome, instanceID string) error {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return err
	}
	directory := filepath.Join(multicodexHome, "editor", "ssh", instanceID)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("refuse to clean an unsafe editor SSH directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return errors.New("inspect editor SSH control paths")
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sock") || validateID(strings.TrimSuffix(name, ".sock"), "host identifier") != nil {
			continue
		}
		if err := cleanupSSHControlPath(multicodexHome, instanceID, strings.TrimSuffix(name, ".sock")); err != nil {
			return errors.New("remove editor SSH control socket")
		}
	}
	if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil // Preserve a non-empty directory that contains anything unexpected.
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
	return fmt.Errorf("SSH host %q did not provide a compatible multicodex editor host", host.Name)
}
