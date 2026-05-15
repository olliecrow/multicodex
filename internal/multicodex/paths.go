package multicodex

import (
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
	DefaultCodexHome string
}

func ResolvePaths() (Paths, error) {
	return resolvePaths()
}

func ResolvePathsReadOnly() (Paths, error) {
	return resolvePaths()
}

func resolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}

	defaultMulticodexHome := filepath.Join(home, "multicodex")
	defaultCodexHome, err := resolveConfiguredPath(os.Getenv("MULTICODEX_DEFAULT_CODEX_HOME"), home)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve MULTICODEX_DEFAULT_CODEX_HOME: %w", err)
	}
	if defaultCodexHome == "" {
		defaultCodexHome = filepath.Join(home, ".codex")
	}
	multicodexHome, err := resolveConfiguredPath(os.Getenv("MULTICODEX_HOME"), home)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve MULTICODEX_HOME: %w", err)
	}
	if multicodexHome == "" {
		multicodexHome = defaultMulticodexHome
	}

	return Paths{
		MulticodexHome:   multicodexHome,
		ConfigPath:       filepath.Join(multicodexHome, "config.json"),
		ProfilesDir:      filepath.Join(multicodexHome, "profiles"),
		DefaultCodexHome: defaultCodexHome,
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
