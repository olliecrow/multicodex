package multicodex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCmdExecWritesSelectedProfileMetadata(t *testing.T) {
	app, _ := newExecTestApp(t)
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

	metadataPath := filepath.Join(t.TempDir(), "selected-profile.json")
	t.Setenv(envSelectedProfilePath, metadataPath)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var payload struct {
		Profile              string `json:"profile"`
		SelectionSource      string `json:"selection_source"`
		PrimaryUsedPercent   *int   `json:"primary_used_percent"`
		SecondaryUsedPercent *int   `json:"secondary_used_percent"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if payload.Profile != "beta" {
		t.Fatalf("expected selected profile beta, got %q", payload.Profile)
	}
	if payload.SelectionSource != "usage_selector" {
		t.Fatalf("expected selection source usage_selector, got %q", payload.SelectionSource)
	}
	if payload.PrimaryUsedPercent == nil || *payload.PrimaryUsedPercent != 15 {
		t.Fatalf("expected primary_used_percent 15, got %v", payload.PrimaryUsedPercent)
	}
	if payload.SecondaryUsedPercent == nil || *payload.SecondaryUsedPercent != 10 {
		t.Fatalf("expected secondary_used_percent 10, got %v", payload.SecondaryUsedPercent)
	}
}

func TestCmdExecHelpWorksWithoutProfiles(t *testing.T) {
	app, logPath := newExecTestApp(t)

	if err := app.Run([]string{"exec", "--help"}); err != nil {
		t.Fatalf("exec --help failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=") {
		t.Fatalf("expected fake codex invocation to be recorded, got %q", log)
	}
	if !strings.Contains(log, "args=exec --help") {
		t.Fatalf("expected exec --help passthrough, got %q", log)
	}
}

func TestCmdExecFailsWhenSharedConfigDoesNotUseFileStore(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha")
	writeDefaultConfig(t, app, "model = \"global\"\n")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{Account: usage.MonitorAccount{Label: "alpha"}}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"})
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

func TestExecArgsAreHelpRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "empty", args: nil, want: false},
		{name: "help flag", args: []string{"--help"}, want: true},
		{name: "short help flag", args: []string{"-h"}, want: true},
		{name: "help subcommand", args: []string{"help"}, want: true},
		{name: "help after other args", args: []string{"--model", "gpt-5", "--help"}, want: true},
		{name: "normal exec", args: []string{"prompt"}, want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := execArgsAreHelpRequest(tc.args); got != tc.want {
				t.Fatalf("execArgsAreHelpRequest(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestCmdExecSelectsBestProfileUsingDefaultSelector(t *testing.T) {
	app, logPath, root := newExecSelectionTestApp(t)
	createExecProfiles(t, app, "alpha", "beta", "gamma")
	writeExecSelectionProfileData(t, root, "alpha", 10, 40, 96*time.Hour)
	writeExecSelectionProfileData(t, root, "beta", 20, 20, 36*time.Hour)
	writeExecSelectionProfileData(t, root, "gamma", 80, 1, 12*time.Hour)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "prompt with spaces"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=beta") {
		t.Fatalf("expected beta profile from default selector, got %q", log)
	}
	if !strings.Contains(log, "arg[2]=prompt with spaces") {
		t.Fatalf("expected prompt arg to pass through unchanged, got %q", log)
	}
}

func TestCmdExecSkipsWeeklyExhaustedProfileUsingDefaultSelector(t *testing.T) {
	app, logPath, root := newExecSelectionTestApp(t)
	createExecProfiles(t, app, "alpha", "beta", "gamma")
	writeExecSelectionProfileData(t, root, "alpha", 0, 100, 1*time.Hour)
	writeExecSelectionProfileData(t, root, "beta", 0, 85, 2*time.Hour)
	writeExecSelectionProfileData(t, root, "gamma", 50, 10, 30*time.Minute)

	metadataPath := filepath.Join(t.TempDir(), "selected-profile.json")
	t.Setenv(envSelectedProfilePath, metadataPath)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=beta") {
		t.Fatalf("expected beta profile from default selector, got %q", log)
	}

	metadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var payload struct {
		Profile              string `json:"profile"`
		SelectionSource      string `json:"selection_source"`
		PrimaryUsedPercent   *int   `json:"primary_used_percent"`
		SecondaryUsedPercent *int   `json:"secondary_used_percent"`
	}
	if err := json.Unmarshal(metadata, &payload); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if payload.Profile != "beta" {
		t.Fatalf("expected selected profile beta, got %q", payload.Profile)
	}
	if payload.SelectionSource != "usage_selector" {
		t.Fatalf("expected selection source usage_selector, got %q", payload.SelectionSource)
	}
	if payload.PrimaryUsedPercent == nil || *payload.PrimaryUsedPercent != 0 {
		t.Fatalf("expected primary_used_percent 0, got %v", payload.PrimaryUsedPercent)
	}
	if payload.SecondaryUsedPercent == nil || *payload.SecondaryUsedPercent != 85 {
		t.Fatalf("expected secondary_used_percent 85, got %v", payload.SecondaryUsedPercent)
	}
}

func TestSelectExecProfileFallsBackToRandomProfileWhenSelectionFails(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha", "beta")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	originalChooser := chooseRandomProfileName
	chooseRandomProfileName = func(names []string) string {
		if len(names) != 2 {
			t.Fatalf("expected 2 names, got %d", len(names))
		}
		return "beta"
	}
	defer func() { chooseRandomProfileName = originalChooser }()

	selected, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("boom")
	})
	if err != nil {
		t.Fatalf("selectExecProfile: %v", err)
	}
	if selected.Name != "beta" {
		t.Fatalf("expected beta random fallback, got %q", selected.Name)
	}
	if selected.Metadata.SelectionSource != "random_profile_fallback" {
		t.Fatalf("expected random fallback selection source, got %q", selected.Metadata.SelectionSource)
	}
}

func TestSelectExecProfileFallsBackToOnlyProfileWhenSelectionFails(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	originalChooser := chooseRandomProfileName
	chooseRandomProfileName = func(names []string) string {
		if len(names) != 1 {
			t.Fatalf("expected 1 name, got %d", len(names))
		}
		return names[0]
	}
	defer func() { chooseRandomProfileName = originalChooser }()

	selected, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("boom")
	})
	if err != nil {
		t.Fatalf("selectExecProfile: %v", err)
	}
	if selected.Name != "alpha" {
		t.Fatalf("expected alpha fallback, got %q", selected.Name)
	}
	if selected.Metadata.SelectionSource != "random_profile_fallback" {
		t.Fatalf("expected random fallback selection source, got %q", selected.Metadata.SelectionSource)
	}
}

func TestCmdExecFallsBackToRandomProfileWhenUsageSelectionFails(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha", "beta")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("boom")
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	originalChooser := chooseRandomProfileName
	chooseRandomProfileName = func(names []string) string {
		if len(names) != 2 {
			t.Fatalf("expected 2 names, got %d", len(names))
		}
		return "beta"
	}
	defer func() { chooseRandomProfileName = originalChooser }()

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
}

func TestSelectExecProfilePersistsUsageSelectionMetadata(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha", "beta")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	selected, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{
			Account:              usage.MonitorAccount{Label: "beta"},
			PrimaryUsedPercent:   39,
			SecondaryUsedPercent: 7,
		}, nil
	})
	if err != nil {
		t.Fatalf("selectExecProfile: %v", err)
	}
	if selected.Name != "beta" {
		t.Fatalf("expected beta selected, got %q", selected.Name)
	}
	if selected.Metadata.SelectionSource != "usage_selector" {
		t.Fatalf("expected usage_selector selection source, got %q", selected.Metadata.SelectionSource)
	}
	if selected.Metadata.PrimaryUsedPercent == nil || *selected.Metadata.PrimaryUsedPercent != 39 {
		t.Fatalf("expected primary_used_percent 39, got %v", selected.Metadata.PrimaryUsedPercent)
	}
	if selected.Metadata.SecondaryUsedPercent == nil || *selected.Metadata.SecondaryUsedPercent != 7 {
		t.Fatalf("expected secondary_used_percent 7, got %v", selected.Metadata.SecondaryUsedPercent)
	}
}

func TestWriteSelectedProfileMetadataNoPathIsNoOp(t *testing.T) {
	if err := writeSelectedProfileMetadata("", execSelectionMetadata{Profile: "alpha"}); err != nil {
		t.Fatalf("writeSelectedProfileMetadata without path failed: %v", err)
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
	script := "#!/usr/bin/env bash\nset -euo pipefail\nprofile=\"${MULTICODEX_ACTIVE_PROFILE:-}\"\nprintf 'profile=%s\\ncodex_home=%s\\nargs=%s\\n' \"$profile\" \"${CODEX_HOME:-}\" \"$*\" > " + shellQuote(logPath) + "\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	writeDefaultFileStoreConfig(t, app)
	return app, logPath
}

func newExecSelectionTestApp(t *testing.T) (*App, string, string) {
	t.Helper()

	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multi"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "default-codex"))

	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	logPath := filepath.Join(root, "codex.log")
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "--version" ]]; then
  echo "codex-cli fake"
  exit 0
fi
if [[ "${1:-}" == "-s" && "${2:-}" == "read-only" && "${3:-}" == "-a" && "${4:-}" == "untrusted" && "${5:-}" == "app-server" ]]; then
  python3 -c '
import json
import os
import sys

home = sys.argv[1]
usage_path = os.path.join(home, "usage.json")
with open(usage_path, "r", encoding="utf-8") as fh:
    usage = json.load(fh)

primary = int(usage["primary_used_percent"])
secondary = int(usage["weekly_used_percent"])
email = usage.get("email", "")
primary_resets_at = usage.get("primary_resets_at")
secondary_resets_at = usage.get("secondary_resets_at")
rate_limits = {
    "limitId": "codex",
    "planType": "pro",
    "primary": {"usedPercent": primary, "windowDurationMins": 300, "resetsAt": primary_resets_at},
    "secondary": {"usedPercent": secondary, "windowDurationMins": 10080, "resetsAt": secondary_resets_at},
}

for raw_line in sys.stdin:
    raw_line = raw_line.strip()
    if not raw_line:
        continue
    req = json.loads(raw_line)
    method = req.get("method")
    req_id = req.get("id")
    if method == "initialized":
        continue
    if method == "initialize":
        result = {}
    elif method == "account/rateLimits/read":
        result = {"rateLimits": rate_limits, "rateLimitsByLimitId": {"codex": rate_limits}}
    elif method == "account/read":
        result = {"account": {"email": email}, "requiresOpenAIAuth": False}
    else:
        result = {}
    if req_id is not None:
        print(json.dumps({"jsonrpc": "2.0", "id": req_id, "result": result}), flush=True)
' "$CODEX_HOME"
  exit $?
fi
if [[ "${1:-}" == "exec" ]]; then
  : "${MULTICODEX_FAKE_CODEX_LOG:?MULTICODEX_FAKE_CODEX_LOG must be set}"
  {
    printf 'profile=%s\n' "${MULTICODEX_ACTIVE_PROFILE:-}"
    i=0
    for arg in "$@"; do
      printf 'arg[%d]=%s\n' "$i" "$arg"
      i=$((i+1))
    done
  } >> "${MULTICODEX_FAKE_CODEX_LOG}"
  exit 0
fi
echo "unexpected fake codex invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))
	t.Setenv("MULTICODEX_FAKE_CODEX_LOG", logPath)

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	writeDefaultFileStoreConfig(t, app)
	return app, logPath, root
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

func writeExecSelectionProfileData(t *testing.T, root, name string, primaryUsed, weeklyUsed int, weeklyResetIn time.Duration) {
	t.Helper()

	home := filepath.Join(root, "multi", "profiles", name, "codex-home")
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(fmt.Sprintf(`{"tokens":{"access_token":"token-%s"}}`, name)), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	now := time.Now().UTC()
	usageJSON := fmt.Sprintf(
		`{"primary_used_percent": %d, "weekly_used_percent": %d, "email": "%s@example.com", "primary_resets_at": %d, "secondary_resets_at": %d}`,
		primaryUsed,
		weeklyUsed,
		name,
		now.Add(5*time.Hour).Unix(),
		now.Add(weeklyResetIn).Unix(),
	)
	if err := os.WriteFile(filepath.Join(home, "usage.json"), []byte(usageJSON), 0o600); err != nil {
		t.Fatalf("write usage: %v", err)
	}
}
