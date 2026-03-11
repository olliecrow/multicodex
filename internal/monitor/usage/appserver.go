package usage

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	clientName    = "multicodex-monitor"
	clientVersion = "0.1.0"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int   `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcMessage struct {
	ID     *int            `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	ClientInfo   clientInfo             `json:"clientInfo"`
	Capabilities map[string]interface{} `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type AppServerSource struct {
	mu      sync.Mutex
	reqMu   sync.Mutex
	session *appServerSession

	codexHome         string
	authFingerprint   string
	authFingerprintFn func() (string, error)
}

func NewAppServerSource() *AppServerSource {
	home, _ := defaultCodexHome()
	return NewAppServerSourceForHome(home)
}

func NewAppServerSourceForHome(codexHome string) *AppServerSource {
	return &AppServerSource{codexHome: strings.TrimSpace(codexHome)}
}

func (s *AppServerSource) Name() string {
	return "app-server"
}

func (s *AppServerSource) Fetch(ctx context.Context) (*Summary, error) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()

	var warnings []string
	if warning := s.refreshAuthState(); warning != "" {
		warnings = append(warnings, warning)
	}

	session, err := s.ensureSession(ctx)
	if err != nil {
		return nil, err
	}

	result, err := session.fetchRateLimits(ctx)
	if err != nil {
		s.resetSession()
		return nil, err
	}

	additional := 0
	if len(result.RateLimitsByLimitID) > 1 {
		additional = len(result.RateLimitsByLimitID) - 1
	}

	identity, err := session.fetchAccount(ctx)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("account identity unavailable: %v", err))
	}

	return normalizeSummary(s.Name(), result.RateLimits, additional, identity, warnings)
}

func (s *AppServerSource) Close() error {
	s.mu.Lock()
	session := s.session
	s.session = nil
	s.mu.Unlock()

	if session == nil {
		return nil
	}
	return session.close()
}

func (s *AppServerSource) ensureSession(ctx context.Context) (*appServerSession, error) {
	s.mu.Lock()
	if s.session == nil {
		s.session = newAppServerSession(s.codexHome)
	}
	session := s.session
	s.mu.Unlock()

	if err := session.ensureStarted(); err != nil {
		return nil, fmt.Errorf("start app-server source: %w", err)
	}
	if err := session.ensureInitialized(ctx); err != nil {
		_ = session.close()
		return nil, fmt.Errorf("initialize app-server source: %w", err)
	}
	return session, nil
}

func (s *AppServerSource) resetSession() {
	s.mu.Lock()
	session := s.session
	s.session = nil
	s.mu.Unlock()
	if session != nil {
		_ = session.close()
	}
}

func (s *AppServerSource) refreshAuthState() string {
	fingerprintFn := s.authFingerprintFn
	if fingerprintFn == nil {
		fingerprintFn = func() (string, error) {
			return currentAuthFingerprintForHome(s.codexHome)
		}
	}

	fingerprint, err := fingerprintFn()
	if err != nil {
		if s.authFingerprint == "" {
			return ""
		}
		s.resetSession()
		s.authFingerprint = ""
		return "auth state changed; restarted app-server session"
	}

	if s.authFingerprint == "" {
		s.authFingerprint = fingerprint
		return ""
	}
	if s.authFingerprint == fingerprint {
		return ""
	}

	s.resetSession()
	s.authFingerprint = fingerprint
	return "auth state changed; restarted app-server session"
}

func currentAuthFingerprintForHome(codexHome string) (string, error) {
	authPath, err := findAuthJSONPathForHome(codexHome)
	if err != nil {
		return "", err
	}
	token, err := readAccessToken(authPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(token))
	return authPath + ":" + hex.EncodeToString(sum[:]), nil
}

type appServerSession struct {
	mu sync.Mutex

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	encoder *json.Encoder

	pending map[int]chan rpcMessage
	nextID  int

	initialized bool

	done    chan struct{}
	doneErr error

	codexHome string
}

type accountReadResultRaw struct {
	Account            *accountReadAccountRaw `json:"account"`
	RequiresOpenAIAuth bool                   `json:"requiresOpenaiAuth"`
}

type accountReadAccountRaw struct {
	Email string `json:"email"`
}

func newAppServerSession(codexHome string) *appServerSession {
	return &appServerSession{
		pending:   make(map[int]chan rpcMessage),
		codexHome: strings.TrimSpace(codexHome),
	}
}

func (s *appServerSession) ensureStarted() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil {
		select {
		case <-s.done:
			return fmt.Errorf("app-server process is not running: %w", s.doneErrOrDefault())
		default:
			return nil
		}
	}

	cmd := exec.Command("codex", "-s", "read-only", "-a", "untrusted", "app-server")
	env := os.Environ()
	if s.codexHome != "" {
		env = upsertEnvVar(env, "CODEX_HOME", s.codexHome)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start codex app-server: %w", err)
	}

	s.cmd = cmd
	s.stdin = stdin
	s.encoder = json.NewEncoder(stdin)
	s.initialized = false
	s.done = make(chan struct{})
	s.doneErr = nil

	go drain(stderr)
	go s.readLoop(stdout)

	return nil
}

func (s *appServerSession) ensureInitialized(ctx context.Context) error {
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	var initResult map[string]interface{}
	if err := s.request(ctx, "initialize", initializeParams{
		ClientInfo: clientInfo{
			Name:    clientName,
			Version: clientVersion,
		},
		Capabilities: map[string]interface{}{},
	}, &initResult); err != nil {
		return err
	}

	if err := s.notify("initialized", map[string]interface{}{}); err != nil {
		return err
	}

	s.mu.Lock()
	s.initialized = true
	s.mu.Unlock()
	return nil
}

func (s *appServerSession) fetchRateLimits(ctx context.Context) (*rateLimitsReadResultRaw, error) {
	var out rateLimitsReadResultRaw
	if err := s.request(ctx, "account/rateLimits/read", map[string]interface{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *appServerSession) fetchAccount(ctx context.Context) (*identityInfo, error) {
	var out accountReadResultRaw
	if err := s.request(ctx, "account/read", map[string]interface{}{}, &out); err != nil {
		return nil, err
	}
	if out.Account == nil {
		if out.RequiresOpenAIAuth {
			return nil, errors.New("account/read requires OpenAI auth")
		}
		return nil, errors.New("account/read missing account")
	}
	return &identityInfo{
		Email: strings.TrimSpace(out.Account.Email),
	}, nil
}

func (s *appServerSession) request(ctx context.Context, method string, params any, out any) error {
	s.mu.Lock()
	if s.cmd == nil || s.encoder == nil {
		s.mu.Unlock()
		return errors.New("app-server process not started")
	}
	reqID := s.nextID + 1
	s.nextID = reqID

	respCh := make(chan rpcMessage, 1)
	s.pending[reqID] = respCh

	encodeErr := s.encoder.Encode(rpcRequest{
		JSONRPC: "2.0",
		ID:      &reqID,
		Method:  method,
		Params:  params,
	})
	done := s.done
	if encodeErr != nil {
		delete(s.pending, reqID)
		s.mu.Unlock()
		return fmt.Errorf("send request %s: %w", method, encodeErr)
	}
	s.mu.Unlock()

	select {
	case msg, ok := <-respCh:
		if !ok {
			return fmt.Errorf("request %s aborted: %w", method, s.doneErrSnapshot())
		}
		if msg.Error != nil {
			return fmt.Errorf("%s failed: %s", method, msg.Error.Message)
		}
		if out != nil {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, reqID)
		s.mu.Unlock()
		return fmt.Errorf("%s timeout: %w", method, ctx.Err())
	case <-done:
		s.mu.Lock()
		delete(s.pending, reqID)
		s.mu.Unlock()
		return fmt.Errorf("%s failed: %w", method, s.doneErrSnapshot())
	}
}

func (s *appServerSession) notify(method string, params any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil || s.encoder == nil {
		return errors.New("app-server process not started")
	}
	if err := s.encoder.Encode(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}); err != nil {
		return fmt.Errorf("send notification %s: %w", method, err)
	}
	return nil
}

func (s *appServerSession) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.ID == nil {
			continue
		}

		s.mu.Lock()
		respCh := s.pending[*msg.ID]
		if respCh != nil {
			delete(s.pending, *msg.ID)
		}
		s.mu.Unlock()

		if respCh != nil {
			respCh <- msg
			close(respCh)
		}
	}

	streamErr := scanner.Err()
	if streamErr == nil {
		streamErr = errors.New("app-server stream closed")
	}

	s.mu.Lock()
	s.doneErr = streamErr
	for id, ch := range s.pending {
		delete(s.pending, id)
		close(ch)
	}
	cmd := s.cmd
	done := s.done
	s.cmd = nil
	s.stdin = nil
	s.encoder = nil
	s.initialized = false
	s.mu.Unlock()

	if cmd != nil {
		_ = cmd.Wait()
	}
	if done != nil {
		close(done)
	}
}

func (s *appServerSession) close() error {
	s.mu.Lock()
	cmd := s.cmd
	done := s.done
	s.mu.Unlock()

	if cmd == nil {
		return nil
	}
	_ = cmd.Process.Kill()

	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return errors.New("timeout waiting for app-server shutdown")
		}
	}
	return nil
}

func (s *appServerSession) doneErrSnapshot() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doneErrOrDefault()
}

func (s *appServerSession) doneErrOrDefault() error {
	if s.doneErr != nil {
		return s.doneErr
	}
	return errors.New("app-server exited")
}

func drain(r io.Reader) {
	_, _ = io.Copy(io.Discard, r)
}

func upsertEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
