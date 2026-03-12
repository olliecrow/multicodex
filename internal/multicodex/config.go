package multicodex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	return Profile{Name: name, CodexHome: codexHome}, nil
}

func (s *Store) EnsureProfileDir(profile Profile) error {
	if profile.CodexHome == "" {
		return errors.New("profile codex home is empty")
	}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		return fmt.Errorf("create profile codex home: %w", err)
	}
	return s.ensureProfileConfig(profile.CodexHome)
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
