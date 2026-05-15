package multicodex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIHelpDoesNotMigrateLegacyState(t *testing.T) {
	home := t.TempDir()
	legacyHome := filepath.Join(home, ".multicodex")
	legacyProfileHome := filepath.Join(legacyHome, "profiles", "work", "codex-home")
	if err := os.MkdirAll(legacyProfileHome, 0o700); err != nil {
		t.Fatalf("mkdir legacy profile: %v", err)
	}
	defaultCodexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(defaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	defaultAuthPath := filepath.Join(defaultCodexHome, "auth.json")
	legacyTarget := filepath.Join(legacyProfileHome, "auth.json")
	if err := os.Symlink(legacyTarget, defaultAuthPath); err != nil {
		t.Fatalf("symlink default auth path: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	if err := RunCLI([]string{"help"}); err != nil {
		t.Fatalf("RunCLI help: %v", err)
	}
	if _, err := os.Stat(legacyHome); err != nil {
		t.Fatalf("expected legacy home to remain, stat err=%v", err)
	}
	gotTarget, err := os.Readlink(defaultAuthPath)
	if err != nil {
		t.Fatalf("read default auth symlink: %v", err)
	}
	if gotTarget != legacyTarget {
		t.Fatalf("expected auth target %q, got %q", legacyTarget, gotTarget)
	}
}

func TestRunCLIUnknownCommandDoesNotMigrateLegacyState(t *testing.T) {
	home := t.TempDir()
	legacyHome := filepath.Join(home, ".multicodex")
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatalf("mkdir legacy home: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	err := RunCLI([]string{"typo-command"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if _, err := os.Stat(legacyHome); err != nil {
		t.Fatalf("expected legacy home to remain, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "multicodex")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected new home not to be created, stat err=%v", err)
	}
}

func TestRunCLIStatusDoesNotCreateConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	if err := RunCLI([]string{"status"}); err != nil {
		t.Fatalf("RunCLI status: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "multicodex", "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected status not to create config, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "multicodex")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected status not to create multicodex home, stat err=%v", err)
	}
}

func TestRunCLIExecHelpDoesNotMigrateLegacyState(t *testing.T) {
	home := t.TempDir()
	legacyHome := filepath.Join(home, ".multicodex")
	legacyProfileHome := filepath.Join(legacyHome, "profiles", "work", "codex-home")
	if err := os.MkdirAll(legacyProfileHome, 0o700); err != nil {
		t.Fatalf("mkdir legacy profile: %v", err)
	}
	defaultCodexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(defaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	defaultAuthPath := filepath.Join(defaultCodexHome, "auth.json")
	legacyTarget := filepath.Join(legacyProfileHome, "auth.json")
	if err := os.Symlink(legacyTarget, defaultAuthPath); err != nil {
		t.Fatalf("symlink default auth path: %v", err)
	}

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	logPath := filepath.Join(home, "codex.log")
	script := "#!/usr/bin/env bash\nset -euo pipefail\nprintf 'args=%s\\nprofile=%s\\ncodex_home=%s\\n' \"$*\" \"${MULTICODEX_ACTIVE_PROFILE:-}\" \"${CODEX_HOME:-}\" > " + shellQuote(logPath) + "\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", "/tmp/stale-codex")
	t.Setenv("MULTICODEX_ACTIVE_PROFILE", "stale")
	t.Setenv("MULTICODEX_HOME", "")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	if err := RunCLI([]string{"exec", "--help"}); err != nil {
		t.Fatalf("RunCLI exec help: %v", err)
	}
	if _, err := os.Stat(legacyHome); err != nil {
		t.Fatalf("expected legacy home to remain, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "multicodex")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected new home not to be created, stat err=%v", err)
	}
	gotTarget, err := os.Readlink(defaultAuthPath)
	if err != nil {
		t.Fatalf("read default auth symlink: %v", err)
	}
	if gotTarget != legacyTarget {
		t.Fatalf("expected auth target %q, got %q", legacyTarget, gotTarget)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read codex log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "args=exec --help") {
		t.Fatalf("expected help passthrough args, got %q", log)
	}
	if !strings.Contains(log, "profile=\n") {
		t.Fatalf("expected stale active profile to be cleared, got %q", log)
	}
	if !strings.Contains(log, "codex_home=\n") {
		t.Fatalf("expected stale codex home to be cleared, got %q", log)
	}
}

func TestRunCLICommandHelpDoesNotMigrateLegacyState(t *testing.T) {
	home := t.TempDir()
	legacyHome := filepath.Join(home, ".multicodex")
	legacyProfileHome := filepath.Join(legacyHome, "profiles", "work", "codex-home")
	if err := os.MkdirAll(legacyProfileHome, 0o700); err != nil {
		t.Fatalf("mkdir legacy profile: %v", err)
	}
	defaultCodexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(defaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	defaultAuthPath := filepath.Join(defaultCodexHome, "auth.json")
	legacyTarget := filepath.Join(legacyProfileHome, "auth.json")
	if err := os.Symlink(legacyTarget, defaultAuthPath); err != nil {
		t.Fatalf("symlink default auth path: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	if err := RunCLI([]string{"cli", "--help"}); err != nil {
		t.Fatalf("RunCLI cli help: %v", err)
	}
	if _, err := os.Stat(legacyHome); err != nil {
		t.Fatalf("expected legacy home to remain, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "multicodex")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected new home not to be created, stat err=%v", err)
	}
	gotTarget, err := os.Readlink(defaultAuthPath)
	if err != nil {
		t.Fatalf("read default auth symlink: %v", err)
	}
	if gotTarget != legacyTarget {
		t.Fatalf("expected auth target %q, got %q", legacyTarget, gotTarget)
	}
}
