package multicodex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestCodexLoginStatusRedactsFailureOutput(t *testing.T) {
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	script := "#!/bin/sh\nprintf 'opaque-provider-diagnostic\\n' >&2\nexit 7\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	state, _, detail := codexLoginStatus(filepath.Join(root, "codex-home"))
	if state != "error" {
		t.Fatalf("expected error state, got %q", state)
	}
	if strings.Contains(detail, "opaque-provider-diagnostic") {
		t.Fatalf("failure output leaked into detail: %q", detail)
	}
	if !strings.Contains(detail, "exit code 7") {
		t.Fatalf("expected safe exit-code detail, got %q", detail)
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

func TestStatusDoesNotResolveOrMutateProfileResources(t *testing.T) {
	app, _ := newStatusTestApp(t)
	if err := app.store.EnsureBaseDirs(); err != nil {
		t.Fatal(err)
	}
	inherit := true
	sources := []string{"missing"}
	cfg := DefaultConfig()
	cfg.ProfileResources = &ProfileResources{Skills: &SkillResources{Inherit: &inherit, Sources: &sources}}
	if err := app.store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.cmdStatus(); err != nil {
		t.Fatalf("auth-only status should not resolve resource paths: %v", err)
	}
	entries, err := os.ReadDir(app.store.paths.ProfilesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("status mutated profile state: %v", entries)
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

func writeFakeLoginStatusCodex(t *testing.T, script string) string {
	t.Helper()
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := filepath.Join(root, "codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	return home
}

func TestCodexLoginStatusReportsOnlyFixedDetails(t *testing.T) {
	const marker = "SYNTHETIC-PRIVATE-MARKER"
	tests := []struct {
		name   string
		script string
		state  string
		detail string
	}{
		{"chatgpt", "echo 'Logged in using ChatGPT' >&2\n", "logged-in", "logged in using ChatGPT"},
		{"api key suffix", "echo 'Logged in using an API key - sk-" + marker + "' >&2\n", "logged-in", "logged in using an API key"},
		{"warning before confirmation", "echo 'WARNING: " + marker + "' >&2\necho 'Logged in using ChatGPT' >&2\n", "logged-in", "logged in using ChatGPT"},
		{"unknown method", "echo 'Logged in using " + marker + "'\n", "logged-in", "logged in"},
		{"secret-like success", "echo '" + marker + " authenticated active logged'\n", "error", "codex login status output is not a recognized login confirmation"},
		{"empty success", "exit 0\n", "error", "codex login status output is not a recognized login confirmation"},
		{"logged out", "echo 'Not logged in' >&2\nexit 1\n", "logged-out", "not logged in"},
		{"failure", "echo '" + marker + "' >&2\nexit 7\n", "error", "codex login status failed with exit code 7"},
		{"confirmation with failure exit", "echo 'Logged in using ChatGPT'\nexit 3\n", "error", "codex login status failed with exit code 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := writeFakeLoginStatusCodex(t, tt.script)
			state, account, detail := codexLoginStatus(home)
			if state != tt.state || detail != tt.detail {
				t.Fatalf("codexLoginStatus = %q, %q; want %q, %q", state, detail, tt.state, tt.detail)
			}
			if account != "-" || strings.Contains(detail, marker) {
				t.Fatalf("raw status output leaked: account=%q detail=%q", account, detail)
			}
		})
	}
}

func TestCodexLoginStatusStopsOversizedOutput(t *testing.T) {
	home := writeFakeLoginStatusCodex(t, "while :; do echo 'Logged in using ChatGPT padding padding padding'; done\n")
	start := time.Now()
	state, _, detail := codexLoginStatus(home)
	if state != "error" || detail != "codex login status output exceeded 64 KiB" {
		t.Fatalf("oversized status = %q, %q", state, detail)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("oversized status was not stopped promptly: %s", elapsed)
	}
}

func TestCodexLoginStatusSanitizesAuthFileAccount(t *testing.T) {
	home := writeFakeLoginStatusCodex(t, "echo 'Logged in using ChatGPT'\n")
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"email":"user@example.com\u001b[2J"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if _, account, _ := codexLoginStatus(home); account != "-" {
		t.Fatalf("control characters reached account display: %q", account)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"email":"user@example.com"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if _, account, _ := codexLoginStatus(home); account != "user@example.com" {
		t.Fatalf("plain email account = %q", account)
	}
}
