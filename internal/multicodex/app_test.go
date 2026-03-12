package multicodex

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCmdUseMigratesGeneratedProfileConfig(t *testing.T) {
	app, profile, defaultConfigPath := newTestAppWithGeneratedProfileConfig(t)

	restoreStdout := captureAppTestStdout(t)
	defer restoreStdout()

	if err := app.cmdUse([]string{profile.Name}); err != nil {
		t.Fatalf("cmdUse: %v", err)
	}

	assertProfileConfigSymlink(t, filepath.Join(profile.CodexHome, "config.toml"), defaultConfigPath)
}

func TestCmdRunMigratesGeneratedProfileConfig(t *testing.T) {
	app, profile, defaultConfigPath := newTestAppWithGeneratedProfileConfig(t)

	if err := app.cmdRun([]string{profile.Name, "--", "true"}); err != nil {
		t.Fatalf("cmdRun: %v", err)
	}

	assertProfileConfigSymlink(t, filepath.Join(profile.CodexHome, "config.toml"), defaultConfigPath)
}

func newTestAppWithGeneratedProfileConfig(t *testing.T) (*App, Profile, string) {
	t.Helper()

	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multicodex"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex-default"))

	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	if err := app.store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}

	defaultConfigPath := filepath.Join(app.store.paths.DefaultCodexHome, "config.toml")
	if err := os.MkdirAll(app.store.paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(defaultConfigPath, []byte("model = \"global\"\ncli_auth_credentials_store = \"file\"\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	profile := Profile{Name: "work", CodexHome: filepath.Join(app.store.paths.ProfilesDir, "work", "codex-home")}
	if err := os.MkdirAll(profile.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir profile codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profile.CodexHome, "config.toml"), []byte(generatedProfileConfigContent), 0o600); err != nil {
		t.Fatalf("write generated profile config: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Profiles[profile.Name] = profile
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	return app, profile, defaultConfigPath
}

func assertProfileConfigSymlink(t *testing.T, path, wantTarget string) {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat profile config: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected profile config to be a symlink")
	}

	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink profile config: %v", err)
	}
	if target != wantTarget {
		t.Fatalf("unexpected symlink target. got=%q want=%q", target, wantTarget)
	}
}

func captureAppTestStdout(t *testing.T) func() {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = writer

	return func() {
		os.Stdout = original
		_ = writer.Close()
		_, _ = io.Copy(io.Discard, reader)
		_ = reader.Close()
	}
}
