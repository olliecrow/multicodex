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
	Global   GlobalState        `json:"global"`
}

// Profile maps a user-friendly name to an isolated Codex home path.
type Profile struct {
	Name      string `json:"name"`
	CodexHome string `json:"codex_home"`
}

// GlobalState tracks minimal backup metadata for safe global auth switching.
type GlobalState struct {
	CurrentProfile    string `json:"current_profile,omitempty"`
	BackupMode        string `json:"backup_mode,omitempty"`
	BackupFilePath    string `json:"backup_file_path,omitempty"`
	BackupLinkTarget  string `json:"backup_link_target,omitempty"`
	BackupInitialized bool   `json:"backup_initialized,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		Version:  configVersion,
		Profiles: map[string]Profile{},
		Global:   GlobalState{},
	}
}

// Store persists config and manages profile/global-switch filesystem state.
type Store struct {
	paths Paths
}

func NewStore(paths Paths) *Store {
	return &Store{paths: paths}
}

func (s *Store) EnsureBaseDirs() error {
	if err := os.MkdirAll(s.paths.MulticodexHome, 0o700); err != nil {
		return fmt.Errorf("create multicodex home: %w", err)
	}
	if err := os.MkdirAll(s.paths.ProfilesDir, 0o700); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	if err := os.MkdirAll(s.paths.BackupsDir, 0o700); err != nil {
		return fmt.Errorf("create backups dir: %w", err)
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

	tmpPath := s.paths.ConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
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
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return Profile{}, fmt.Errorf("create profile dir: %w", err)
	}

	if err := s.ensureProfileConfig(codexHome); err != nil {
		return Profile{}, err
	}
	if err := s.ensureProfileSkills(codexHome); err != nil {
		return Profile{}, err
	}

	return Profile{Name: name, CodexHome: codexHome}, nil
}

func (s *Store) EnsureProfileDir(profile Profile) error {
	if profile.CodexHome == "" {
		return errors.New("profile codex home is empty")
	}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		return fmt.Errorf("create profile codex home: %w", err)
	}
	if err := s.ensureProfileConfig(profile.CodexHome); err != nil {
		return err
	}
	return s.ensureProfileSkills(profile.CodexHome)
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
