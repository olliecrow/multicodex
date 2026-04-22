package multicodex

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const envCodexAppPath = "MULTICODEX_APP_PATH"

var desktopAppGOOS = runtime.GOOS

func (a *App) cmdApp(args []string) error {
	if len(args) != 1 {
		return &ExitError{Code: 2, Message: "usage: multicodex app <name>"}
	}
	if desktopAppGOOS != "darwin" {
		return &ExitError{Code: 2, Message: "multicodex app is supported on macOS only"}
	}

	name := strings.TrimSpace(args[0])
	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}
	profile, ok := cfg.Profiles[name]
	if !ok {
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	if err := a.store.EnsureProfileDir(profile); err != nil {
		return err
	}
	if err := ensureProfileCodexExecutionReady(a.store.paths, profile); err != nil {
		return err
	}
	if err := a.store.SwitchGlobalAuthToProfile(cfg, profile); err != nil {
		return err
	}
	if err := a.store.Save(cfg); err != nil {
		return err
	}

	appPath, err := findCodexAppPath()
	if err != nil {
		return err
	}
	appUserDataDir, err := profileAppUserDataDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(appUserDataDir, 0o700); err != nil {
		return fmt.Errorf("create app data dir: %w", err)
	}

	fmt.Printf("launching Codex app for profile %q with shared sidebar state and profile app data\n", name)
	return runCommandWithEnv(
		"open",
		[]string{"-n", "-a", appPath, "--args", "--user-data-dir=" + appUserDataDir},
		withProfileEnv(os.Environ(), a.store.paths.DefaultCodexHome, name),
		"codex app launch failed",
	)
}

func profileAppUserDataDir(profile string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "Codex-multicodex", profile), nil
}

func findCodexAppPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(envCodexAppPath)); override != "" {
		if isCodexAppBundle(override) {
			return override, nil
		}
		return "", &ExitError{Code: 2, Message: fmt.Sprintf("Codex.app not found at %s", override)}
	}

	for _, candidate := range candidateCodexAppPaths() {
		if isCodexAppBundle(candidate) {
			return candidate, nil
		}
	}

	return "", &ExitError{
		Code:    2,
		Message: "Codex.app not found. Install Codex in /Applications, /System/Volumes/Data/Applications, or ~/Applications, or set MULTICODEX_APP_PATH",
	}
}

func candidateCodexAppPaths() []string {
	paths := []string{
		"/Applications/Codex.app",
		"/System/Volumes/Data/Applications/Codex.app",
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		paths = append(paths, filepath.Join(home, "Applications", "Codex.app"))
	}
	return paths
}

func isCodexAppBundle(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
