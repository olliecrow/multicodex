package multicodex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var codexHeartbeatTimeout = 60 * time.Second

const heartbeatPrompt = "hello"
const heartbeatRetryCount = 1
const heartbeatBackoff = 20 * time.Second

type heartbeatSettings struct {
	Prompt   string
	Timeout  time.Duration
	Retries  int
	Backoff  time.Duration
	LockPath string
}

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
	settings, err := loadHeartbeatSettings(a.store.paths)
	if err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}

	lockFile, acquired, err := acquireHeartbeatLock(settings.LockPath)
	if err != nil {
		return err
	}
	if !acquired {
		fmt.Println("multicodex heartbeat")
		fmt.Println("skip: another heartbeat run is already in progress")
		return nil
	}
	defer releaseHeartbeatLock(lockFile)

	names := sortedProfileNames(cfg)
	rows := make([]heartbeatRow, 0, len(names))
	loggedIn := 0
	success := 0
	failed := 0
	for _, name := range names {
		profile := cfg.Profiles[name]
		if err := a.store.EnsureProfileDir(profile); err != nil {
			failed++
			rows = append(rows, heartbeatRow{
				Profile: name,
				Status:  "fail",
				Detail:  err.Error(),
			})
			continue
		}
		hasAuth, err := HasAuthFile(profile.CodexHome)
		if err != nil {
			failed++
			rows = append(rows, heartbeatRow{
				Profile: name,
				Status:  "fail",
				Detail:  err.Error(),
			})
			continue
		}
		if err := ensureProfileCodexExecutionReady(a.store.paths, profile); err != nil {
			if !hasAuth {
				rows = append(rows, heartbeatRow{
					Profile: name,
					Status:  "skipped",
					Detail:  "auth.json not found. run multicodex login <name>",
				})
				continue
			}
			failed++
			rows = append(rows, heartbeatRow{
				Profile: name,
				Status:  "fail",
				Detail:  err.Error(),
			})
			continue
		}
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
		helloDetail, err := runCodexHeartbeatWithRetries(profile.CodexHome, settings)
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

func runCodexHeartbeatWithRetries(codexHome string, settings heartbeatSettings) (string, error) {
	maxAttempts := settings.Retries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	var lastDetail string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		detail, err := runCodexHeartbeat(codexHome, settings)
		if err == nil {
			if attempt > 1 {
				return fmt.Sprintf("heartbeat sent after %d attempts", attempt), nil
			}
			return detail, nil
		}
		lastErr = err
		lastDetail = detail

		if attempt == maxAttempts {
			return fmt.Sprintf("%s after %d attempts", lastDetail, attempt), lastErr
		}
		time.Sleep(settings.Backoff * time.Duration(attempt))
	}
	return lastDetail, lastErr
}

func runCodexHeartbeat(codexHome string, settings heartbeatSettings) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), settings.Timeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		"codex",
		"exec",
		"--skip-git-repo-check",
		"--sandbox",
		"read-only",
		"--color",
		"never",
		settings.Prompt,
	)
	cmd.Dir = codexHome
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
		return fmt.Sprintf("timed out after %s", settings.Timeout), err
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("codex exec failed with exit code %d", ee.ExitCode()), err
	}
	return "codex exec failed", err
}

func loadHeartbeatSettings(paths Paths) (heartbeatSettings, error) {
	settings := heartbeatSettings{
		Prompt:   heartbeatPrompt,
		Timeout:  codexHeartbeatTimeout,
		Retries:  heartbeatRetryCount,
		Backoff:  heartbeatBackoff,
		LockPath: filepath.Join(paths.MulticodexHome, "heartbeat.lock"),
	}

	if value := strings.TrimSpace(os.Getenv("MULTICODEX_HEARTBEAT_PROMPT")); value != "" {
		settings.Prompt = value
	}

	timeoutSeconds, err := parsePositiveEnvInt("MULTICODEX_HEARTBEAT_TIMEOUT_SECONDS", int(settings.Timeout/time.Second))
	if err != nil {
		return heartbeatSettings{}, err
	}
	settings.Timeout = time.Duration(timeoutSeconds) * time.Second

	retries, err := parseNonNegativeEnvInt("MULTICODEX_HEARTBEAT_RETRIES", settings.Retries)
	if err != nil {
		return heartbeatSettings{}, err
	}
	settings.Retries = retries

	backoffSeconds, err := parseNonNegativeEnvInt("MULTICODEX_HEARTBEAT_BACKOFF_SECONDS", int(settings.Backoff/time.Second))
	if err != nil {
		return heartbeatSettings{}, err
	}
	settings.Backoff = time.Duration(backoffSeconds) * time.Second

	if value := strings.TrimSpace(os.Getenv("MULTICODEX_HEARTBEAT_LOCK_PATH")); value != "" {
		settings.LockPath = value
	}

	return settings, nil
}

func parsePositiveEnvInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := parseNonNegativeEnvInt(name, fallback)
	if err != nil {
		return 0, err
	}
	if parsed == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func parseNonNegativeEnvInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return parsed, nil
}

func acquireHeartbeatLock(path string) (*os.File, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, fmt.Errorf("create heartbeat lock dir: %w", err)
	}

	lockFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open heartbeat lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("acquire heartbeat lock: %w", err)
	}

	if err := lockFile.Truncate(0); err == nil {
		_, _ = lockFile.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	}
	return lockFile, true, nil
}

func releaseHeartbeatLock(lockFile *os.File) {
	if lockFile == nil {
		return
	}
	_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	_ = lockFile.Close()
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
