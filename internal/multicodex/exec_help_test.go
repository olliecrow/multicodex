package multicodex

import (
	"os"
	"strings"
	"testing"
)

func TestCmdExecHelpClearsStaleProfileEnv(t *testing.T) {
	app, logPath := newExecTestApp(t)
	t.Setenv("CODEX_HOME", "/tmp/stale-codex")
	t.Setenv("MULTICODEX_ACTIVE_PROFILE", "stale")

	if err := app.Run([]string{"exec", "--help"}); err != nil {
		t.Fatalf("exec help failed: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "args=exec --help") {
		t.Fatalf("expected help passthrough args, got %q", log)
	}
	if !strings.Contains(log, "profile=\n") {
		t.Fatalf("expected active profile to be cleared, got %q", log)
	}
	if !strings.Contains(log, "codex_home=\n") {
		t.Fatalf("expected codex home to be cleared, got %q", log)
	}
}
