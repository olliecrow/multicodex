package multicodex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdAppLaunchesOpenWithProfileEnv(t *testing.T) {
	app, logPath, appPath := newAppLaunchTestApp(t)
	createExecProfiles(t, app, "alpha")
	alphaHome := filepath.Join(app.store.paths.ProfilesDir, "alpha", "codex-home")
	if err := os.WriteFile(filepath.Join(alphaHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write profile auth: %v", err)
	}

	originalGOOS := desktopAppGOOS
	desktopAppGOOS = "darwin"
	defer func() { desktopAppGOOS = originalGOOS }()

	t.Setenv(envCodexAppPath, appPath)

	if err := app.Run([]string{"app", "alpha"}); err != nil {
		t.Fatalf("app command failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	wantAppDataDir, err := profileAppUserDataDir("alpha")
	if err != nil {
		t.Fatalf("resolve app data dir: %v", err)
	}
	if !strings.Contains(log, "args=-n -a "+appPath+" --args --user-data-dir="+wantAppDataDir) {
		t.Fatalf("expected open args in log, got %q", log)
	}
	wantHome := app.store.paths.DefaultCodexHome
	if !strings.Contains(log, "codex_home="+wantHome) {
		t.Fatalf("expected shared default codex home in log, got %q", log)
	}
	if !strings.Contains(log, "profile=alpha") {
		t.Fatalf("expected active profile in log, got %q", log)
	}
	if info, err := os.Stat(wantAppDataDir); err != nil || !info.IsDir() {
		t.Fatalf("expected app data dir %q to exist, stat err=%v", wantAppDataDir, err)
	}
	target, err := os.Readlink(app.store.paths.DefaultAuthPath)
	if err != nil {
		t.Fatalf("read switched default auth path: %v", err)
	}
	wantTarget := filepath.Join(app.store.paths.ProfilesDir, "alpha", "codex-home", "auth.json")
	if target != wantTarget {
		t.Fatalf("expected default auth path to point to %q, got %q", wantTarget, target)
	}

	cfg, err := app.store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Global.CurrentProfile != "alpha" {
		t.Fatalf("expected current profile alpha, got %q", cfg.Global.CurrentProfile)
	}
}

func TestCmdAppRequiresMacOS(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha")

	originalGOOS := desktopAppGOOS
	desktopAppGOOS = "linux"
	defer func() { desktopAppGOOS = originalGOOS }()

	err := app.Run([]string{"app", "alpha"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Message, "macOS only") {
		t.Fatalf("unexpected message: %s", exitErr.Message)
	}
}

func TestCmdAppUnknownProfile(t *testing.T) {
	app := newTestAppForCLI(t)

	originalGOOS := desktopAppGOOS
	desktopAppGOOS = "darwin"
	defer func() { desktopAppGOOS = originalGOOS }()

	err := app.Run([]string{"app", "missing"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Message, "unknown profile") {
		t.Fatalf("unexpected message: %s", exitErr.Message)
	}
}

func TestCmdAppUsesHelpUsage(t *testing.T) {
	app := newTestAppForCLI(t)

	originalGOOS := desktopAppGOOS
	desktopAppGOOS = "darwin"
	defer func() { desktopAppGOOS = originalGOOS }()

	err := app.Run([]string{"app"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Message, "usage: multicodex app <name>") {
		t.Fatalf("unexpected message: %s", exitErr.Message)
	}
}

func newAppLaunchTestApp(t *testing.T) (*App, string, string) {
	t.Helper()

	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multi"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "default-codex"))

	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	logPath := filepath.Join(root, "open.log")
	script := "#!/usr/bin/env bash\nset -euo pipefail\nprintf 'args=%s\\n' \"$*\" > " + shellQuote(logPath) + "\nprintf 'codex_home=%s\\n' \"${CODEX_HOME:-}\" >> " + shellQuote(logPath) + "\nprintf 'profile=%s\\n' \"${MULTICODEX_ACTIVE_PROFILE:-}\" >> " + shellQuote(logPath) + "\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "open"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake open: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	writeDefaultFileStoreConfig(t, app)

	appPath := filepath.Join(root, "Applications", "Codex.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatalf("mkdir fake app: %v", err)
	}
	return app, logPath, appPath
}
