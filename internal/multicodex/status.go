package multicodex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type profileStatus struct {
	Name         string
	GlobalMarker string
	AuthFile     bool
	State        string
	Account      string
	Detail       string
}

var emailRe = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

var codexLoginStatusTimeout = 5 * time.Second
var profileCheckWorkerLimit = 6

func PrintStatus(store *Store, cfg *Config) error {
	if len(cfg.Profiles) == 0 {
		fmt.Println("no profiles configured")
		fmt.Println("add a profile with: multicodex add <name>")
		return nil
	}

	currentGlobalName, err := activeGlobalProfile(store.paths, cfg)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := collectProfileRows(cfg, names, currentGlobalName)

	fmt.Println("multicodex status")
	if currentGlobalName == "" {
		fmt.Println("no global default profile selected by multicodex")
	} else {
		fmt.Println("*", "current global default profile")
	}
	fmt.Println()
	fmt.Printf("%-2s %-16s %-10s %-10s %-30s %s\n", "", "profile", "auth.json", "state", "account", "detail")
	for _, row := range rows {
		auth := "no"
		if row.AuthFile {
			auth = "yes"
		}
		fmt.Printf("%-2s %-16s %-10s %-10s %-30s %s\n", row.GlobalMarker, row.Name, auth, row.State, truncate(row.Account, 30), truncate(row.Detail, 80))
	}

	fmt.Println()
	fmt.Printf("default codex auth path: %s\n", store.paths.DefaultAuthPath)
	return nil
}

func collectProfileRows(cfg *Config, names []string, currentGlobalName string) []profileStatus {
	rows := make([]profileStatus, len(names))
	workers := parallelWorkers(len(names))
	if workers == 1 {
		for i, name := range names {
			rows[i] = buildProfileRow(name, cfg.Profiles[name], currentGlobalName)
		}
		return rows
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, name := range names {
		i := i
		name := name
		profile := cfg.Profiles[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[i] = buildProfileRow(name, profile, currentGlobalName)
		}()
	}
	wg.Wait()
	return rows
}

func buildProfileRow(name string, profile Profile, currentGlobalName string) profileStatus {
	row := profileStatus{Name: name}
	if name == currentGlobalName {
		row.GlobalMarker = "*"
	} else {
		row.GlobalMarker = " "
	}
	hasAuth, err := HasAuthFile(profile.CodexHome)
	if err != nil {
		row.State = "error"
		row.Detail = err.Error()
		return row
	}
	row.AuthFile = hasAuth
	state, account, detail := codexLoginStatus(profile.CodexHome)
	row.State = state
	row.Account = account
	row.Detail = detail
	return row
}

func parallelWorkers(total int) int {
	if total <= 1 {
		return 1
	}
	if profileCheckWorkerLimit <= 1 {
		return 1
	}
	if total < profileCheckWorkerLimit {
		return total
	}
	return profileCheckWorkerLimit
}

func codexLoginStatus(codexHome string) (state, account, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), codexLoginStatusTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "login", "status")
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Env = withProfileEnv(os.Environ(), codexHome, "")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	all := strings.TrimSpace(out.String())
	lower := strings.ToLower(all)

	account = emailRe.FindString(all)
	if account == "" {
		email, err := emailFromAuthFile(filepath.Join(codexHome, "auth.json"))
		if err == nil && email != "" {
			account = email
		} else {
			account = "-"
		}
	}

	if err == nil {
		if strings.Contains(lower, "logged") || strings.Contains(lower, "active") || strings.Contains(lower, "authenticated") {
			return "logged-in", account, firstLineOrDash(all)
		}
		return "ok", account, firstLineOrDash(all)
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "error", account, fmt.Sprintf("codex login status timed out after %s", codexLoginStatusTimeout)
		}
		if strings.Contains(lower, "not logged") || strings.Contains(lower, "log in") || strings.Contains(lower, "unauth") {
			return "logged-out", account, firstLineOrDash(all)
		}
		return "error", account, firstLineOrDash(all)
	}

	return "error", account, err.Error()
}

func activeGlobalProfile(paths Paths, cfg *Config) (string, error) {
	info, err := os.Lstat(paths.DefaultAuthPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("inspect default auth path: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if cfg.Global.CurrentProfile != "" {
			return cfg.Global.CurrentProfile, nil
		}
		return "", nil
	}

	target, err := os.Readlink(paths.DefaultAuthPath)
	if err != nil {
		return "", fmt.Errorf("read default auth symlink: %w", err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(paths.DefaultAuthPath), target)
	}

	for _, profile := range cfg.Profiles {
		if filepath.Clean(target) == filepath.Clean(filepath.Join(profile.CodexHome, "auth.json")) {
			return profile.Name, nil
		}
	}
	if cfg.Global.CurrentProfile != "" {
		return cfg.Global.CurrentProfile, nil
	}
	return "", nil
}

func firstLineOrDash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func emailFromAuthFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var payload struct {
		Email  string `json:"email"`
		Tokens struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return "", err
	}
	if payload.Email != "" {
		return payload.Email, nil
	}
	if payload.Tokens.IDToken == "" {
		return "", nil
	}

	parts := strings.Split(payload.Tokens.IDToken, ".")
	if len(parts) < 2 {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return "", err
	}
	v, ok := claims["email"]
	if !ok {
		return "", nil
	}
	email, ok := v.(string)
	if !ok {
		return "", nil
	}
	return email, nil
}
