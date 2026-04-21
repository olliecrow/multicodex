package multicodex

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpCommandGlobal(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"help"})
	})
	if err != nil {
		t.Fatalf("help command failed: %v", err)
	}
	if !strings.Contains(out, "completion <shell>") {
		t.Fatalf("expected completion command in help output")
	}
	if !strings.Contains(out, "monitor [flags]") {
		t.Fatalf("expected monitor command in help output")
	}
	if !strings.Contains(out, "exec [codex exec args]") {
		t.Fatalf("expected exec command in help output")
	}
	if !strings.Contains(out, "app <name>") {
		t.Fatalf("expected app command in help output")
	}
	if !strings.Contains(out, "multicodex help <command>") {
		t.Fatalf("expected help topic usage in help output")
	}
}

func TestHelpCommandTopic(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"help", "heartbeat"})
	})
	if err != nil {
		t.Fatalf("help topic failed: %v", err)
	}
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "multicodex heartbeat") {
		t.Fatalf("unexpected help topic output: %s", out)
	}
}

func TestHelpUnknownTopic(t *testing.T) {
	app := newTestAppForCLI(t)
	_, err := captureStdout(t, func() error {
		return app.Run([]string{"help", "does-not-exist"})
	})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Message, "unknown help topic") {
		t.Fatalf("unexpected message: %s", exitErr.Message)
	}
}

func TestCompletionCommandBash(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"completion", "bash"})
	})
	if err != nil {
		t.Fatalf("completion bash failed: %v", err)
	}
	if !strings.Contains(out, "complete -F _multicodex_complete multicodex") {
		t.Fatalf("expected bash completion registration")
	}
	if !strings.Contains(out, "monitor") {
		t.Fatalf("expected monitor command in completion output")
	}
	if !strings.Contains(out, "exec") {
		t.Fatalf("expected exec command in completion output")
	}
	if !strings.Contains(out, "app") {
		t.Fatalf("expected app command in completion output")
	}
	if !strings.Contains(out, "__complete-profiles") {
		t.Fatalf("expected dynamic profile completion helper")
	}
	if !strings.Contains(out, "monitor\\ tui") {
		t.Fatalf("expected nested monitor tui help topic in bash completion output")
	}
}

func TestCompletionCommandUnsupportedShell(t *testing.T) {
	app := newTestAppForCLI(t)
	_, err := captureStdout(t, func() error {
		return app.Run([]string{"completion", "tcsh"})
	})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %d", exitErr.Code)
	}
}

func TestCompleteProfilesSorted(t *testing.T) {
	app := newTestAppForCLI(t)
	if err := app.store.EnsureBaseDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Profiles["zeta"] = Profile{Name: "zeta", CodexHome: filepath.Join(app.store.paths.ProfilesDir, "zeta", "codex-home")}
	cfg.Profiles["alpha"] = Profile{Name: "alpha", CodexHome: filepath.Join(app.store.paths.ProfilesDir, "alpha", "codex-home")}
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return app.Run([]string{"__complete-profiles"})
	})
	if err != nil {
		t.Fatalf("complete profiles failed: %v", err)
	}
	lines := strings.Fields(strings.TrimSpace(out))
	if len(lines) != 2 || lines[0] != "alpha" || lines[1] != "zeta" {
		t.Fatalf("unexpected profile list: %q", out)
	}
}

func newTestAppForCLI(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multi"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "default-codex"))

	app, err := NewApp()
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		_ = w.Close()
		os.Stdout = old
	}()

	runErr := fn()
	_ = w.Close()
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read output: %v", readErr)
	}
	_ = r.Close()
	return string(out), runErr
}
