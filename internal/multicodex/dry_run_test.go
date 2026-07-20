package multicodex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDryRunOverview(t *testing.T) {
	t.Parallel()

	paths := Paths{MulticodexHome: "/tmp/multi", ConfigPath: "/tmp/multi/config.json", DefaultCodexHome: "/tmp/codex"}
	store := NewStore(paths)
	cfg := DefaultConfig()
	cfg.Profiles["work"] = Profile{Name: "work", CodexHome: "/tmp/multi/profiles/work/codex-home"}

	text, err := RenderDryRun(store, cfg, nil)
	if err != nil {
		t.Fatalf("RenderDryRun: %v", err)
	}
	for _, want := range []string{"multicodex dry-run", "profile resources: omitted", "no guidance changes", "planned sequence:", "without persisting Codex sessions", "dry-run only:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output", want)
		}
	}
	if strings.Contains(text, "work") {
		t.Fatalf("overview should avoid printing profile names, got %q", text)
	}
}

func TestRenderDryRunShowsExplicitResourcesWithoutMutation(t *testing.T) {
	root := t.TempDir()
	guidance := filepath.Join(root, "guidance")
	skills := filepath.Join(root, "skills")
	for _, path := range []string{guidance, skills} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(Paths{ConfigPath: filepath.Join(root, "state", "config.json")})
	inherit := true
	sources := []string{skills}
	cfg := DefaultConfig()
	cfg.ProfileResources = &ProfileResources{
		Guidance: &GuidanceResources{Inherit: &inherit, Source: guidance},
		Skills:   &SkillResources{Inherit: &inherit, Sources: &sources},
	}
	text, err := RenderDryRun(store, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{guidance, skills, "preserving regular local guidance", "preserving regular local skills"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output: %s", want, text)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run mutated filesystem: %v", err)
	}
}

func TestRenderDryRunRejectsInvalidResourceSourceWithoutMutation(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Paths{ConfigPath: filepath.Join(root, "state", "config.json")})
	inherit := true
	sources := []string{"missing"}
	cfg := DefaultConfig()
	cfg.ProfileResources = &ProfileResources{Skills: &SkillResources{Inherit: &inherit, Sources: &sources}}
	_, err := RenderDryRun(store, cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing source error, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run mutated filesystem: %v", err)
	}
}

func TestRenderDryRunLoginUnknown(t *testing.T) {
	t.Parallel()

	store := NewStore(Paths{})
	cfg := DefaultConfig()
	_, err := RenderDryRun(store, cfg, []string{"login", "missing"})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRenderDryRunLoginShowsResourceReconciliation(t *testing.T) {
	t.Parallel()

	store := NewStore(Paths{})
	cfg := DefaultConfig()
	cfg.Profiles["work"] = Profile{Name: "work", CodexHome: "/tmp/work"}
	text, err := RenderDryRun(store, cfg, []string{"login", "work"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "would reconcile profile resources: no guidance changes; existing strict default skill reconciliation") {
		t.Fatalf("missing resource reconciliation: %s", text)
	}
}

func TestRenderDryRunRejectsUnsupportedOperation(t *testing.T) {
	t.Parallel()

	store := NewStore(Paths{})
	cfg := DefaultConfig()
	_, err := RenderDryRun(store, cfg, []string{"unsupported", "personal"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "usage: multicodex dry-run [operation]") {
		t.Fatalf("unexpected error: %v", err)
	}
}
