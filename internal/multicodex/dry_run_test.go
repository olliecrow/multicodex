package multicodex

import (
	"strings"
	"testing"
)

func TestRenderDryRunOverview(t *testing.T) {
	t.Parallel()

	paths := Paths{MulticodexHome: "/tmp/multi", DefaultCodexHome: "/tmp/codex"}
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

func TestRenderDryRunRejectsUnsupportedOperation(t *testing.T) {
	t.Parallel()

	store := NewStore(Paths{})
	cfg := DefaultConfig()
	_, err := RenderDryRun(store, cfg, []string{"unsupported", "personal"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "usage: multicodex dry-run [use|login|run]") {
		t.Fatalf("unexpected error: %v", err)
	}
}
