package multicodex

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

	if err := os.MkdirAll(legacyProfileHome, 0o700); err != nil {
		t.Fatalf("mkdir legacy profile: %v", err)
	}

	cfg := map[string]any{
		"version": 1,
		"profiles": map[string]Profile{
			"work": {Name: "work", CodexHome: legacyProfileHome},
		},
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyHome, "config.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
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
}

func TestResolvePathsWithoutMigrationLeavesLegacyStateUntouched(t *testing.T) {
	home := t.TempDir()
	legacyHome := filepath.Join(home, ".multicodex")
	newHome := filepath.Join(home, "multicodex")
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatalf("mkdir legacy home: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "")

	paths, err := ResolvePathsWithoutMigration()
	if err != nil {
		t.Fatalf("ResolvePathsWithoutMigration: %v", err)
	}
	if got, want := paths.MulticodexHome, newHome; got != want {
		t.Fatalf("unexpected multicodex home: got=%q want=%q", got, want)
	}
	if _, err := os.Stat(legacyHome); err != nil {
		t.Fatalf("expected legacy home to remain, stat err=%v", err)
	}
	if _, err := os.Stat(newHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected new home not to be created, stat err=%v", err)
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

func TestResolvePathsExpandsConfiguredHomePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "~/custom-multicodex")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "~/custom-codex")

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if got, want := paths.MulticodexHome, filepath.Join(home, "custom-multicodex"); got != want {
		t.Fatalf("unexpected expanded multicodex home: got=%q want=%q", got, want)
	}
	if got, want := paths.DefaultCodexHome, filepath.Join(home, "custom-codex"); got != want {
		t.Fatalf("unexpected expanded default codex home: got=%q want=%q", got, want)
	}
}

func TestResolvePathsNormalizesRelativeConfiguredHomePaths(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MULTICODEX_HOME", "relative-multicodex")
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", "relative-codex")
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	resolvedCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldCwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if got, want := paths.MulticodexHome, filepath.Join(resolvedCwd, "relative-multicodex"); got != want {
		t.Fatalf("unexpected relative multicodex home: got=%q want=%q", got, want)
	}
	if got, want := paths.DefaultCodexHome, filepath.Join(resolvedCwd, "relative-codex"); got != want {
		t.Fatalf("unexpected relative default codex home: got=%q want=%q", got, want)
	}
}
