package multicodex

import (
	"errors"
	"strings"
	"testing"
)

func TestMonitorHelpIncludesDoctorAndTerminalUserInterfaceText(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"monitor", "help"})
	})
	if err != nil {
		t.Fatalf("monitor help failed: %v", err)
	}
	if !strings.Contains(out, "terminal user interface") {
		t.Fatalf("expected expanded terminal user interface text, got:\n%s", out)
	}
	if !strings.Contains(out, "multicodex monitor doctor") {
		t.Fatalf("expected doctor usage in monitor help, got:\n%s", out)
	}
}

func TestHelpMonitorTopic(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"help", "monitor"})
	})
	if err != nil {
		t.Fatalf("help monitor failed: %v", err)
	}
	if !strings.Contains(out, "multicodex monitor") {
		t.Fatalf("expected monitor help topic output, got:\n%s", out)
	}
}

func TestMonitorUnknownSubcommand(t *testing.T) {
	app := newTestAppForCLI(t)
	_, err := captureStdout(t, func() error {
		return app.Run([]string{"monitor", "snapshot"})
	})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Message, "unknown monitor command") {
		t.Fatalf("unexpected message: %s", exitErr.Message)
	}
}

func TestMonitorRequiresTTY(t *testing.T) {
	app := newTestAppForCLI(t)
	_, err := captureStdout(t, func() error {
		return app.Run([]string{"monitor"})
	})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Message, "requires a TTY") {
		t.Fatalf("unexpected message: %s", exitErr.Message)
	}
}

func TestMonitorCompletionDefaultsToBash(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"monitor", "completion"})
	})
	if err != nil {
		t.Fatalf("monitor completion failed: %v", err)
	}
	if !strings.Contains(out, "complete -F _multicodex_complete multicodex") {
		t.Fatalf("expected bash completion registration, got:\n%s", out)
	}
}

func TestHelpMonitorCompletionTopic(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error {
		return app.Run([]string{"help", "monitor", "completion"})
	})
	if err != nil {
		t.Fatalf("help monitor completion failed: %v", err)
	}
	if !strings.Contains(out, "multicodex monitor completion") {
		t.Fatalf("expected monitor completion help topic output, got:\n%s", out)
	}
}
