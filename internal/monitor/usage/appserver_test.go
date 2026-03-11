package usage

import (
	"errors"
	"testing"
)

func TestRefreshAuthStateFirstFingerprintNoWarning(t *testing.T) {
	s := &AppServerSource{
		authFingerprintFn: func() (string, error) {
			return "fp-a", nil
		},
	}

	warning := s.refreshAuthState()
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

	warning := s.refreshAuthState()
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

	warning := s.refreshAuthState()
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

func TestRefreshAuthStateErrorAfterKnownFingerprintReturnsWarning(t *testing.T) {
	s := &AppServerSource{
		authFingerprint: "fp-a",
		authFingerprintFn: func() (string, error) {
			return "", errors.New("missing auth")
		},
		session: &appServerSession{},
	}

	warning := s.refreshAuthState()
	if warning == "" {
		t.Fatalf("expected warning on auth-state error after prior fingerprint")
	}
	if s.authFingerprint != "" {
		t.Fatalf("expected fingerprint to be cleared")
	}
	if s.session != nil {
		t.Fatalf("expected session reset on auth-state error")
	}
}

