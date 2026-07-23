package multicodex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const configVersion = 1
const generatedProfileConfigContent = "cli_auth_credentials_store = \"file\"\n"

// Config stores multicodex metadata only. It does not store secrets.
type Config struct {
	Version          int                `json:"version"`
	ProfileResources *ProfileResources  `json:"profile_resources,omitempty"`
	Profiles         map[string]Profile `json:"profiles"`
}

// Profile maps a user-friendly name to an isolated Codex home path.
type Profile struct {
	Name      string `json:"name"`
	CodexHome string `json:"codex_home"`
}

func DefaultConfig() *Config {
	return &Config{
		Version:  configVersion,
		Profiles: map[string]Profile{},
	}
}

// Store persists config and manages profile filesystem state.
type Store struct {
	paths Paths
}

func NewStore(paths Paths) *Store {
	return &Store{paths: paths}
}

func (s *Store) EnsureBaseDirs() error {
	if err := ensurePathNotSymlinkIfExists(s.paths.MulticodexHome); err != nil {
		return err
	}
	if err := os.MkdirAll(s.paths.MulticodexHome, 0o700); err != nil {
		return fmt.Errorf("create multicodex home: %w", err)
	}
	if err := os.Chmod(s.paths.MulticodexHome, 0o700); err != nil {
		return fmt.Errorf("secure multicodex home permissions: %w", err)
	}
	if err := ensurePathNotSymlinkIfExists(s.paths.ProfilesDir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.paths.ProfilesDir, 0o700); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	if err := os.Chmod(s.paths.ProfilesDir, 0o700); err != nil {
		return fmt.Errorf("secure profiles dir permissions: %w", err)
	}
	return nil
}

func (s *Store) WithConfigLock(fn func() error) error {
	if err := s.EnsureBaseDirs(); err != nil {
		return err
	}
	lockPath := filepath.Join(s.paths.MulticodexHome, "config.lock")
	if err := ensureRegularSingleFileForWrite(lockPath, "multicodex config lock"); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open config lock: %w", err)
	}
	defer lockFile.Close()
	if err := lockFile.Chmod(0o600); err != nil {
		return fmt.Errorf("secure config lock permissions: %w", err)
	}
	info, err := lockFile.Stat()
	if err != nil {
		return fmt.Errorf("inspect config lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("multicodex config lock is not a regular file: %s", lockPath)
	}
	if fileHasMultipleLinks(info) {
		return fmt.Errorf("multicodex config lock has multiple hard links: %s", lockPath)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock config: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func (s *Store) Load() (*Config, error) {
	if err := ensureRegularSingleFile(s.paths.ConfigPath, "multicodex config"); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.paths.ConfigPath)
	if err != nil {
		return nil, err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Version != 0 && cfg.Version != configVersion {
		return nil, fmt.Errorf("unsupported config version %d; expected %d", cfg.Version, configVersion)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	for name := range cfg.Profiles {
		if err := ValidateProfileName(name); err != nil {
			return nil, fmt.Errorf("invalid stored profile name %q: %w", name, err)
		}
		profile := cfg.Profiles[name]
		if profile.Name != name {
			return nil, fmt.Errorf("stored profile %q has mismatched name %q", name, profile.Name)
		}
	}
	if cfg.Version == 0 {
		cfg.Version = configVersion
	}
	return cfg, nil
}

func (s *Store) Save(cfg *Config) error {
	if err := s.EnsureBaseDirs(); err != nil {
		return err
	}
	if err := ensureRegularSingleFileForWrite(s.paths.ConfigPath, "multicodex config"); err != nil {
		return err
	}
	cfg.Version = configVersion
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.paths.ConfigPath), filepath.Base(s.paths.ConfigPath)+".tmp.")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	tmpClosed := false
	defer func() {
		if !tmpClosed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		tmpClosed = true
		return fmt.Errorf("close temp config: %w", err)
	}
	tmpClosed = true
	if err := os.Rename(tmpPath, s.paths.ConfigPath); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func (s *Store) CreateProfile(name string, resources *ProfileResources) (Profile, []ResourceChange, error) {
	if err := ValidateProfileName(name); err != nil {
		return Profile{}, nil, err
	}
	resolved, err := s.ResolveProfileResources(resources)
	if err != nil {
		return Profile{}, nil, err
	}
	if err := s.EnsureBaseDirs(); err != nil {
		return Profile{}, nil, err
	}
	profileDir := filepath.Join(s.paths.ProfilesDir, name)
	codexHome := filepath.Join(profileDir, "codex-home")
	profile := Profile{Name: name, CodexHome: codexHome}
	if err := s.ensureProfileStoragePathSafe(profile); err != nil {
		return Profile{}, nil, err
	}
	if err := s.validateProfileResourceDestinations(codexHome, resources); err != nil {
		return Profile{}, nil, err
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return Profile{}, nil, fmt.Errorf("create profile dir: %w", err)
	}

	if err := s.ensureProfileConfig(codexHome); err != nil {
		return Profile{}, nil, err
	}
	changes, err := s.reconcileProfileResources(codexHome, resources, resolved)
	if err != nil {
		return Profile{}, nil, err
	}

	return profile, changes, nil
}

func (s *Store) EnsureProfileDir(profile Profile, resources *ProfileResources) ([]ResourceChange, error) {
	if profile.CodexHome == "" {
		return nil, errors.New("profile codex home is empty")
	}
	resolved, err := s.ResolveProfileResources(resources)
	if err != nil {
		return nil, err
	}
	if err := s.ensureProfileStoragePathSafe(profile); err != nil {
		return nil, err
	}
	if err := s.validateProfileResourceDestinations(profile.CodexHome, resources); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		return nil, fmt.Errorf("create profile codex home: %w", err)
	}
	if err := s.ensureProfileConfig(profile.CodexHome); err != nil {
		return nil, err
	}
	return s.reconcileProfileResources(profile.CodexHome, resources, resolved)
}

func (s *Store) ensureProfileStoragePathSafe(profile Profile) error {
	if err := ValidateProfileName(profile.Name); err != nil {
		return fmt.Errorf("invalid profile name %q: %w", profile.Name, err)
	}
	profileDir := filepath.Join(s.paths.ProfilesDir, profile.Name)
	expectedCodexHome := filepath.Join(profileDir, "codex-home")
	if filepath.Clean(profile.CodexHome) != profile.CodexHome {
		return fmt.Errorf("profile codex home %s does not match clean profile-local path %s", profile.CodexHome, filepath.Clean(profile.CodexHome))
	}
	if rel, err := filepath.Rel(s.paths.ProfilesDir, profile.CodexHome); err != nil || rel != filepath.Join(profile.Name, "codex-home") {
		return fmt.Errorf("profile codex home %s does not match profile-local path under %s", profile.CodexHome, s.paths.ProfilesDir)
	}
	if !sameProfilePath(profile.CodexHome, expectedCodexHome) {
		return fmt.Errorf("profile codex home %s does not match expected profile-local path %s", profile.CodexHome, expectedCodexHome)
	}
	for _, path := range uniquePaths(s.paths.MulticodexHome, s.paths.ProfilesDir, profileDir, expectedCodexHome, profile.CodexHome) {
		if err := ensurePathNotSymlinkIfExists(path); err != nil {
			return err
		}
		if err := ensureExistingDirPrivate(path); err != nil {
			return err
		}
	}
	for _, path := range uniquePaths(s.paths.ProfilesDir, profileDir, expectedCodexHome, profile.CodexHome) {
		if err := ensurePathPrefixesBelowRootNotSymlinks(s.paths.MulticodexHome, path); err != nil {
			return err
		}
	}
	return nil
}

func ensurePathPrefixesBelowRootNotSymlinks(root, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("inspect profile path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("profile path is a symlink, expected profile-local directory: %s", current)
		}
	}
	return nil
}

func ensurePathNotSymlinkIfExists(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect profile path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("profile path is a symlink, expected profile-local directory: %s", path)
	}
	return nil
}

func ensureExistingDirPrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("profile path permissions are %o, expected no group/world permissions: %s", info.Mode().Perm(), path)
	}
	return nil
}

func uniquePaths(paths ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		key := filepath.Clean(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, path)
	}
	return out
}

func sameProfilePath(a, b string) bool {
	return canonicalProfilePath(a) == canonicalProfilePath(b)
}

func canonicalProfilePath(p string) string {
	cleaned := filepath.Clean(p)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved)
	}

	prefix := cleaned
	suffix := []string{}
	for {
		info, err := os.Lstat(prefix)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				suffix = append([]string{filepath.Base(prefix)}, suffix...)
				next := filepath.Dir(prefix)
				if next == prefix {
					return filepath.Clean(cleaned)
				}
				prefix = next
				continue
			}
			return filepath.Clean(cleaned)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolvedPrefix, err := filepath.EvalSymlinks(prefix)
			if err != nil {
				return filepath.Clean(cleaned)
			}
			resolvedInfo, err := os.Stat(resolvedPrefix)
			if err != nil || !resolvedInfo.IsDir() {
				return filepath.Clean(cleaned)
			}
			if len(suffix) == 0 {
				return filepath.Clean(resolvedPrefix)
			}
			parts := append([]string{resolvedPrefix}, suffix...)
			return filepath.Clean(filepath.Join(parts...))
		}
		if !info.IsDir() {
			return filepath.Clean(cleaned)
		}
		break
	}
	resolvedPrefix, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		return filepath.Clean(cleaned)
	}
	if len(suffix) == 0 {
		return filepath.Clean(resolvedPrefix)
	}
	parts := append([]string{resolvedPrefix}, suffix...)
	return filepath.Clean(filepath.Join(parts...))
}

func HasAuthFile(codexHome string) (bool, error) {
	_, err := os.Stat(filepath.Join(codexHome, "auth.json"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("check auth file: %w", err)
}

func profileConfigUsesFileStore(configPath string) (bool, error) {
	store, found, err := profileConfigCredentialStore(configPath)
	if err != nil {
		return false, err
	}
	return found && store == "file", nil
}

func profileConfigCredentialStore(configPath string) (string, bool, error) {
	if err := ensureRegularFileOrSymlinkTarget(configPath, "profile config"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read profile config: %w", err)
	}
	store, found, err := parseCredentialStoreFromTOML(string(b))
	if err != nil {
		return "", false, fmt.Errorf("parse profile config: %w", err)
	}
	return store, found, nil
}

func parseCredentialStoreFromTOML(content string) (string, bool, error) {
	inRootTable := true
	multilineDelimiter := ""
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripTOMLComment(rawLine))
		if line == "" {
			continue
		}
		if multilineDelimiter != "" {
			if strings.Contains(line, multilineDelimiter) {
				multilineDelimiter = ""
			}
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			inRootTable = false
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inRootTable = false
			continue
		}
		if !inRootTable {
			continue
		}

		assignIdx := indexTOMLUnquotedByte(line, '=')
		if assignIdx == -1 {
			continue
		}

		key, err := parseTOMLKey(line[:assignIdx])
		if err != nil {
			return "", false, err
		}
		if key != "cli_auth_credentials_store" {
			value := strings.TrimSpace(line[assignIdx+1:])
			if strings.HasPrefix(value, `"""`) && strings.Count(value, `"""`)%2 == 1 {
				multilineDelimiter = `"""`
			} else if strings.HasPrefix(value, `'''`) && strings.Count(value, `'''`)%2 == 1 {
				multilineDelimiter = `'''`
			}
			continue
		}

		value, err := parseTOMLStringOrBareValue(line[assignIdx+1:])
		if err != nil {
			return "", false, err
		}
		return value, true, nil
	}
	return "", false, nil
}

func ensureRegularFileOrSymlinkTarget(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		targetInfo, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("%s symlink target is not readable: %w", label, err)
		}
		if !targetInfo.Mode().IsRegular() {
			return fmt.Errorf("%s symlink target is not a regular file: %s", label, path)
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: %s", label, path)
	}
	return nil
}

func ensureRegularSingleFile(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink: %s", label, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: %s", label, path)
	}
	if fileHasMultipleLinks(info) {
		return fmt.Errorf("%s has multiple hard links: %s", label, path)
	}
	return nil
}

func ensureRegularSingleFileForWrite(path, label string) error {
	if err := ensureRegularSingleFile(path, label); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

func parseTOMLKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", errors.New("empty config key")
	}
	if len(key) >= 2 && key[0] == '"' && key[len(key)-1] == '"' {
		unquoted, err := strconv.Unquote(key)
		if err != nil {
			return "", fmt.Errorf("invalid quoted config key %q: %w", key, err)
		}
		return unquoted, nil
	}
	if len(key) >= 2 && key[0] == '\'' && key[len(key)-1] == '\'' {
		return key[1 : len(key)-1], nil
	}
	return key, nil
}

func parseTOMLStringOrBareValue(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("cli_auth_credentials_store has empty value")
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid quoted cli_auth_credentials_store value %q: %w", value, err)
		}
		return unquoted, nil
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1], nil
	}
	return "", fmt.Errorf("invalid cli_auth_credentials_store value %q", value)
}

func stripTOMLComment(line string) string {
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

func indexTOMLUnquotedByte(s string, needle byte) int {
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

func (s *Store) ensureProfileConfig(codexHome string) error {
	configPath := filepath.Join(codexHome, "config.toml")
	defaultConfigPath := filepath.Join(s.paths.DefaultCodexHome, "config.toml")

	info, err := os.Lstat(configPath)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("profile config path is a directory: %s", configPath)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			targetPath, err := resolveExistingSymlinkTarget(configPath)
			if err != nil {
				return fmt.Errorf("resolve profile config symlink: %w", err)
			}
			defaultTargetPath, err := resolveExistingPath(defaultConfigPath)
			if err != nil {
				return fmt.Errorf("resolve default Codex config: %w", err)
			}
			if targetPath != defaultTargetPath {
				return fmt.Errorf("profile config symlink must point to default Codex config %s: %s", defaultConfigPath, configPath)
			}
			targetInfo, err := os.Stat(configPath)
			if err != nil {
				return fmt.Errorf("profile config symlink target is not readable: %w", err)
			}
			if !targetInfo.Mode().IsRegular() {
				return fmt.Errorf("profile config symlink target is not a regular file: %s", configPath)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("profile config path is not a regular file: %s", configPath)
		}
		content, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("read profile config: %w", err)
		}
		if string(content) != generatedProfileConfigContent {
			return nil
		}
		if err := os.Remove(configPath); err != nil {
			return fmt.Errorf("replace generated profile config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check profile config: %w", err)
	}

	if err := os.Symlink(defaultConfigPath, configPath); err != nil {
		return fmt.Errorf("link profile config to default codex config: %w", err)
	}
	return nil
}

func (s *Store) ensureProfileSkills(codexHome string) error {
	defaultSkillsPath := filepath.Join(s.paths.DefaultCodexHome, "skills")
	profileSkillsPath := filepath.Join(codexHome, "skills")

	if err := ensurePathNotSymlinkIfExists(profileSkillsPath); err != nil {
		return err
	}
	if info, err := os.Lstat(profileSkillsPath); err == nil && !info.IsDir() {
		return fmt.Errorf("profile skills path is not a directory: %s", profileSkillsPath)
	}
	if err := ensurePathPrefixesBelowRootNotSymlinks(codexHome, profileSkillsPath); err != nil {
		return err
	}

	if err := os.MkdirAll(profileSkillsPath, 0o700); err != nil {
		return fmt.Errorf("create profile skills dir: %w", err)
	}
	if err := os.Chmod(profileSkillsPath, 0o700); err != nil {
		return fmt.Errorf("secure profile skills dir permissions: %w", err)
	}

	entries, err := os.ReadDir(defaultSkillsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read default skills dir: %w", err)
	}
	if err := s.removeStaleManagedSkillLinks(defaultSkillsPath, profileSkillsPath); err != nil {
		return err
	}

	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if !isInheritableSkillName(name) {
			continue
		}

		defaultEntryPath := filepath.Join(defaultSkillsPath, name)
		profileEntryPath := filepath.Join(profileSkillsPath, name)

		info, err := os.Lstat(profileEntryPath)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				target, readErr := resolveExistingSymlinkTarget(profileEntryPath)
				if readErr != nil {
					if errors.Is(readErr, syscall.EINVAL) {
						if info, statErr := os.Lstat(profileEntryPath); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
							continue
						}
					}
					if !errors.Is(readErr, os.ErrNotExist) {
						return fmt.Errorf("resolve profile skill symlink %s: %w", profileEntryPath, readErr)
					}
					target, readErr = resolveBrokenManagedSymlinkTarget(profileEntryPath)
					if readErr != nil {
						return fmt.Errorf("resolve profile skill symlink %s: %w", profileEntryPath, readErr)
					}
					if pathIsInsideRoot(defaultSkillsPath, target) {
						if err := os.Remove(profileEntryPath); err != nil {
							return fmt.Errorf("replace stale profile skill symlink %s: %w", profileEntryPath, err)
						}
						break
					}
					return fmt.Errorf("profile skill symlink must point under default skills directory: %s", profileEntryPath)
				}
				if canonicalProfilePath(target) == canonicalProfilePath(defaultEntryPath) {
					continue
				}
				if pathIsInsideRoot(defaultSkillsPath, target) {
					if err := os.Remove(profileEntryPath); err != nil {
						return fmt.Errorf("replace stale profile skill symlink %s: %w", profileEntryPath, err)
					}
					break
				}
				return fmt.Errorf("profile skill symlink must point under default skills directory: %s", profileEntryPath)
			}
			continue
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("inspect profile skill entry %s: %w", profileEntryPath, err)
		}

		if err := linkProfileSkill(defaultEntryPath, profileEntryPath); err != nil {
			return fmt.Errorf("link profile skill %s: %w", name, err)
		}
	}

	return nil
}

func (s *Store) removeStaleManagedSkillLinks(defaultSkillsPath, profileSkillsPath string) error {
	entries, err := os.ReadDir(profileSkillsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read profile skills dir: %w", err)
	}
	for _, entry := range entries {
		profileEntryPath := filepath.Join(profileSkillsPath, entry.Name())
		info, err := os.Lstat(profileEntryPath)
		if err != nil {
			return fmt.Errorf("inspect profile skill entry %s: %w", profileEntryPath, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := resolveExistingSymlinkTarget(profileEntryPath)
		if err != nil {
			if errors.Is(err, syscall.EINVAL) {
				if info, statErr := os.Lstat(profileEntryPath); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
					continue
				}
			}
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("resolve profile skill symlink %s: %w", profileEntryPath, err)
			}
			target, err = resolveBrokenManagedSymlinkTarget(profileEntryPath)
			if err != nil {
				return fmt.Errorf("resolve profile skill symlink %s: %w", profileEntryPath, err)
			}
			if !pathIsInsideRoot(defaultSkillsPath, target) {
				return fmt.Errorf("profile skill symlink must point under default skills directory: %s", profileEntryPath)
			}
			if err := os.Remove(profileEntryPath); err != nil {
				return fmt.Errorf("remove stale profile skill symlink %s: %w", profileEntryPath, err)
			}
			continue
		}
		if !pathIsInsideRoot(defaultSkillsPath, target) {
			return fmt.Errorf("profile skill symlink must point under default skills directory: %s", profileEntryPath)
		}
		if !isInheritableSkillName(strings.TrimSpace(entry.Name())) {
			if err := os.Remove(profileEntryPath); err != nil {
				return fmt.Errorf("remove non-inheritable profile skill symlink %s: %w", profileEntryPath, err)
			}
		}
	}
	return nil
}

func linkProfileSkill(defaultEntryPath, profileEntryPath string) error {
	if err := os.Symlink(defaultEntryPath, profileEntryPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := os.Lstat(profileEntryPath)
			if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				target, readErr := resolveExistingSymlinkTarget(profileEntryPath)
				if readErr == nil && canonicalProfilePath(target) == canonicalProfilePath(defaultEntryPath) {
					return nil
				}
			}
		}
		return err
	}
	return nil
}

func resolveExistingSymlinkTarget(path string) (string, error) {
	return resolveExistingPath(path)
}

func resolveExistingPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func resolveBrokenManagedSymlinkTarget(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if containsParentPathSegment(target) {
		return "", fmt.Errorf("symlink target contains parent directory traversal: %s", target)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target), nil
}

func containsParentPathSegment(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func pathIsInsideRoot(root, path string) bool {
	root = canonicalProfilePath(root)
	path = canonicalProfilePath(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
