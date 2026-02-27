package multicodex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
}

type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Details string `json:"details"`
}

func (r DoctorReport) HasFailures() bool {
	for _, c := range r.Checks {
		if c.Status == "fail" {
			return true
		}
	}
	return false
}

func RunDoctor(store *Store, cfg *Config, timeout time.Duration) DoctorReport {
	checks := make([]DoctorCheck, 0, 24)

	checks = append(checks, checkDirExists("multicodex home", store.paths.MulticodexHome, true))
	checks = append(checks, DoctorCheck{
		Name:    "config",
		Status:  "ok",
		Details: fmt.Sprintf("loaded config with %d profile(s)", len(cfg.Profiles)),
	})

	codexFound := false
	if path, err := exec.LookPath("codex"); err != nil {
		checks = append(checks, DoctorCheck{
			Name:    "codex binary",
			Status:  "fail",
			Details: "codex was not found in PATH",
		})
	} else {
		codexFound = true
		detail := path
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "codex", "--version")
		out, err := cmd.CombinedOutput()
		if err != nil {
			detail = fmt.Sprintf("%s (codex --version failed: %v)", path, err)
			checks = append(checks, DoctorCheck{Name: "codex binary", Status: "warn", Details: detail})
		} else {
			version := strings.TrimSpace(string(out))
			if version == "" {
				version = "version output is empty"
			}
			checks = append(checks, DoctorCheck{Name: "codex binary", Status: "ok", Details: fmt.Sprintf("%s (%s)", path, version)})
		}
	}

	checks = append(checks, checkDirExists("default codex home", store.paths.DefaultCodexHome, false))
	checks = append(checks, checkDefaultAuthPath(store.paths.DefaultAuthPath))
	checks = append(checks, checkFileStoreConfig("default codex config", filepath.Join(store.paths.DefaultCodexHome, "config.toml"), false))
	checks = append(checks, checkRepositoryLeakGuards(store.paths)...)

	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	profileChecks := collectProfileDoctorChecks(cfg, names, codexFound)
	for i := range profileChecks {
		checks = append(checks, profileChecks[i]...)
	}

	return DoctorReport{Checks: checks}
}

func collectProfileDoctorChecks(cfg *Config, names []string, codexFound bool) [][]DoctorCheck {
	result := make([][]DoctorCheck, len(names))
	workers := parallelWorkers(len(names))
	if workers == 1 {
		for i, name := range names {
			result[i] = profileDoctorChecks(name, cfg.Profiles[name], codexFound)
		}
		return result
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
			result[i] = profileDoctorChecks(name, profile, codexFound)
		}()
	}
	wg.Wait()
	return result
}

func profileDoctorChecks(name string, profile Profile, codexFound bool) []DoctorCheck {
	prefix := "profile " + name
	out := make([]DoctorCheck, 0, 4)

	if err := ValidateProfileName(name); err != nil {
		out = append(out, DoctorCheck{Name: prefix + " name", Status: "fail", Details: err.Error()})
	} else {
		out = append(out, DoctorCheck{Name: prefix + " name", Status: "ok", Details: "valid"})
	}

	out = append(out, checkDirExists(prefix+" codex home", profile.CodexHome, true))
	out = append(out, checkFileStoreConfig(prefix+" config", filepath.Join(profile.CodexHome, "config.toml"), true))
	out = append(out, checkAuthFile(prefix+" auth", filepath.Join(profile.CodexHome, "auth.json")))

	if codexFound {
		state, account, detail := codexLoginStatus(profile.CodexHome)
		status := "warn"
		if state == "logged-in" || state == "ok" {
			status = "ok"
		}
		out = append(out, DoctorCheck{
			Name:    prefix + " login status",
			Status:  status,
			Details: fmt.Sprintf("state=%s account=%s detail=%s", state, account, detail),
		})
	}
	return out
}

func checkRepositoryLeakGuards(paths Paths) []DoctorCheck {
	root, err := gitRootFromCWD()
	if err != nil {
		return []DoctorCheck{{
			Name:    "repo leak guard",
			Status:  "warn",
			Details: "git root not detected from current working directory. skipped tracked-file leak checks",
		}}
	}

	checks := make([]DoctorCheck, 0, 6)
	checks = append(checks, DoctorCheck{
		Name:    "repo leak guard git root",
		Status:  "ok",
		Details: root,
	})

	checks = append(checks, checkPathOutsideRepo("multicodex home path isolation", root, paths.MulticodexHome))
	checks = append(checks, checkPathOutsideRepo("default codex home path isolation", root, paths.DefaultCodexHome))
	checks = append(checks, checkRequiredIgnorePatterns(root))
	checks = append(checks, checkTrackedSensitiveFiles(root))
	return checks
}

func gitRootFromCWD() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func checkPathOutsideRepo(name, root, p string) DoctorCheck {
	if p == "" {
		return DoctorCheck{Name: name, Status: "warn", Details: "path is empty"}
	}
	if isSubpath(root, p) {
		return DoctorCheck{
			Name:    name,
			Status:  "fail",
			Details: fmt.Sprintf("path is inside git working tree: %s", p),
		}
	}
	return DoctorCheck{Name: name, Status: "ok", Details: p}
}

func isSubpath(root, p string) bool {
	absRoot, err := canonicalPath(root)
	if err != nil {
		return false
	}
	absPath, err := canonicalPath(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return false
	}
	return true
}

func canonicalPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}

	current := abs
	var tail []string
	for {
		if _, err := os.Stat(current); err == nil {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		tail = append([]string{filepath.Base(current)}, tail...)
		current = parent
	}

	resolvedBase, err := filepath.EvalSymlinks(current)
	if err != nil {
		resolvedBase = current
	}
	if len(tail) == 0 {
		return resolvedBase, nil
	}
	parts := append([]string{resolvedBase}, tail...)
	return filepath.Join(parts...), nil
}

func checkRequiredIgnorePatterns(root string) DoctorCheck {
	content, err := collectGitignoreContent(root)
	if err != nil {
		return DoctorCheck{Name: "repo leak guard ignore patterns", Status: "warn", Details: err.Error()}
	}
	if strings.TrimSpace(content) == "" {
		return DoctorCheck{
			Name:    "repo leak guard ignore patterns",
			Status:  "warn",
			Details: "no .gitignore entries found from current directory to git root",
		}
	}
	missing := missingIgnorePatterns(content)
	if len(missing) > 0 {
		return DoctorCheck{
			Name:    "repo leak guard ignore patterns",
			Status:  "warn",
			Details: "missing recommended patterns: " + strings.Join(missing, ", "),
		}
	}
	return DoctorCheck{Name: "repo leak guard ignore patterns", Status: "ok", Details: "required patterns present"}
}

func collectGitignoreContent(root string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	var chunks []string
	current := cwd
	for {
		if !isSubpath(root, current) {
			break
		}
		p := filepath.Join(current, ".gitignore")
		if b, err := os.ReadFile(p); err == nil {
			chunks = append(chunks, string(b))
		}
		if filepath.Clean(current) == filepath.Clean(root) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return strings.Join(chunks, "\n"), nil
}

func missingIgnorePatterns(content string) []string {
	required := []string{
		".codex/",
		"**/auth.json",
		".env",
		".env.*",
	}
	missing := make([]string, 0, len(required)+1)
	hasLegacyMulticodexPattern := strings.Contains(content, ".multicodex/")
	hasTargetedMulticodexPatterns := containsAnyPattern(content, []string{
		"multicodex/config.json",
		"multicodex/profiles/",
		"multicodex/backups/",
	})
	if !hasLegacyMulticodexPattern && !hasTargetedMulticodexPatterns {
		missing = append(missing, ".multicodex/ or **/multicodex/{config.json,profiles/,backups/}")
	}
	for _, pattern := range required {
		if !strings.Contains(content, pattern) {
			missing = append(missing, pattern)
		}
	}
	return missing
}

func containsAnyPattern(content string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(content, pattern) {
			return true
		}
	}
	return false
}

func checkTrackedSensitiveFiles(root string) DoctorCheck {
	cmd := exec.Command("git", "-C", root, "ls-files")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return DoctorCheck{Name: "repo leak guard tracked files", Status: "warn", Details: "could not enumerate tracked files"}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	found := make([]string, 0, 8)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isSensitiveTrackedPath(line) {
			found = append(found, line)
			if len(found) >= 8 {
				break
			}
		}
	}
	if len(found) > 0 {
		return DoctorCheck{
			Name:    "repo leak guard tracked files",
			Status:  "fail",
			Details: "tracked sensitive-looking files detected: " + strings.Join(found, ", "),
		}
	}
	return DoctorCheck{Name: "repo leak guard tracked files", Status: "ok", Details: "no sensitive-looking tracked files detected"}
}

func isSensitiveTrackedPath(p string) bool {
	clean := path.Clean(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")))
	base := path.Base(clean)
	if strings.Contains(clean, "/.multicodex/") || strings.HasPrefix(clean, ".multicodex/") {
		return true
	}
	if clean == "multicodex/config.json" || strings.Contains(clean, "/multicodex/config.json") {
		return true
	}
	if strings.Contains(clean, "/multicodex/profiles/") || strings.HasPrefix(clean, "multicodex/profiles/") {
		return true
	}
	if strings.Contains(clean, "/multicodex/backups/") || strings.HasPrefix(clean, "multicodex/backups/") {
		return true
	}
	if strings.Contains(clean, "/.codex/") || strings.HasPrefix(clean, ".codex/") {
		return true
	}
	if base == "auth.json" {
		return true
	}
	if base == ".env" {
		return true
	}
	if strings.HasPrefix(base, ".env.") && !strings.HasSuffix(base, ".example") && !strings.HasSuffix(base, ".sample") {
		return true
	}
	if strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".key") {
		return true
	}
	return false
}

func checkDirExists(name, path string, strictPerms bool) DoctorCheck {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DoctorCheck{Name: name, Status: "warn", Details: "not found"}
		}
		return DoctorCheck{Name: name, Status: "fail", Details: err.Error()}
	}
	if !info.IsDir() {
		return DoctorCheck{Name: name, Status: "fail", Details: "expected directory"}
	}
	if strictPerms && info.Mode().Perm()&0o077 != 0 {
		return DoctorCheck{Name: name, Status: "warn", Details: fmt.Sprintf("permissions are %o, recommend 700", info.Mode().Perm())}
	}
	return DoctorCheck{Name: name, Status: "ok", Details: path}
}

func checkDefaultAuthPath(path string) DoctorCheck {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DoctorCheck{Name: "default auth path", Status: "warn", Details: "not found"}
		}
		return DoctorCheck{Name: "default auth path", Status: "fail", Details: err.Error()}
	}
	if info.IsDir() {
		return DoctorCheck{Name: "default auth path", Status: "fail", Details: "path is a directory, expected file or symlink"}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return DoctorCheck{Name: "default auth path", Status: "fail", Details: fmt.Sprintf("symlink read failed: %v", err)}
		}
		return DoctorCheck{Name: "default auth path", Status: "ok", Details: "symlink -> " + target}
	}
	return DoctorCheck{Name: "default auth path", Status: "ok", Details: "regular file"}
}

func checkFileStoreConfig(name, path string, required bool) DoctorCheck {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if required {
				return DoctorCheck{Name: name, Status: "fail", Details: "missing config.toml with cli_auth_credentials_store = \"file\""}
			}
			return DoctorCheck{Name: name, Status: "warn", Details: "config.toml not found"}
		}
		return DoctorCheck{Name: name, Status: "fail", Details: err.Error()}
	}
	content := string(b)
	if !strings.Contains(content, "cli_auth_credentials_store") || !strings.Contains(content, "file") {
		status := "warn"
		if required {
			status = "fail"
		}
		return DoctorCheck{Name: name, Status: status, Details: "config present but file credential store is not configured"}
	}
	return DoctorCheck{Name: name, Status: "ok", Details: "file credential store configured"}
}

func checkAuthFile(name, path string) DoctorCheck {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DoctorCheck{Name: name, Status: "warn", Details: "auth.json not found. run multicodex login <name>"}
		}
		return DoctorCheck{Name: name, Status: "fail", Details: err.Error()}
	}
	if info.IsDir() {
		return DoctorCheck{Name: name, Status: "fail", Details: "auth.json is a directory"}
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return DoctorCheck{Name: name, Status: "fail", Details: fmt.Sprintf("auth.json read failed: %v", err)}
	}

	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		return DoctorCheck{Name: name, Status: "fail", Details: fmt.Sprintf("auth.json is invalid JSON: %v", err)}
	}

	tokensRaw, ok := parsed["tokens"]
	hasAPIKey := false
	if _, ok := parsed["OPENAI_API_KEY"]; ok {
		hasAPIKey = true
	}

	warnings := make([]string, 0, 4)
	if info.Mode().Perm()&0o077 != 0 {
		warnings = append(warnings, fmt.Sprintf("permissions are %o, recommend 600", info.Mode().Perm()))
	}

	if !ok {
		if hasAPIKey {
			warnings = append(warnings, "api-key auth detected. multicodex is designed for subscription account auth")
		} else {
			warnings = append(warnings, "auth.json parsed but token object is missing")
		}
	} else {
		tokens, ok := tokensRaw.(map[string]any)
		if !ok {
			return DoctorCheck{Name: name, Status: "fail", Details: "auth.json token object has unexpected shape"}
		}
		if _, ok := tokens["access_token"]; !ok {
			warnings = append(warnings, "auth.json token object is missing access_token")
		}
		if _, ok := tokens["refresh_token"]; !ok {
			warnings = append(warnings, "auth.json token object is missing refresh_token")
		}
		if _, ok := tokens["id_token"]; !ok {
			warnings = append(warnings, "auth.json token object is missing id_token")
		}
	}

	if len(warnings) > 0 {
		return DoctorCheck{Name: name, Status: "warn", Details: strings.Join(warnings, "; ")}
	}
	return DoctorCheck{Name: name, Status: "ok", Details: "auth.json present and structured as token auth"}
}

func printDoctorHuman(report DoctorReport) {
	fmt.Println("multicodex doctor")
	fmt.Println()
	fails := 0
	warns := 0
	for _, c := range report.Checks {
		label := "ok"
		switch c.Status {
		case "fail":
			label = "fail"
			fails++
		case "warn":
			label = "warn"
			warns++
		}
		fmt.Printf("[%s] %s: %s\n", label, c.Name, c.Details)
	}
	fmt.Println()
	if fails > 0 {
		fmt.Printf("doctor result: FAIL (%d fail, %d warn)\n", fails, warns)
		return
	}
	if warns > 0 {
		fmt.Printf("doctor result: PASS (%d warn)\n", warns)
		return
	}
	fmt.Println("doctor result: PASS")
}
