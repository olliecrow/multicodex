package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwitchGlobalAndRestore(t *testing.T) {
	root := t.TempDir()
	mhome := filepath.Join(root, "multicodex")
	dhome := filepath.Join(root, "codex-default")
	t.Setenv("MULTICODEX_HOME", mhome)
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", dhome)

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)

	cfg := DefaultConfig()
	profile, err := store.CreateProfile("personal")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	cfg.Profiles[profile.Name] = profile

	if err := os.WriteFile(filepath.Join(profile.CodexHome, "auth.json"), []byte("profile-auth"), 0o600); err != nil {
		t.Fatalf("write profile auth: %v", err)
	}
	if err := os.MkdirAll(paths.DefaultCodexHome, 0o700); err != nil {
		t.Fatalf("mkdir default codex home: %v", err)
	}
	if err := os.WriteFile(paths.DefaultAuthPath, []byte("original-auth"), 0o600); err != nil {
		t.Fatalf("write default auth: %v", err)
	}

	if err := store.SwitchGlobalAuthToProfile(cfg, profile); err != nil {
		t.Fatalf("SwitchGlobalAuthToProfile: %v", err)
	}

	info, err := os.Lstat(paths.DefaultAuthPath)
	if err != nil {
		t.Fatalf("lstat switched auth: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected default auth path to be symlink")
	}

	target, err := os.Readlink(paths.DefaultAuthPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	expectedTarget := filepath.Join(profile.CodexHome, "auth.json")
	if target != expectedTarget {
		t.Fatalf("unexpected symlink target. got=%q want=%q", target, expectedTarget)
	}

	changed, err := store.RestoreGlobalAuth(cfg)
	if err != nil {
		t.Fatalf("RestoreGlobalAuth: %v", err)
	}
	if !changed {
		t.Fatalf("expected restore to report changed=true")
	}

	b, err := os.ReadFile(paths.DefaultAuthPath)
	if err != nil {
		t.Fatalf("read restored default auth: %v", err)
	}
	if got := string(b); got != "original-auth" {
		t.Fatalf("unexpected restored auth content: %q", got)
	}

	restoredInfo, err := os.Lstat(paths.DefaultAuthPath)
	if err != nil {
		t.Fatalf("lstat restored auth path: %v", err)
	}
	if restoredInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected restored default auth to be a regular file, got symlink")
	}
}
