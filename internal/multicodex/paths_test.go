package multicodex

import (
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

func TestResolvePathsLeavesHiddenStateUntouched(t *testing.T) {
	home := t.TempDir()
	hiddenHome := filepath.Join(home, ".unowned-local-state")
	newHome := filepath.Join(home, "multicodex")
	if err := os.MkdirAll(hiddenHome, 0o700); err != nil {
		t.Fatalf("mkdir hidden home: %v", err)
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
	if _, err := os.Stat(hiddenHome); err != nil {
		t.Fatalf("expected hidden home to remain, stat err=%v", err)
	}
	if _, err := os.Stat(newHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected new home not to be created, stat err=%v", err)
	}
}

func TestResolvePathsUsesExplicitHome(t *testing.T) {
	home := t.TempDir()
	customHome := filepath.Join(home, "custom-multicodex")

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
