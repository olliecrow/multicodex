package multicodex

import (
	"fmt"
)

func defaultInteractiveCodexArgs() []string {
	return []string{
		"--search",
		"--dangerously-bypass-approvals-and-sandbox",
		"-m",
		"gpt-5.5",
		"-c",
		"model_reasoning_effort=medium",
	}
}

func (a *App) cmdCLI(args []string) error {
	if len(args) < 1 {
		return &ExitError{Code: 2, Message: "usage: multicodex cli <name> [codex args...]"}
	}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		return a.cmdHelp([]string{"cli"})
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
	if err := a.store.EnsureProfileDir(profile); err != nil {
		return err
	}
	if err := ensureProfileCodexExecutionReady(a.store.paths, profile); err != nil {
		return err
	}

	cmdArgs := defaultInteractiveCodexArgs()
	cmdArgs = append(cmdArgs, args[1:]...)
	return RunInteractiveWithProfile(profile.CodexHome, name, "codex", cmdArgs)
}
