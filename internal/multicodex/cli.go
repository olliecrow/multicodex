package multicodex

import (
	"context"
	"fmt"
)

func (a *App) cmdCLI(args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		return a.cmdHelp([]string{"cli"})
	}
	if len(args) > 0 && args[0] == "--account" {
		return a.cmdCLIWithAccount(args[1:])
	}

	selected, err := a.selectAccountForCodexArgs(context.Background(), args)
	if err != nil {
		return err
	}
	if selected.IsProfile {
		if err := ensureProfileCodexExecutionReady(a.store.paths, selected.Profile); err != nil {
			return err
		}
	}

	activeProfile := selected.Name
	if !selected.IsProfile {
		activeProfile = ""
	}
	return RunInteractiveCodexWithProfile(selected.CodexHome, activeProfile, args)
}

func (a *App) cmdCLIWithAccount(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return &ExitError{Code: 2, Message: "usage: multicodex cli --account <name> [--] [codex args...]"}
	}

	name := args[0]
	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}
	profile, ok := cfg.Profiles[name]
	if !ok {
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	changes, err := a.store.EnsureProfileDir(profile, cfg.ProfileResources)
	if err != nil {
		return err
	}
	printResourceChanges(changes)
	if err := ensureProfileCodexExecutionReady(a.store.paths, profile); err != nil {
		return err
	}

	codexArgs := args[1:]
	if len(codexArgs) > 0 && codexArgs[0] == "--" {
		codexArgs = codexArgs[1:]
	}
	return RunInteractiveCodexWithProfile(profile.CodexHome, name, codexArgs)
}
