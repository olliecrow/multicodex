package multicodex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSaveAndLoad(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)

	cfg := DefaultConfig()
	cfg.Profiles["personal"] = Profile{Name: "personal", CodexHome: filepath.Join(paths.ProfilesDir, "personal", "codex-home")}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, ok := loaded.Profiles["personal"]; !ok {
		t.Fatalf("expected profile to be loaded")
	}

	info, err := os.Stat(paths.ConfigPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected config mode 0600, got %o", got)
	}

	raw, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), `"global"`) {
		t.Fatalf("config should not contain global auth state: %s", string(raw))
	}
	if _, err := os.Stat(filepath.Join(paths.MulticodexHome, "backups")); !os.IsNotExist(err) {
		t.Fatalf("expected no backup directory, stat err=%v", err)
	}
}

func TestStoreLoadRejectsInvalidProfileNames(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	raw := `{"version":1,"profiles":{"../escape":{"name":"../escape","codex_home":"/tmp/escape"}}}`
	if err := os.WriteFile(paths.ConfigPath, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := store.Load(); err == nil {
		t.Fatalf("expected invalid stored profile name to be rejected")
	}
}

func TestStoreLoadRejectsMismatchedStoredProfileName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	raw := `{"version":1,"profiles":{"work":{"name":"personal","codex_home":"/tmp/personal"}}}`
	if err := os.WriteFile(paths.ConfigPath, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err = store.Load()
	if err == nil {
		t.Fatal("expected mismatched stored profile name to be rejected")
	}
	if !strings.Contains(err.Error(), "mismatched name") {
		t.Fatalf("expected mismatched-name error, got %v", err)
	}
}

func TestStoreLoadRejectsMalformedProfileResourcesWithoutProfileMutation(t *testing.T) {
	root := t.TempDir()
	paths := Paths{
		MulticodexHome:   filepath.Join(root, "multi"),
		ConfigPath:       filepath.Join(root, "multi", "config.json"),
		ProfilesDir:      filepath.Join(root, "multi", "profiles"),
		DefaultCodexHome: filepath.Join(root, "codex"),
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		`{"version":1,"profile_resources":{"guidance":{"inhert":true}},"profiles":{}}`,
		`{"version":1,"profile_resources":{"skills":{}},"profiles":{}}`,
		`{"version":1,"profile_resources":{"unknown":{}},"profiles":{}}`,
	}
	for i, raw := range cases {
		if err := os.WriteFile(paths.ConfigPath, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(); err == nil {
			t.Fatalf("case %d: expected malformed resource config to fail", i)
		}
		entries, err := os.ReadDir(paths.ProfilesDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("case %d: malformed config touched profile files: %v", i, entries)
		}
	}
}

func TestStoreLoadKeepsUnrelatedTopLevelFieldsPermissive(t *testing.T) {
	root := t.TempDir()
	paths := Paths{MulticodexHome: filepath.Join(root, "multi"), ConfigPath: filepath.Join(root, "multi", "config.json"), ProfilesDir: filepath.Join(root, "multi", "profiles")}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":1,"future_top_level":true,"profiles":{}}`
	if err := os.WriteFile(paths.ConfigPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("unrelated top-level field should remain permissive: %v", err)
	}
}

func TestStoreLoadRejectsUnsupportedVersionWithoutRewritingConfig(t *testing.T) {
	root := t.TempDir()
	paths := Paths{
		MulticodexHome: filepath.Join(root, "multi"),
		ConfigPath:     filepath.Join(root, "multi", "config.json"),
		ProfilesDir:    filepath.Join(root, "multi", "profiles"),
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":2,"future_field":{"keep":true},"profiles":{}}`
	if err := os.WriteFile(paths.ConfigPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "unsupported config version 2") {
		t.Fatalf("expected unsupported-version error, got %v", err)
	}
	after, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != raw {
		t.Fatalf("unsupported config was rewritten: %q", after)
	}
}

func TestStoreSaveDoesNotWriteThroughPredictableTempSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("keep me\n"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	tmpPath := paths.ConfigPath + ".tmp"
	if err := os.Symlink(victim, tmpPath); err != nil {
		t.Fatalf("symlink temp config: %v", err)
	}

	err = store.Save(DefaultConfig())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatalf("read victim: %v", readErr)
	}
	if string(b) != "keep me\n" {
		t.Fatalf("expected victim not to be overwritten, got %q", string(b))
	}
	if _, statErr := os.Stat(paths.ConfigPath); statErr != nil {
		t.Fatalf("expected config to be written, stat err=%v", statErr)
	}
}

func TestStoreWithConfigLockRejectsSymlinkedLockFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	victim := filepath.Join(root, "victim.lock")
	if err := os.WriteFile(victim, []byte("keep me\n"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	if err := os.Symlink(victim, filepath.Join(paths.MulticodexHome, "config.lock")); err != nil {
		t.Fatalf("symlink config lock: %v", err)
	}

	err = store.WithConfigLock(func() error {
		t.Fatal("expected lock callback not to run")
		return nil
	})
	if err == nil {
		t.Fatal("expected symlinked config lock to fail")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	b, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatalf("read victim: %v", readErr)
	}
	if string(b) != "keep me\n" {
		t.Fatalf("expected victim not to be overwritten, got %q", string(b))
	}
}

func TestCreateProfileLinksProfileConfigToDefaultConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)

	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.DefaultCodexHome, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	profile, _, err := store.CreateProfile("work", nil)
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	configPath := filepath.Join(profile.CodexHome, "config.toml")
	info, err := os.Lstat(configPath)
	if err != nil {
		t.Fatalf("lstat config.toml: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected profile config.toml to be a symlink")
	}

	target, err := os.Readlink(configPath)
	if err != nil {
		t.Fatalf("readlink config.toml: %v", err)
	}
	expectedTarget := filepath.Join(paths.DefaultCodexHome, "config.toml")
	if target != expectedTarget {
		t.Fatalf("unexpected symlink target. got=%q want=%q", target, expectedTarget)
	}

	b, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if string(b) != "model = \"gpt-5\"\n" {
		t.Fatalf("unexpected config.toml content: %q", string(b))
	}
}

func TestEnsureProfileDirRejectsSymlinkedProfileDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	realProfileDir := filepath.Join(t.TempDir(), "real-profile")
	if err := os.MkdirAll(realProfileDir, 0o700); err != nil {
		t.Fatalf("mkdir real profile dir: %v", err)
	}
	profileDir := filepath.Join(paths.ProfilesDir, "work")
	if err := os.Symlink(realProfileDir, profileDir); err != nil {
		t.Fatalf("symlink profile dir: %v", err)
	}
	profile := Profile{Name: "work", CodexHome: filepath.Join(profileDir, "codex-home")}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected symlinked profile dir to fail")
	}
	if !strings.Contains(err.Error(), "profile path is a symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realProfileDir, "codex-home")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected setup not to write through symlink, stat err=%v", statErr)
	}
}

func TestEnsureProfileDirRejectsGroupReadableProfileRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if err := os.MkdirAll(paths.MulticodexHome, 0o700); err != nil {
		t.Fatalf("mkdir multicodex home: %v", err)
	}
	if err := os.MkdirAll(paths.ProfilesDir, 0o750); err != nil {
		t.Fatalf("mkdir profiles dir: %v", err)
	}
	store := NewStore(paths)
	profileDir := filepath.Join(paths.ProfilesDir, "work")
	profile := Profile{Name: "work", CodexHome: filepath.Join(profileDir, "codex-home")}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected group-readable profile root to fail")
	}
	if !strings.Contains(err.Error(), "expected no group/world permissions") {
		t.Fatalf("expected private-permissions error, got %v", err)
	}
}

func TestCreateProfileRejectsSymlinkedProfileDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	realProfileDir := filepath.Join(t.TempDir(), "real-profile")
	if err := os.MkdirAll(realProfileDir, 0o700); err != nil {
		t.Fatalf("mkdir real profile dir: %v", err)
	}
	profileDir := filepath.Join(paths.ProfilesDir, "work")
	if err := os.Symlink(realProfileDir, profileDir); err != nil {
		t.Fatalf("symlink profile dir: %v", err)
	}

	_, _, err = store.CreateProfile("work", nil)
	if err == nil {
		t.Fatal("expected symlinked profile dir to fail")
	}
	if !strings.Contains(err.Error(), "profile path is a symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realProfileDir, "codex-home")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected create not to write through symlink, stat err=%v", statErr)
	}
}

func TestEnsureBaseDirsRejectsSymlinkedMulticodexHome(t *testing.T) {
	root := t.TempDir()
	realHome := filepath.Join(root, "real-home")
	if err := os.MkdirAll(realHome, 0o700); err != nil {
		t.Fatalf("mkdir real home: %v", err)
	}
	linkedHome := filepath.Join(root, "linked-home")
	if err := os.Symlink(realHome, linkedHome); err != nil {
		t.Fatalf("symlink multicodex home: %v", err)
	}

	store := NewStore(Paths{
		MulticodexHome:   linkedHome,
		ConfigPath:       filepath.Join(linkedHome, "config.json"),
		ProfilesDir:      filepath.Join(linkedHome, "profiles"),
		DefaultCodexHome: filepath.Join(root, "codex-default"),
	})

	err := store.EnsureBaseDirs()
	if err == nil {
		t.Fatal("expected symlinked multicodex home to fail")
	}
	if !strings.Contains(err.Error(), "profile path is a symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realHome, "profiles")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected setup not to write through symlink, stat err=%v", statErr)
	}
}

func TestEnsureBaseDirsSecuresExistingDirectories(t *testing.T) {
	root := t.TempDir()
	multicodexHome := filepath.Join(root, "multicodex")
	profilesDir := filepath.Join(multicodexHome, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatalf("mkdir existing dirs: %v", err)
	}
	if err := os.Chmod(multicodexHome, 0o755); err != nil {
		t.Fatalf("chmod multicodex home: %v", err)
	}
	if err := os.Chmod(profilesDir, 0o755); err != nil {
		t.Fatalf("chmod profiles dir: %v", err)
	}

	store := NewStore(Paths{
		MulticodexHome:   multicodexHome,
		ConfigPath:       filepath.Join(multicodexHome, "config.json"),
		ProfilesDir:      profilesDir,
		DefaultCodexHome: filepath.Join(root, "codex-default"),
	})

	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	for _, path := range []string{multicodexHome, profilesDir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("expected %s mode 0700, got %o", path, got)
		}
	}
}

func TestEnsureProfileDirRejectsStoredSymlinkedCodexHomeAlias(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	expectedHome := filepath.Join(paths.ProfilesDir, "work", "codex-home")
	if err := os.MkdirAll(expectedHome, 0o700); err != nil {
		t.Fatalf("mkdir expected home: %v", err)
	}
	aliasHome := filepath.Join(root, "alias-home")
	if err := os.Symlink(expectedHome, aliasHome); err != nil {
		t.Fatalf("symlink alias home: %v", err)
	}
	profile := Profile{Name: "work", CodexHome: aliasHome}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected symlinked stored codex home alias to fail")
	}
	if !strings.Contains(err.Error(), "profile-local path under") {
		t.Fatalf("expected profile-local path error, got %v", err)
	}
}

func TestEnsureProfileDirRejectsStoredCodexHomeOutsideProfilesDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	expectedProfilesDir := filepath.Join(paths.MulticodexHome, "profiles")
	aliasProfilesDir := filepath.Join(root, "alias-profiles")
	if err := os.MkdirAll(expectedProfilesDir, 0o700); err != nil {
		t.Fatalf("mkdir profiles dir: %v", err)
	}
	if err := os.Symlink(expectedProfilesDir, aliasProfilesDir); err != nil {
		t.Fatalf("symlink profiles alias: %v", err)
	}
	profile := Profile{Name: "work", CodexHome: filepath.Join(aliasProfilesDir, "work", "codex-home")}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected profile path outside profiles dir to fail")
	}
	if !strings.Contains(err.Error(), "profile-local path under") {
		t.Fatalf("expected profile-local path error, got %v", err)
	}
}

func TestEnsureProfileDirRejectsUncleanStoredCodexHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	uncleanHome := paths.ProfilesDir + string(os.PathSeparator) + "work" + string(os.PathSeparator) + "link" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "codex-home"
	profile := Profile{Name: "work", CodexHome: uncleanHome}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected unclean stored codex home to fail")
	}
	if !strings.Contains(err.Error(), "clean profile-local path") {
		t.Fatalf("expected clean-path error, got %v", err)
	}
}

func TestEnsureProfileDirRejectsSymlinkedSkillsDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	defaultSkill := filepath.Join(paths.DefaultCodexHome, "skills", "tool")
	if err := os.MkdirAll(defaultSkill, 0o700); err != nil {
		t.Fatalf("mkdir default skill: %v", err)
	}
	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir profile codex home: %v", err)
	}
	externalSkills := filepath.Join(root, "external-skills")
	if err := os.MkdirAll(externalSkills, 0o700); err != nil {
		t.Fatalf("mkdir external skills: %v", err)
	}
	if err := os.Symlink(externalSkills, filepath.Join(profile.CodexHome, "skills")); err != nil {
		t.Fatalf("symlink profile skills: %v", err)
	}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected symlinked profile skills dir to fail")
	}
	if !strings.Contains(err.Error(), "profile path is a symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(externalSkills, "tool")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected setup not to write through skills symlink, stat err=%v", statErr)
	}
}

func TestEnsureProfileDirRejectsSymlinkedSkillsDirWithoutDefaultSkills(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir profile codex home: %v", err)
	}
	externalSkills := filepath.Join(root, "external-skills")
	if err := os.MkdirAll(externalSkills, 0o700); err != nil {
		t.Fatalf("mkdir external skills: %v", err)
	}
	if err := os.Symlink(externalSkills, filepath.Join(profile.CodexHome, "skills")); err != nil {
		t.Fatalf("symlink profile skills: %v", err)
	}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected symlinked profile skills dir to fail")
	}
	if !strings.Contains(err.Error(), "profile path is a symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestEnsureProfileDirMigratesGeneratedConfigToDefaultConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir profile codex home: %v", err)
	}
	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.DefaultCodexHome, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	configPath := filepath.Join(profile.CodexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(generatedProfileConfigContent), 0o600); err != nil {
		t.Fatalf("write generated profile config: %v", err)
	}

	if _, err := store.EnsureProfileDir(profile, nil); err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}

	info, err := os.Lstat(configPath)
	if err != nil {
		t.Fatalf("lstat config.toml: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected generated profile config to be replaced with symlink")
	}
}

func TestEnsureProfileDirPreservesManualProfileConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir profile codex home: %v", err)
	}

	configPath := filepath.Join(profile.CodexHome, "config.toml")
	want := "model = \"gpt-5\"\n"
	if err := os.WriteFile(configPath, []byte(want), 0o600); err != nil {
		t.Fatalf("write manual profile config: %v", err)
	}

	if _, err := store.EnsureProfileDir(profile, nil); err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}

	info, err := os.Lstat(configPath)
	if err != nil {
		t.Fatalf("lstat config.toml: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected manual profile config to remain a regular file")
	}

	b, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read manual profile config: %v", err)
	}
	if string(b) != want {
		t.Fatalf("unexpected manual profile config content: %q", string(b))
	}
}

func TestEnsureProfileDirRejectsProfileConfigSymlinkOutsideDefaultConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.DefaultCodexHome, "config.toml"), []byte("model = \"shared\"\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir profile codex home: %v", err)
	}
	otherConfig := filepath.Join(root, "other-config.toml")
	if err := os.WriteFile(otherConfig, []byte("model = \"other\"\n"), 0o600); err != nil {
		t.Fatalf("write other config: %v", err)
	}
	if err := os.Symlink(otherConfig, filepath.Join(profile.CodexHome, "config.toml")); err != nil {
		t.Fatalf("symlink profile config: %v", err)
	}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected unsafe profile config symlink to fail")
	}
	if !strings.Contains(err.Error(), "must point to default Codex config") {
		t.Fatalf("expected default config symlink error, got %v", err)
	}
}

func TestEnsureProfileDirRejectsProfileConfigSymlinkWithTraversalThroughSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.DefaultCodexHome, "config.toml"), []byte("model = \"shared\"\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}
	outsideDir := filepath.Join(root, "outside")
	outsideChild := filepath.Join(outsideDir, "child")
	if err := os.MkdirAll(outsideChild, 0o700); err != nil {
		t.Fatalf("mkdir outside child: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "config.toml"), []byte("model = \"outside\"\n"), 0o600); err != nil {
		t.Fatalf("write outside config: %v", err)
	}
	if err := os.Symlink(outsideChild, filepath.Join(paths.DefaultCodexHome, "pivot")); err != nil {
		t.Fatalf("symlink default pivot: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir profile codex home: %v", err)
	}
	rawTarget := paths.DefaultCodexHome + string(os.PathSeparator) + "pivot" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "config.toml"
	if err := os.Symlink(rawTarget, filepath.Join(profile.CodexHome, "config.toml")); err != nil {
		t.Fatalf("symlink profile config: %v", err)
	}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected traversal-through-symlink profile config to fail")
	}
	if !strings.Contains(err.Error(), "must point to default Codex config") {
		t.Fatalf("expected default config symlink error, got %v", err)
	}
}

func TestEnsureProfileDirLinksMissingDefaultSkillsEntries(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)

	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.DefaultCodexHome, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", "shared-skill"), 0o700); err != nil {
		t.Fatalf("mkdir default shared skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", "codex-primary-runtime", "slides"), 0o700); err != nil {
		t.Fatalf("mkdir default runtime skill family: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", ".system", "openai-docs"), 0o700); err != nil {
		t.Fatalf("mkdir default system skill family: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	if _, err := store.EnsureProfileDir(profile, nil); err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}

	for _, name := range []string{"shared-skill", "codex-primary-runtime"} {
		profilePath := filepath.Join(profile.CodexHome, "skills", name)
		info, err := os.Lstat(profilePath)
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected %s to be a symlink", name)
		}
		target, err := os.Readlink(profilePath)
		if err != nil {
			t.Fatalf("readlink %s: %v", name, err)
		}
		want := filepath.Join(paths.DefaultCodexHome, "skills", name)
		if target != want {
			t.Fatalf("unexpected symlink target for %s. got=%q want=%q", name, target, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(profile.CodexHome, "skills", ".system")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime-managed .system must not be inherited, stat err=%v", err)
	}
}

func TestEnsureProfileDirSecuresExistingProfileSkillsDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.DefaultCodexHome, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", "shared-skill"), 0o700); err != nil {
		t.Fatalf("mkdir default skill: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	profileSkillsPath := filepath.Join(profile.CodexHome, "skills")
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir profile codex home: %v", err)
	}
	if err := os.MkdirAll(profileSkillsPath, 0o700); err != nil {
		t.Fatalf("mkdir profile skills: %v", err)
	}
	if err := os.Chmod(profileSkillsPath, 0o755); err != nil {
		t.Fatalf("chmod profile skills: %v", err)
	}

	if _, err := store.EnsureProfileDir(profile, nil); err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}
	info, err := os.Stat(profileSkillsPath)
	if err != nil {
		t.Fatalf("stat profile skills: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("expected profile skills mode 0700, got %o", got)
	}
}

func TestEnsureProfileDirPreservesManualProfileSkillOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)

	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.DefaultCodexHome, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", "shared-skill"), 0o700); err != nil {
		t.Fatalf("mkdir default shared skill: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	manualSkillPath := filepath.Join(profile.CodexHome, "skills", "shared-skill")
	if err := os.MkdirAll(manualSkillPath, 0o700); err != nil {
		t.Fatalf("mkdir manual profile skill override: %v", err)
	}

	if _, err := store.EnsureProfileDir(profile, nil); err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}

	info, err := os.Lstat(manualSkillPath)
	if err != nil {
		t.Fatalf("lstat manual skill override: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected manual profile skill override to remain a directory")
	}
}

func TestEnsureProfileDirRejectsProfileSkillSymlinkOutsideDefaultSkills(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", "shared-skill"), 0o700); err != nil {
		t.Fatalf("mkdir default shared skill: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	profileSkillDir := filepath.Join(profile.CodexHome, "skills")
	if err := os.MkdirAll(profileSkillDir, 0o700); err != nil {
		t.Fatalf("mkdir profile skills: %v", err)
	}
	otherSkill := filepath.Join(root, "other-skill")
	if err := os.MkdirAll(otherSkill, 0o700); err != nil {
		t.Fatalf("mkdir other skill: %v", err)
	}
	if err := os.Symlink(otherSkill, filepath.Join(profileSkillDir, "shared-skill")); err != nil {
		t.Fatalf("symlink profile skill: %v", err)
	}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected unsafe profile skill symlink to fail")
	}
	if !strings.Contains(err.Error(), "must point under default skills directory") {
		t.Fatalf("expected default skills symlink error, got %v", err)
	}
}

func TestEnsureProfileDirRejectsProfileSkillSymlinkWithTraversalThroughSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	defaultSkillsPath := filepath.Join(paths.DefaultCodexHome, "skills")
	if err := os.MkdirAll(filepath.Join(defaultSkillsPath, ".system"), 0o700); err != nil {
		t.Fatalf("mkdir default system skill: %v", err)
	}
	outsideDir := filepath.Join(root, "outside")
	outsideChild := filepath.Join(outsideDir, "child")
	if err := os.MkdirAll(filepath.Join(outsideDir, ".system"), 0o700); err != nil {
		t.Fatalf("mkdir outside system skill: %v", err)
	}
	if err := os.MkdirAll(outsideChild, 0o700); err != nil {
		t.Fatalf("mkdir outside child: %v", err)
	}
	if err := os.Symlink(outsideChild, filepath.Join(defaultSkillsPath, "pivot")); err != nil {
		t.Fatalf("symlink default skills pivot: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	profileSkillDir := filepath.Join(profile.CodexHome, "skills")
	if err := os.MkdirAll(profileSkillDir, 0o700); err != nil {
		t.Fatalf("mkdir profile skills: %v", err)
	}
	rawTarget := defaultSkillsPath + string(os.PathSeparator) + "pivot" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + ".system"
	if err := os.Symlink(rawTarget, filepath.Join(profileSkillDir, ".system")); err != nil {
		t.Fatalf("symlink profile system skill: %v", err)
	}

	_, err = store.EnsureProfileDir(profile, nil)
	if err == nil {
		t.Fatal("expected traversal-through-symlink profile skill to fail")
	}
	if !strings.Contains(err.Error(), "must point under default skills directory") {
		t.Fatalf("expected default skills symlink error, got %v", err)
	}
}

func TestEnsureProfileDirRemovesStaleManagedProfileSkillSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	defaultSkillsPath := filepath.Join(paths.DefaultCodexHome, "skills")
	if err := os.MkdirAll(filepath.Join(defaultSkillsPath, "kept"), 0o700); err != nil {
		t.Fatalf("mkdir default kept skill: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	profileSkillDir := filepath.Join(profile.CodexHome, "skills")
	if err := os.MkdirAll(profileSkillDir, 0o700); err != nil {
		t.Fatalf("mkdir profile skills: %v", err)
	}
	staleProfilePath := filepath.Join(profileSkillDir, "removed")
	if err := os.Symlink(filepath.Join(defaultSkillsPath, "removed"), staleProfilePath); err != nil {
		t.Fatalf("symlink stale profile skill: %v", err)
	}

	if _, err := store.EnsureProfileDir(profile, nil); err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}
	if _, err := os.Lstat(staleProfilePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale managed skill link removed, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(profileSkillDir, "kept")); err != nil {
		t.Fatalf("expected kept skill link: %v", err)
	}
}

func TestEnsureProfileDirRemovesStaleSkillLinkWhenDefaultSkillsDirIsSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	actualDefaultSkills := filepath.Join(root, "actual-default-skills")
	if err := os.MkdirAll(filepath.Join(actualDefaultSkills, "kept"), 0o700); err != nil {
		t.Fatalf("mkdir actual default skills: %v", err)
	}
	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	defaultSkillsPath := filepath.Join(paths.DefaultCodexHome, "skills")
	if err := os.Symlink(actualDefaultSkills, defaultSkillsPath); err != nil {
		t.Fatalf("symlink default skills: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	profileSkillDir := filepath.Join(profile.CodexHome, "skills")
	if err := os.MkdirAll(profileSkillDir, 0o700); err != nil {
		t.Fatalf("mkdir profile skills: %v", err)
	}
	staleProfilePath := filepath.Join(profileSkillDir, "removed")
	if err := os.Symlink(filepath.Join(defaultSkillsPath, "removed"), staleProfilePath); err != nil {
		t.Fatalf("symlink stale profile skill: %v", err)
	}

	if _, err := store.EnsureProfileDir(profile, nil); err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}
	if _, err := os.Lstat(staleProfilePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale managed skill link removed, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(profileSkillDir, "kept")); err != nil {
		t.Fatalf("expected kept skill link: %v", err)
	}
}

func TestProfileConfigUsesFileStoreMatchesExactKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "exact root key",
			content: strings.Join([]string{
				"model = \"gpt-5\"",
				"cli_auth_credentials_store = \"file\"",
			}, "\n"),
			want: true,
		},
		{
			name: "literal string value",
			content: strings.Join([]string{
				"model = \"gpt-5\"",
				"cli_auth_credentials_store = 'file'",
			}, "\n"),
			want: true,
		},
		{
			name: "comment false positive",
			content: strings.Join([]string{
				"# cli_auth_credentials_store = \"file\"",
				"model = \"file\"",
			}, "\n"),
			want: false,
		},
		{
			name:    "string false positive",
			content: `note = "cli_auth_credentials_store should not imply file"`,
			want:    false,
		},
		{
			name: "wrong exact value with file in comment",
			content: strings.Join([]string{
				"cli_auth_credentials_store = \"keychain\" # file",
			}, "\n"),
			want: false,
		},
		{
			name: "nested table ignored",
			content: strings.Join([]string{
				"[auth]",
				"cli_auth_credentials_store = \"file\"",
			}, "\n"),
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			got, err := profileConfigUsesFileStore(path)
			if err != nil {
				t.Fatalf("profileConfigUsesFileStore: %v", err)
			}
			if got != tc.want {
				t.Fatalf("profileConfigUsesFileStore(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
