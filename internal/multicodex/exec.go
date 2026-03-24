package multicodex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"multicodex/internal/monitor/usage"
)

const (
	execSelectionPrimaryUsageLimit = 60
	execSelectionTimeout           = 10 * time.Second
	envSelectedProfilePath         = "MULTICODEX_SELECTED_PROFILE_PATH"
)

type execAccountSelector func(context.Context, []usage.MonitorAccount, int) (usage.SelectedAccount, error)

type execSelectionMetadata struct {
	Profile                      string `json:"profile"`
	SelectionSource              string `json:"selection_source,omitempty"`
	PrimaryUsedPercent           *int   `json:"primary_used_percent,omitempty"`
	SecondaryUsedPercent         *int   `json:"secondary_used_percent,omitempty"`
	UsedPrimaryThresholdFallback bool   `json:"used_primary_threshold_fallback,omitempty"`
}

type execSelection struct {
	Name     string
	Profile  Profile
	Metadata execSelectionMetadata
}

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

	selected, err := a.selectExecProfile(cfg, defaultExecAccountSelector)
	if err != nil {
		return err
	}
	if err := a.store.EnsureProfileDir(selected.Profile); err != nil {
		return err
	}
	if err := ensureProfileCodexExecutionReady(a.store.paths, selected.Profile); err != nil {
		return err
	}
	if err := writeSelectedProfileMetadata(os.Getenv(envSelectedProfilePath), selected.Metadata); err != nil {
		return err
	}

	return RunWithProfile(selected.Profile.CodexHome, selected.Name, "codex", append([]string{"exec"}, args...))
}

func writeSelectedProfileMetadata(path string, metadata execSelectionMetadata) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	metadata.Profile = strings.TrimSpace(metadata.Profile)
	metadata.SelectionSource = strings.TrimSpace(metadata.SelectionSource)
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal selected profile metadata: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write selected profile metadata: %w", err)
	}
	return nil
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

func (a *App) selectExecProfile(cfg *Config, selector execAccountSelector) (execSelection, error) {
	if len(cfg.Profiles) == 0 {
		return execSelection{}, &ExitError{Code: 2, Message: "no profiles configured. add one with: multicodex add <name>"}
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
				metadata := execSelectionMetadata{
					Profile:                      name,
					SelectionSource:              "usage_selector",
					PrimaryUsedPercent:           intPtr(selected.PrimaryUsedPercent),
					SecondaryUsedPercent:         intPtr(selected.SecondaryUsedPercent),
					UsedPrimaryThresholdFallback: selected.UsedPrimaryThresholdFallback,
				}
				return execSelection{Name: name, Profile: profile, Metadata: metadata}, nil
			}
		}
	}

	if firstWithAuth != "" {
		return execSelection{
			Name:    firstWithAuth,
			Profile: cfg.Profiles[firstWithAuth],
			Metadata: execSelectionMetadata{
				Profile:         firstWithAuth,
				SelectionSource: "first_with_auth_fallback",
			},
		}, nil
	}

	first := names[0]
	return execSelection{
		Name:    first,
		Profile: cfg.Profiles[first],
		Metadata: execSelectionMetadata{
			Profile:         first,
			SelectionSource: "first_sorted_fallback",
		},
	}, nil
}

func intPtr(v int) *int {
	return &v
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
