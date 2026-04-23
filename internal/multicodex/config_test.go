package multicodex

import (
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
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", "battletest"), 0o700); err != nil {
		t.Fatalf("mkdir default battletest skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", "codex-primary-runtime", "slides"), 0o700); err != nil {
		t.Fatalf("mkdir default runtime skill family: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	if err := store.EnsureProfileDir(profile); err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}

	for _, name := range []string{"battletest", "codex-primary-runtime"} {
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
	if err := os.MkdirAll(filepath.Join(paths.DefaultCodexHome, "skills", "battletest"), 0o700); err != nil {
		t.Fatalf("mkdir default battletest skill: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(paths.ProfilesDir, "work", "codex-home")}
	manualSkillPath := filepath.Join(profile.CodexHome, "skills", "battletest")
	if err := os.MkdirAll(manualSkillPath, 0o700); err != nil {
		t.Fatalf("mkdir manual profile skill override: %v", err)
	}

	if err := store.EnsureProfileDir(profile); err != nil {
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
