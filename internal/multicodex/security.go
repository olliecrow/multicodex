package multicodex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func secureAuthFilePermissions(codexHome string) error {
	authPath, hasAuth, err := ensureProfileAuthPathSafe(codexHome)
	if err != nil {
		return err
	}
	if !hasAuth {
		return nil
	}
	if err := os.Chmod(authPath, 0o600); err != nil {
		return fmt.Errorf("set auth file permissions: %w", err)
	}
	return nil
}

func ensureProfileAuthPathSafe(codexHome string) (string, bool, error) {
	homeInfo, err := os.Lstat(codexHome)
	if err != nil {
		return "", false, fmt.Errorf("inspect profile codex home: %w", err)
	}
	if homeInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("profile codex home is a symlink, expected profile-local directory: %s", codexHome)
	}
	if !homeInfo.IsDir() {
		return "", false, fmt.Errorf("profile codex home is not a directory: %s", codexHome)
	}

	authPath := filepath.Join(codexHome, "auth.json")
	info, err := os.Lstat(authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return authPath, false, nil
		}
		return "", false, fmt.Errorf("inspect auth file permissions: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("auth path is a symlink, expected profile-local file: %s", authPath)
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("auth path is a directory, expected file: %s", authPath)
	}
	return authPath, true, nil
}
