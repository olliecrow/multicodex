package multicodex

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"multicodex/internal/monitor/usage"
)

const (
	execSelectionPrimaryUsageLimit = 60
	execSelectionTimeout           = 10 * time.Second
)

type execAccountSelector func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error)

var defaultExecAccountSelector execAccountSelector = func(ctx context.Context, accounts []usage.MonitorAccount, maxPrimaryUsedPercent int) (usage.SelectedAccount, error) {
	return usage.SelectBestAccount(ctx, accounts, maxPrimaryUsedPercent)
}

func (a *App) cmdExec(args []string) error {
	if execArgsAreHelpRequest(args) {
		return RunCommand("codex", append([]string{"exec"}, args...))
	}

	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}

	name, profile, err := a.selectExecProfile(cfg, defaultExecAccountSelector)
	if err != nil {
		return err
	}
	if err := a.store.EnsureProfileDir(profile); err != nil {
		return err
	}

	return RunWithProfile(profile.CodexHome, name, "codex", append([]string{"exec"}, args...))
}

func execArgsAreHelpRequest(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			return true
		}
	}
	return args[0] == "help"
}

func (a *App) selectExecProfile(cfg *Config, selector execAccountSelector) (string, Profile, error) {
	if len(cfg.Profiles) == 0 {
		return "", Profile{}, &ExitError{Code: 2, Message: "no profiles configured. add one with: multicodex add <name>"}
	}

	names := sortedProfileNames(cfg)
	accounts := make([]usage.MonitorAccount, 0, len(names))
	firstWithAuth := ""
	for _, name := range names {
		profile := cfg.Profiles[name]
		accounts = append(accounts, usage.MonitorAccount{
			Label:     name,
			CodexHome: profile.CodexHome,
		})
		if firstWithAuth == "" {
			hasAuth, err := HasAuthFile(profile.CodexHome)
			if err == nil && hasAuth {
				firstWithAuth = name
			}
		}
	}

	if selector != nil {
		ctx, cancel := context.WithTimeout(context.Background(), execSelectionTimeout)
		defer cancel()

		selected, err := selector(ctx, accounts, execSelectionPrimaryUsageLimit)
		if err == nil {
			if name, profile, ok := lookupSelectedExecProfile(cfg, selected); ok {
				return name, profile, nil
			}
		}
	}

	if firstWithAuth != "" {
		return firstWithAuth, cfg.Profiles[firstWithAuth], nil
	}

	first := names[0]
	return first, cfg.Profiles[first], nil
}

func lookupSelectedExecProfile(cfg *Config, selected usage.SelectedAccount) (string, Profile, bool) {
	if name := strings.TrimSpace(selected.Account.Label); name != "" {
		if profile, ok := cfg.Profiles[name]; ok {
			return name, profile, true
		}
	}

	selectedHome := normalizeExecCodexHome(selected.Account.CodexHome)
	if selectedHome == "" {
		return "", Profile{}, false
	}

	for _, name := range sortedProfileNames(cfg) {
		profile := cfg.Profiles[name]
		if normalizeExecCodexHome(profile.CodexHome) == selectedHome {
			return name, profile, true
		}
	}
	return "", Profile{}, false
}

func normalizeExecCodexHome(home string) string {
	trimmed := strings.TrimSpace(home)
	if trimmed == "" {
		return ""
	}
	normalized := filepath.Clean(trimmed)
	if abs, err := filepath.Abs(normalized); err == nil {
		normalized = abs
	}
	if resolved, err := filepath.EvalSymlinks(normalized); err == nil && strings.TrimSpace(resolved) != "" {
		normalized = resolved
	}
	return filepath.Clean(normalized)
}
