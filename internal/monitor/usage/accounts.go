package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	defaultMulticodexHomeDirName = "multicodex"
	defaultMonitorSubdirName     = "monitor"
	defaultAccountsFileName      = "accounts.json"
	accountsFileEnvVar           = "CODEX_USAGE_MONITOR_ACCOUNTS_FILE"
	multicodexHomeEnvVar         = "MULTICODEX_HOME"
	defaultCodexHomeEnvVar       = "MULTICODEX_DEFAULT_CODEX_HOME"
)

type accountFile struct {
	Version  int           `json:"version"`
	Accounts []accountItem `json:"accounts"`
}

type accountItem struct {
	Label     string `json:"label"`
	CodexHome string `json:"codex_home"`
}

type MonitorAccount struct {
	Label             string `json:"label"`
	CodexHome         string `json:"codex_home"`
	SelectionPriority int    `json:"-"`
	UseAppServer      bool   `json:"-"`
}

type MonitorAccountOptions struct {
	IncludeDefault bool
	IncludeActive  bool
	Discover       bool
}

type multicodexConfigFile struct {
	Profiles map[string]struct {
		Name      string `json:"name"`
		CodexHome string `json:"codex_home"`
	} `json:"profiles"`
}

func loadMonitorAccounts() ([]MonitorAccount, string, error) {
	return loadMonitorAccountsWithOptions(MonitorAccountOptions{IncludeDefault: true})
}

func loadMonitorAccountsWithOptions(options MonitorAccountOptions) ([]MonitorAccount, string, error) {
	collector := newAccountCollector()

	if options.IncludeDefault {
		defaultHome, err := defaultCodexHome()
		if err != nil {
			return nil, "", err
		}
		collector.add("global", defaultHome, 50, false, false)
	}

	if options.IncludeActive {
		envHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if envHome == "" {
			collector.warnf("active CODEX_HOME is not set")
		} else {
			expanded, expandErr := expandPath(envHome)
			if expandErr != nil {
				collector.warnf("could not resolve CODEX_HOME: %v", expandErr)
			} else {
				collector.add("active", expanded, 40, true, false)
			}
		}
	}

	profileAccounts, profileWarning, profileErr := loadAccountsFromMulticodexConfig()
	if profileErr != nil {
		collector.warnf("multicodex profile discovery error: %v", profileErr)
	} else {
		if profileWarning != "" {
			collector.warnf("%s", profileWarning)
		}
		for _, account := range profileAccounts {
			collector.add(account.Label, account.CodexHome, 90, false, true)
		}
	}

	fileAccounts, fileWarning, fileErr := loadAccountsFromFile()
	if fileErr != nil {
		collector.warnf("accounts file could not be read: %v", fileErr)
	} else {
		if fileWarning != "" {
			collector.warnf("%s", fileWarning)
		}
		for _, account := range fileAccounts {
			collector.add(account.Label, account.CodexHome, 100, true, false)
		}
	}

	if options.Discover {
		autoAccounts, autoWarning, autoErr := discoverMonitorAccountsFromFilesystem()
		if autoErr != nil {
			collector.warnf("auto discovery error: %v", autoErr)
		} else {
			if autoWarning != "" {
				collector.warnf("%s", autoWarning)
			}
			for _, account := range autoAccounts {
				if isMulticodexProfileHome(account.CodexHome) {
					continue
				}
				collector.add(account.Label, account.CodexHome, 30, false, false)
			}
		}
	}

	out := collector.toAccounts()
	return out, collector.warningString(), nil
}

func loadAccountsFromMulticodexConfig() ([]MonitorAccount, string, error) {
	configPath, err := multicodexConfigPath()
	if err != nil {
		return nil, "", fmt.Errorf("resolve multicodex config: %w", err)
	}
	multicodexHome := filepath.Dir(configPath)
	if info, err := os.Lstat(multicodexHome); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, "skipping multicodex profiles: multicodex home is a symlink", nil
		}
		if info.IsDir() && info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Sprintf("skipping multicodex profiles: multicodex home permissions are %o, expected no group/world permissions", info.Mode().Perm()), nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("inspect multicodex home %s: %w", multicodexHome, err)
	}

	if err := monitorRegularSingleFile(configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read multicodex config %s: %w", configPath, err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read multicodex config %s: %w", configPath, err)
	}

	var raw multicodexConfigFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, "", fmt.Errorf("decode multicodex config %s: %w", configPath, err)
	}
	if len(raw.Profiles) == 0 {
		return nil, "", nil
	}

	names := make([]string, 0, len(raw.Profiles))
	for name := range raw.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]MonitorAccount, 0, len(names))
	warnings := make([]string, 0)
	profilesDir := filepath.Join(multicodexHome, "profiles")
	for _, name := range names {
		profile := raw.Profiles[name]
		if !monitorProfileNameValid(name) {
			warnings = append(warnings, fmt.Sprintf("skipping multicodex profile %q: invalid profile name", name))
			continue
		}
		if strings.TrimSpace(profile.Name) != name {
			warnings = append(warnings, fmt.Sprintf("skipping multicodex profile %q: stored name mismatch", name))
			continue
		}
		label := strings.TrimSpace(profile.Name)
		if label == "" {
			label = name
		}
		home, err := expandPath(strings.TrimSpace(profile.CodexHome))
		if err != nil {
			return nil, "", fmt.Errorf("resolve codex_home for multicodex profile %q: %w", label, err)
		}
		if strings.TrimSpace(home) == "" {
			continue
		}
		home = filepath.Clean(home)
		if err := monitorProfileHomeSafe(profilesDir, name, home); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping multicodex profile %q: %v", label, err))
			continue
		}
		out = append(out, MonitorAccount{
			Label:     safeLabel(label),
			CodexHome: home,
		})
	}

	return out, strings.Join(warnings, "; "), nil
}

func monitorProfileHomeSafe(profilesDir, name, home string) error {
	expected := filepath.Join(profilesDir, name, "codex-home")
	if filepath.Clean(home) != home {
		return fmt.Errorf("codex_home is not clean")
	}
	if rel, err := filepath.Rel(profilesDir, home); err != nil || rel != filepath.Join(name, "codex-home") {
		return fmt.Errorf("codex_home is not profile-local")
	}
	authPath := filepath.Join(home, "auth.json")
	for _, path := range []string{profilesDir, filepath.Join(profilesDir, name), expected, home, authPath} {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && strings.HasSuffix(path, "auth.json") {
				continue
			}
			return fmt.Errorf("inspect %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", path)
		}
		if path != authPath && info.IsDir() && info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%s permissions are %o, expected no group/world permissions", path, info.Mode().Perm())
		}
		if path == authPath && !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		if path == authPath && monitorFileHasMultipleLinks(info) {
			return fmt.Errorf("%s has multiple hard links", path)
		}
		if path == authPath && info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%s permissions are %o, expected 600", path, info.Mode().Perm())
		}
	}
	ok, err := monitorProfileConfigUsesFileStore(filepath.Join(home, "config.toml"))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("config does not enable file-backed auth")
	}
	return nil
}

func monitorProfileNameValid(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}

func monitorFileHasMultipleLinks(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}

func monitorConfigUsesFileStore(path string) (bool, error) {
	if err := monitorRegularSingleFile(path); err != nil {
		return false, err
	}
	return monitorConfigFileUsesFileStore(path)
}

func monitorProfileConfigUsesFileStore(path string) (bool, error) {
	defaultHome, err := defaultCodexHome()
	if err != nil {
		return false, err
	}
	defaultConfigPath := filepath.Join(defaultHome, "config.toml")
	if err := monitorProfileConfigPathSafe(path, defaultConfigPath); err != nil {
		return false, err
	}
	return monitorConfigFileUsesFileStore(path)
}

func monitorProfileConfigPathSafe(path, defaultConfigPath string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		return nil
	}

	targetPath, err := monitorResolveExistingPath(path)
	if err != nil {
		return fmt.Errorf("resolve profile config symlink: %w", err)
	}
	defaultTargetPath, err := monitorResolveExistingPath(defaultConfigPath)
	if err != nil {
		return fmt.Errorf("resolve default Codex config: %w", err)
	}
	if targetPath != defaultTargetPath {
		return fmt.Errorf("profile config symlink must point to default Codex config %s", defaultConfigPath)
	}
	targetInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("profile config symlink target is not readable: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return fmt.Errorf("profile config symlink target is not a regular file: %s", path)
	}
	return nil
}

func monitorConfigFileUsesFileStore(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read profile config: %w", err)
	}
	multilineDelimiter := ""
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(monitorStripTOMLComment(rawLine))
		if line == "" {
			continue
		}
		if multilineDelimiter != "" {
			if strings.Contains(line, multilineDelimiter) {
				multilineDelimiter = ""
			}
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			return false, nil
		}
		assignIdx := monitorIndexTOMLUnquotedByte(line, '=')
		if assignIdx == -1 || monitorTOMLKey(strings.TrimSpace(line[:assignIdx])) != "cli_auth_credentials_store" {
			if assignIdx != -1 {
				value := strings.TrimSpace(line[assignIdx+1:])
				if strings.HasPrefix(value, `"""`) && strings.Count(value, `"""`)%2 == 1 {
					multilineDelimiter = `"""`
				} else if strings.HasPrefix(value, `'''`) && strings.Count(value, `'''`)%2 == 1 {
					multilineDelimiter = `'''`
				}
			}
			continue
		}
		value, ok := monitorTOMLStringValue(strings.TrimSpace(line[assignIdx+1:]))
		if !ok {
			return false, nil
		}
		return value == "file", nil
	}
	return false, nil
}

func monitorTOMLKey(raw string) string {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		unquoted, err := strconv.Unquote(raw)
		if err == nil {
			return unquoted
		}
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1]
	}
	return raw
}

func monitorTOMLStringValue(raw string) (string, bool) {
	if len(raw) < 2 {
		return "", false
	}
	if raw[0] == '"' && raw[len(raw)-1] == '"' {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return "", false
		}
		return unquoted, true
	}
	if raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1], true
	}
	return "", false
}

func monitorIndexTOMLUnquotedByte(s string, needle byte) int {
	inDouble := false
	inSingle := false
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case escaped:
			escaped = false
		case inDouble:
			if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inDouble = false
			}
		case inSingle:
			if ch == '\'' {
				inSingle = false
			}
		default:
			switch ch {
			case '"':
				inDouble = true
			case '\'':
				inSingle = true
			case needle:
				return i
			}
		}
	}
	return -1
}

func monitorResolveExistingPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func isMulticodexProfileHome(home string) bool {
	configPath, err := multicodexConfigPath()
	if err != nil {
		return false
	}
	profilesDir := filepath.Join(filepath.Dir(configPath), "profiles")
	rel, err := filepath.Rel(profilesDir, filepath.Clean(home))
	if err != nil || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return false
	}
	return strings.HasSuffix(rel, string(os.PathSeparator)+"codex-home")
}

func monitorStripTOMLComment(line string) string {
	inDouble := false
	inSingle := false
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case escaped:
			escaped = false
		case inDouble:
			if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inDouble = false
			}
		case inSingle:
			if ch == '\'' {
				inSingle = false
			}
		default:
			switch ch {
			case '"':
				inDouble = true
			case '\'':
				inSingle = true
			case '#':
				return line[:i]
			}
		}
	}
	return line
}

func loadAccountsFromFile() ([]MonitorAccount, string, error) {
	accountsPath, err := resolveAccountsFilePath()
	if err != nil {
		return nil, "", fmt.Errorf("resolve accounts file: %w", err)
	}

	if err := monitorRegularSingleFile(accountsPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read accounts file %s: %w", accountsPath, err)
	}
	data, err := os.ReadFile(accountsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read accounts file %s: %w", accountsPath, err)
	}

	var raw accountFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, "", fmt.Errorf("decode accounts file %s: %w", accountsPath, err)
	}
	if len(raw.Accounts) == 0 {
		return nil, fmt.Sprintf("accounts file %s is empty", accountsPath), nil
	}

	out := make([]MonitorAccount, 0, len(raw.Accounts))
	for i, a := range raw.Accounts {
		label := strings.TrimSpace(a.Label)
		if label == "" {
			label = fmt.Sprintf("account-%d", i+1)
		}
		home, err := expandPath(strings.TrimSpace(a.CodexHome))
		if err != nil {
			return nil, "", fmt.Errorf("resolve codex_home for account %q: %w", label, err)
		}
		if strings.TrimSpace(home) == "" {
			return nil, "", fmt.Errorf("account %q has empty codex_home", label)
		}
		out = append(out, MonitorAccount{
			Label:     label,
			CodexHome: filepath.Clean(home),
		})
	}
	return out, "", nil
}

func monitorRegularSingleFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if monitorFileHasMultipleLinks(info) {
		return fmt.Errorf("%s has multiple hard links", path)
	}
	return nil
}

func discoverMonitorAccountsFromFilesystem() ([]MonitorAccount, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("resolve home directory: %w", err)
	}
	paths, warnings, err := discoverCodexHomesFromSystem(home)
	if err != nil {
		return nil, "", err
	}

	out := make([]MonitorAccount, 0, len(paths))
	for _, path := range paths {
		if shouldIgnoreDiscoveredHome(path) {
			continue
		}
		if !hasUsageSignals(path) {
			continue
		}
		out = append(out, MonitorAccount{
			Label:     labelForDiscoveredHome(path),
			CodexHome: filepath.Clean(path),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, strings.Join(dedupeStrings(warnings), "; "), nil
}

func discoverCodexHomesFromSystem(home string) ([]string, []string, error) {
	candidates := map[string]struct{}{}
	var warnings []string

	const maxDiscoveryDepth = 5
	cleanHome := filepath.Clean(home)
	err := filepath.WalkDir(cleanHome, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("skipping discovery path %q: %v", path, walkErr))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != cleanHome {
			if d.Type()&os.ModeSymlink != 0 || shouldIgnoreDiscoveredHome(path) {
				return filepath.SkipDir
			}
		}

		depth := discoveryDepth(cleanHome, path)
		if path != cleanHome && shouldPruneDiscoveryDir(path, depth) {
			return filepath.SkipDir
		}
		name := d.Name()
		if (depth == 1 && strings.HasPrefix(name, ".codex")) || (depth >= 1 && depth <= maxDiscoveryDepth && (name == ".codex" || name == "codex-home")) {
			if dirExists(path) {
				candidates[filepath.Clean(path)] = struct{}{}
			}
		}
		if depth >= maxDiscoveryDepth {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, warnings, err
	}

	out := make([]string, 0, len(candidates))
	for candidate := range candidates {
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out, warnings, nil
}

func shouldPruneDiscoveryDir(path string, depth int) bool {
	if depth <= 0 {
		return false
	}
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case ".cache", ".git", "caches", "library", "node_modules":
		return true
	default:
		return false
	}
}

func discoveryDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}

func labelForDiscoveredHome(codexHome string) string {
	base := filepath.Base(codexHome)
	switch {
	case base == "codex-home":
		parent := filepath.Base(filepath.Dir(codexHome))
		if strings.TrimSpace(parent) != "" && parent != "." && parent != string(filepath.Separator) {
			return safeLabel(parent)
		}
	case strings.HasPrefix(base, ".codex"):
		if base == ".codex" {
			return "global"
		}
		return safeLabel(strings.TrimPrefix(base, "."))
	}
	return safeLabel(base)
}

func hasUsageSignals(codexHome string) bool {
	if fileExists(filepath.Join(codexHome, "auth.json")) {
		return true
	}
	if dirExists(filepath.Join(codexHome, "sessions")) {
		return true
	}
	if dirExists(filepath.Join(codexHome, "archived_sessions")) {
		return true
	}
	return false
}

func shouldIgnoreDiscoveredHome(codexHome string) bool {
	normalized := strings.ToLower(normalizeHome(codexHome))
	if normalized == "" {
		return false
	}

	separators := string(filepath.Separator)
	fragments := []string{
		separators + "loopy" + separators + "launches" + separators,
		separators + ".codex" + separators + "worktrees" + separators,
		separators + ".cache" + separators,
		separators + "library" + separators + "caches" + separators,
		separators + "archived-contexts" + separators,
	}
	for _, fragment := range fragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

type accountCollector struct {
	byHome   map[string]accountCandidate
	warnings []string
}

type accountCandidate struct {
	account  MonitorAccount
	priority int
}

func newAccountCollector() *accountCollector {
	return &accountCollector{
		byHome: map[string]accountCandidate{},
	}
}

func (c *accountCollector) add(label, codexHome string, priority int, allowWithoutSignals bool, useAppServer bool) {
	normalized := normalizeHome(codexHome)
	if normalized == "" {
		return
	}
	if !allowWithoutSignals && !hasUsageSignals(normalized) {
		return
	}
	if existing, ok := c.byHome[normalized]; ok {
		if existing.priority >= priority {
			if useAppServer && !existing.account.UseAppServer {
				existing.account.UseAppServer = true
				c.byHome[normalized] = existing
			}
			return
		}
		useAppServer = useAppServer || existing.account.UseAppServer
	}
	c.byHome[normalized] = accountCandidate{
		account: MonitorAccount{
			Label:        safeLabel(label),
			CodexHome:    normalized,
			UseAppServer: useAppServer,
		},
		priority: priority,
	}
}

func (c *accountCollector) warnf(format string, args ...any) {
	msg := strings.TrimSpace(fmt.Sprintf(format, args...))
	if msg == "" {
		return
	}
	c.warnings = append(c.warnings, msg)
}

func (c *accountCollector) warningString() string {
	deduped := dedupeStrings(c.warnings)
	return strings.Join(deduped, "; ")
}

func (c *accountCollector) toAccounts() []MonitorAccount {
	out := make([]MonitorAccount, 0, len(c.byHome))
	for _, candidate := range c.byHome {
		out = append(out, candidate.account)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].CodexHome < out[j].CodexHome
	})
	return out
}

func safeLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "account"
	}
	return label
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func resolveAccountsFilePath() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv(accountsFileEnvVar)); explicit != "" {
		return expandPath(explicit)
	}
	dir, err := monitorDataDir()
	if err != nil {
		return "", err
	}
	defaultPath := filepath.Join(dir, defaultAccountsFileName)
	if fileExists(defaultPath) {
		return defaultPath, nil
	}
	return defaultPath, nil
}

func monitorDataDir() (string, error) {
	multicodexHome, err := multicodexHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(multicodexHome, defaultMonitorSubdirName), nil
}

func EnsureMonitorDataDir() error {
	dir, err := monitorDataDir()
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o700)
}

func defaultCodexHome() (string, error) {
	if codexHome := strings.TrimSpace(os.Getenv(defaultCodexHomeEnvVar)); codexHome != "" {
		return expandPath(codexHome)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func multicodexConfigPath() (string, error) {
	home, err := multicodexHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config.json"), nil
}

func multicodexHomeDir() (string, error) {
	if path := strings.TrimSpace(os.Getenv(multicodexHomeEnvVar)); path != "" {
		return expandPath(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, defaultMulticodexHomeDirName), nil
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Abs(home)
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Abs(filepath.Join(home, path[2:]))
	}
	return filepath.Abs(path)
}
