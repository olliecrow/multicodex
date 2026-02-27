package multicodex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecureAuthFilePermissions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexHome := filepath.Join(root, "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	authPath := filepath.Join(codexHome, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"a"}}`), 0o644); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	if err := secureAuthFilePermissions(codexHome); err != nil {
		t.Fatalf("secureAuthFilePermissions: %v", err)
	}

	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected mode 0600, got %o", got)
	}
}
