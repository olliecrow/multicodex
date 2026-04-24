package multicodex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdCLIRunsInteractiveCodexWithProfileDefaults(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "crowoy")

	if err := app.Run([]string{"cli", "crowoy", "check this repo"}); err != nil {
		t.Fatalf("cli failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=crowoy") {
		t.Fatalf("expected crowoy profile in log, got %q", log)
	}
	wantCodexHome := filepath.Join(app.store.paths.ProfilesDir, "crowoy", "codex-home")
	if !strings.Contains(log, "codex_home="+wantCodexHome) {
		t.Fatalf("expected crowoy CODEX_HOME in log, got %q", log)
	}
	wantArgs := "--search --dangerously-bypass-approvals-and-sandbox -m gpt-5.5 -c model_reasoning_effort=medium check this repo"
	if !strings.Contains(log, "args="+wantArgs) {
		t.Fatalf("expected cli args %q in log, got %q", wantArgs, log)
	}
}

func TestCmdCLIFailsWhenSharedConfigDoesNotUseFileStore(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "crowoy")
	writeDefaultConfig(t, app, "model = \"global\"\n")

	err := app.Run([]string{"cli", "crowoy"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Message, "requires file-backed auth") {
		t.Fatalf("unexpected error message: %s", exitErr.Message)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected codex to not be invoked, stat err=%v", err)
	}
}

func TestCmdCLIHelpWorksWithoutProfiles(t *testing.T) {
	app := newTestAppForCLI(t)

	out, err := captureStdout(t, func() error {
		return app.Run([]string{"cli", "--help"})
	})
	if err != nil {
		t.Fatalf("cli --help failed: %v", err)
	}
	if !strings.Contains(out, "multicodex cli <name>") {
		t.Fatalf("expected cli help, got %q", out)
	}
}
