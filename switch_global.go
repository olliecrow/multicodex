package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) SwitchGlobalAuthToProfile(cfg *Config, profile Profile) error {
	profileAuthPath := filepath.Join(profile.CodexHome, "auth.json")
	if _, err := os.Stat(profileAuthPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &ExitError{Code: 2, Message: fmt.Sprintf("profile %q has no auth.json. run: multicodex login %s", profile.Name, profile.Name)}
		}
		return fmt.Errorf("check profile auth file: %w", err)
	}

	if err := os.MkdirAll(s.paths.DefaultCodexHome, 0o700); err != nil {
		return fmt.Errorf("create default codex dir: %w", err)
	}

	if err := s.ensureGlobalBackup(cfg); err != nil {
		return err
	}

	if err := removeAuthPath(s.paths.DefaultAuthPath); err != nil {
		return fmt.Errorf("remove existing default auth pointer: %w", err)
	}
	if err := os.Symlink(profileAuthPath, s.paths.DefaultAuthPath); err != nil {
		return fmt.Errorf("link default auth to profile: %w", err)
	}

	cfg.Global.CurrentProfile = profile.Name
	return nil
}

func (s *Store) RestoreGlobalAuth(cfg *Config) (bool, error) {
	if !cfg.Global.BackupInitialized {
		return false, nil
	}
	if err := os.MkdirAll(s.paths.DefaultCodexHome, 0o700); err != nil {
		return false, fmt.Errorf("create default codex dir: %w", err)
	}

	switch cfg.Global.BackupMode {
	case "missing":
		if err := removeAuthPath(s.paths.DefaultAuthPath); err != nil {
			return false, fmt.Errorf("remove default auth pointer: %w", err)
		}
	case "file":
		b, err := os.ReadFile(cfg.Global.BackupFilePath)
		if err != nil {
			return false, fmt.Errorf("read auth backup file: %w", err)
		}
		if err := removeAuthPath(s.paths.DefaultAuthPath); err != nil {
			return false, fmt.Errorf("remove default auth path before file restore: %w", err)
		}
		if err := os.WriteFile(s.paths.DefaultAuthPath, b, 0o600); err != nil {
			return false, fmt.Errorf("restore default auth file: %w", err)
		}
	case "symlink":
		if err := removeAuthPath(s.paths.DefaultAuthPath); err != nil {
			return false, fmt.Errorf("remove default auth pointer: %w", err)
		}
		if err := os.Symlink(cfg.Global.BackupLinkTarget, s.paths.DefaultAuthPath); err != nil {
			return false, fmt.Errorf("restore default auth symlink: %w", err)
		}
	default:
		return false, fmt.Errorf("unknown backup mode: %s", cfg.Global.BackupMode)
	}

	cfg.Global.CurrentProfile = ""
	return true, nil
}

func (s *Store) ensureGlobalBackup(cfg *Config) error {
	if cfg.Global.BackupInitialized {
		return nil
	}

	info, err := os.Lstat(s.paths.DefaultAuthPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.Global.BackupMode = "missing"
			cfg.Global.BackupInitialized = true
			return nil
		}
		return fmt.Errorf("inspect default auth: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(s.paths.DefaultAuthPath)
		if err != nil {
			return fmt.Errorf("read default auth symlink: %w", err)
		}
		cfg.Global.BackupMode = "symlink"
		cfg.Global.BackupLinkTarget = target
		cfg.Global.BackupInitialized = true
		return nil
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("default auth exists but is not regular file or symlink")
	}

	backupPath := filepath.Join(s.paths.BackupsDir, "default-auth.backup")
	b, err := os.ReadFile(s.paths.DefaultAuthPath)
	if err != nil {
		return fmt.Errorf("read default auth file: %w", err)
	}
	if err := os.WriteFile(backupPath, b, 0o600); err != nil {
		return fmt.Errorf("write auth backup file: %w", err)
	}

	cfg.Global.BackupMode = "file"
	cfg.Global.BackupFilePath = backupPath
	cfg.Global.BackupInitialized = true
	return nil
}

func IsFileStoreLikelyConfigured(defaultCodexHome string) bool {
	b, err := os.ReadFile(filepath.Join(defaultCodexHome, "config.toml"))
	if err != nil {
		return false
	}
	content := string(b)
	return strings.Contains(content, "cli_auth_credentials_store") && strings.Contains(content, "file")
}

func removeAuthPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("refusing to remove directory at auth path: %s", path)
	}
	return os.Remove(path)
}
