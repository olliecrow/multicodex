package multicodex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCmdHeartbeatSuccessWithSkippedProfiles(t *testing.T) {
	app := newHeartbeatTestApp(t, fakeCodexScript{
		loginStatusByProfile: map[string]fakeStatus{
			"alpha":   {exitCode: 0, output: "Logged in using ChatGPT"},
			"bravo":   {exitCode: 1, output: "Not logged in"},
			"default": {exitCode: 0, output: "Logged in using ChatGPT"},
		},
		execByProfile: map[string]fakeStatus{
			"alpha": {exitCode: 0, output: "ok"},
		},
	})
	createHeartbeatProfiles(t, app, "alpha", "bravo")

	if err := app.cmdHeartbeat(nil); err != nil {
		t.Fatalf("expected heartbeat success, got %v", err)
	}
}

func TestCmdHeartbeatFailsWhenAnyLoggedInProfileFails(t *testing.T) {
	app := newHeartbeatTestApp(t, fakeCodexScript{
		loginStatusByProfile: map[string]fakeStatus{
			"alpha":   {exitCode: 0, output: "Logged in using ChatGPT"},
			"bravo":   {exitCode: 0, output: "Logged in using ChatGPT"},
			"default": {exitCode: 0, output: "Logged in using ChatGPT"},
		},
		execByProfile: map[string]fakeStatus{
			"alpha": {exitCode: 0, output: "ok"},
			"bravo": {exitCode: 1, output: "provider unavailable"},
		},
	})
	createHeartbeatProfiles(t, app, "alpha", "bravo")

	err := app.cmdHeartbeat(nil)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.Code)
	}
}

func TestCmdHeartbeatFailsWhenNoLoggedInProfiles(t *testing.T) {
	app := newHeartbeatTestApp(t, fakeCodexScript{
		loginStatusByProfile: map[string]fakeStatus{
			"alpha":   {exitCode: 1, output: "Not logged in"},
			"bravo":   {exitCode: 1, output: "Not logged in"},
			"default": {exitCode: 1, output: "Not logged in"},
		},
		execByProfile: map[string]fakeStatus{},
	})
	createHeartbeatProfiles(t, app, "alpha", "bravo")

	err := app.cmdHeartbeat(nil)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Message, "no logged-in profiles") {
		t.Fatalf("unexpected message: %s", exitErr.Message)
	}
}

func TestCmdHeartbeatSkipsWhenRunAlreadyInProgress(t *testing.T) {
	app := newHeartbeatTestApp(t, fakeCodexScript{
		loginStatusByProfile: map[string]fakeStatus{
			"alpha": {exitCode: 0, output: "Logged in using ChatGPT"},
		},
		execByProfile: map[string]fakeStatus{
			"alpha": {exitCode: 0, output: "ok"},
		},
	})
	createHeartbeatProfiles(t, app, "alpha")

	settings, err := loadHeartbeatSettings(app.store.paths)
	if err != nil {
		t.Fatalf("load heartbeat settings: %v", err)
	}
	lockFile, acquired, err := acquireHeartbeatLock(settings.LockPath)
	if err != nil {
		t.Fatalf("acquire heartbeat lock: %v", err)
	}
	if !acquired {
		t.Fatalf("expected first lock acquisition to succeed")
	}
	defer releaseHeartbeatLock(lockFile)

	out, err := captureStdout(t, func() error {
		return app.cmdHeartbeat(nil)
	})
	if err != nil {
		t.Fatalf("expected skip without error, got %v", err)
	}
	if !strings.Contains(out, "already in progress") {
		t.Fatalf("expected lock skip output, got %q", out)
	}
}

func TestRunCodexHeartbeatTimeout(t *testing.T) {
	root := t.TempDir()
	profileHome := filepath.Join(root, "profile")
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	codexPath := filepath.Join(fakeBin, "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "exec" ]; then
  sleep 3
  echo "ok"
  exit 0
fi
if [ "${1:-}" = "login" ] && [ "${2:-}" = "status" ]; then
  echo "Logged in using ChatGPT"
  exit 0
fi
if [ "${1:-}" = "--version" ]; then
  echo "codex-cli fake"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	prev := codexHeartbeatTimeout
	codexHeartbeatTimeout = 150 * time.Millisecond
	defer func() { codexHeartbeatTimeout = prev }()

	detail, err := runCodexHeartbeat(profileHome, heartbeatSettings{
		Prompt:   heartbeatPrompt,
		Timeout:  codexHeartbeatTimeout,
		Retries:  heartbeatRetryCount,
		Backoff:  heartbeatBackoff,
		LockPath: filepath.Join(root, "heartbeat.lock"),
	})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !strings.Contains(detail, "timed out") {
		t.Fatalf("expected timeout detail, got %q", detail)
	}
}

func TestRunCodexHeartbeatRedactsCLIOutputOnFailure(t *testing.T) {
	root := t.TempDir()
	profileHome := filepath.Join(root, "profile")
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	codexPath := filepath.Join(fakeBin, "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "exec" ]; then
  echo "Authorization: Bearer REDACTED_SECRET_PLACEHOLDER"
  exit 1
fi
if [ "${1:-}" = "--version" ]; then
  echo "codex-cli fake"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	detail, err := runCodexHeartbeat(profileHome, heartbeatSettings{
		Prompt:   heartbeatPrompt,
		Timeout:  codexHeartbeatTimeout,
		Retries:  heartbeatRetryCount,
		Backoff:  heartbeatBackoff,
		LockPath: filepath.Join(root, "heartbeat.lock"),
	})
	if err == nil {
		t.Fatalf("expected failure")
	}
	if strings.Contains(detail, "REDACTED_SECRET_PLACEHOLDER") {
		t.Fatalf("expected redacted detail, got %q", detail)
	}
	if !strings.Contains(detail, "exit code 1") {
		t.Fatalf("expected exit code detail, got %q", detail)
	}
}

func TestRunCodexHeartbeatRetriesWithReadOnlyExec(t *testing.T) {
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	attemptsPath := filepath.Join(root, "attempts.txt")
	argsPath := filepath.Join(root, "args.txt")
	cwdPath := filepath.Join(root, "cwd.txt")
	codexPath := filepath.Join(fakeBin, "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "exec" ]; then
  printf '%s\n' "$*" > ` + shellQuote(argsPath) + `
  printf '%s\n' "$PWD" > ` + shellQuote(cwdPath) + `
  count=0
  if [ -f ` + shellQuote(attemptsPath) + ` ]; then
    count="$(cat ` + shellQuote(attemptsPath) + `)"
  fi
  count=$((count + 1))
  printf '%s\n' "$count" > ` + shellQuote(attemptsPath) + `
  if [ "$count" -eq 1 ]; then
    exit 1
  fi
  echo ok
  exit 0
fi
if [ "${1:-}" = "--version" ]; then
  echo "codex-cli fake"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	profileHome := filepath.Join(root, "profile")
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}

	detail, err := runCodexHeartbeatWithRetries(profileHome, heartbeatSettings{
		Prompt:   heartbeatPrompt,
		Timeout:  codexHeartbeatTimeout,
		Retries:  1,
		Backoff:  0,
		LockPath: filepath.Join(root, "heartbeat.lock"),
	})
	if err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if !strings.Contains(detail, "after 2 attempts") {
		t.Fatalf("expected retry detail, got %q", detail)
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	argsText := string(argsBytes)
	if !strings.Contains(argsText, "--sandbox read-only") {
		t.Fatalf("expected read-only sandbox args, got %q", argsText)
	}
	if !strings.Contains(argsText, "--color never") {
		t.Fatalf("expected color suppression args, got %q", argsText)
	}
	if !strings.Contains(argsText, "exec --skip-git-repo-check --sandbox read-only --color never hello") {
		t.Fatalf("expected heartbeat prompt args, got %q", argsText)
	}

	cwdBytes, err := os.ReadFile(cwdPath)
	if err != nil {
		t.Fatalf("read cwd: %v", err)
	}
	gotCWD := strings.TrimSpace(string(cwdBytes))
	wantCanonical, err := filepath.EvalSymlinks(profileHome)
	if err != nil {
		t.Fatalf("eval want cwd: %v", err)
	}
	gotCanonical, err := filepath.EvalSymlinks(gotCWD)
	if err != nil {
		t.Fatalf("eval got cwd: %v", err)
	}
	if gotCanonical != wantCanonical {
		t.Fatalf("expected heartbeat cwd %q, got %q", wantCanonical, gotCanonical)
	}
}

func TestHeartbeatSkipDetailCodexMissing(t *testing.T) {
	t.Parallel()
	got := heartbeatSkipDetail("error", `exec: "codex": executable file not found in $PATH`)
	if got != "codex binary not found in PATH" {
		t.Fatalf("unexpected message: %q", got)
	}
}

type fakeStatus struct {
	exitCode int
	output   string
}

type fakeCodexScript struct {
	loginStatusByProfile map[string]fakeStatus
	execByProfile        map[string]fakeStatus
}

func newHeartbeatTestApp(t *testing.T, cfg fakeCodexScript) *App {
	t.Helper()

	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multi"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))
	t.Setenv("MULTICODEX_HEARTBEAT_BACKOFF_SECONDS", "0")

	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	loginDefault := cfg.loginStatusByProfile["default"]
	if loginDefault.output == "" {
		loginDefault = fakeStatus{exitCode: 1, output: "Not logged in"}
	}
	execDefault := cfg.execByProfile["default"]
	if execDefault.output == "" {
		execDefault = fakeStatus{exitCode: 0, output: "ok"}
	}

	var script strings.Builder
	script.WriteString("#!/usr/bin/env bash\nset -euo pipefail\n")
	script.WriteString("profile=\"$(basename \"$(dirname \"$CODEX_HOME\")\")\"\n")
	script.WriteString("if [ \"${1:-}\" = \"login\" ] && [ \"${2:-}\" = \"status\" ]; then\n")
	script.WriteString("  case \"$profile\" in\n")
	for name, st := range cfg.loginStatusByProfile {
		script.WriteString("    ")
		script.WriteString(name)
		script.WriteString(")\n")
		script.WriteString("      echo ")
		script.WriteString(shellQuote(st.output))
		script.WriteString("\n")
		script.WriteString("      exit ")
		script.WriteString(intToString(st.exitCode))
		script.WriteString("\n")
		script.WriteString("      ;;\n")
	}
	script.WriteString("    *)\n")
	script.WriteString("      echo ")
	script.WriteString(shellQuote(loginDefault.output))
	script.WriteString("\n")
	script.WriteString("      exit ")
	script.WriteString(intToString(loginDefault.exitCode))
	script.WriteString("\n")
	script.WriteString("      ;;\n")
	script.WriteString("  esac\n")
	script.WriteString("fi\n")
	script.WriteString("if [ \"${1:-}\" = \"exec\" ]; then\n")
	script.WriteString("  case \"$profile\" in\n")
	for name, st := range cfg.execByProfile {
		script.WriteString("    ")
		script.WriteString(name)
		script.WriteString(")\n")
		script.WriteString("      echo ")
		script.WriteString(shellQuote(st.output))
		script.WriteString("\n")
		script.WriteString("      exit ")
		script.WriteString(intToString(st.exitCode))
		script.WriteString("\n")
		script.WriteString("      ;;\n")
	}
	script.WriteString("    *)\n")
	script.WriteString("      echo ")
	script.WriteString(shellQuote(execDefault.output))
	script.WriteString("\n")
	script.WriteString("      exit ")
	script.WriteString(intToString(execDefault.exitCode))
	script.WriteString("\n")
	script.WriteString("      ;;\n")
	script.WriteString("  esac\n")
	script.WriteString("fi\n")
	script.WriteString("if [ \"${1:-}\" = \"--version\" ]; then echo 'codex-cli fake'; exit 0; fi\n")
	script.WriteString("echo 'unexpected invocation' >&2\nexit 1\n")

	codexPath := filepath.Join(fakeBin, "codex")
	if err := os.WriteFile(codexPath, []byte(script.String()), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	app, err := NewApp()
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

func createHeartbeatProfiles(t *testing.T, app *App, names ...string) {
	t.Helper()

	if err := app.store.EnsureBaseDirs(); err != nil {
		t.Fatalf("ensure base dirs: %v", err)
	}
	cfg := DefaultConfig()
	for _, name := range names {
		profileHome := filepath.Join(app.store.paths.ProfilesDir, name, "codex-home")
		if err := os.MkdirAll(profileHome, 0o700); err != nil {
			t.Fatalf("create profile home: %v", err)
		}
		cfg.Profiles[name] = Profile{Name: name, CodexHome: profileHome}
	}
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func shellQuote(s string) string {
	s = strings.ReplaceAll(s, `'`, `'\''`)
	return "'" + s + "'"
}

func intToString(v int) string {
	switch v {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	default:
		return "1"
	}
}
