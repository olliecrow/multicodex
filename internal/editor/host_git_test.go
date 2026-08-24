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

func TestInspectAdoptedSessionClassifiesEmptyTmuxReplyByExactID(t *testing.T) {
	for _, test := range []struct {
		name string
		ids  string
		want tmuxSessionState
	}{
		{name: "absent", ids: "$5\n", want: sessionAbsent},
		{name: "still present", ids: "$4\n", want: sessionAltered},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewHostService(privateTestHome(t), mustID(t))
			if err != nil {
				t.Fatal(err)
			}
			window := Window{
				ID: mustID(t), WorkspaceID: mustID(t), Name: "Terminal",
				Session: "ended-session", TmuxSessionID: "$4", Adopted: true,
			}
			service.runner = runnerFunc(func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "tmux" {
					t.Fatalf("unexpected command %q", name)
				}
				if slices.Contains(args, "display-message") {
					// tmux 3.4 on Linux can report success with no usable output
					// when queried by an adopted session ID.
					return nil, nil
				}
				if slices.Contains(args, "list-sessions") {
					return []byte(test.ids), nil
				}
				t.Fatalf("unexpected tmux arguments: %v", args)
				return nil, errors.New("unexpected call")
			})
			state, alive, err := service.inspectSession(context.Background(), window)
			if err != nil || state != test.want || alive {
				t.Fatalf("adopted session = %v, %v, %v; want %v", state, alive, err, test.want)
			}
		})
	}
}

func TestInspectCreatedSessionClassifiesEmptyTmuxReplyByExactName(t *testing.T) {
	for _, test := range []struct {
		name       string
		present    bool
		wantAbsent bool
	}{
		{name: "absent", wantAbsent: true},
		{name: "malformed live reply", present: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewHostService(privateTestHome(t), mustID(t))
			if err != nil {
				t.Fatal(err)
			}
			window := Window{ID: mustID(t), WorkspaceID: mustID(t), Name: "Terminal"}
			window.Session = service.sessionName(window.ID)
			service.runner = runnerFunc(func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "tmux" {
					t.Fatalf("unexpected command %q", name)
				}
				if slices.Contains(args, "display-message") {
					if !slices.Contains(args, "="+window.Session+":") {
						t.Fatalf("created session target is not exact: %v", args)
					}
					// tmux 3.4 can report success with no output for a missing
					// exact session name while the server has other sessions.
					return nil, nil
				}
				if slices.Contains(args, "has-session") {
					if !slices.Contains(args, "="+window.Session) {
						t.Fatalf("created session presence target is not exact: %v", args)
					}
					if test.present {
						return nil, nil
					}
					return nil, commandFailure{exitCode: 1}
				}
				t.Fatalf("unexpected tmux arguments: %v", args)
				return nil, errors.New("unexpected call")
			})
			state, alive, err := service.inspectSession(context.Background(), window)
			if test.wantAbsent {
				if err != nil || state != sessionAbsent || alive {
					t.Fatalf("absent created session = %v, %v, %v", state, alive, err)
				}
			} else if err == nil {
				t.Fatal("a malformed reply from a live created session was accepted")
			}
		})
	}
}

func TestAdoptedMarkerStateClassifiesEmptyTmuxReplyByExactID(t *testing.T) {
	for _, test := range []struct {
		name       string
		ids        string
		wantExists bool
	}{
		{name: "absent", ids: "$5\n", wantExists: false},
		{name: "still present", ids: "$4\n", wantExists: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewHostService(privateTestHome(t), mustID(t))
			if err != nil {
				t.Fatal(err)
			}
			window := Window{
				ID: mustID(t), WorkspaceID: mustID(t), Name: "Terminal",
				Session: "ended-session", TmuxSessionID: "$4", Adopted: true,
			}
			service.runner = runnerFunc(func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "tmux" {
					t.Fatalf("unexpected command %q", name)
				}
				if slices.Contains(args, "display-message") {
					return []byte("\t\t\t\t\n"), nil
				}
				if slices.Contains(args, "list-sessions") {
					return []byte(test.ids), nil
				}
				t.Fatalf("unexpected tmux arguments: %v", args)
				return nil, errors.New("unexpected call")
			})
			exists, exact, empty, _, err := service.adoptedMarkerState(context.Background(), window)
			if err != nil || exists != test.wantExists || exact || empty {
				t.Fatalf("adopted marker state = %v, %v, %v, %v; want exists %v", exists, exact, empty, err, test.wantExists)
			}
		})
	}
}

func TestSnapshotKeepsOtherHostStateWhenWindowDisappearsDuringCapture(t *testing.T) {
	home := privateTestHome(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{
		ID: mustID(t), ProjectID: mustID(t), ProjectPath: t.TempDir(), Name: "Work", Path: t.TempDir(),
		CreatedAt: time.Now().UTC(), LastUsedAt: time.Now().UTC(),
	}
	workspace.Path = workspace.ProjectPath
	window := Window{
		ID: mustID(t), WorkspaceID: workspace.ID, Name: "Terminal",
		CreatedAt: time.Now().UTC(), LastUsedAt: time.Now().UTC(),
	}
	window.Session = "mce-" + window.ID
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces = append(registry.Workspaces, workspace)
		registry.Windows = append(registry.Windows, window)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service.runner = runnerFunc(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" {
			t.Fatalf("unexpected command %q", name)
		}
		calls++
		switch calls {
		case 1:
			return []byte(window.Session + "\t" + service.store.instanceID + "\t" + window.ID + "\t" + workspace.ID + "\t\t0\t1\t1\t123\n"), nil
		case 2, 3:
			return nil, commandFailure{exitCode: 1}
		default:
			t.Fatalf("unexpected tmux call %d: %v", calls, args)
			return nil, errors.New("unexpected call")
		}
	})
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Windows) != 1 || snapshot.Windows[0].Alive || snapshot.Windows[0].Exited {
		t.Fatalf("disappeared window snapshot = %+v", snapshot.Windows)
	}
}

func TestFailedGitWorktreeAddRemovesEmptyOwnedDirectories(t *testing.T) {
	requireCommands(t, "git")
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	service.runner = runnerFunc(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "git" && slices.Contains(args, "add") && slices.Contains(args, "worktree") {
			return nil, commandFailure{exitCode: 1}
		}
		return (execRunner{}).run(ctx, name, args...)
	})
	projectID := mustID(t)
	if _, err := service.CreateWorkspace(context.Background(), CreateWorkspaceRequest{ProjectID: projectID, ProjectPath: project, Name: "Failed add"}); err == nil {
		t.Fatal("expected Git worktree creation to fail")
	}
	if _, err := os.Stat(filepath.Join(service.store.worktreeRoot, projectID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed worktree left empty owned directories: %v", err)
	}
	if err := service.store.withReadLock(func(registry hostRegistry) error {
		if len(registry.Workspaces) != 0 {
			t.Fatalf("failed worktree retained registry state: %+v", registry.Workspaces)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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

func TestExecRunnerHasNoControllingTerminal(t *testing.T) {
	requireCommands(t, "sh")
	if _, err := (execRunner{}).run(context.Background(), "sh", "-c", "if sh -c ': </dev/tty' 2>/dev/null; then exit 9; fi"); err != nil {
		t.Fatal("transient command could open the editor controlling terminal")
	}
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
	for _, required := range []string{
		"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_ASKPASS=", "SSH_ASKPASS=", "SSH_ASKPASS_REQUIRE=never",
		"GIT_SSH_COMMAND=ssh-custom",
	} {
		if strings.Count(gitEnvironment, required) != 1 {
			t.Fatalf("Git environment does not enforce %q exactly once: %s", required, gitEnvironment)
		}
	}
	if strings.Count(gitEnvironment, "GIT_TERMINAL_PROMPT=0") != 1 || strings.Count(gitEnvironment, "GCM_INTERACTIVE=Never") != 1 {
		t.Fatalf("Git environment permits an interactive credential prompt: %s", gitEnvironment)
	}
	tmuxEnvironment := strings.Join(sanitizedCommandEnvironment(environment, "tmux"), "|")
	if strings.Contains(tmuxEnvironment, "TMUX=") || strings.Contains(tmuxEnvironment, "TMUX_TMPDIR=") {
		t.Fatalf("tmux environment retained nesting overrides: %s", tmuxEnvironment)
	}
	sshEnvironment := strings.Join(sanitizedCommandEnvironment(append(environment,
		"CODEX_HOME=/private/codex", "CODEX_AUTH_TOKEN=secret", "OPENAI_API_KEY=secret",
		"MULTICODEX_HOME=/private/multicodex", "SSH_AUTH_SOCK=/private/agent"), "ssh"), "|")
	for _, removed := range []string{"CODEX_", "OPENAI_", "MULTICODEX_"} {
		if strings.Contains(sshEnvironment, removed) {
			t.Fatalf("SSH environment retained %q: %s", removed, sshEnvironment)
		}
	}
	if !strings.Contains(sshEnvironment, "SSH_AUTH_SOCK=/private/agent") || !strings.Contains(sshEnvironment, "PATH=/bin") {
		t.Fatalf("SSH environment removed required process state: %s", sshEnvironment)
	}
}

func TestEditorSSHCommandNeverInheritsAccountEnvironment(t *testing.T) {
	t.Setenv("CODEX_TEST_SECRET", "dummy")
	t.Setenv("OPENAI_TEST_SECRET", "dummy")
	t.Setenv("MULTICODEX_TEST_SECRET", "dummy")
	t.Setenv("SSH_AUTH_SOCK", "/private/test-agent")
	command := editorCommandContext(context.Background(), "ssh", "test-host")
	keys := map[string]string{}
	for _, entry := range command.Env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			keys[key] = value
		}
	}
	for _, removed := range []string{"CODEX_TEST_SECRET", "OPENAI_TEST_SECRET", "MULTICODEX_TEST_SECRET"} {
		if _, ok := keys[removed]; ok {
			t.Fatalf("SSH command retained %s", removed)
		}
	}
	if keys["SSH_AUTH_SOCK"] != "/private/test-agent" {
		t.Fatal("SSH command removed its authentication socket")
	}
}

func TestPrepareGitWorktreePreservesPreexistingGeneratedBranch(t *testing.T) {
	requireCommands(t, "git")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), testInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	id := "abcdef1234567890abcdef12"
	branch := "multicodex/collision-" + id[:8]
	runTestCommand(t, "git", "-C", project, "branch", branch, "HEAD")
	wantOID := commandOutput(t, "git", "-C", project, "rev-parse", branch)
	request := CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Collision"}
	if _, _, _, err := service.prepareGitWorktree(ctx, request, id); err == nil {
		t.Fatal("expected a pre-existing generated branch to stop workspace creation")
	}
	if got := commandOutput(t, "git", "-C", project, "rev-parse", branch); got != wantOID {
		t.Fatalf("pre-existing branch changed from %s to %s", wantOID, got)
	}
}

func TestFailedWorkspaceRollbackNeverDeletesBranchWithoutWorktreeProof(t *testing.T) {
	requireCommands(t, "git")
	ctx := context.Background()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, testInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	id := "abcdef1234567890abcdef12"
	projectID := mustID(t)
	branch := "multicodex/collision-" + id[:8]
	runTestCommand(t, "git", "-C", project, "branch", branch, "HEAD")
	wantOID := commandOutput(t, "git", "-C", project, "rev-parse", branch)
	commonDir, err := service.gitCommonDir(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	workspace := Workspace{
		ID: id, ProjectID: projectID, ProjectPath: project, Name: "Collision", Git: true,
		GitCommonDir: commonDir, Branch: branch, BaseRef: "HEAD",
		Path: filepath.Join(service.store.worktreeRoot, projectID, id), CreatedAt: now, LastUsedAt: now, CreatePending: true,
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces = append(registry.Workspaces, workspace)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service.rollbackWorkspaceCreation(workspace, false)
	if got := commandOutput(t, "git", "-C", project, "rev-parse", branch); got != wantOID {
		t.Fatalf("unproved branch changed from %s to %s", wantOID, got)
	}
	if err := service.store.withReadLock(func(registry hostRegistry) error {
		if len(registry.Workspaces) != 1 || registry.Workspaces[0].ID != id {
			t.Fatal("uncertain pending ownership record was removed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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
