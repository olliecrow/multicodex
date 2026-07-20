package multicodex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olliecrow/multicodex/internal/monitor/usage"
)

func TestCmdExecRunsCodexExecWithSelectedProfile(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha", "beta")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{
			Account:           usage.MonitorAccount{Label: "beta"},
			WeeklyUsedPercent: 10,
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

func TestCmdExecRunsCodexExecWithDefaultReserveAccount(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(_ context.Context, accounts []usage.MonitorAccount, _ string) (usage.SelectedAccount, error) {
		var defaultAccount usage.MonitorAccount
		var profileAccount usage.MonitorAccount
		for _, account := range accounts {
			if account.Label == defaultExecAccountLabel {
				defaultAccount = account
			}
			if account.Label == "alpha" {
				profileAccount = account
			}
		}
		if profileAccount.CodexHome == "" {
			t.Fatalf("expected configured profile account in selector candidates, got %#v", accounts)
		}
		if !profileAccount.UseAppServer {
			t.Fatalf("expected validated exec profile to use app-server, got %#v", profileAccount)
		}
		if defaultAccount.CodexHome == "" {
			t.Fatalf("expected default reserve account in selector candidates, got %#v", accounts)
		}
		if defaultAccount.SelectionPriority <= 0 {
			t.Fatalf("expected default reserve account to have lower selection priority, got %#v", defaultAccount)
		}
		if defaultAccount.UseAppServer {
			t.Fatalf("expected default reserve account not to use app-server without profile validation, got %#v", defaultAccount)
		}
		return usage.SelectedAccount{
			Account:           defaultAccount,
			WeeklyUsedPercent: 5,
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
	if !strings.Contains(log, "profile=\n") {
		t.Fatalf("expected default reserve exec not to set active profile, got %q", log)
	}
	if !strings.Contains(log, "codex_home="+normalizeExecCodexHome(app.store.paths.DefaultCodexHome)) {
		t.Fatalf("expected default reserve exec to use default Codex home, got %q", log)
	}
}

func TestCmdExecWritesSelectedProfileMetadata(t *testing.T) {
	app, _ := newExecTestApp(t)
	createExecProfiles(t, app, "alpha", "beta")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{
			Account:           usage.MonitorAccount{Label: "beta"},
			WeeklyUsedPercent: 10,
		}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	metadataPath := filepath.Join(app.store.paths.MulticodexHome, "run", "selected-profile.json")
	t.Setenv(envSelectedProfilePath, metadataPath)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var payload struct {
		Profile           string `json:"profile"`
		SelectionSource   string `json:"selection_source"`
		WeeklyUsedPercent *int   `json:"weekly_used_percent"`
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
	if payload.WeeklyUsedPercent == nil || *payload.WeeklyUsedPercent != 10 {
		t.Fatalf("expected weekly_used_percent 10, got %v", payload.WeeklyUsedPercent)
	}
	if strings.Contains(string(data), "primary_used_percent") || strings.Contains(string(data), "secondary_used_percent") {
		t.Fatalf("did not expect old percent fields in metadata: %s", data)
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

	selectorCalled := false
	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		selectorCalled = true
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
	if selectorCalled {
		t.Fatal("expected selector not to run before file-store safety check")
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected codex to not be invoked, stat err=%v", err)
	}
}

func TestCmdExecRejectsUnsafeAuthBeforeSelection(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha")
	home := filepath.Join(app.store.paths.ProfilesDir, "alpha", "codex-home")
	target := filepath.Join(t.TempDir(), "shared-auth.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"access_token":"a"}}`), 0o600); err != nil {
		t.Fatalf("write target auth: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(home, "auth.json")); err != nil {
		t.Fatalf("symlink auth: %v", err)
	}

	selectorCalled := false
	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		selectorCalled = true
		return usage.SelectedAccount{Account: usage.MonitorAccount{Label: "alpha"}}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"})
	if err == nil {
		t.Fatal("expected auth symlink exec to fail")
	}
	if !strings.Contains(err.Error(), "auth path is a symlink") {
		t.Fatalf("expected auth symlink error, got %v", err)
	}
	if selectorCalled {
		t.Fatal("expected selector not to run before auth path safety check")
	}
	if _, statErr := os.Stat(logPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected codex not to be invoked, stat err=%v", statErr)
	}
}

func TestCmdExecPreparesMissingProfileBeforeSelection(t *testing.T) {
	app, logPath := newExecTestApp(t)
	profile := Profile{Name: "alpha", CodexHome: filepath.Join(app.store.paths.ProfilesDir, "alpha", "codex-home")}
	cfg := DefaultConfig()
	cfg.Profiles[profile.Name] = profile
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	selectorCalled := false
	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		selectorCalled = true
		return usage.SelectedAccount{Account: usage.MonitorAccount{Label: "alpha"}}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if !selectorCalled {
		t.Fatal("expected selector to run after profile preparation")
	}
	if _, err := os.Stat(filepath.Join(profile.CodexHome, "config.toml")); err != nil {
		t.Fatalf("expected profile config to be prepared, stat err=%v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected codex to be invoked after safe preparation, stat err=%v", err)
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
		{name: "help prompt text", args: []string{"help", "me", "debug"}, want: false},
		{name: "help after other args", args: []string{"--model", "gpt-5", "--help"}, want: true},
		{name: "help after terminator is prompt text", args: []string{"--", "--help"}, want: false},
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

func TestParseModelFromExecArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "long-flag separate", args: []string{"--model", "gpt-5-codex-spark"}, want: "gpt-5-codex-spark"},
		{name: "long-flag equals", args: []string{"--model=gpt-5-codex-spark"}, want: "gpt-5-codex-spark"},
		{name: "short-flag equals", args: []string{"-m=gpt-5-codex-spark"}, want: "gpt-5-codex-spark"},
		{name: "short flag", args: []string{"-m", "gpt-5-codex-spark"}, want: "gpt-5-codex-spark"},
		{name: "flag after terminator is prompt text", args: []string{"--", "-m", "gpt-5-codex-spark"}, want: ""},
		{name: "missing", args: []string{"hello", "world"}, want: ""},
		{name: "short not model", args: []string{"-m"}, want: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := parseModelFromExecArgs(tc.args)
			if got != tc.want {
				t.Fatalf("parseModelFromExecArgs(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestSelectExecProfilePassesModelToSelector(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	model := "gpt-5-codex-spark"
	calledWith := ""
	selected, err := app.selectExecProfile(cfg, func(_ context.Context, _ []usage.MonitorAccount, selectedModel string) (usage.SelectedAccount, error) {
		calledWith = selectedModel
		return usage.SelectedAccount{
			Account:           usage.MonitorAccount{Label: "alpha"},
			WeeklyUsedPercent: 20,
		}, nil
	}, model)
	if err != nil {
		t.Fatalf("selectExecProfile: %v", err)
	}
	if selected.Name != "alpha" {
		t.Fatalf("expected selected profile alpha, got %q", selected.Name)
	}
	if calledWith != model {
		t.Fatalf("expected selector called with %q model, got %q", model, calledWith)
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
	if !strings.Contains(log, "profile=gamma") {
		t.Fatalf("expected gamma with the soonest weekly reset, got %q", log)
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

	metadataPath := filepath.Join(app.store.paths.MulticodexHome, "run", "selected-profile.json")
	t.Setenv(envSelectedProfilePath, metadataPath)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=gamma") {
		t.Fatalf("expected usable gamma with the soonest weekly reset, got %q", log)
	}

	metadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var payload struct {
		Profile           string `json:"profile"`
		SelectionSource   string `json:"selection_source"`
		WeeklyUsedPercent *int   `json:"weekly_used_percent"`
	}
	if err := json.Unmarshal(metadata, &payload); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if payload.Profile != "gamma" {
		t.Fatalf("expected selected profile gamma, got %q", payload.Profile)
	}
	if payload.SelectionSource != "usage_selector" {
		t.Fatalf("expected selection source usage_selector, got %q", payload.SelectionSource)
	}
	if payload.WeeklyUsedPercent == nil || *payload.WeeklyUsedPercent != 10 {
		t.Fatalf("expected weekly_used_percent 10, got %v", payload.WeeklyUsedPercent)
	}
}

func TestCmdExecUsesConfiguredProfilesBeforeDefaultReserveAccount(t *testing.T) {
	app, logPath, root := newExecSelectionTestApp(t)
	createExecProfiles(t, app, "alpha")
	writeExecSelectionProfileData(t, root, "alpha", 10, 30, 0)
	writeExecSelectionDefaultData(t, app, 1, 1, 30*time.Minute)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=alpha") {
		t.Fatalf("expected eligible profile before default reserve account, got %q", log)
	}
}

func TestCmdExecUsesDefaultReserveAccountWhenProfilesAreWeeklyExhausted(t *testing.T) {
	app, logPath, root := newExecSelectionTestApp(t)
	createExecProfiles(t, app, "alpha")
	writeExecSelectionProfileData(t, root, "alpha", 80, 100, 10*time.Minute)
	writeExecSelectionDefaultData(t, app, 1, 1, 30*time.Minute)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=\n") {
		t.Fatalf("expected reserve default account not to set active profile, got %q", log)
	}
	if !strings.Contains(log, "codex_home="+normalizeExecCodexHome(app.store.paths.DefaultCodexHome)) {
		t.Fatalf("expected reserve default account Codex home, got %q", log)
	}
}

func TestCmdExecUsesDefaultReserveAccountWithoutConfiguredProfiles(t *testing.T) {
	app, logPath, _ := newExecSelectionTestApp(t)
	writeExecSelectionDefaultData(t, app, 100, 100, 30*time.Minute)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=\n") {
		t.Fatalf("expected default reserve account without configured profiles, got %q", log)
	}
	if !strings.Contains(log, "codex_home="+normalizeExecCodexHome(app.store.paths.DefaultCodexHome)) {
		t.Fatalf("expected default reserve account Codex home, got %q", log)
	}
}

func TestCmdExecUsesDefaultReserveAccountWhenConfiguredProfileUsageUnavailable(t *testing.T) {
	app, logPath, _ := newExecSelectionTestApp(t)
	createExecProfiles(t, app, "alpha")
	writeExecSelectionDefaultData(t, app, 20, 20, 30*time.Minute)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=\n") {
		t.Fatalf("expected default reserve account when configured profile usage is unavailable, got %q", log)
	}
	if !strings.Contains(log, "codex_home="+normalizeExecCodexHome(app.store.paths.DefaultCodexHome)) {
		t.Fatalf("expected default reserve account Codex home, got %q", log)
	}
}

func TestCmdExecUsesRedProfileForCurrentUsageShape(t *testing.T) {
	app, logPath, root := newExecSelectionTestApp(t)
	createExecProfiles(t, app, "apple", "oc")
	writeExecSelectionProfileData(t, root, "apple", 8, 100, 1*time.Hour)
	writeExecSelectionProfileData(t, root, "oc", 66, 67, 48*time.Hour)
	writeExecSelectionDefaultData(t, app, 52, 78, 30*time.Minute)

	if err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=oc") {
		t.Fatalf("expected oc profile from current red-but-usable shape, got %q", log)
	}
}

func TestCmdExecSparkModelUsesDefaultReserveWhenSparkWindowMissing(t *testing.T) {
	app, logPath, root := newExecSelectionTestApp(t)
	createExecProfiles(t, app, "alpha")
	writeExecSelectionProfileData(t, root, "alpha", 10, 20, 1*time.Hour)

	if err := app.Run([]string{"exec", "-m=gpt-5-codex-spark", "--skip-git-repo-check", "hello"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=\n") {
		t.Fatalf("expected default reserve account when configured Spark window is missing, got %q", log)
	}
}

func TestCmdExecTreatsFlagsAfterTerminatorAsPromptText(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(_ context.Context, _ []usage.MonitorAccount, model string) (usage.SelectedAccount, error) {
		if model != "" {
			t.Fatalf("expected args after -- not to be parsed as model, got %q", model)
		}
		return usage.SelectedAccount{
			Account:           usage.MonitorAccount{Label: "alpha"},
			WeeklyUsedPercent: 10,
		}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	if err := app.Run([]string{"exec", "--", "-m=gpt-5-codex-spark", "--help"}); err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "profile=alpha") {
		t.Fatalf("expected alpha profile in log, got %q", log)
	}
	if !strings.Contains(log, "args=exec -- -m=gpt-5-codex-spark --help") {
		t.Fatalf("expected args after -- to pass through unchanged, got %q", log)
	}
}

func TestSelectExecProfileReturnsErrorWhenSelectionFails(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha", "beta")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	selected, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("boom")
	}, "")
	if err == nil {
		t.Fatalf("expected selection error, got profile %q", selected.Name)
	}
}

func TestSelectExecProfileReturnsErrorForOnlyProfileWhenSelectionFails(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	selected, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("boom")
	}, "")
	if err == nil {
		t.Fatalf("expected selection error, got profile %q", selected.Name)
	}
}

func TestSelectExecProfileReturnsErrorForSparkModelWhenNoModelWindowAvailable(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	selected, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("usage selection failed")
	}, "gpt-5-codex-spark")
	if err == nil {
		t.Fatalf("expected error for spark model, got profile %q", selected.Name)
	}
}

func TestCmdExecReturnsErrorWhenUsageSelectionFails(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha", "beta")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{}, errors.New("boom")
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	err := app.Run([]string{"exec", "--skip-git-repo-check", "hello"})
	if err == nil {
		t.Fatal("expected exec to fail when usage selection fails")
	}
	if _, statErr := os.Stat(logPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected codex not to be invoked, stat err=%v", statErr)
	}
}

func TestCmdExecRejectsAnyInvalidConfiguredProfileBeforeSelection(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha", "beta")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}
	cfg.Profiles["alpha"] = Profile{Name: "alpha", CodexHome: filepath.Join(t.TempDir(), "outside-codex-home")}
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	selectorCalled := false
	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		selectorCalled = true
		return usage.SelectedAccount{}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	err = app.Run([]string{"exec", "--skip-git-repo-check", "hello"})
	if err == nil {
		t.Fatal("expected exec to fail when any configured profile is invalid")
	}
	if selectorCalled {
		t.Fatal("expected selector not to run when any configured profile is invalid")
	}
	if !strings.Contains(err.Error(), "profile-local path") {
		t.Fatalf("expected profile-local path error, got %v", err)
	}
	if _, statErr := os.Stat(logPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected codex not to be invoked, stat err=%v", statErr)
	}
}

func TestCmdExecFailsBeforeSelectionWhenNoProfilesAreReady(t *testing.T) {
	app, logPath := newExecTestApp(t)
	createExecProfiles(t, app, "alpha")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}
	cfg.Profiles["alpha"] = Profile{Name: "alpha", CodexHome: filepath.Join(t.TempDir(), "outside-codex-home")}
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	selectorCalled := false
	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		selectorCalled = true
		return usage.SelectedAccount{}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	err = app.Run([]string{"exec", "--skip-git-repo-check", "hello"})
	if err == nil {
		t.Fatal("expected exec to fail when no profiles are ready")
	}
	if selectorCalled {
		t.Fatal("expected selector not to run when no profiles are ready")
	}
	if !strings.Contains(err.Error(), "profile-local path") {
		t.Fatalf("expected profile-local path error, got %v", err)
	}
	if _, statErr := os.Stat(logPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected codex not to be invoked, stat err=%v", statErr)
	}
}

func TestSelectExecProfilePersistsUsageSelectionMetadata(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha", "beta")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}

	selected, err := app.selectExecProfile(cfg, func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		return usage.SelectedAccount{
			Account:           usage.MonitorAccount{Label: "beta"},
			WeeklyUsedPercent: 7,
		}, nil
	}, "")
	if err != nil {
		t.Fatalf("selectExecProfile: %v", err)
	}
	if selected.Name != "beta" {
		t.Fatalf("expected beta selected, got %q", selected.Name)
	}
	if selected.Metadata.SelectionSource != "usage_selector" {
		t.Fatalf("expected usage_selector selection source, got %q", selected.Metadata.SelectionSource)
	}
	if selected.Metadata.WeeklyUsedPercent == nil || *selected.Metadata.WeeklyUsedPercent != 7 {
		t.Fatalf("expected weekly_used_percent 7, got %v", selected.Metadata.WeeklyUsedPercent)
	}
}

func TestSelectExecProfileOmitsUnavailableWeeklyUsageMetadataForReserve(t *testing.T) {
	app := newTestAppForCLI(t)
	createExecProfiles(t, app, "alpha")

	cfg, err := app.loadConfigIfExists()
	if err != nil {
		t.Fatalf("loadConfigIfExists: %v", err)
	}
	selected, err := app.selectExecProfile(cfg, func(_ context.Context, accounts []usage.MonitorAccount, _ string) (usage.SelectedAccount, error) {
		for _, account := range accounts {
			if account.Label == defaultExecAccountLabel {
				return usage.SelectedAccount{Account: account, WeeklyUsedPercent: -1}, nil
			}
		}
		return usage.SelectedAccount{}, errors.New("default reserve account missing")
	}, "")
	if err != nil {
		t.Fatalf("selectExecProfile: %v", err)
	}
	if selected.Metadata.SelectionSource != "usage_selector_default_reserve" {
		t.Fatalf("unexpected selection source %q", selected.Metadata.SelectionSource)
	}
	if selected.Metadata.WeeklyUsedPercent != nil {
		t.Fatalf("expected unavailable weekly usage to be omitted, got %d", *selected.Metadata.WeeklyUsedPercent)
	}
}

func TestWriteSelectedProfileMetadataNoPathIsNoOp(t *testing.T) {
	if err := writeSelectedProfileMetadata(Paths{MulticodexHome: t.TempDir()}, "", execSelectionMetadata{Profile: "alpha"}); err != nil {
		t.Fatalf("writeSelectedProfileMetadata without path failed: %v", err)
	}
}

func TestWriteSelectedProfileMetadataRejectsHardLinkedFile(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	metadataPath := filepath.Join(runDir, "selected-profile.json")
	linkedPath := filepath.Join(runDir, "selected-profile.link")
	if err := os.WriteFile(metadataPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write metadata file: %v", err)
	}
	if err := os.Link(metadataPath, linkedPath); err != nil {
		t.Fatalf("hard link metadata file: %v", err)
	}

	err := writeSelectedProfileMetadata(Paths{MulticodexHome: dir}, metadataPath, execSelectionMetadata{Profile: "alpha"})
	if err == nil {
		t.Fatal("expected hard-linked metadata file to fail")
	}
	if !strings.Contains(err.Error(), "multiple hard links") {
		t.Fatalf("expected multiple-hard-links error, got %v", err)
	}
	data, readErr := os.ReadFile(metadataPath)
	if readErr != nil {
		t.Fatalf("read metadata file: %v", readErr)
	}
	if string(data) != "{}\n" {
		t.Fatalf("expected hard-linked metadata not to be truncated, got %q", string(data))
	}
}

func TestWriteSelectedProfileMetadataRejectsPathOutsideRuntimeRoot(t *testing.T) {
	root := t.TempDir()
	metadataPath := filepath.Join(root, "selected-profile.json")

	err := writeSelectedProfileMetadata(Paths{MulticodexHome: root}, metadataPath, execSelectionMetadata{Profile: "alpha"})
	if err == nil {
		t.Fatal("expected metadata path outside runtime root to fail")
	}
	if !strings.Contains(err.Error(), "must stay under") {
		t.Fatalf("expected under-runtime-root error, got %v", err)
	}
	if _, statErr := os.Stat(metadataPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected root-level metadata file not to be created, stat err=%v", statErr)
	}
}

func TestWriteSelectedProfileMetadataRelativePathUsesRuntimeRoot(t *testing.T) {
	root := t.TempDir()
	if err := writeSelectedProfileMetadata(Paths{MulticodexHome: root}, "selected-profile.json", execSelectionMetadata{Profile: "alpha"}); err != nil {
		t.Fatalf("writeSelectedProfileMetadata: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "run", "selected-profile.json")); err != nil {
		t.Fatalf("expected metadata under runtime root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "selected-profile.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no root-level metadata file, stat err=%v", err)
	}
}

func TestWriteSelectedProfileMetadataSecuresRuntimePaths(t *testing.T) {
	root := t.TempDir()
	if err := writeSelectedProfileMetadata(Paths{MulticodexHome: root}, "nested/selected-profile.json", execSelectionMetadata{Profile: "alpha"}); err != nil {
		t.Fatalf("writeSelectedProfileMetadata: %v", err)
	}
	for _, path := range []string{filepath.Join(root, "run"), filepath.Join(root, "run", "nested")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat metadata dir %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("expected %s mode 0700, got %o", path, got)
		}
	}
	info, err := os.Stat(filepath.Join(root, "run", "nested", "selected-profile.json"))
	if err != nil {
		t.Fatalf("stat metadata file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected metadata file mode 0600, got %o", got)
	}
}

func TestWriteSelectedProfileMetadataRejectsSymlinkedRuntimeRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "run")); err != nil {
		t.Fatalf("symlink runtime root: %v", err)
	}

	err := writeSelectedProfileMetadata(Paths{MulticodexHome: root}, "selected-profile.json", execSelectionMetadata{Profile: "alpha"})
	if err == nil {
		t.Fatal("expected symlinked metadata root to fail")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "selected-profile.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected outside metadata file not to be created, stat err=%v", statErr)
	}
}

func TestWriteSelectedProfileMetadataRejectsSymlinkedRuntimeParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	runDir := filepath.Join(root, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(runDir, "linked")); err != nil {
		t.Fatalf("symlink runtime parent: %v", err)
	}

	err := writeSelectedProfileMetadata(Paths{MulticodexHome: root}, "linked/selected-profile.json", execSelectionMetadata{Profile: "alpha"})
	if err == nil {
		t.Fatal("expected symlinked metadata parent to fail")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "selected-profile.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected outside metadata file not to be created, stat err=%v", statErr)
	}
}

func TestWriteSelectedProfileMetadataRejectsPathOutsideMulticodexHome(t *testing.T) {
	root := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "selected-profile.json")

	err := writeSelectedProfileMetadata(Paths{MulticodexHome: root}, outsidePath, execSelectionMetadata{Profile: "alpha"})
	if err == nil {
		t.Fatal("expected outside metadata path to fail")
	}
	if !strings.Contains(err.Error(), "must stay under") {
		t.Fatalf("expected under-root error, got %v", err)
	}
	if _, statErr := os.Stat(outsidePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected outside metadata file not to be created, stat err=%v", statErr)
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

primary = 0
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
    printf 'codex_home=%s\n' "${CODEX_HOME:-}"
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
	originalTransport := http.DefaultTransport
	http.DefaultTransport = execSelectionOAuthTransport{root: root}
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	writeDefaultFileStoreConfig(t, app)
	return app, logPath, root
}

type execSelectionOAuthTransport struct {
	root string
}

func (t execSelectionOAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token := strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
	if !strings.HasPrefix(token, "token-") {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad token"}`)),
			Request:    req,
		}, nil
	}
	name := strings.TrimPrefix(token, "token-")
	usagePath := filepath.Join(t.root, "multi", "profiles", name, "codex-home", "usage.json")
	if name == defaultExecAccountLabel {
		usagePath = filepath.Join(t.root, "default-codex", "usage.json")
	}
	data, err := os.ReadFile(usagePath)
	if err != nil {
		return nil, err
	}
	var usage struct {
		WeeklyUsedPercent int    `json:"weekly_used_percent"`
		Email             string `json:"email"`
		PrimaryResetsAt   int64  `json:"primary_resets_at"`
		SecondaryResetsAt int64  `json:"secondary_resets_at"`
	}
	if err := json.Unmarshal(data, &usage); err != nil {
		return nil, err
	}
	body := fmt.Sprintf(`{
  "email": %q,
  "plan_type": "pro",
  "rate_limit": {
    "primary_window": {"used_percent": %d, "limit_window_seconds": 18000, "reset_at": %d},
    "secondary_window": {"used_percent": %d, "limit_window_seconds": 604800, "reset_at": %d}
  }
}`,
		usage.Email,
		0,
		usage.PrimaryResetsAt,
		usage.WeeklyUsedPercent,
		usage.SecondaryResetsAt,
	)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func createExecProfiles(t *testing.T, app *App, names ...string) {
	t.Helper()

	createTestProfiles(t, app, names...)
}

func createTestProfiles(t *testing.T, app *App, names ...string) {
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

func writeExecSelectionProfileData(t *testing.T, root, name string, _ int, weeklyUsed int, weeklyResetIn time.Duration) {
	t.Helper()

	home := filepath.Join(root, "multi", "profiles", name, "codex-home")
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(fmt.Sprintf(`{"tokens":{"access_token":"token-%s"}}`, name)), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	now := time.Now().UTC()
	usageJSON := fmt.Sprintf(
		`{"weekly_used_percent": %d, "email": "%s@example.com", "primary_resets_at": %d, "secondary_resets_at": %d}`,
		weeklyUsed,
		name,
		now.Add(5*time.Hour).Unix(),
		now.Add(weeklyResetIn).Unix(),
	)
	if err := os.WriteFile(filepath.Join(home, "usage.json"), []byte(usageJSON), 0o600); err != nil {
		t.Fatalf("write usage: %v", err)
	}
}

func writeExecSelectionDefaultData(t *testing.T, app *App, _ int, weeklyUsed int, weeklyResetIn time.Duration) {
	t.Helper()

	home := app.store.paths.DefaultCodexHome
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir default Codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"token-default"}}`), 0o600); err != nil {
		t.Fatalf("write default auth: %v", err)
	}
	now := time.Now().UTC()
	usageJSON := fmt.Sprintf(
		`{"weekly_used_percent": %d, "email": "default@example.com", "primary_resets_at": %d, "secondary_resets_at": %d}`,
		weeklyUsed,
		now.Add(5*time.Hour).Unix(),
		now.Add(weeklyResetIn).Unix(),
	)
	if err := os.WriteFile(filepath.Join(home, "usage.json"), []byte(usageJSON), 0o600); err != nil {
		t.Fatalf("write default usage: %v", err)
	}
}
