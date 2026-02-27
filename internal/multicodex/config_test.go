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

func TestCreateProfileCreatesFileStoreConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)

	profile, err := store.CreateProfile("work")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(profile.CodexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if string(b) != "cli_auth_credentials_store = \"file\"\n" {
		t.Fatalf("unexpected config.toml content: %q", string(b))
	}
}
