package multicodex

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderShellExports(t *testing.T) {
	t.Parallel()

	out := RenderShellExports("/tmp/codex-home", "work")
	expected := "export CODEX_HOME=\"/tmp/codex-home\"\nexport MULTICODEX_ACTIVE_PROFILE=\"work\"\n"
	if out != expected {
		t.Fatalf("unexpected exports:\n%s", out)
	}
}

func TestRunInteractiveWithProfileExecsWhenTerminalAttached(t *testing.T) {
	oldLookPath := execLookPath
	oldSyscallExec := syscallExec
	oldInteractive := isInteractiveTerminalAttached
	t.Cleanup(func() {
		execLookPath = oldLookPath
		syscallExec = oldSyscallExec
		isInteractiveTerminalAttached = oldInteractive
	})

	isInteractiveTerminalAttached = func() bool { return true }
	execLookPath = func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("unexpected bin lookup: %s", bin)
		}
		return "/tmp/fake-codex", nil
	}

	var gotPath string
	var gotArgs []string
	var gotEnv []string
	sentinel := errors.New("exec called")
	syscallExec = func(path string, args []string, env []string) error {
		gotPath = path
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		return sentinel
	}

	err := RunInteractiveWithProfile("/tmp/codex-home", "work", "codex", []string{"--version"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if gotPath != "/tmp/fake-codex" {
		t.Fatalf("unexpected exec path: %q", gotPath)
	}
	if want := []string{"codex", "--version"}; strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected exec args: got=%q want=%q", gotArgs, want)
	}
	env := strings.Join(gotEnv, "\n")
	if !strings.Contains(env, "CODEX_HOME=/tmp/codex-home") {
		t.Fatalf("expected CODEX_HOME in env, got %q", env)
	}
	if !strings.Contains(env, "MULTICODEX_ACTIVE_PROFILE=work") {
		t.Fatalf("expected profile env in env, got %q", env)
	}
}
