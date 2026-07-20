package multicodex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileAppliesResourcesForAllProfiles(t *testing.T) {
	app := newTestAppForCLI(t)
	if err := app.store.EnsureBaseDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	defaultHome := app.store.paths.DefaultCodexHome
	defaultSkills := filepath.Join(defaultHome, "skills")
	writeDefaultFileStoreConfig(t, app)
	if err := os.MkdirAll(defaultSkills, 0o700); err != nil {
		t.Fatalf("mkdir default skills: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultHome, "AGENTS.md"), []byte("shared guidance\n"), 0o600); err != nil {
		t.Fatalf("write default guidance: %v", err)
	}
	if err := os.Mkdir(filepath.Join(defaultSkills, "current"), 0o700); err != nil {
		t.Fatalf("mkdir current skill: %v", err)
	}
	defaultEntriesBefore, err := os.ReadDir(defaultHome)
	if err != nil {
		t.Fatalf("read default home: %v", err)
	}
	defaultGuidanceBefore, err := os.ReadFile(filepath.Join(defaultHome, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read default guidance: %v", err)
	}

	inherit := true
	cfg := DefaultConfig()
	cfg.ProfileResources = &ProfileResources{
		Guidance: &GuidanceResources{Inherit: &inherit},
		Skills:   &SkillResources{Inherit: &inherit},
	}
	for _, name := range []string{"zeta", "alpha"} {
		profileHome := filepath.Join(app.store.paths.ProfilesDir, name, "codex-home")
		if err := os.MkdirAll(filepath.Join(profileHome, "skills"), 0o700); err != nil {
			t.Fatalf("mkdir profile skills: %v", err)
		}
		if err := os.Symlink(filepath.Join(defaultSkills, "retired"), filepath.Join(profileHome, "skills", "retired")); err != nil {
			t.Fatalf("link retired skill: %v", err)
		}
		cfg.Profiles[name] = Profile{Name: name, CodexHome: profileHome}
	}
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	out, err := captureStdout(t, func() error { return app.Run([]string{"reconcile"}) })
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	alphaIndex := strings.Index(out, "== alpha ==")
	zetaIndex := strings.Index(out, "== zeta ==")
	if alphaIndex < 0 || zetaIndex < 0 || alphaIndex > zetaIndex {
		t.Fatalf("expected sorted profile output, got:\n%s", out)
	}
	if !strings.Contains(out, "reconcile result: PASS") {
		t.Fatalf("expected success summary, got:\n%s", out)
	}

	for _, name := range []string{"alpha", "zeta"} {
		profileHome := cfg.Profiles[name].CodexHome
		assertLinkTarget(t, filepath.Join(profileHome, "AGENTS.md"), filepath.Join(defaultHome, "AGENTS.md"))
		assertLinkTarget(t, filepath.Join(profileHome, "skills", "current"), filepath.Join(defaultSkills, "current"))
		if _, err := os.Lstat(filepath.Join(profileHome, "skills", "retired")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected retired skill link removed for %s, stat err=%v", name, err)
		}
	}
	defaultEntriesAfter, err := os.ReadDir(defaultHome)
	if err != nil {
		t.Fatalf("read default home after reconcile: %v", err)
	}
	if len(defaultEntriesAfter) != len(defaultEntriesBefore) {
		t.Fatalf("default home entries changed: before=%d after=%d", len(defaultEntriesBefore), len(defaultEntriesAfter))
	}
	defaultGuidanceAfter, err := os.ReadFile(filepath.Join(defaultHome, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read default guidance after reconcile: %v", err)
	}
	if string(defaultGuidanceAfter) != string(defaultGuidanceBefore) {
		t.Fatalf("default guidance changed")
	}

	second, err := captureStdout(t, func() error { return app.Run([]string{"reconcile"}) })
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if !strings.Contains(second, "reconcile result: PASS (0 change(s))") {
		t.Fatalf("expected idempotent second run, got:\n%s", second)
	}
}

func TestReconcileDoesNotRunCodexOrTouchAuth(t *testing.T) {
	app := newTestAppForCLI(t)
	if err := app.store.EnsureBaseDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	writeDefaultFileStoreConfig(t, app)
	profile, _, err := app.store.CreateProfile("work", nil)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	authPath := filepath.Join(profile.CodexHome, "auth.json")
	if err := os.WriteFile(authPath, []byte("unchanged auth\n"), 0o600); err != nil {
		t.Fatalf("write auth marker: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Profiles[profile.Name] = profile
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	fakeBin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "codex-called")
	script := "#!/bin/sh\ntouch " + shellQuote(marker) + "\nexit 99\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	if _, err := captureStdout(t, func() error { return app.Run([]string{"reconcile"}) }); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected codex not to run, stat err=%v", err)
	}
	got, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth marker: %v", err)
	}
	if string(got) != "unchanged auth\n" {
		t.Fatalf("auth changed: %q", got)
	}
}

func TestReconcileContinuesAfterProfileFailure(t *testing.T) {
	app := newTestAppForCLI(t)
	if err := app.store.EnsureBaseDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	writeDefaultFileStoreConfig(t, app)
	defaultSkills := filepath.Join(app.store.paths.DefaultCodexHome, "skills")
	if err := os.MkdirAll(filepath.Join(defaultSkills, "current"), 0o700); err != nil {
		t.Fatalf("mkdir default skill: %v", err)
	}

	cfg := DefaultConfig()
	for _, name := range []string{"alpha", "zeta"} {
		profileHome := filepath.Join(app.store.paths.ProfilesDir, name, "codex-home")
		if err := os.MkdirAll(profileHome, 0o700); err != nil {
			t.Fatalf("mkdir profile: %v", err)
		}
		cfg.Profiles[name] = Profile{Name: name, CodexHome: profileHome}
	}
	outsideSkills := filepath.Join(t.TempDir(), "outside-skills")
	if err := os.Mkdir(outsideSkills, 0o700); err != nil {
		t.Fatalf("mkdir outside skills: %v", err)
	}
	if err := os.Symlink(outsideSkills, filepath.Join(cfg.Profiles["alpha"].CodexHome, "skills")); err != nil {
		t.Fatalf("link unsafe profile skills: %v", err)
	}
	if err := app.store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	_, err := captureStdout(t, func() error { return app.Run([]string{"reconcile"}) })
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("expected partial failure, got %T (%v)", err, err)
	}
	assertLinkTarget(t, filepath.Join(cfg.Profiles["zeta"].CodexHome, "skills", "current"), filepath.Join(defaultSkills, "current"))
}

func TestReconcileWithNoProfiles(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error { return app.Run([]string{"reconcile"}) })
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !strings.Contains(out, "PASS (no profiles configured)") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestReconcileRejectsArguments(t *testing.T) {
	app := newTestAppForCLI(t)
	err := app.Run([]string{"reconcile", "work"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected usage error, got %T (%v)", err, err)
	}
}
