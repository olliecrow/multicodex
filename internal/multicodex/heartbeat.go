package multicodex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

var codexHeartbeatTimeout = 60 * time.Second

const heartbeatPrompt = "hello"

type heartbeatRow struct {
	Profile  string
	Account  string
	Status   string
	Detail   string
	Duration time.Duration
}

func (a *App) cmdHeartbeat(args []string) error {
	if len(args) != 0 {
		return &ExitError{Code: 2, Message: "usage: multicodex heartbeat"}
	}

	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}
	if len(cfg.Profiles) == 0 {
		return &ExitError{Code: 2, Message: "no profiles configured. add one with: multicodex add <name>"}
	}

	names := sortedProfileNames(cfg)
	rows := make([]heartbeatRow, 0, len(names))
	loggedIn := 0
	success := 0
	failed := 0
	for _, name := range names {
		profile := cfg.Profiles[name]
		state, account, detail := codexLoginStatus(profile.CodexHome)
		if state != "logged-in" {
			rows = append(rows, heartbeatRow{
				Profile: name,
				Account: account,
				Status:  "skipped",
				Detail:  heartbeatSkipDetail(state, detail),
			})
			continue
		}
		loggedIn++
		start := time.Now()
		helloDetail, err := runCodexHeartbeat(profile.CodexHome)
		elapsed := time.Since(start)
		if err != nil {
			failed++
			rows = append(rows, heartbeatRow{
				Profile:  name,
				Account:  account,
				Status:   "fail",
				Detail:   helloDetail,
				Duration: elapsed,
			})
			continue
		}
		success++
		rows = append(rows, heartbeatRow{
			Profile:  name,
			Account:  account,
			Status:   "ok",
			Detail:   "heartbeat sent",
			Duration: elapsed,
		})
	}

	fmt.Println("multicodex heartbeat")
	fmt.Printf("%-24s %-30s %-8s %-8s %s\n", "profile", "account", "status", "elapsed", "detail")
	for _, row := range rows {
		elapsed := "-"
		if row.Duration > 0 {
			elapsed = row.Duration.Round(time.Millisecond).String()
		}
		fmt.Printf("%-24s %-30s %-8s %-8s %s\n", truncate(row.Profile, 24), truncate(row.Account, 30), row.Status, elapsed, truncate(row.Detail, 80))
	}
	fmt.Println()
	fmt.Printf("summary: total=%d logged-in=%d ok=%d fail=%d skipped=%d\n", len(rows), loggedIn, success, failed, len(rows)-loggedIn)

	if loggedIn == 0 {
		return &ExitError{Code: 1, Message: "heartbeat completed with no logged-in profiles"}
	}
	if failed > 0 {
		return &ExitError{Code: 1, Message: fmt.Sprintf("heartbeat completed with %d failure(s)", failed)}
	}
	return nil
}

func runCodexHeartbeat(codexHome string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexHeartbeatTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "exec", "--skip-git-repo-check", heartbeatPrompt)
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Env = withProfileEnv(os.Environ(), codexHome, "")

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err == nil {
		return "heartbeat sent", nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("timed out after %s", codexHeartbeatTimeout), err
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("codex exec failed with exit code %d", ee.ExitCode()), err
	}
	return "codex exec failed", err
}

func heartbeatSkipDetail(state, detail string) string {
	switch state {
	case "logged-out":
		return "not logged in"
	case "error":
		if detail == "" || detail == "-" {
			return "login status unavailable"
		}
		lower := strings.ToLower(detail)
		if strings.Contains(lower, "executable file not found") || strings.Contains(lower, "not found in $path") {
			return "codex binary not found in PATH"
		}
		if strings.Contains(strings.ToLower(detail), "timed out") {
			return detail
		}
		return "login status unavailable"
	default:
		return fmt.Sprintf("login state %q", state)
	}
}
