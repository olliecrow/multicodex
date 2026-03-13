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
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if payload.Profile != "beta" {
		t.Fatalf("expected selected profile beta, got %q", payload.Profile)
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
	writeExecSelectionProfileData(t, root, "alpha", 10, 40)
	writeExecSelectionProfileData(t, root, "beta", 55, 20)
	writeExecSelectionProfileData(t, root, "gamma", 80, 1)

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

func TestWriteSelectedProfileMetadataNoPathIsNoOp(t *testing.T) {
	if err := writeSelectedProfileMetadata("", "alpha"); err != nil {
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
rate_limits = {
    "limitId": "codex",
    "planType": "pro",
    "primary": {"usedPercent": primary, "windowDurationMins": 300},
    "secondary": {"usedPercent": secondary, "windowDurationMins": 10080},
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

func writeExecSelectionProfileData(t *testing.T, root, name string, primaryUsed, weeklyUsed int) {
	t.Helper()

	home := filepath.Join(root, "multi", "profiles", name, "codex-home")
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(fmt.Sprintf(`{"tokens":{"access_token":"token-%s"}}`, name)), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	usageJSON := fmt.Sprintf(`{"primary_used_percent": %d, "weekly_used_percent": %d, "email": "%s@example.com"}`, primaryUsed, weeklyUsed, name)
	if err := os.WriteFile(filepath.Join(home, "usage.json"), []byte(usageJSON), 0o600); err != nil {
		t.Fatalf("write usage: %v", err)
	}
}
