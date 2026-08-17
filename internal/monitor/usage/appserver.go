package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/olliecrow/multicodex/internal/buildinfo"
	"github.com/olliecrow/multicodex/internal/codexappserver"
	"github.com/olliecrow/multicodex/internal/codexstate"
)

const clientName = "multicodex-monitor"

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type AppServerSource struct {
	mu      sync.Mutex
	reqMu   sync.Mutex
	session *appServerSession

	codexHome         string
	authFingerprint   string
	authFingerprintFn func() (string, error)
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
	warning, authErr := s.refreshAuthState()
	if authErr != nil {
		return nil, authErr
	}
	if warning != "" {
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

	return normalizeSummary(s.Name(), result.RateLimits, result.RateLimitsByLimitID, additional, identity, warnings)
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

func (s *AppServerSource) refreshAuthState() (string, error) {
	fingerprintFn := s.authFingerprintFn
	if fingerprintFn == nil {
		fingerprintFn = func() (string, error) {
			return currentAuthFingerprintForHome(s.codexHome)
		}
	}

	fingerprint, err := fingerprintFn()
	if err != nil {
		s.resetSession()
		s.authFingerprint = ""
		return "", err
	}

	if s.authFingerprint == "" {
		s.authFingerprint = fingerprint
		return "", nil
	}
	if s.authFingerprint == fingerprint {
		return "", nil
	}

	s.resetSession()
	s.authFingerprint = fingerprint
	return "auth state changed; restarted app-server session", nil
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
	mu          sync.Mutex
	client      *codexappserver.Client
	initialized bool
	codexHome   string
}

type accountReadResultRaw struct {
	Account            *accountReadAccountRaw `json:"account"`
	RequiresOpenAIAuth bool                   `json:"requiresOpenaiAuth"`
}

type accountReadAccountRaw struct {
	Email string `json:"email"`
}

func newAppServerSession(codexHome string) *appServerSession {
	return &appServerSession{codexHome: strings.TrimSpace(codexHome)}
}

func (s *appServerSession) ensureStarted() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		select {
		case <-s.client.Done():
			s.client = nil
			s.initialized = false
		default:
			return nil
		}
	}

	client := codexappserver.New(codexappserver.Config{
		GlobalArgs:    []string{"-s", "read-only", "-a", "untrusted"},
		BaseEnv:       os.Environ(),
		CodexHome:     s.codexHome,
		ClientName:    clientName,
		ClientVersion: buildinfo.Version,
		ErrorSanitizer: func(method string, code int, message string) error {
			return safeProviderRPCError(method, &rpcError{Code: code, Message: message})
		},
	})
	if err := client.Start(); err != nil {
		return err
	}
	s.client = client
	s.initialized = false
	return nil
}

func (s *appServerSession) ensureInitialized(ctx context.Context) error {
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return errors.New("app-server process not started")
	}
	if err := client.Initialize(ctx); err != nil {
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
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return errors.New("app-server process not started")
	}
	return client.Request(ctx, method, params, out)
}

func (s *appServerSession) notify(method string, params any) error {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return errors.New("app-server process not started")
	}
	return client.Notify(method, params)
}

func (s *appServerSession) close() error {
	s.mu.Lock()
	client := s.client
	s.client = nil
	s.initialized = false
	s.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.Close()
}

func withoutCodexProfileEnv(env []string) []string {
	return codexstate.SanitizedEnv(env, "")
}
