package multicodex

import (
	"strings"
	"testing"
)

func TestRenderShellExportsUsesSingleQuoteShellEscaping(t *testing.T) {
	out := RenderShellExports("/tmp/$(touch bad)'home", "work`bad`")
	if !strings.Contains(out, `'/tmp/$(touch bad)'"'"'home'`) {
		t.Fatalf("expected CODEX_HOME to be single-quote escaped, got %q", out)
	}
	if !strings.Contains(out, "'work`bad`'") {
		t.Fatalf("expected profile to be single-quote escaped, got %q", out)
	}
}
