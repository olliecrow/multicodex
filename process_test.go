package main

import "testing"

func TestRenderShellExports(t *testing.T) {
	t.Parallel()

	out := RenderShellExports("/tmp/codex-home", "work")
	expected := "export CODEX_HOME=\"/tmp/codex-home\"\nexport MULTICODEX_ACTIVE_PROFILE=\"work\"\n"
	if out != expected {
		t.Fatalf("unexpected exports:\n%s", out)
	}
}
