package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsDefaultsToHomeMulticodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}

	if got, want := paths.MulticodexHome, filepath.Join(home, "multicodex"); got != want {
		t.Fatalf("unexpected multicodex home: got=%q want=%q", got, want)
	}
	if got, want := paths.DefaultCodexHome, filepath.Join(home, ".codex"); got != want {
		t.Fatalf("unexpected default codex home: got=%q want=%q", got, want)
	}
}

func TestResolvePathsMigratesLegacyHome(t *testing.T) {
	home := t.TempDir()
	legacyHome := filepath.Join(home, ".multicodex")
	newHome := filepath.Join(home, "multicodex")
	legacyProfileHome := filepath.Join(legacyHome, "profiles", "work", "codex-home")
	legacyBackupPath := filepath.Join(legacyHome, "backups", "default-auth.backup")

	if err := os.MkdirAll(legacyProfileHome, 0o700); err != nil {
		t.Fatalf("mkdir legacy profile: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyBackupPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy backups: %v", err)
	}
	if err := os.WriteFile(legacyBackupPath, []byte("backup"), 0o600); err != nil {
		t.Fatalf("write legacy backup: %v", err)
	}

	cfg := &Config{
		Version: 1,
		Profiles: map[string]Profile{
			"work": {Name: "work", CodexHome: legacyProfileHome},
		},
		Global: GlobalState{
			BackupInitialized: true,
			BackupMode:        "file",
			BackupFilePath:    legacyBackupPath,
			BackupLinkTarget:  filepath.Join(legacyProfileHome, "auth.json"),
		},
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyHome, "config.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	defaultCodexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(defaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	defaultAuthPath := filepath.Join(defaultCodexHome, "auth.json")
	if err := os.Symlink(filepath.Join(legacyProfileHome, "auth.json"), defaultAuthPath); err != nil {
		t.Fatalf("symlink default auth path: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if got, want := paths.MulticodexHome, newHome; got != want {
		t.Fatalf("unexpected multicodex home: got=%q want=%q", got, want)
	}

	if _, err := os.Stat(legacyHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy home to be moved, stat err=%v", err)
	}
	b, err := os.ReadFile(filepath.Join(newHome, "config.json"))
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	var migrated Config
	if err := json.Unmarshal(b, &migrated); err != nil {
		t.Fatalf("parse migrated config: %v", err)
	}
	if got, want := migrated.Profiles["work"].CodexHome, filepath.Join(newHome, "profiles", "work", "codex-home"); got != want {
		t.Fatalf("unexpected migrated profile codex home: got=%q want=%q", got, want)
	}
	if got, want := migrated.Global.BackupFilePath, filepath.Join(newHome, "backups", "default-auth.backup"); got != want {
		t.Fatalf("unexpected migrated backup file path: got=%q want=%q", got, want)
	}
	if got, want := migrated.Global.BackupLinkTarget, filepath.Join(newHome, "profiles", "work", "codex-home", "auth.json"); got != want {
		t.Fatalf("unexpected migrated backup link target: got=%q want=%q", got, want)
	}

	gotTarget, err := os.Readlink(defaultAuthPath)
	if err != nil {
		t.Fatalf("read migrated default auth symlink: %v", err)
	}
	if got, want := gotTarget, filepath.Join(newHome, "profiles", "work", "codex-home", "auth.json"); got != want {
		t.Fatalf("unexpected default auth symlink target: got=%q want=%q", got, want)
	}
}

func TestResolvePathsSkipsLegacyMigrationWhenHomeIsExplicit(t *testing.T) {
	home := t.TempDir()
	legacyHome := filepath.Join(home, ".multicodex")
	customHome := filepath.Join(home, "custom-multicodex")

	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatalf("mkdir legacy home: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", customHome)
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if got, want := paths.MulticodexHome, customHome; got != want {
		t.Fatalf("unexpected multicodex home: got=%q want=%q", got, want)
	}

	if _, err := os.Stat(legacyHome); err != nil {
		t.Fatalf("expected legacy home to remain when override is set: %v", err)
	}
}
