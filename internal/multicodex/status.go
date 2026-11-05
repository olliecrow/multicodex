package multicodex

import (
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

	"github.com/olliecrow/multicodex/internal/codexstate"
)

type profileStatus struct {
	Name     string
	AuthFile bool
	State    string
	Account  string
	Detail   string
}

var accountEmailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

const loginStatusOutputLimit = 64 << 10

var codexLoginStatusTimeout = 5 * time.Second
var profileCheckWorkerLimit = 6

func PrintStatus(store *Store, cfg *Config) error {
	if len(cfg.Profiles) == 0 {
		fmt.Println("no profiles configured")
		fmt.Println("add a profile with: multicodex add <name>")
		return nil
	}

	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := collectProfileRows(store, cfg, names)

	fmt.Println("multicodex status")
	fmt.Println("profile-local auth status")
	fmt.Println()
	fmt.Printf("%-16s %-10s %-10s %-30s %s\n", "profile", "auth.json", "state", "account", "detail")
	for _, row := range rows {
		auth := "no"
		if row.AuthFile {
			auth = "yes"
		}
		fmt.Printf("%-16s %-10s %-10s %-30s %s\n", row.Name, auth, row.State, truncate(row.Account, 30), truncate(row.Detail, 80))
	}
	return nil
}

func collectProfileRows(store *Store, cfg *Config, names []string) []profileStatus {
	rows := make([]profileStatus, len(names))
	workers := parallelWorkers(len(names))
	if workers == 1 {
		for i, name := range names {
			rows[i] = buildProfileRow(store, name, cfg.Profiles[name])
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
			rows[i] = buildProfileRow(store, name, profile)
		}()
	}
	wg.Wait()
	return rows
}

func buildProfileRow(store *Store, name string, profile Profile) profileStatus {
	row := profileStatus{Name: name}
	if err := store.ensureProfileStoragePathSafe(profile); err != nil {
		row.State = "error"
		row.Detail = err.Error()
		return row
	}
	if _, err := os.Stat(profile.CodexHome); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			row.State = "missing"
			row.Detail = "profile codex home not found"
			return row
		}
		row.State = "error"
		row.Detail = err.Error()
		return row
	}
	_, hasAuth, err := ensureProfileAuthPathSafe(profile.CodexHome)
	if err != nil {
		row.State = "error"
		row.Detail = err.Error()
		return row
	}
	row.AuthFile = hasAuth
	if err := ensureProfileCodexExecutionReady(store.paths, profile); err != nil {
		row.State = "error"
		row.Detail = err.Error()
		return row
	}
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
	account = accountFromAuthFile(filepath.Join(codexHome, "auth.json"))

	ctx, cancel := context.WithTimeout(context.Background(), codexLoginStatusTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "login", "status")
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Env = profileCodexEnv(os.Environ(), codexHome, "")
	out := codexstate.NewCappedOutput(loginStatusOutputLimit, cancel)
	cmd.Stdout = out
	cmd.Stderr = out

	err := cmd.Run()
	if out.Exceeded() {
		return "error", account, "codex login status output exceeded 64 KiB"
	}
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "error", account, fmt.Sprintf("codex login status timed out after %s", codexLoginStatusTimeout)
	}
	text := out.String()
	if err == nil {
		if confirmation, ok := loginStatusConfirmation(text); ok {
			return "logged-in", account, confirmation
		}
	}
	if loginStatusTextIndicatesLoggedOut(strings.ToLower(text)) {
		return "logged-out", account, "not logged in"
	}
	if err == nil {
		return "error", account, "codex login status output is not a recognized login confirmation"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return "error", account, fmt.Sprintf("codex login status failed with exit code %d", ee.ExitCode())
	}
	return "error", account, "codex login status could not be started"
}

// loginStatusMethods maps the official `codex login status` confirmation
// suffixes to fixed details. Raw status text never reaches diagnostics.
var loginStatusMethods = []struct {
	method string
	detail string
}{
	{"ChatGPT", "logged in using ChatGPT"},
	{"an API key", "logged in using an API key"},
	{"access token", "logged in using an access token"},
	{"personal access token", "logged in using a personal access token"},
	{"workload identity", "logged in using workload identity"},
	{"Amazon Bedrock", "logged in using Amazon Bedrock"},
}

func loginStatusConfirmation(text string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		method, ok := strings.CutPrefix(strings.TrimSpace(line), "Logged in using ")
		if !ok {
			continue
		}
		for _, known := range loginStatusMethods {
			if strings.HasPrefix(method, known.method) {
				return known.detail, true
			}
		}
		return "logged in", true
	}
	return "", false
}

func loginStatusTextIndicatesLoggedOut(lower string) bool {
	for _, phrase := range []string{
		"not logged",
		"logged out",
		"not authenticated",
		"unauth",
		"log in",
		"sign in",
		"signed out",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
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

// accountFromAuthFile returns the display email from a profile auth file, or
// "-" when it is missing or does not look like a plain email address.
func accountFromAuthFile(path string) string {
	email, err := emailFromAuthFile(path)
	if err != nil || len(email) > 254 || !accountEmailRe.MatchString(email) {
		return "-"
	}
	return email
}

func emailFromAuthFile(path string) (string, error) {
	b, _, err := codexstate.ReadAuthFile(path)
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
