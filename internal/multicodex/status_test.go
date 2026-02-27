package multicodex

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEmailFromAuthFileTopLevel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "auth.json")
	content := `{"email":"top@example.com","tokens":{"id_token":""}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	email, err := emailFromAuthFile(path)
	if err != nil {
		t.Fatalf("emailFromAuthFile returned error: %v", err)
	}
	if email != "top@example.com" {
		t.Fatalf("unexpected email: %q", email)
	}
}

func TestEmailFromAuthFileIDToken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "auth.json")
	idToken := syntheticJWT(t, map[string]any{"email": "claim@example.com"})
	content := `{"tokens":{"id_token":"` + idToken + `"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	email, err := emailFromAuthFile(path)
	if err != nil {
		t.Fatalf("emailFromAuthFile returned error: %v", err)
	}
	if email != "claim@example.com" {
		t.Fatalf("unexpected email: %q", email)
	}
}

func TestEmailFromAuthFileMissingEmail(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "auth.json")
	idToken := syntheticJWT(t, map[string]any{"sub": "abc123"})
	content := `{"tokens":{"id_token":"` + idToken + `"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	email, err := emailFromAuthFile(path)
	if err != nil {
		t.Fatalf("emailFromAuthFile returned error: %v", err)
	}
	if email != "" {
		t.Fatalf("expected empty email, got %q", email)
	}
}

func syntheticJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	header := map[string]any{"alg": "none", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	enc := base64.RawURLEncoding
	return enc.EncodeToString(headerJSON) + "." + enc.EncodeToString(claimsJSON) + "."
}
