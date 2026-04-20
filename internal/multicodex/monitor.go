package multicodex

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
	"multicodex/internal/monitor/tui"
	"multicodex/internal/monitor/usage"
)

func (a *App) cmdMonitor(args []string) error {
	if len(args) == 0 {
		return a.runMonitorTUI(nil)
	}

	switch args[0] {
	case "tui":
		return a.runMonitorTUI(args[1:])
	case "doctor":
		return a.runMonitorDoctor(args[1:])
	case "completion":
		return a.runMonitorCompletion(args[1:])
	case "help", "-h", "--help":
		printMonitorUsage()
		return nil
	default:
		if strings.HasPrefix(args[0], "-") {
			return a.runMonitorTUI(args)
		}
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown monitor command: %s\nrun \"multicodex help monitor\" for monitor usage", args[0])}
	}
}

func (a *App) runMonitorDoctor(args []string) error {
	fs := flag.NewFlagSet("monitor doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "output doctor report as JSON")
	timeout := fs.Duration("timeout", 60*time.Second, "doctor timeout")
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: "usage: multicodex monitor doctor [--json] [--timeout 60s]"}
	}
	if *timeout <= 0 {
		return &ExitError{Code: 2, Message: "error: --timeout must be > 0"}
	}
	if err := usage.EnsureMonitorDataDir(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not ensure monitor data dir: %v\n", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	report := usage.RunDoctor(ctx)
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printMonitorDoctorHuman(report)
	}
	if !report.Healthy() {
		return &ExitError{Code: 1, Message: "monitor doctor checks failed"}
	}
	return nil
}

func (a *App) runMonitorCompletion(args []string) error {
	if len(args) > 1 {
		return &ExitError{Code: 2, Message: "usage: multicodex monitor completion [bash|zsh|fish]"}
	}

	shell := "bash"
	if len(args) == 1 {
		shell = strings.TrimSpace(args[0])
		if shell == "" {
			shell = "bash"
		}
	}

	return a.cmdCompletion([]string{shell})
}

func (a *App) runMonitorTUI(args []string) error {
	fs := flag.NewFlagSet("monitor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	interval := fs.Duration("interval", 60*time.Second, "poll interval")
	timeout := fs.Duration("timeout", 60*time.Second, "per-poll fetch timeout")
	noColor := fs.Bool("no-color", false, "disable color styling")
	noAltScreen := fs.Bool("no-alt-screen", false, "disable alternate screen mode")
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: "usage: multicodex monitor [--interval 60s] [--timeout 60s] [--no-color] [--no-alt-screen]"}
	}
	if *interval <= 0 {
		return &ExitError{Code: 2, Message: "error: --interval must be > 0"}
	}
	if *timeout <= 0 {
		return &ExitError{Code: 2, Message: "error: --timeout must be > 0"}
	}
	if err := usage.EnsureMonitorDataDir(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not ensure monitor data dir: %v\n", err)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return &ExitError{Code: 1, Message: "interactive monitor requires a TTY"}
	}

	fetcher := usage.NewDefaultFetcher()
	defer fetcher.Close()

	if err := tui.Run(tui.Options{
		Interval:  *interval,
		Timeout:   *timeout,
		NoColor:   *noColor,
		AltScreen: !*noAltScreen,
		Fetch: func(ctx context.Context) (*usage.Summary, error) {
			return fetcher.Fetch(ctx)
		},
	}); err != nil {
		return err
	}
	return nil
}

func printMonitorDoctorHuman(report usage.DoctorReport) {
	fmt.Println("multicodex monitor doctor")
	fmt.Println()
	for _, c := range report.Checks {
		state := "FAIL"
		if c.OK {
			state = "PASS"
		}
		fmt.Printf("[%s] %s\n", state, c.Name)
		fmt.Printf("  %s\n", c.Details)
	}
	fmt.Println()
	switch report.Status() {
	case "healthy":
		fmt.Println("monitor doctor result: PASS")
	case "degraded":
		fmt.Println("monitor doctor result: PASS (degraded: at least one usage source is unavailable)")
	default:
		fmt.Println("monitor doctor result: FAIL")
	}
}

func printMonitorUsage() {
	fmt.Println("multicodex monitor")
	fmt.Println()
	fmt.Println("Show live Codex subscription usage across multicodex profiles and compatible local accounts.")
	fmt.Println("The monitor is read-only and does not mutate Codex account data.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  multicodex monitor                       Run terminal user interface (default)")
	fmt.Println("  multicodex monitor tui [flags]           Run terminal user interface explicitly")
	fmt.Println("  multicodex monitor doctor [flags]        Run setup and source checks")
	fmt.Println("  multicodex monitor completion [shell]    Print shell completion script")
	fmt.Println()
	fmt.Println("Shell completion:")
	fmt.Println("  multicodex completion bash")
	fmt.Println("  multicodex completion zsh")
	fmt.Println("  multicodex completion fish")
	fmt.Println("  multicodex monitor completion bash")
	fmt.Println()
	fmt.Println("Monitor doctor flags:")
	fmt.Println("  --json            Output report as JSON")
	fmt.Println("  --timeout 60s     Doctor timeout")
	fmt.Println()
	fmt.Println("Monitor terminal user interface flags:")
	fmt.Println("  --interval 60s    Poll interval")
	fmt.Println("  --timeout 60s     Per-poll fetch timeout")
	fmt.Println("  --no-color        Disable color styling")
	fmt.Println("  --no-alt-screen   Disable alternate screen mode")
}
