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

func TestEnsureProfileAuthPathSafeRejectsLoosePermissions(t *testing.T) {
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

	_, _, err := ensureProfileAuthPathSafe(codexHome)
	if err == nil {
		t.Fatal("expected loose auth permissions to fail")
	}
	if !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("expected permissions error, got %v", err)
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

func TestSecureAuthFilePermissionsRejectsHardLink(t *testing.T) {
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
	authPath := filepath.Join(codexHome, "auth.json")
	if err := os.Link(target, authPath); err != nil {
		t.Skipf("hard links are not supported here: %v", err)
	}

	err := secureAuthFilePermissions(codexHome)
	if err == nil {
		t.Fatal("expected hard-linked auth file to fail")
	}
	if !strings.Contains(err.Error(), "multiple hard links") {
		t.Fatalf("expected hard-link error, got %v", err)
	}
}

func TestEnsureProfileAuthPathSafeRejectsSymlinkedHome(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realHome := filepath.Join(root, "real-codex-home")
	if err := os.MkdirAll(realHome, 0o700); err != nil {
		t.Fatalf("mkdir real home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realHome, "auth.json"), []byte(`{"tokens":{"access_token":"a"}}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
	linkHome := filepath.Join(root, "linked-codex-home")
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Fatalf("symlink codex home: %v", err)
	}

	_, _, err := ensureProfileAuthPathSafe(linkHome)
	if err == nil {
		t.Fatal("expected symlinked codex home to fail")
	}
	if !strings.Contains(err.Error(), "profile codex home is a symlink") {
		t.Fatalf("expected symlinked home error, got %v", err)
	}
}
