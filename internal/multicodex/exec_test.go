package multicodex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"multicodex/internal/monitor/usage"
)

func TestCmdExecRunsCodexExecWithSelectedProfile(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha", "beta")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{
			Account:              usage.MonitorAccount{Label: "beta"},
			PrimaryUsedPercent:   15,
			SecondaryUsedPercent: 10,
		}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=beta") {
		t.Fatalf("expected beta profile in log, got %q", log)
	}
	if !strings.Contains(log, "args=exec --skip-git-repo-check hello") {
		t.Fatalf("expected exec args in log, got %q", log)
	}
}

func TestSelectExecProfileFallsBackToFirstProfileWithAuth(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha", "beta")
	if err := os.WriteFile(filepath.Join(app.store.paths.ProfilesDir, "beta", "codex-home", "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	name, _, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("boom")
	})
	if err != nil {
		t.Fatalf("selectExecProfile: %v", err)
	}
	if name != "beta" {
		t.Fatalf("expected beta auth fallback, got %q", name)
	}
}

func TestSelectExecProfileFallsBackToFirstSortedProfileWithoutAuth(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "zeta", "alpha")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	name, _, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("boom")
	})
	if err != nil {
		t.Fatalf("selectExecProfile: %v", err)
	}
	if name != "alpha" {
		t.Fatalf("expected alpha sorted fallback, got %q", name)
	}
}

func newExecTestApp(t *testing.T) (*App, string) {
	t.Helper()

	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multi"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "default-codex"))

	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	logPath := filepath.Join(root, "codex.log")
	script := "#!/usr/bin/env bash\nset -euo pipefail\nprofile=\"${MULTICODEX_ACTIVE_PROFILE:-}\"\nprintf 'profile=%s\\nargs=%s\\n' \"$profile\" \"$*\" > " + shellQuote(logPath) + "\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return app, logPath
}

func createExecProfiles(t *testing.T, app *App, names ...string) {
	t.Helper()

	if err := app.store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	cfg := DefaultConfig()
	for _, name := range names {
		profileHome := filepath.Join(app.store.paths.ProfilesDir, name, "codex-home")
		if err := os.MkdirAll(profileHome, 0o700); err != nil {
			t.Fatalf("mkdir profile home: %v", err)
		}
		cfg.Profiles[name] = Profile{Name: name, CodexHome: profileHome}
	}
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
}
