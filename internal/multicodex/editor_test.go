package multicodex

import (
	"strings"
	"testing"
)

func TestEditorPermanentlyRejectsNestedTmux(t *testing.T) {
	app := newTestAppForCLI(t)
	t.Setenv("TMUX", "/tmp/tmux-fixture,1,0")
	err := app.Run([]string{"editor"})
	if err == nil || !strings.Contains(err.Error(), "never runs inside tmux") {
		t.Fatalf("unexpected nested tmux result: %v", err)
	}
}

func TestInternalEditorHostIsNotShownInPublicHelp(t *testing.T) {
	app := newTestAppForCLI(t)
	out, err := captureStdout(t, func() error { return app.Run([]string{"help"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "__editor-host") {
		t.Fatalf("internal protocol command leaked into public help: %s", out)
	}
}
