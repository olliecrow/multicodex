package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func secureAuthFilePermissions(codexHome string) error {
	authPath := filepath.Join(codexHome, "auth.json")
	info, err := os.Lstat(authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect auth file permissions: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if info.IsDir() {
		return fmt.Errorf("auth path is a directory, expected file: %s", authPath)
	}
	if err := os.Chmod(authPath, 0o600); err != nil {
		return fmt.Errorf("set auth file permissions: %w", err)
	}
	return nil
}
