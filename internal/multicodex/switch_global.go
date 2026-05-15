package multicodex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const globalAuthLockSuffix = ".lock"

func (s *Store) WithGlobalAuthLock(fn func() error) error {
	if err := os.MkdirAll(s.paths.DefaultCodexHome, 0o700); err != nil {
		return fmt.Errorf("create default codex home for global auth lock: %w", err)
	}
	lockPath := s.paths.DefaultAuthPath + globalAuthLockSuffix
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open global auth lock: %w", err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire global auth lock: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	return fn()
}

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

	if err := replaceAuthWithSymlink(s.paths.DefaultAuthPath, profileAuthPath); err != nil {
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
		if err := replaceAuthWithFile(s.paths.DefaultAuthPath, b, 0o600); err != nil {
			return false, fmt.Errorf("restore default auth file: %w", err)
		}
	case "symlink":
		if err := replaceAuthWithSymlink(s.paths.DefaultAuthPath, cfg.Global.BackupLinkTarget); err != nil {
			return false, fmt.Errorf("restore default auth symlink: %w", err)
		}
	default:
		return false, fmt.Errorf("unknown backup mode: %s", cfg.Global.BackupMode)
	}

	cfg.Global.CurrentProfile = ""
	return true, nil
}

func (s *Store) ensureGlobalBackup(cfg *Config) error {
	snapshot, err := s.captureDefaultAuthSnapshot()
	if err != nil {
		return err
	}
	if !cfg.Global.BackupInitialized {
		return s.storeBackupSnapshot(cfg, snapshot)
	}
	if s.isManagedDefaultAuthSnapshot(snapshot) {
		return nil
	}
	matches, err := s.backupSnapshotMatches(cfg, snapshot)
	if err != nil {
		return err
	}
	if matches {
		return nil
	}
	return s.storeBackupSnapshot(cfg, snapshot)
}

func (s *Store) captureDefaultAuthSnapshot() (authPathSnapshot, error) {
	info, err := os.Lstat(s.paths.DefaultAuthPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return authPathSnapshot{Mode: "missing"}, nil
		}
		return authPathSnapshot{}, fmt.Errorf("inspect default auth: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(s.paths.DefaultAuthPath)
		if err != nil {
			return authPathSnapshot{}, fmt.Errorf("read default auth symlink: %w", err)
		}
		return authPathSnapshot{Mode: "symlink", LinkTarget: target}, nil
	}
	if !info.Mode().IsRegular() {
		return authPathSnapshot{}, fmt.Errorf("default auth exists but is not regular file or symlink")
	}
	b, err := os.ReadFile(s.paths.DefaultAuthPath)
	if err != nil {
		return authPathSnapshot{}, fmt.Errorf("read default auth file: %w", err)
	}
	return authPathSnapshot{Mode: "file", FileBytes: b}, nil
}

func (s *Store) storeBackupSnapshot(cfg *Config, snapshot authPathSnapshot) error {
	cfg.Global.BackupMode = snapshot.Mode
	cfg.Global.BackupFilePath = ""
	cfg.Global.BackupLinkTarget = ""
	cfg.Global.BackupInitialized = true

	switch snapshot.Mode {
	case "missing":
		return nil
	case "symlink":
		cfg.Global.BackupLinkTarget = snapshot.LinkTarget
		return nil
	case "file":
		backupPath := filepath.Join(s.paths.BackupsDir, "default-auth.backup")
		if err := os.MkdirAll(s.paths.BackupsDir, 0o700); err != nil {
			return fmt.Errorf("create backups dir: %w", err)
		}
		if err := os.WriteFile(backupPath, snapshot.FileBytes, 0o600); err != nil {
			return fmt.Errorf("write auth backup file: %w", err)
		}
		cfg.Global.BackupFilePath = backupPath
		return nil
	default:
		return fmt.Errorf("unknown auth snapshot mode: %s", snapshot.Mode)
	}
}

func (s *Store) backupSnapshotMatches(cfg *Config, snapshot authPathSnapshot) (bool, error) {
	if !cfg.Global.BackupInitialized {
		return false, nil
	}
	if cfg.Global.BackupMode != snapshot.Mode {
		return false, nil
	}

	switch snapshot.Mode {
	case "missing":
		return true, nil
	case "symlink":
		return normalizeAuthLinkTarget(s.paths.DefaultAuthPath, cfg.Global.BackupLinkTarget) == normalizeAuthLinkTarget(s.paths.DefaultAuthPath, snapshot.LinkTarget), nil
	case "file":
		if cfg.Global.BackupFilePath == "" {
			return false, nil
		}
		b, err := os.ReadFile(cfg.Global.BackupFilePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("read auth backup file: %w", err)
		}
		return bytes.Equal(b, snapshot.FileBytes), nil
	default:
		return false, fmt.Errorf("unknown backup mode: %s", snapshot.Mode)
	}
}

func (s *Store) isManagedDefaultAuthSnapshot(snapshot authPathSnapshot) bool {
	if snapshot.Mode != "symlink" {
		return false
	}
	target := normalizeAuthLinkTarget(s.paths.DefaultAuthPath, snapshot.LinkTarget)
	if target == "" {
		return false
	}
	return isSubpath(s.paths.ProfilesDir, target) && filepath.Base(target) == "auth.json"
}

func normalizeAuthLinkTarget(linkPath, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	target = filepath.Clean(target)
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil && strings.TrimSpace(resolved) != "" {
		target = resolved
	}
	return filepath.Clean(target)
}

type authPathSnapshot struct {
	Mode       string
	FileBytes  []byte
	LinkTarget string
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

func replaceAuthWithSymlink(path, target string) error {
	if err := ensureAuthPathReplaceable(path); err != nil {
		return err
	}
	tmpPath, err := temporaryAuthPath(path)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func replaceAuthWithFile(path string, data []byte, perm os.FileMode) error {
	if err := ensureAuthPathReplaceable(path); err != nil {
		return err
	}
	tmpPath, err := temporaryAuthPath(path)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	if err := os.WriteFile(tmpPath, data, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func ensureAuthPathReplaceable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("refusing to replace directory at auth path: %s", path)
	}
	return nil
}

func temporaryAuthPath(path string) (string, error) {
	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Remove(tmpPath); err != nil {
		return "", err
	}
	return tmpPath, nil
}
