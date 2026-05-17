package multicodex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const configVersion = 1
const generatedProfileConfigContent = "cli_auth_credentials_store = \"file\"\n"

// Config stores multicodex metadata only. It does not store secrets.
type Config struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
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
	if err := ensurePathNotSymlinkIfExists(s.paths.ProfilesDir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.paths.ProfilesDir, 0o700); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	return nil
}

func (s *Store) Load() (*Config, error) {
	b, err := os.ReadFile(s.paths.ConfigPath)
	if err != nil {
		return nil, err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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

func (s *Store) CreateProfile(name string) (Profile, error) {
	if err := ValidateProfileName(name); err != nil {
		return Profile{}, err
	}
	if err := s.EnsureBaseDirs(); err != nil {
		return Profile{}, err
	}
	profileDir := filepath.Join(s.paths.ProfilesDir, name)
	codexHome := filepath.Join(profileDir, "codex-home")
	profile := Profile{Name: name, CodexHome: codexHome}
	if err := s.ensureProfileStoragePathSafe(profile); err != nil {
		return Profile{}, err
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return Profile{}, fmt.Errorf("create profile dir: %w", err)
	}

	if err := s.ensureProfileConfig(codexHome); err != nil {
		return Profile{}, err
	}
	if err := s.ensureProfileSkills(codexHome); err != nil {
		return Profile{}, err
	}

	return profile, nil
}

func (s *Store) EnsureProfileDir(profile Profile) error {
	if profile.CodexHome == "" {
		return errors.New("profile codex home is empty")
	}
	if err := s.ensureProfileStoragePathSafe(profile); err != nil {
		return err
	}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		return fmt.Errorf("create profile codex home: %w", err)
	}
	if err := s.ensureProfileConfig(profile.CodexHome); err != nil {
		return err
	}
	return s.ensureProfileSkills(profile.CodexHome)
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
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripTOMLComment(rawLine))
		if line == "" {
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
		return strings.TrimSpace(unquoted), nil
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.TrimSpace(value[1 : len(value)-1]), nil
	}
	if fields := strings.Fields(value); len(fields) == 1 {
		return strings.TrimSpace(fields[0]), nil
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
			return nil
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
	if err := ensurePathPrefixesBelowRootNotSymlinks(codexHome, profileSkillsPath); err != nil {
		return err
	}

	entries, err := os.ReadDir(defaultSkillsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read default skills dir: %w", err)
	}
	if err := os.MkdirAll(profileSkillsPath, 0o700); err != nil {
		return fmt.Errorf("create profile skills dir: %w", err)
	}

	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == "." || name == ".." || name == ".system" {
			continue
		}

		defaultEntryPath := filepath.Join(defaultSkillsPath, name)
		profileEntryPath := filepath.Join(profileSkillsPath, name)

		info, err := os.Lstat(profileEntryPath)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				target, readErr := os.Readlink(profileEntryPath)
				if readErr != nil {
					return fmt.Errorf("read profile skill symlink %s: %w", profileEntryPath, readErr)
				}
				if target == defaultEntryPath {
					continue
				}
			}
			continue
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("inspect profile skill entry %s: %w", profileEntryPath, err)
		}

		if err := os.Symlink(defaultEntryPath, profileEntryPath); err != nil {
			return fmt.Errorf("link profile skill %s: %w", name, err)
		}
	}

	return nil
}
