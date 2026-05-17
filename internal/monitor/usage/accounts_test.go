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

func TestLoadMonitorAccountsUsesConfiguredDefaultCodexHome(t *testing.T) {
	tmp := t.TempDir()
	configuredHome := filepath.Join(tmp, "custom-default-codex")
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(defaultCodexHomeEnvVar, configuredHome)
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	accounts, warning, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if len(accounts) == 0 {
		t.Fatalf("expected default account")
	}
	if accounts[0].CodexHome != configuredHome {
		t.Fatalf("expected configured default codex home %q, got %q", configuredHome, accounts[0].CodexHome)
	}
}

func TestLoadMonitorAccountsUsesActiveCodexHomeAsDefault(t *testing.T) {
	tmp := t.TempDir()
	activeHome := filepath.Join(tmp, "active-codex")
	if err := os.MkdirAll(activeHome, 0o700); err != nil {
		t.Fatalf("mkdir active home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write active auth file: %v", err)
	}
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv(defaultCodexHomeEnvVar, "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	accounts, warning, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if len(accounts) == 0 {
		t.Fatalf("expected default account")
	}
	if accounts[0].Label != "default" {
		t.Fatalf("expected active CODEX_HOME to be the default account, got label %q", accounts[0].Label)
	}
	if accounts[0].CodexHome != normalizeHome(activeHome) {
		t.Fatalf("expected default codex home %q, got %q", normalizeHome(activeHome), accounts[0].CodexHome)
	}
}

func TestLoadMonitorAccountsPrefersConfiguredDefaultOverActiveCodexHome(t *testing.T) {
	tmp := t.TempDir()
	configuredHome := filepath.Join(tmp, "custom-default-codex")
	activeHome := filepath.Join(tmp, "stale-profile-codex")
	if err := os.MkdirAll(configuredHome, 0o700); err != nil {
		t.Fatalf("mkdir configured home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configuredHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write configured auth: %v", err)
	}
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv(defaultCodexHomeEnvVar, configuredHome)
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	accounts, _, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byLabel := map[string]string{}
	for _, account := range accounts {
		byLabel[account.Label] = account.CodexHome
	}
	expectedConfiguredHome := normalizeHome(configuredHome)
	if byLabel["default"] != expectedConfiguredHome {
		t.Fatalf("expected default account from configured home %q, got %q", expectedConfiguredHome, byLabel["default"])
	}
	expectedActiveHome := normalizeHome(activeHome)
	if byLabel["active"] != expectedActiveHome {
		t.Fatalf("expected active account from CODEX_HOME %q, got %q", expectedActiveHome, byLabel["active"])
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

func TestLoadMonitorAccountsDiscoveryDoesNotDescendIntoSymlinkedDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	outsideHome := filepath.Join(t.TempDir(), "outside", "codex-home")
	if err := os.MkdirAll(outsideHome, 0o755); err != nil {
		t.Fatalf("mkdir outside home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write outside auth file: %v", err)
	}
	if err := os.Symlink(filepath.Dir(outsideHome), filepath.Join(tmp, "linked-outside")); err != nil {
		t.Fatalf("symlink outside dir: %v", err)
	}

	accounts, _, err := loadMonitorAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, account := range accounts {
		if account.CodexHome == normalizeHome(outsideHome) {
			t.Fatalf("expected symlinked outside home not to be discovered: %#v", accounts)
		}
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
	if err := os.WriteFile(filepath.Join(profileHome, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0o600); err != nil {
		t.Fatalf("write profile config: %v", err)
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

func TestLoadMonitorAccountsSkipsUnsafeMulticodexProfile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))
	t.Setenv(accountsFileEnvVar, filepath.Join(tmp, "missing.json"))

	configDir := filepath.Join(tmp, defaultMulticodexHomeDirName)
	profileHome := filepath.Join(configDir, "profiles", "personal", "codex-home")
	if err := os.MkdirAll(profileHome, 0o755); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	target := filepath.Join(tmp, "shared-auth.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write auth target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(profileHome, "auth.json")); err != nil {
		t.Fatalf("symlink auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileHome, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0o600); err != nil {
		t.Fatalf("write profile config: %v", err)
	}
	configPath := filepath.Join(configDir, "config.json")
	configBody := `{"version":1,"profiles":{"personal":{"name":"personal","codex_home":"` + profileHome + `"}}}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	accounts, warning, err := loadAccountsFromMulticodexConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected unsafe multicodex profile to be skipped, got %#v", accounts)
	}
	if !strings.Contains(warning, "skipping multicodex profile") {
		t.Fatalf("expected skip warning, got %q", warning)
	}
}

func TestLoadAccountsFromMulticodexConfigRejectsInvalidProfileName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))

	configDir := filepath.Join(tmp, defaultMulticodexHomeDirName)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.json")
	configBody := `{"version":1,"profiles":{"../shared":{"name":"../shared","codex_home":"` + filepath.Join(configDir, "shared", "codex-home") + `"}}}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	accounts, warning, err := loadAccountsFromMulticodexConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected invalid profile to be skipped, got %#v", accounts)
	}
	if !strings.Contains(warning, "invalid profile name") {
		t.Fatalf("expected invalid-name warning, got %q", warning)
	}
}

func TestLoadAccountsFromMulticodexConfigRejectsHardLinkedAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(multicodexHomeEnvVar, filepath.Join(tmp, defaultMulticodexHomeDirName))

	configDir := filepath.Join(tmp, defaultMulticodexHomeDirName)
	profileHome := filepath.Join(configDir, "profiles", "personal", "codex-home")
	if err := os.MkdirAll(profileHome, 0o755); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	target := filepath.Join(tmp, "shared-auth.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write auth target: %v", err)
	}
	if err := os.Link(target, filepath.Join(profileHome, "auth.json")); err != nil {
		t.Skipf("hard links are not supported here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileHome, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0o600); err != nil {
		t.Fatalf("write profile config: %v", err)
	}
	configPath := filepath.Join(configDir, "config.json")
	configBody := `{"version":1,"profiles":{"personal":{"name":"personal","codex_home":"` + profileHome + `"}}}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	accounts, warning, err := loadAccountsFromMulticodexConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected hard-linked auth profile to be skipped, got %#v", accounts)
	}
	if !strings.Contains(warning, "multiple hard links") {
		t.Fatalf("expected hard-link warning, got %q", warning)
	}
}

func TestMonitorConfigUsesFileStoreRequiresExactRootKey(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	content := "cli_auth_credentials_store_backup = \"file\"\n[other]\ncli_auth_credentials_store = \"file\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ok, err := monitorConfigUsesFileStore(path)
	if err != nil {
		t.Fatalf("monitorConfigUsesFileStore: %v", err)
	}
	if ok {
		t.Fatal("expected lookalike or nested credential store key not to pass")
	}
}
