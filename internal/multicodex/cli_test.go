package multicodex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olliecrow/multicodex/internal/monitor/usage"
)

func TestCmdCLIAutomaticallySelectsAccountAndRunsInteractiveCodex(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "primary")

	originalSelector := defaultExecAccountSelector
	var selectedModel string
	defaultExecAccountSelector = func(_ context.Context, _ []usage.MonitorAccount, model string) (usage.SelectedAccount, error) {
		selectedModel = model
		return usage.SelectedAccount{Account: usage.MonitorAccount{Label: "primary"}, WeeklyUsedPercent: 12}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	if err := app.Run([]string{"cli", "-m=gpt-5-codex-spark", "check this repo"}); err != nil {
		t.Fatalf("cli failed: %v", err)
	}
	if selectedModel != "gpt-5-codex-spark" {
		t.Fatalf("expected explicit model to reach selector, got %q", selectedModel)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=primary") {
		t.Fatalf("expected primary profile in log, got %q", log)
	}
	wantCodexHome := filepath.Join(app.store.paths.ProfilesDir, "primary", "codex-home")
	if !strings.Contains(log, "codex_home="+wantCodexHome) {
		t.Fatalf("expected primary CODEX_HOME in log, got %q", log)
	}
	wantArgs := "-m=gpt-5-codex-spark check this repo"
	if !strings.Contains(log, "args="+wantArgs) {
		t.Fatalf("expected cli args %q in log, got %q", wantArgs, log)
	}
}

func TestCmdCLISelectsBestProfileUsingDefaultSelector(t *testing.T) {
	app, logPath, root := newExecSelectionTestApp(t)
	t.Setenv("MULTICODEX_FAKE_ALLOW_INTERACTIVE", "1")
	createExecProfiles(t, app, "alpha", "beta", "gamma")
	writeExecSelectionProfileData(t, root, "alpha", 10, 40, 96*time.Hour)
	writeExecSelectionProfileData(t, root, "beta", 20, 20, 36*time.Hour)
	writeExecSelectionProfileData(t, root, "gamma", 80, 1, 12*time.Hour)

	if err := app.Run([]string{"cli", "prompt with spaces"}); err != nil {
		t.Fatalf("cli failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=gamma") {
		t.Fatalf("expected gamma with the soonest weekly reset, got %q", log)
	}
	if !strings.Contains(log, "arg[0]=prompt with spaces") {
		t.Fatalf("expected prompt arg to pass through unchanged, got %q", log)
	}
}

func TestCmdCLIManualAccountBypassesRoutingAndStripsSeparator(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "primary")

	originalSelector := defaultExecAccountSelector
	selectorCalled := false
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		selectorCalled = true
		return usage.SelectedAccount{}, errors.New("selector must not run")
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	if err := app.Run([]string{"cli", "--account", "primary", "--", "check this repo"}); err != nil {
		t.Fatalf("cli failed: %v", err)
	}
	if selectorCalled {
		t.Fatal("manual account selection unexpectedly ran usage routing")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=primary") || !strings.Contains(log, "args=check this repo") {
		t.Fatalf("unexpected manual cli launch: %q", log)
	}
	if strings.Contains(log, "args=-- ") {
		t.Fatalf("manual separator reached Codex: %q", log)
	}
}

func TestCmdCLIAutomaticRoutingCanUseDefaultReserve(t *testing.T) {
	app, logPath := newExecTestApp(t)

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = selectDefaultExecAccountForTest(t)
	defer func() { defaultExecAccountSelector = originalSelector }()

	if err := app.Run([]string{"cli"}); err != nil {
		t.Fatalf("cli failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=\n") {
		t.Fatalf("expected no active profile for default reserve, got %q", log)
	}
	if !strings.Contains(log, "codex_home="+normalizeExecCodexHome(app.store.paths.DefaultCodexHome)) {
		t.Fatalf("expected default reserve CODEX_HOME, got %q", log)
	}
	if !strings.Contains(log, "args=\n") {
		t.Fatalf("expected interactive launch without arguments, got %q", log)
	}
}

func TestCmdCLIAutomaticRoutingFailsClosedForAnyInvalidProfile(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "primary", "broken")
	makeStoredProfilePathInvalid(t, app, "broken")

	originalSelector := defaultExecAccountSelector
	selectorCalled := false
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		selectorCalled = true
		return usage.SelectedAccount{Account: usage.MonitorAccount{Label: "primary"}}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	err := app.Run([]string{"cli"})
	if err == nil || !strings.Contains(err.Error(), "profile-local path") {
		t.Fatalf("expected unsafe profile to stop automatic routing, got %v", err)
	}
	if selectorCalled {
		t.Fatal("usage selector ran before every profile passed validation")
	}
	if _, statErr := os.Stat(logPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected Codex not to launch, stat err=%v", statErr)
	}
}

func TestCmdCLIManualAccountIgnoresUnrelatedInvalidProfile(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "primary", "broken")
	makeStoredProfilePathInvalid(t, app, "broken")

	if err := app.Run([]string{"cli", "--account", "primary"}); err != nil {
		t.Fatalf("manual cli failed because of unrelated profile: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "profile=primary") {
		t.Fatalf("expected primary manual profile, got %q", data)
	}
}

func TestCmdCLIAutomaticDefaultReserveMustBeLoggedIn(t *testing.T) {
	app, logPath := newExecTestApp(t)
	t.Setenv("MULTICODEX_FAKE_LOGIN_STATE", "logged-out")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = selectDefaultExecAccountForTest(t)
	defer func() { defaultExecAccountSelector = originalSelector }()

	err := app.Run([]string{"cli"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected logged-out reserve error, got %T (%v)", err, err)
	}
	if !strings.Contains(exitErr.Message, "default Codex account is not logged in") {
		t.Fatalf("unexpected reserve error: %s", exitErr.Message)
	}
	if _, statErr := os.Stat(logPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected Codex not to launch, stat err=%v", statErr)
	}
}

func makeStoredProfilePathInvalid(t *testing.T, app *App, name string) {
	t.Helper()
	cfg, err := app.loadOrInitConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profile := cfg.Profiles[name]
	profile.CodexHome = filepath.Join(t.TempDir(), "outside-profile-home")
	cfg.Profiles[name] = profile
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("save invalid profile fixture: %v", err)
	}
}

func TestCmdCLIManualAccountRequiresName(t *testing.T) {
	app := newTestAppForCLI(t)
	err := app.Run([]string{"cli", "--account"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected usage ExitError, got %T (%v)", err, err)
	}
	if !strings.Contains(exitErr.Message, "--account <name>") {
		t.Fatalf("unexpected usage error: %s", exitErr.Message)
	}
}

func TestCmdCLIFailsWhenSharedConfigDoesNotUseFileStore(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "primary")
	writeDefaultConfig(t, app, "model = \"global\"\n")

	err := app.Run([]string{"cli", "--account", "primary"})
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
	if !strings.Contains(out, "multicodex cli [--account <name>] [--] [codex args...]") {
		t.Fatalf("expected cli help, got %q", out)
	}
}

func TestCmdCLIKeepsGoalStateProfileLocalAcrossConcurrentTerminals(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multi"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "default-codex"))
	t.Setenv("MULTICODEX_FAKE_CODEX_LOG_DIR", root)

	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	script := `#!/usr/bin/env bash
set -euo pipefail
: "${CODEX_HOME:?CODEX_HOME must be set}"
: "${MULTICODEX_ACTIVE_PROFILE:?MULTICODEX_ACTIVE_PROFILE must be set}"
: "${MULTICODEX_FAKE_CODEX_LOG_DIR:?MULTICODEX_FAKE_CODEX_LOG_DIR must be set}"
mkdir -p "$CODEX_HOME"
goal_enabled=false
if [[ -f "$CODEX_HOME/config.toml" ]] && grep -Eq '^[[:space:]]*goals[[:space:]]*=[[:space:]]*true[[:space:]]*$' "$CODEX_HOME/config.toml"; then
  goal_enabled=true
fi
printf 'goal-state-for=%s\n' "$MULTICODEX_ACTIVE_PROFILE" > "$CODEX_HOME/state_5.sqlite"
{
  printf 'profile=%s\n' "$MULTICODEX_ACTIVE_PROFILE"
  printf 'codex_home=%s\n' "$CODEX_HOME"
  printf 'goal_enabled=%s\n' "$goal_enabled"
  printf 'args=%s\n' "$*"
} > "$MULTICODEX_FAKE_CODEX_LOG_DIR/$MULTICODEX_ACTIVE_PROFILE.log"
sleep 0.1
`
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	writeDefaultConfig(t, app, "model = \"global\"\ncli_auth_credentials_store = \"file\"\n\n[features]\ngoals = true\n")
	createExecProfiles(t, app, "alpha", "beta")

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, profileName := range []string{"alpha", "beta"} {
		profileName := profileName
		wg.Add(1)
		go func() {
			defer wg.Done()
			runApp, runErr := NewApp()
			if runErr != nil {
				errs <- runErr
				return
			}
			errs <- runApp.Run([]string{"cli", "--account", profileName, "--", "check goal state"})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("cli failed: %v", err)
		}
	}

	for _, profileName := range []string{"alpha", "beta"} {
		logPath := filepath.Join(root, profileName+".log")
		logData, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read %s log: %v", profileName, err)
		}
		log := string(logData)
		wantHome := filepath.Join(root, "multi", "profiles", profileName, "codex-home")
		if !strings.Contains(log, "profile="+profileName) {
			t.Fatalf("expected profile %s in log, got %q", profileName, log)
		}
		if !strings.Contains(log, "codex_home="+wantHome) {
			t.Fatalf("expected CODEX_HOME %s in log, got %q", wantHome, log)
		}
		if !strings.Contains(log, "goal_enabled=true") {
			t.Fatalf("expected goals enabled through profile config, got %q", log)
		}

		statePath := filepath.Join(wantHome, "state_5.sqlite")
		stateData, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatalf("read %s goal state: %v", profileName, err)
		}
		if got, want := string(stateData), "goal-state-for="+profileName+"\n"; got != want {
			t.Fatalf("unexpected %s goal state: got=%q want=%q", profileName, got, want)
		}
	}

	if _, err := os.Stat(filepath.Join(root, "default-codex", "state_5.sqlite")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected default Codex state to stay untouched, stat err=%v", err)
	}
}
