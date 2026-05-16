package multicodex

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSecureAuthFilePermissionsRejectsSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexHome := filepath.Join(root, "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	target := filepath.Join(root, "shared-auth.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"access_token":"a"}}`), 0o600); err != nil {
		t.Fatalf("write target auth file: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(codexHome, "auth.json")); err != nil {
		t.Fatalf("symlink auth file: %v", err)
	}

	err := secureAuthFilePermissions(codexHome)
	if err == nil {
		t.Fatal("expected symlink auth file to fail")
	}
	if !strings.Contains(err.Error(), "auth path is a symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}
