package multicodex

import (
	"fmt"
	"sort"
	"strings"
)

type commandHelp struct {
	Usage       string
	Description string
	Examples    []string
}

var commandSummaries = []struct {
	Name    string
	Summary string
}{
	{Name: "init", Summary: "initialize multicodex local state"},
	{Name: "add <name>", Summary: "add a named account profile"},
	{Name: "login <name> [codex login args]", Summary: "login profile using official codex flow"},
	{Name: "login-all", Summary: "run login for every known profile"},
	{Name: "use <name> [--shell]", Summary: "switch profile in current terminal context"},
	{Name: "run <name> -- <command...>", Summary: "run one command in profile context"},
	{Name: "switch-global <name>", Summary: "switch default global codex auth to profile"},
	{Name: "switch-global --restore-default", Summary: "restore original global default auth"},
	{Name: "status", Summary: "show all profile auth states"},
	{Name: "heartbeat", Summary: "send a minimal keepalive hello for logged-in profiles"},
	{Name: "doctor [--json] [--timeout 8s]", Summary: "run non-mutating setup and auth checks"},
	{Name: "dry-run [operation]", Summary: "print planned operations without mutating state"},
	{Name: "completion <shell>", Summary: "print shell completion script for bash, zsh, or fish"},
	{Name: "version", Summary: "print multicodex version"},
	{Name: "help [command]", Summary: "show global or command-specific help"},
}

var commandHelpByName = map[string]commandHelp{
	"init": {
		Usage:       "multicodex init",
		Description: "Create local multicodex metadata directories and config. This does not change your default Codex session.",
		Examples: []string{
			"multicodex init",
		},
	},
	"add": {
		Usage:       "multicodex add <name>",
		Description: "Create a named profile with an isolated profile CODEX_HOME.",
		Examples: []string{
			"multicodex add personal",
			"multicodex add me@example.com",
		},
	},
	"login": {
		Usage:       "multicodex login <name> [codex login args]",
		Description: "Run official codex login inside the selected profile context.",
		Examples: []string{
			"multicodex login personal",
			"multicodex login personal --device-auth",
		},
	},
	"login-all": {
		Usage:       "multicodex login-all",
		Description: "Run login for all configured profiles in sorted order and show per-profile outcomes.",
		Examples: []string{
			"multicodex login-all",
		},
	},
	"use": {
		Usage:       "multicodex use <name> [--shell]",
		Description: "Set profile context in your current terminal with shell exports, or open a profile-bound subshell.",
		Examples: []string{
			`eval "$(multicodex use personal)"`,
			"multicodex use personal --shell",
		},
	},
	"run": {
		Usage:       "multicodex run <name> -- <command...>",
		Description: "Run one command in a selected profile context without changing your current shell.",
		Examples: []string{
			"multicodex run personal -- codex login status",
		},
	},
	"switch-global": {
		Usage:       "multicodex switch-global <name> | --restore-default",
		Description: "Explicitly switch global default auth pointer to a profile, or restore pre-multicodex default state.",
		Examples: []string{
			"multicodex switch-global personal",
			"multicodex switch-global --restore-default",
		},
	},
	"status": {
		Usage:       "multicodex status",
		Description: "Show profile login status, account hints, and which profile is the global default when known.",
		Examples: []string{
			"multicodex status",
		},
	},
	"heartbeat": {
		Usage:       "multicodex heartbeat",
		Description: "Fire-and-forget keepalive across logged-in profiles with per-profile summary output.",
		Examples: []string{
			"multicodex heartbeat",
		},
	},
	"doctor": {
		Usage:       "multicodex doctor [--json] [--timeout 8s]",
		Description: "Run non-mutating setup, auth, and leak-guard checks.",
		Examples: []string{
			"multicodex doctor",
			"multicodex doctor --json",
			"multicodex doctor --timeout 12s",
		},
	},
	"dry-run": {
		Usage:       "multicodex dry-run [operation]",
		Description: "Preview commands and filesystem operations without making changes.",
		Examples: []string{
			"multicodex dry-run",
			"multicodex dry-run switch-global personal",
			"multicodex dry-run run personal -- codex login status",
		},
	},
	"completion": {
		Usage:       "multicodex completion <bash|zsh|fish>",
		Description: "Print completion script for your shell. This supports command names and profile name completion.",
		Examples: []string{
			`eval "$(multicodex completion zsh)"`,
			`eval "$(multicodex completion bash)"`,
			"multicodex completion fish > ~/.config/fish/completions/multicodex.fish",
		},
	},
	"version": {
		Usage:       "multicodex version",
		Description: "Print multicodex version.",
		Examples: []string{
			"multicodex version",
		},
	},
	"help": {
		Usage:       "multicodex help [command]",
		Description: "Show global help or detailed help for one command.",
		Examples: []string{
			"multicodex help",
			"multicodex help heartbeat",
		},
	},
}

func printHelp() {
	fmt.Println("multicodex")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  multicodex <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	for _, c := range commandSummaries {
		fmt.Printf("  %-36s %s\n", c.Name, c.Summary)
	}
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  multicodex init")
	fmt.Println("  multicodex add personal")
	fmt.Println(`  eval "$(multicodex use personal)"`)
	fmt.Println("  multicodex heartbeat")
	fmt.Println(`  eval "$(multicodex completion zsh)"`)
	fmt.Println()
	fmt.Println("Help:")
	fmt.Println("  multicodex help <command>")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  - default behavior is local-first and does not change your system default session")
	fmt.Println("  - global switching only happens with explicit switch-global commands")
}

func (a *App) cmdHelp(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}
	if len(args) != 1 {
		return &ExitError{Code: 2, Message: "usage: multicodex help [command]"}
	}

	name := normalizeHelpTopic(args[0])
	topic, ok := commandHelpByName[name]
	if !ok {
		known := make([]string, 0, len(commandHelpByName))
		for k := range commandHelpByName {
			known = append(known, k)
		}
		sort.Strings(known)
		return &ExitError{
			Code:    2,
			Message: fmt.Sprintf("unknown help topic: %s\nknown topics: %s", args[0], strings.Join(known, ", ")),
		}
	}

	fmt.Println("multicodex", name)
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Printf("  %s\n", topic.Usage)
	fmt.Println()
	fmt.Println("Description:")
	fmt.Printf("  %s\n", topic.Description)
	if len(topic.Examples) > 0 {
		fmt.Println()
		fmt.Println("Examples:")
		for _, ex := range topic.Examples {
			fmt.Printf("  %s\n", ex)
		}
	}
	return nil
}

func normalizeHelpTopic(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "--help", "-h":
		return "help"
	case "--version", "-v":
		return "version"
	default:
		return s
	}
}
