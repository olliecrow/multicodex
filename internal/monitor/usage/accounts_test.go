package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMonitorAccountsDefaultsWhenFileMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	accounts, warning, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
	if accounts[0].Label != "default" {
		t.Fatalf("expected default label, got %q", accounts[0].Label)
	}
	expectedHome := filepath.Join(tmp, ".codex")
	if accounts[0].CodexHome != expectedHome {
		t.Fatalf("expected default codex home %q, got %q", expectedHome, accounts[0].CodexHome)
	}
}

func TestLoadMonitorAccountsFromFileWithDedup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	accountsPath := filepath.Join(tmp, "accounts.json")
	t.Setenv(accountsFileEnvVar, accountsPath)

	content := `{
  "version": 1,
  "accounts": [
    {"label":"personal","codex_home":"~/codex/a"},
    {"label":"work","codex_home":"` + filepath.Join(tmp, "codex", "b") + `"},
    {"label":"dupe","codex_home":"` + filepath.Join(tmp, "codex", "b") + `"}
  ]
}`
	if err := os.WriteFile(accountsPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}

	accounts, warning, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts after dedup, got %d", len(accounts))
	}
	if accounts[0].Label != "personal" {
		t.Fatalf("expected first label personal, got %q", accounts[0].Label)
	}
	if !strings.HasSuffix(accounts[0].CodexHome, filepath.Join("codex", "a")) {
		t.Fatalf("expected expanded home path, got %q", accounts[0].CodexHome)
	}
	if accounts[1].Label != "work" {
		t.Fatalf("expected second label work, got %q", accounts[1].Label)
	}
}

func TestLoadMonitorAccountsWarnsOnEmptyAccounts(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	accountsPath := filepath.Join(tmp, "accounts.json")
	t.Setenv(accountsFileEnvVar, accountsPath)
	if err := os.WriteFile(accountsPath, []byte(`{"version":1,"accounts":[]}`), 0o600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}

	accounts, warning, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Label != "default" {
		t.Fatalf("expected fallback default account")
	}
	if warning == "" {
		t.Fatalf("expected warning for empty accounts list")
	}
}

func TestLoadMonitorAccountsAutoDiscoversSystemCodexHomes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	discoveredHome := filepath.Join(tmp, "profiles", "work", "codex-home")
	if err := os.MkdirAll(discoveredHome, 0o755); err != nil {
		t.Fatalf("mkdir discovered home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(discoveredHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	accounts, _, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	expectedHome := normalizeHome(discoveredHome)
	for _, account := range accounts {
		if account.CodexHome == expectedHome {
			found = true
			if account.Label != "work" {
				t.Fatalf("expected discovered label work, got %q", account.Label)
			}
		}
	}
	if !found {
		t.Fatalf("expected discovered codex home to be included")
	}
}

func TestLoadMonitorAccountsSkipsTransientAutoDiscoveredHomes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	stableHome := filepath.Join(tmp, "profiles", "work", "codex-home")
	if err := os.MkdirAll(stableHome, 0o755); err != nil {
		t.Fatalf("mkdir stable home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stableHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write stable auth file: %v", err)
	}

	transientHome := filepath.Join(tmp, "loopy", "launches", "20260317T071323Z-ba3a94ce", "codex-home")
	if err := os.MkdirAll(transientHome, 0o755); err != nil {
		t.Fatalf("mkdir transient home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(transientHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write transient auth file: %v", err)
	}

	accounts, _, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stableFound := false
	transientFound := false
	for _, account := range accounts {
		switch account.CodexHome {
		case normalizeHome(stableHome):
			stableFound = true
		case normalizeHome(transientHome):
			transientFound = true
		}
	}
	if !stableFound {
		t.Fatalf("expected stable discovered home to be included")
	}
	if transientFound {
		t.Fatalf("expected transient loopy launch home to be excluded")
	}
}

func TestAccountCollectorDeduplicatesSymlinkAndRealHomes(t *testing.T) {
	tmp := t.TempDir()
	realHome := filepath.Join(tmp, "profiles", "work", "codex-home")
	if err := os.MkdirAll(realHome, 0o755); err != nil {
		t.Fatalf("mkdir real home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
	symlinkHome := filepath.Join(tmp, "symlink-home")
	if err := os.Symlink(realHome, symlinkHome); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	collector := newAccountCollector()
	collector.add("real", realHome, 50, false)
	collector.add("link", symlinkHome, 60, false)

	accounts := collector.toAccounts()
	if len(accounts) != 1 {
		t.Fatalf("expected one deduplicated account, got %d", len(accounts))
	}
	if accounts[0].Label != "link" {
		t.Fatalf("expected higher-priority symlink label to win, got %q", accounts[0].Label)
	}
}

func TestResolveAccountsFilePathUsesLegacyWhenDefaultMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, "")

	legacyDir := filepath.Join(tmp, legacyMonitorDirName)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	legacyFile := filepath.Join(legacyDir, defaultAccountsFileName)
	if err := os.WriteFile(legacyFile, []byte(`{"version":1,"accounts":[]}`), 0o600); err != nil {
		t.Fatalf("write legacy accounts file: %v", err)
	}

	path, err := resolveAccountsFilePath()
	if err != nil {
		t.Fatalf("resolve accounts file path: %v", err)
	}
	if path != legacyFile {
		t.Fatalf("expected legacy path %q, got %q", legacyFile, path)
	}
}

func TestResolveAccountsFilePathUsesHiddenLegacyWhenVisibleLegacyMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, "")

	legacyDir := filepath.Join(tmp, legacyHiddenMonitorDirName)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir hidden legacy dir: %v", err)
	}
	legacyFile := filepath.Join(legacyDir, defaultAccountsFileName)
	if err := os.WriteFile(legacyFile, []byte(`{"version":1,"accounts":[]}`), 0o600); err != nil {
		t.Fatalf("write hidden legacy accounts file: %v", err)
	}

	path, err := resolveAccountsFilePath()
	if err != nil {
		t.Fatalf("resolve accounts file path: %v", err)
	}
	if path != legacyFile {
		t.Fatalf("expected hidden legacy path %q, got %q", legacyFile, path)
	}
}

func TestResolveAccountsFilePathPrefersDefaultDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, "")

	defaultDir := filepath.Join(tmp, defaultMulticodexHomeDirName, defaultMonitorSubdirName)
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		t.Fatalf("mkdir default dir: %v", err)
	}
	defaultFile := filepath.Join(defaultDir, defaultAccountsFileName)
	if err := os.WriteFile(defaultFile, []byte(`{"version":1,"accounts":[]}`), 0o600); err != nil {
		t.Fatalf("write default accounts file: %v", err)
	}

	legacyDir := filepath.Join(tmp, legacyMonitorDirName)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	legacyFile := filepath.Join(legacyDir, defaultAccountsFileName)
	if err := os.WriteFile(legacyFile, []byte(`{"version":1,"accounts":[]}`), 0o600); err != nil {
		t.Fatalf("write legacy accounts file: %v", err)
	}

	path, err := resolveAccountsFilePath()
	if err != nil {
		t.Fatalf("resolve accounts file path: %v", err)
	}
	if path != defaultFile {
		t.Fatalf("expected default path %q, got %q", defaultFile, path)
	}
}

func TestLoadMonitorAccountsPrefersMulticodexProfiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	configDir := filepath.Join(tmp, defaultMulticodexHomeDirName)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir multicodex dir: %v", err)
	}
	profileHome := filepath.Join(configDir, "profiles", "personal", "codex-home")
	if err := os.MkdirAll(profileHome, 0o755); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
	configPath := filepath.Join(configDir, "config.json")
	configBody := `{"version":1,"profiles":{"personal":{"name":"personal","codex_home":"` + profileHome + `"}}}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	accounts, warning, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	found := false
	for _, account := range accounts {
		if account.Label == "personal" && account.CodexHome == normalizeHome(profileHome) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected multicodex profile account to be included, got %#v", accounts)
	}
}
