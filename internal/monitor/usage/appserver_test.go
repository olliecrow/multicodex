package usage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRefreshAuthStateFirstFingerprintNoWarning(t *testing.T) {
	s := &AppServerSource{
		authFingerprintFn: func() (string, error) {
			return "fp-a", nil
		},
	}

	warning, err := s.refreshAuthState()
	if err != nil {
		t.Fatalf("refreshAuthState: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if s.authFingerprint != "fp-a" {
		t.Fatalf("expected fingerprint to be stored")
	}
}

func TestRefreshAuthStateUnchangedNoWarning(t *testing.T) {
	s := &AppServerSource{
		authFingerprint: "fp-a",
		authFingerprintFn: func() (string, error) {
			return "fp-a", nil
		},
	}

	warning, err := s.refreshAuthState()
	if err != nil {
		t.Fatalf("refreshAuthState: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if s.authFingerprint != "fp-a" {
		t.Fatalf("expected fingerprint to remain unchanged")
	}
}

func TestRefreshAuthStateChangedReturnsWarning(t *testing.T) {
	s := &AppServerSource{
		authFingerprint: "fp-a",
		authFingerprintFn: func() (string, error) {
			return "fp-b", nil
		},
		session: &appServerSession{},
	}

	warning, err := s.refreshAuthState()
	if err != nil {
		t.Fatalf("refreshAuthState: %v", err)
	}
	if warning == "" {
		t.Fatalf("expected warning on fingerprint change")
	}
	if s.authFingerprint != "fp-b" {
		t.Fatalf("expected fingerprint to update")
	}
	if s.session != nil {
		t.Fatalf("expected session reset on fingerprint change")
	}
}

func TestRefreshAuthStateErrorAfterKnownFingerprintReturnsError(t *testing.T) {
	s := &AppServerSource{
		authFingerprint: "fp-a",
		authFingerprintFn: func() (string, error) {
			return "", errors.New("missing auth")
		},
		session: &appServerSession{},
	}

	warning, err := s.refreshAuthState()
	if err == nil {
		t.Fatal("expected auth-state error")
	}
	if warning != "" {
		t.Fatalf("expected no warning on auth-state error, got %q", warning)
	}
	if s.authFingerprint != "" {
		t.Fatalf("expected fingerprint to be cleared")
	}
	if s.session != nil {
		t.Fatalf("expected session reset on auth-state error")
	}
}

func TestRefreshAuthStateFirstErrorReturnsError(t *testing.T) {
	s := &AppServerSource{
		authFingerprintFn: func() (string, error) {
			return "", errors.New("unsafe auth")
		},
	}

	warning, err := s.refreshAuthState()
	if err == nil {
		t.Fatal("expected auth-state error")
	}
	if warning != "" {
		t.Fatalf("expected no warning before a known fingerprint, got %q", warning)
	}
}

func TestFetchBlocksAuthStateErrorBeforeStartingSession(t *testing.T) {
	s := &AppServerSource{
		authFingerprintFn: func() (string, error) {
			return "", errors.New("unsafe auth")
		},
	}

	_, err := s.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected auth-state error")
	}
	if !strings.Contains(err.Error(), "unsafe auth") {
		t.Fatalf("expected unsafe auth error, got %v", err)
	}
	if s.session != nil {
		t.Fatalf("expected app-server session not to start")
	}
}

func TestWithoutCodexProfileEnvRemovesStaleProfileState(t *testing.T) {
	env := withoutCodexProfileEnv([]string{
		"CODEX_HOME=/tmp/stale",
		"MULTICODEX_ACTIVE_PROFILE=stale",
		"MULTICODEX_SELECTED_PROFILE_PATH=/tmp/stale.json",
		"OPENAI_API_KEY=stale",
		"CODEX_AUTH_TOKEN=stale",
		"KEEP=value",
	})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "CODEX_HOME=") {
		t.Fatalf("expected CODEX_HOME to be removed, got %q", env)
	}
	if strings.Contains(joined, "MULTICODEX_ACTIVE_PROFILE=") {
		t.Fatalf("expected MULTICODEX_ACTIVE_PROFILE to be removed, got %q", env)
	}
	if strings.Contains(joined, "MULTICODEX_SELECTED_PROFILE_PATH=") {
		t.Fatalf("expected MULTICODEX_SELECTED_PROFILE_PATH to be removed, got %q", env)
	}
	if strings.Contains(joined, "OPENAI_API_KEY=") || strings.Contains(joined, "CODEX_AUTH_TOKEN=") {
		t.Fatalf("expected account override credentials to be removed, got %q", env)
	}
	if !strings.Contains(joined, "KEEP=value") {
		t.Fatalf("expected unrelated env to remain, got %q", env)
	}
}
