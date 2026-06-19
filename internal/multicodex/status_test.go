package multicodex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestCmdStatusRejectsAuthSymlinkBeforeCodexStatus(t *testing.T) {
	app, logPath := newStatusTestApp(t)
	writeDefaultFileStoreConfig(t, app)
	createTestProfiles(t, app, "work")
	profileHome := filepath.Join(app.store.paths.ProfilesDir, "work", "codex-home")
	target := filepath.Join(t.TempDir(), "shared-auth.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"access_token":"a"}}`), 0o600); err != nil {
		t.Fatalf("write target auth: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(profileHome, "auth.json")); err != nil {
		t.Fatalf("symlink auth: %v", err)
	}

	out, err := captureStdout(t, app.cmdStatus)
	if err != nil {
		t.Fatalf("cmdStatus: %v", err)
	}
	if !strings.Contains(out, "auth path is a symlink") {
		t.Fatalf("expected symlink error in status output, got %q", out)
	}
	if _, statErr := os.Stat(logPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected codex status not to be invoked, stat err=%v", statErr)
	}
}

func TestCmdStatusRequiresFileStoreBeforeCodexStatus(t *testing.T) {
	app, logPath := newStatusTestApp(t)
	writeDefaultConfig(t, app, "model = \"global\"\n")
	createTestProfiles(t, app, "work")

	out, err := captureStdout(t, app.cmdStatus)
	if err != nil {
		t.Fatalf("cmdStatus: %v", err)
	}
	if !strings.Contains(out, "requires file-backed auth") {
		t.Fatalf("expected file-store error in status output, got %q", out)
	}
	if _, statErr := os.Stat(logPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected codex status not to be invoked, stat err=%v", statErr)
	}
}

func TestCodexLoginStatusTreatsZeroExitNegativeOutputAsLoggedOut(t *testing.T) {
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	script := "#!/bin/sh\nprintf 'not logged in\\n'\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	state, _, detail := codexLoginStatus(filepath.Join(root, "codex-home"))
	if state != "logged-out" {
		t.Fatalf("expected logged-out for zero-exit negative status, got state=%q detail=%q", state, detail)
	}
	if detail != "not logged in" {
		t.Fatalf("expected negative status detail to be preserved, got %q", detail)
	}
}

func newStatusTestApp(t *testing.T) (*App, string) {
	t.Helper()

	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multi"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "default-codex"))

	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	logPath := filepath.Join(root, "codex-status.log")
	script := "#!/bin/sh\nprintf 'codex login status invoked\\n' > " + shellQuote(logPath) + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return app, logPath
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
