package multicodex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("auth path is not a regular file: %s", authPath)
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("auth path is a directory, expected file: %s", authPath)
	}
	if fileHasMultipleLinks(info) {
		return "", false, fmt.Errorf("auth path has multiple hard links, expected profile-local file: %s", authPath)
	}
	return authPath, true, nil
}

func fileHasMultipleLinks(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}

func resolvePathInsideRoot(root, rawPath, label string) (string, error) {
	root = strings.TrimSpace(root)
	rawPath = strings.TrimSpace(rawPath)
	if root == "" {
		return "", fmt.Errorf("%s root is empty", label)
	}
	if rawPath == "" {
		return "", fmt.Errorf("%s is empty", label)
	}
	if !filepath.IsAbs(rawPath) {
		rawPath = filepath.Join(root, rawPath)
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve %s root: %w", label, err)
	}
	pathAbs, err := filepath.Abs(filepath.Clean(rawPath))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s must stay under %s", label, rootAbs)
	}
	if rel == "." {
		return "", fmt.Errorf("%s must be a file under %s", label, rootAbs)
	}
	return pathAbs, nil
}
