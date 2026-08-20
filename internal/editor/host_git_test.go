package editor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type runnerFunc func(context.Context, string, ...string) ([]byte, error)

func (fn runnerFunc) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return fn(ctx, name, args...)
}

func TestGitRootFailsClosedForUnavailableOrMarkedRepository(t *testing.T) {
	project := t.TempDir()
	service := &HostService{runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, commandFailure{notFound: true}
	})}
	if _, _, err := service.gitRoot(context.Background(), project); err == nil {
		t.Fatal("expected unavailable Git to be rejected")
	}

	if err := os.Mkdir(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	service.runner = runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, commandFailure{exitCode: 128}
	})
	if _, _, err := service.gitRoot(context.Background(), project); err == nil {
		t.Fatal("expected an inaccessible marked Git repository to be rejected")
	}
}

func TestGitRootRecognizesPlainDirectoryAndRejectsUnsafeResults(t *testing.T) {
	project := t.TempDir()
	service := &HostService{runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, commandFailure{exitCode: 128}
	})}
	root, isGit, err := service.gitRoot(context.Background(), project)
	if err != nil || isGit || root != "" {
		t.Fatalf("plain directory = %q, %v, %v", root, isGit, err)
	}

	service.runner = runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("relative/path\n"), nil
	})
	if _, _, err := service.gitRoot(context.Background(), project); err == nil {
		t.Fatal("expected unsafe Git root output to be rejected")
	}

	calls := 0
	service.runner = runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, commandFailure{exitCode: 128}
		}
		return []byte("true\n"), nil
	})
	if _, _, err := service.gitRoot(context.Background(), project); err == nil {
		t.Fatal("expected a bare Git repository to be rejected")
	}
}

func TestGitRootHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := &HostService{runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("stopped")
	})}
	if _, _, err := service.gitRoot(ctx, t.TempDir()); err == nil {
		t.Fatal("expected cancellation to be reported")
	}
}

func TestExecRunnerCancellationKillsOwnedGrandchild(t *testing.T) {
	requireCommands(t, "sh", "sleep")
	pidPath := filepath.Join(t.TempDir(), "grandchild.pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := (execRunner{}).run(ctx, "sh", "-c", "sleep 30 & echo $! > "+quotePOSIX(pidPath)+"; wait")
		done <- err
	}()
	var grandchild int
	waitUntil(t, time.Second, func() bool {
		contents, err := os.ReadFile(pidPath)
		if err != nil {
			return false
		}
		grandchild, err = strconv.Atoi(strings.TrimSpace(string(contents)))
		return err == nil && grandchild > 0
	})
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled command succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled command did not stop")
	}
	waitUntil(t, 3*time.Second, func() bool {
		return errors.Is(syscall.Kill(grandchild, 0), syscall.ESRCH)
	})
}

func TestCommandEnvironmentRemovesRepositoryAndTmuxOverrides(t *testing.T) {
	environment := []string{
		"PATH=/bin", "GIT_DIR=/tmp/wrong", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/tmp/hooks", "GIT_SSH_COMMAND=ssh-custom", "GIT_TERMINAL_PROMPT=1",
		"GCM_INTERACTIVE=Always", "TMUX=wrong", "TMUX_TMPDIR=/tmp/wrong",
	}
	gitEnvironment := strings.Join(sanitizedCommandEnvironment(environment, "/usr/bin/git"), "|")
	for _, removed := range []string{"GIT_DIR=", "GIT_CONFIG_COUNT=", "GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"} {
		if strings.Contains(gitEnvironment, removed) {
			t.Fatalf("Git environment retained %q: %s", removed, gitEnvironment)
		}
	}
	if !strings.Contains(gitEnvironment, "GIT_SSH_COMMAND=ssh-custom") {
		t.Fatalf("Git environment removed the configured SSH transport: %s", gitEnvironment)
	}
	if strings.Count(gitEnvironment, "GIT_TERMINAL_PROMPT=0") != 1 || strings.Count(gitEnvironment, "GCM_INTERACTIVE=Never") != 1 {
		t.Fatalf("Git environment permits an interactive credential prompt: %s", gitEnvironment)
	}
	tmuxEnvironment := strings.Join(sanitizedCommandEnvironment(environment, "tmux"), "|")
	if strings.Contains(tmuxEnvironment, "TMUX=") || strings.Contains(tmuxEnvironment, "TMUX_TMPDIR=") {
		t.Fatalf("tmux environment retained nesting overrides: %s", tmuxEnvironment)
	}
}

func TestRemoveGitWorktreePreservesBranchAdvancedDuringSafetyCheck(t *testing.T) {
	requireCommands(t, "git")
	ctx := context.Background()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{
		ProjectID: mustID(t), ProjectPath: project, Name: "Branch race",
	})
	if err != nil {
		t.Fatal(err)
	}
	baseOID := commandOutput(t, "git", "-C", project, "rev-parse", workspace.BaseRef)
	treeOID := commandOutput(t, "git", "-C", project, "rev-parse", workspace.BaseRef+"^{tree}")
	advancedOID := commandOutput(t, "git", "-C", project, "commit-tree", treeOID, "-p", baseOID, "-m", "concurrent work")

	realRunner := execRunner{}
	advanced := false
	service.runner = runnerFunc(func(callCtx context.Context, name string, args ...string) ([]byte, error) {
		if name == "git" && !advanced && slices.Contains(args, "rev-list") {
			advanced = true
			if _, updateErr := realRunner.run(callCtx, "git", "-C", project, "update-ref", "refs/heads/"+workspace.Branch, advancedOID); updateErr != nil {
				return nil, updateErr
			}
		}
		return realRunner.run(callCtx, name, args...)
	})
	if err := service.removeGitWorktree(ctx, workspace, false); err == nil {
		t.Fatal("expected a concurrent branch advance to prevent deletion")
	}
	if !advanced {
		t.Fatal("test did not advance the branch during the safety check")
	}
	if got := commandOutput(t, "git", "-C", project, "rev-parse", "refs/heads/"+workspace.Branch); got != advancedOID {
		t.Fatalf("advanced branch changed to %q, want %q", got, advancedOID)
	}
}
