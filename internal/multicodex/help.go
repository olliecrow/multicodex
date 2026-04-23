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
	{Name: "app <name>", Summary: "launch a new Codex Mac app instance for one profile"},
	{Name: "cli <name> [codex args...]", Summary: "run the interactive Codex CLI with one profile"},
	{Name: "run <name> -- <command...>", Summary: "run one command in profile context"},
	{Name: "exec [codex exec args]", Summary: "run codex exec on the best available profile"},
	{Name: "switch-global <name> [--force]", Summary: "switch default global codex auth to profile"},
	{Name: "switch-global --restore-default", Summary: "restore latest saved external global auth"},
	{Name: "status", Summary: "show all profile auth states"},
	{Name: "heartbeat", Summary: "send a minimal keepalive hello for logged-in profiles"},
	{Name: "monitor [flags]", Summary: "show live subscription usage across accounts"},
	{Name: "monitor tui [flags]", Summary: "run the monitor terminal UI explicitly"},
	{Name: "monitor doctor [flags]", Summary: "check usage-monitor data sources"},
	{Name: "monitor completion [shell]", Summary: "print shell completion script"},
	{Name: "doctor [--json] [--timeout 8s]", Summary: "run non-mutating setup and auth checks"},
	{Name: "dry-run [operation]", Summary: "print planned operations without mutating state"},
	{Name: "completion <shell>", Summary: "print shell completion script for bash, zsh, or fish"},
	{Name: "version", Summary: "print multicodex version"},
	{Name: "help [command [subcommand]]", Summary: "show global or command-specific help"},
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
	"app": {
		Usage:       "multicodex app <name>",
		Description: "Launch a new Codex Mac app instance for one profile while keeping the shared sidebar. This switches the shared global auth pointer to that profile, then launches Codex with the shared default CODEX_HOME and a stable per-profile app-data folder.",
		Examples: []string{
			"multicodex app personal",
			"multicodex app work",
		},
	},
	"cli": {
		Usage:       "multicodex cli <name> [codex args...]",
		Description: "Run the interactive Codex CLI with the selected profile. This uses the same default args as the local c alias: search on, gpt-5.4, high reasoning, and no sandbox or approval prompts.",
		Examples: []string{
			"multicodex cli personal",
			`multicodex cli work "check this repo"`,
		},
	},
	"run": {
		Usage:       "multicodex run <name> -- <command...>",
		Description: "Run one command in a selected profile context without changing your current shell.",
		Examples: []string{
			"multicodex run personal -- codex login status",
		},
	},
	"exec": {
		Usage:       "multicodex exec [codex exec args]",
		Description: "Run `codex exec` after automatically selecting the best available configured profile. Profiles below 40% five-hour usage are eligible unless their weekly window is known to be exhausted, and multicodex picks the eligible profile whose weekly reset is soonest. When no profile is eligible, it picks a random accessible profile for that call. If usage data is unavailable for every profile, it picks a random configured profile.",
		Examples: []string{
			`multicodex exec -s read-only "Summarize the README in 3 bullets."`,
			"multicodex exec --skip-git-repo-check -C /path/to/repo \"Review the latest diff.\"",
		},
	},
	"switch-global": {
		Usage:       "multicodex switch-global <name> [--force] | --restore-default",
		Description: "Explicitly switch global default auth pointer to a profile, or restore the latest saved non-multicodex-managed default state. By default this refuses when the effective profile config no longer enables file-backed auth isolation; use --force only when you understand the risk.",
		Examples: []string{
			"multicodex switch-global personal",
			"multicodex switch-global --force personal",
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
		Description: "Fire-and-forget keepalive across logged-in profiles with cron-safe locking, retry/backoff, and per-profile summary output.",
		Examples: []string{
			"multicodex heartbeat",
		},
	},
	"monitor": {
		Usage:       "multicodex monitor [--interval 60s] [--timeout 60s] [--no-color] [--no-alt-screen]",
		Description: "Run the live subscription-usage terminal UI. The monitor prefers configured multicodex profiles, then falls back to compatible local Codex homes and legacy monitor account files. If one refresh loses official window data for every account, the last good official window cards stay visible and are marked stale.",
		Examples: []string{
			"multicodex monitor",
			"multicodex monitor --interval 30s",
			"multicodex monitor doctor",
		},
	},
	"monitor doctor": {
		Usage:       "multicodex monitor doctor [--json] [--timeout 60s]",
		Description: "Run read-only monitor checks against the active/default Codex account data sources. The command succeeds when at least one usage source works and reports degraded status when a source is unavailable.",
		Examples: []string{
			"multicodex monitor doctor",
			"multicodex monitor doctor --json",
		},
	},
	"monitor tui": {
		Usage:       "multicodex monitor tui [--interval 60s] [--timeout 60s] [--no-color] [--no-alt-screen]",
		Description: "Explicit alias for the live subscription-usage terminal UI. This behaves the same as `multicodex monitor` with no monitor subcommand.",
		Examples: []string{
			"multicodex monitor tui",
			"multicodex monitor tui --interval 30s",
		},
	},
	"monitor completion": {
		Usage:       "multicodex monitor completion [bash|zsh|fish]",
		Description: "Compatibility alias for shell completion setup. This prints the full multicodex completion script and defaults to bash when no shell is provided.",
		Examples: []string{
			"multicodex monitor completion",
			"multicodex monitor completion zsh",
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
		Usage:       "multicodex help [command [subcommand]]",
		Description: "Show global help or detailed help for one command.",
		Examples: []string{
			"multicodex help",
			"multicodex help heartbeat",
			"multicodex help monitor doctor",
			"multicodex help monitor tui",
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
	fmt.Println("  multicodex app personal")
	fmt.Println("  multicodex cli personal")
	fmt.Println("  multicodex monitor")
	fmt.Println("  multicodex heartbeat")
	fmt.Println(`  eval "$(multicodex completion zsh)"`)
	fmt.Println()
	fmt.Println("Help:")
	fmt.Println("  multicodex help <command> [subcommand]")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  - most commands are local-first and do not change shared default auth")
	fmt.Println("  - multicodex app and switch-global both update shared default auth on purpose")
}

func (a *App) cmdHelp(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}
	if len(args) > 2 {
		return &ExitError{Code: 2, Message: "usage: multicodex help [command [subcommand]]"}
	}

	name := normalizeHelpTopic(strings.Join(args, " "))
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
	case "monitor-doctor", "monitor/doctor":
		return "monitor doctor"
	case "monitor-tui", "monitor/tui":
		return "monitor tui"
	case "monitor-completion", "monitor/completion":
		return "monitor completion"
	default:
		return s
	}
}
