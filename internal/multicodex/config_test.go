package multicodex

import (
	"os"
	"path/filepath"
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

	profile, err := store.CreateProfile("work")
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

	if err := store.EnsureProfileDir(profile); err != nil {
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

	if err := store.EnsureProfileDir(profile); err != nil {
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
