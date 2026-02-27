package multicodex

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDryRunOverview(t *testing.T) {
	t.Parallel()

	paths := Paths{MulticodexHome: "/tmp/multi", DefaultCodexHome: "/tmp/codex", DefaultAuthPath: "/tmp/codex/auth.json", BackupsDir: "/tmp/multi/backups"}
	store := NewStore(paths)
	cfg := DefaultConfig()
	cfg.Profiles["work"] = Profile{Name: "work", CodexHome: "/tmp/multi/profiles/work/codex-home"}

	text, err := RenderDryRun(store, cfg, nil)
	if err != nil {
		t.Fatalf("RenderDryRun: %v", err)
	}
	for _, want := range []string{"multicodex dry-run", "planned sequence:", "dry-run only:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output", want)
		}
	}
}

func TestRenderDryRunUseUnknown(t *testing.T) {
	t.Parallel()

	store := NewStore(Paths{})
	cfg := DefaultConfig()
	_, err := RenderDryRun(store, cfg, []string{"use", "missing"})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRenderDryRunSwitchGlobal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	paths := Paths{
		MulticodexHome:   filepath.Join(root, "multi"),
		BackupsDir:       filepath.Join(root, "multi", "backups"),
		DefaultCodexHome: filepath.Join(root, "codex"),
		DefaultAuthPath:  filepath.Join(root, "codex", "auth.json"),
	}
	store := NewStore(paths)
	cfg := DefaultConfig()
	cfg.Profiles["personal"] = Profile{Name: "personal", CodexHome: filepath.Join(root, "multi", "profiles", "personal", "codex-home")}

	text, err := RenderDryRun(store, cfg, []string{"switch-global", "personal"})
	if err != nil {
		t.Fatalf("RenderDryRun: %v", err)
	}
	for _, want := range []string{"would do:", "create symlink", "dry-run only:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output", want)
		}
	}
}
