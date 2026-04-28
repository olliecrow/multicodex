package multicodex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths centralizes filesystem locations used by multicodex.
type Paths struct {
	MulticodexHome   string
	ConfigPath       string
	ProfilesDir      string
	BackupsDir       string
	DefaultCodexHome string
	DefaultAuthPath  string
}

func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}

	defaultMulticodexHome := filepath.Join(home, "multicodex")
	legacyMulticodexHome := filepath.Join(home, ".multicodex")
	defaultCodexHome, err := resolveConfiguredPath(os.Getenv("MULTICODEX_DEFAULT_CODEX_HOME"), home)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve MULTICODEX_DEFAULT_CODEX_HOME: %w", err)
	}
	if defaultCodexHome == "" {
		defaultCodexHome = filepath.Join(home, ".codex")
	}
	defaultAuthPath := filepath.Join(defaultCodexHome, "auth.json")

	multicodexHome, err := resolveConfiguredPath(os.Getenv("MULTICODEX_HOME"), home)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve MULTICODEX_HOME: %w", err)
	}
	if multicodexHome == "" {
		multicodexHome = defaultMulticodexHome
		if err := migrateLegacyMulticodexHome(legacyMulticodexHome, multicodexHome); err != nil {
			return Paths{}, err
		}
		if err := rewriteMigratedConfigPaths(multicodexHome, legacyMulticodexHome); err != nil {
			return Paths{}, err
		}
		if err := rewriteMigratedDefaultAuthSymlink(defaultAuthPath, legacyMulticodexHome, multicodexHome); err != nil {
			return Paths{}, err
		}
	}

	return Paths{
		MulticodexHome:   multicodexHome,
		ConfigPath:       filepath.Join(multicodexHome, "config.json"),
		ProfilesDir:      filepath.Join(multicodexHome, "profiles"),
		BackupsDir:       filepath.Join(multicodexHome, "backups"),
		DefaultCodexHome: defaultCodexHome,
		DefaultAuthPath:  defaultAuthPath,
	}, nil
}

func resolveConfiguredPath(value, home string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, value[2:])
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func migrateLegacyMulticodexHome(legacyPath, newPath string) error {
	if filepath.Clean(legacyPath) == filepath.Clean(newPath) {
		return nil
	}

	legacyInfo, err := os.Stat(legacyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect legacy multicodex home: %w", err)
	}
	if !legacyInfo.IsDir() {
		return fmt.Errorf("legacy multicodex home is not a directory: %s", legacyPath)
	}

	if _, err := os.Stat(newPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect multicodex home: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
		return fmt.Errorf("prepare multicodex home parent: %w", err)
	}
	if err := os.Rename(legacyPath, newPath); err != nil {
		return fmt.Errorf("migrate multicodex home from %s to %s: %w", legacyPath, newPath, err)
	}
	return nil
}

func rewriteMigratedConfigPaths(newHome, legacyHome string) error {
	configPath := filepath.Join(newHome, "config.json")
	b, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read migrated config: %w", err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(b, cfg); err != nil {
		return fmt.Errorf("parse migrated config: %w", err)
	}

	changed := false
	for name, profile := range cfg.Profiles {
		rewritten := rewriteLegacyPathPrefix(profile.CodexHome, legacyHome, newHome)
		if rewritten == profile.CodexHome {
			continue
		}
		profile.CodexHome = rewritten
		cfg.Profiles[name] = profile
		changed = true
	}

	rewrittenBackupFile := rewriteLegacyPathPrefix(cfg.Global.BackupFilePath, legacyHome, newHome)
	if rewrittenBackupFile != cfg.Global.BackupFilePath {
		cfg.Global.BackupFilePath = rewrittenBackupFile
		changed = true
	}
	rewrittenBackupTarget := rewriteLegacyPathPrefix(cfg.Global.BackupLinkTarget, legacyHome, newHome)
	if rewrittenBackupTarget != cfg.Global.BackupLinkTarget {
		cfg.Global.BackupLinkTarget = rewrittenBackupTarget
		changed = true
	}

	if !changed {
		return nil
	}

	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode migrated config: %w", err)
	}
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write migrated config temp file: %w", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		return fmt.Errorf("replace migrated config: %w", err)
	}
	return nil
}

func rewriteMigratedDefaultAuthSymlink(defaultAuthPath, legacyHome, newHome string) error {
	info, err := os.Lstat(defaultAuthPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect default auth symlink after migration: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}

	target, err := os.Readlink(defaultAuthPath)
	if err != nil {
		return fmt.Errorf("read default auth symlink after migration: %w", err)
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(defaultAuthPath), resolved)
	}
	rewritten := rewriteLegacyPathPrefix(resolved, legacyHome, newHome)
	if rewritten == resolved {
		return nil
	}

	if err := os.Remove(defaultAuthPath); err != nil {
		return fmt.Errorf("replace default auth symlink after migration: %w", err)
	}
	if err := os.Symlink(rewritten, defaultAuthPath); err != nil {
		return fmt.Errorf("rewrite default auth symlink target after migration: %w", err)
	}
	return nil
}

func rewriteLegacyPathPrefix(p, legacyHome, newHome string) string {
	if p == "" {
		return p
	}
	legacy := filepath.Clean(legacyHome)
	current := filepath.Clean(p)
	rel, err := filepath.Rel(legacy, current)
	if err != nil {
		return p
	}
	if rel == "." {
		return filepath.Clean(newHome)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.Join(filepath.Clean(newHome), rel)
}
