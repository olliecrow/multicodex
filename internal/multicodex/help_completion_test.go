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
	if !strings.Contains(out, "exec [--search] [codex exec args]") {
		t.Fatalf("expected exec command in help output")
	}
	if !strings.Contains(out, "cli [--account <name>]") {
		t.Fatalf("expected cli command in help output")
	}
	if !strings.Contains(out, "reconcile") {
		t.Fatalf("expected reconcile command in help output")
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
	if !strings.Contains(out, "do not persist Codex session files") {
		t.Fatalf("expected ephemeral session guarantee in heartbeat help: %s", out)
	}
}

func TestHelpExecDescribesDefaultLoginCheck(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"help", "exec"})
	})
	if err != nil {
		t.Fatalf("help exec failed: %v", err)
	}
	if !strings.Contains(out, "official Codex CLI confirms its login") {
		t.Fatalf("expected default login check in exec help: %s", out)
	}
}

func TestHelpCLIDescribesAutomaticAndManualAccountSelection(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"help", "cli"})
	})
	if err != nil {
		t.Fatalf("help cli failed: %v", err)
	}
	for _, want := range []string{"same weekly-usage rules", "--account <name>", "multicodex cli --account work"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in cli help: %s", want, out)
		}
	}
}

func TestHelpGenerateDescribesCustomGenerationControls(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"help", "generate"})
	})
	if err != nil {
		t.Fatalf("help generate failed: %v", err)
	}
	for _, want := range []string{"--base-instructions-file", "--developer-instructions-file", "--effort", "--output-schema", "--json", "sanitized"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in generate help: %s", want, out)
		}
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
	if !strings.Contains(out, "cli") {
		t.Fatalf("expected cli command in completion output")
	}
	if !strings.Contains(out, "reconcile") {
		t.Fatalf("expected reconcile command in completion output")
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

func TestCompletionSuggestsProfilesOnlyForAccountArguments(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{name: "bash", out: renderBashCompletion(), want: `[[ "${COMP_WORDS[2]}" == "--account" ]]`},
		{name: "zsh", out: renderZshCompletion(), want: `[[ "${words[3]:-}" == "--account" ]]`},
		{name: "fish", out: renderFishCompletion(), want: "__fish_seen_subcommand_from cli' -l account -r"},
	}
	for _, test := range tests {
		if !strings.Contains(test.out, test.want) {
			t.Errorf("%s completion does not scope profiles to --account: missing %q", test.name, test.want)
		}
	}
}

func TestFishCompletionIncludesGenerateHelp(t *testing.T) {
	if !strings.Contains(renderFishCompletion(), "cli exec generate status") {
		t.Fatal("Fish help completion omits generate")
	}
}

func TestGenerateCompletionsIncludeCustomControls(t *testing.T) {
	for name, completion := range map[string]string{
		"bash": renderBashCompletion(),
		"zsh":  renderZshCompletion(),
		"fish": renderFishCompletion(),
	} {
		for _, flag := range []string{"--effort", "--base-instructions-file", "--developer-instructions-file", "--output-schema", "--json"} {
			if !strings.Contains(completion, flag) && !strings.Contains(completion, strings.TrimPrefix(flag, "--")) {
				t.Errorf("%s completion omits %s", name, flag)
			}
		}
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
