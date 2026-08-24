package editor

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const testInstanceID = "0123456789abcdef01234567"

func TestPaneActivityHashIncludesTmuxActivityTime(t *testing.T) {
	capture := []byte("the same repeated terminal rows")
	first := paneActivityHash(capture, "100")
	if first != paneActivityHash(capture, "100") {
		t.Fatal("stable pane state changed its activity hash")
	}
	if first == paneActivityHash(capture, "101") {
		t.Fatal("new tmux activity with identical captured rows kept the old hash")
	}
}

func TestSnapshotDetectsRepeatedIdenticalOutputActivity(t *testing.T) {
	requireCommands(t, "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	opened, err := service.OpenProjectWindow(ctx, OpenProjectWindowRequest{ProjectID: mustID(t), ProjectPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	command := "i=0; while [ $i -lt 200 ]; do printf 'same output\\n'; i=$((i+1)); done; while :; do printf 'same output\\n'; sleep 1; done"
	if _, err := service.tmux(ctx, "respawn-pane", "-k", "-t", service.tmuxTarget(opened.Window), command); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		history, err := service.tmux(ctx, "display-message", "-p", "-t", service.tmuxTarget(opened.Window), "#{history_size}")
		if err != nil {
			t.Fatal(err)
		}
		size, err := strconv.Atoi(strings.TrimSpace(string(history)))
		if err == nil && size >= 150 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("repeated terminal output did not fill the sampled history")
		}
		time.Sleep(20 * time.Millisecond)
	}
	first, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstCapture, err := service.tmux(ctx, "capture-pane", "-p", "-J", "-S", "-"+strconv.Itoa(activityRows), "-t", service.tmuxTarget(opened.Window))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	second, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondCapture, err := service.tmux(ctx, "capture-pane", "-p", "-J", "-S", "-"+strconv.Itoa(activityRows), "-t", service.tmuxTarget(opened.Window))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCapture, secondCapture) {
		t.Fatal("repeated-output fixture did not keep the sampled terminal rows identical")
	}
	if len(first.Windows) != 1 || len(second.Windows) != 1 || first.Windows[0].PaneHash == second.Windows[0].PaneHash {
		t.Fatalf("repeated identical output was not detected: first=%+v second=%+v", first.Windows, second.Windows)
	}
}

func TestProjectTerminalUsesOriginalDirectoryAndIsIdempotent(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	request := OpenProjectWindowRequest{ProjectID: mustID(t), ProjectPath: project}
	before := commandOutput(t, "git", "-C", project, "worktree", "list", "--porcelain")
	first, err := service.OpenProjectWindow(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Window.WorkspaceID != "" || first.Window.ProjectID != request.ProjectID || first.Window.ProjectPath != project || first.Window.Name != projectWindowName {
		t.Fatalf("project terminal = %+v", first)
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "display-message", "-p", "-t", first.Window.Session, "#{pane_current_path}"); !sameDirectory(got, project) {
		t.Fatalf("project terminal directory = %q, want %q", got, project)
	}
	second, err := service.OpenProjectWindow(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Window.ID != first.Window.ID || second.Window.Session != first.Window.Session {
		t.Fatalf("second project terminal = %+v, want reuse of %+v", second, first)
	}
	if _, err := service.tmux(ctx, "kill-session", "-t", first.Window.Session); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.OpenProjectWindow(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Created || recovered.Window.ID != first.Window.ID {
		t.Fatalf("recovered project terminal = %+v, want same registry identity", recovered)
	}
	if after := commandOutput(t, "git", "-C", project, "worktree", "list", "--porcelain"); after != before {
		t.Fatalf("project terminal changed Git worktrees:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	attachment, err := service.PutAttachment(ctx, PutAttachmentRequest{ProjectID: request.ProjectID, Extension: ".txt", Data: []byte("project terminal")})
	if err != nil {
		t.Fatal(err)
	}
	if attachment.WorkspaceID != "" || attachment.ProjectID != request.ProjectID {
		t.Fatalf("project terminal attachment = %+v", attachment)
	}
	if deleted, err := service.DeleteWindow(ctx, DeleteRequest{ID: first.Window.ID, Force: true}); err != nil || !deleted.Deleted {
		t.Fatalf("delete project terminal = %+v, %v", deleted, err)
	}
	if _, err := os.Stat(attachment.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project terminal attachment remained after deletion: %v", err)
	}
}

func TestHostServiceGitWindowReconnectAndSafeDeletion(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)

	projectID := mustID(t)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{
		ProjectID: projectID, ProjectPath: project, Name: "Fix parser",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.Git || workspace.Path == project || !strings.HasPrefix(workspace.Branch, "multicodex/fix-parser-") {
		t.Fatalf("unexpected workspace: %+v", workspace)
	}
	if got := commandOutput(t, "git", "-C", project, "branch", "--show-current"); got != "main" {
		t.Fatalf("source branch changed to %q", got)
	}
	if got := commandOutput(t, "git", "-C", workspace.Path, "branch", "--show-current"); got != workspace.Branch {
		t.Fatalf("worktree branch = %q, want %q", got, workspace.Branch)
	}

	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if window.Name != defaultWindowName || window.Session != "mce-"+window.ID {
		t.Fatalf("non-deterministic session: %+v", window)
	}
	secondWindow, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if secondWindow.Name != "Terminal 2" {
		t.Fatalf("second automatic window name = %q", secondWindow.Name)
	}
	if err := service.RenameWindow(ctx, RenameRequest{ID: secondWindow.ID, Name: window.Name}); err == nil {
		t.Fatal("duplicate window rename was accepted")
	}
	if deleted, err := service.DeleteWindow(ctx, DeleteRequest{ID: secondWindow.ID, Force: true}); err != nil || !deleted.Deleted {
		t.Fatalf("delete second automatic window = %+v, %v", deleted, err)
	}
	branch := workspace.Branch
	if err := service.RenameWorkspace(ctx, RenameRequest{ID: workspace.ID, Name: "Parser work"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RenameWindow(ctx, RenameRequest{ID: window.ID, Name: "Main terminal"}); err != nil {
		t.Fatal(err)
	}
	renamed, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(renamed.Workspaces) != 1 || renamed.Workspaces[0].Name != "Parser work" || renamed.Workspaces[0].Branch != branch || len(renamed.Windows) != 1 || renamed.Windows[0].Name != "Main terminal" {
		t.Fatalf("display rename changed ownership identity: %+v", renamed)
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "show-options", "-g", "-v", "history-limit"); got != "60000" {
		t.Fatalf("history-limit = %q", got)
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "show-options", "-g", "-v", "set-clipboard"); got != "off" {
		t.Fatalf("set-clipboard = %q", got)
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "show-options", "-g", "-v", "mouse"); got != "on" {
		t.Fatalf("mouse = %q, want on", got)
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "show-options", "-g", "-v", "mode-keys"); got != "emacs" {
		t.Fatalf("mode-keys = %q, want emacs", got)
	}
	copyKeys := commandOutput(t, "tmux", "-L", service.socketName(), "list-keys", "-T", "copy-mode")
	for _, want := range []string{"M-Up", "send-keys -X page-up", "M-Down", "send-keys -X page-down"} {
		if !strings.Contains(copyKeys, want) {
			t.Fatalf("copy-mode keys omit %q", want)
		}
	}
	for _, option := range []string{"prefix", "prefix2"} {
		if got := commandOutput(t, "tmux", "-L", service.socketName(), "show-options", "-g", "-v", option); got != "None" {
			t.Fatalf("%s = %q, want None", option, got)
		}
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "show-options", "-s", "-v", "extended-keys"); got != "on" {
		t.Fatalf("extended-keys = %q, want on", got)
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "show-options", "-s", "-v", "terminal-features[99]"); got != "xterm-256color:extkeys" {
		t.Fatalf("terminal-features[99] = %q", got)
	}
	if panes := strings.Fields(commandOutput(t, "tmux", "-L", service.socketName(), "list-panes", "-t", window.Session, "-F", "#{pane_id}")); len(panes) != 1 {
		t.Fatalf("managed session has %d panes, want 1", len(panes))
	}
	if windows := strings.Fields(commandOutput(t, "tmux", "-L", service.socketName(), "list-windows", "-t", window.Session, "-F", "#{window_id}")); len(windows) != 1 {
		t.Fatalf("managed session has %d tmux windows, want 1", len(windows))
	}
	keys, keysErr := service.tmux(ctx, "list-keys", "-T", "prefix")
	if keysErr == nil && len(bytes.TrimSpace(keys)) != 0 {
		t.Fatalf("managed tmux prefix table is not empty: %s", keys)
	}
	if keysErr != nil {
		var failure commandFailure
		if !errors.As(keysErr, &failure) || failure.exitCode != 1 || failure.notFound {
			t.Fatalf("inspect empty tmux prefix table: %v", keysErr)
		}
	}

	attachment, err := attachWindowPTY(ctx, Host{ID: localHostID, Name: localHostName}, "", service.store.instanceID, window, 80, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := attachment.SendKey(tea.KeyPressMsg{Code: 'p', Text: "printf 'EDITOR_%s\\n' READY"}); err != nil {
		t.Fatal(err)
	}
	if err := attachment.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	waitForRender(t, attachment, "EDITOR_READY", 3*time.Second)
	if err := attachment.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Windows) != 1 || !snapshot.Windows[0].Alive || snapshot.Windows[0].PaneHash == "" {
		t.Fatalf("unexpected snapshot after detach: %+v", snapshot)
	}
	reconnected, err := attachWindowPTY(ctx, Host{ID: localHostID, Name: localHostName}, "", service.store.instanceID, window, 80, 20)
	if err != nil {
		t.Fatal(err)
	}
	waitForRender(t, reconnected, "EDITOR_READY", 3*time.Second)
	if err := reconnected.SendMouse(tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelUp}, 10, 10); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := commandOutput(t, "tmux", "-L", service.socketName(), "display-message", "-p", "-t", window.Session, "#{pane_in_mode}"); got == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mouse wheel did not open tmux copy mode")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := reconnected.SendKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); err != nil {
		t.Fatal(err)
	}
	if err := service.CopyMode(ctx, window.ID); err != nil {
		t.Fatal(err)
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "display-message", "-p", "-t", window.Session, "#{pane_in_mode}"); got != "1" {
		t.Fatalf("copy mode action state = %q, want 1", got)
	}
	if err := reconnected.SendKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); err != nil {
		t.Fatal(err)
	}
	_ = reconnected.Close()
	attachContext, cancelAttach := context.WithCancel(ctx)
	canceledAttachment, err := attachWindowPTY(attachContext, Host{ID: localHostID, Name: localHostName}, "", service.store.instanceID, window, 80, 20)
	if err != nil {
		t.Fatal(err)
	}
	cancelAttach()
	select {
	case <-canceledAttachment.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("terminal attachment did not stop after lifecycle cancellation")
	}
	select {
	case <-canceledAttachment.inputDone:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal input worker did not stop after lifecycle cancellation")
	}
	if err := canceledAttachment.SendKey(tea.KeyPressMsg{Code: 'x', Text: "x"}); err == nil {
		t.Fatal("canceled terminal attachment still accepts input")
	}
	if err := canceledAttachment.Close(); err != nil {
		t.Fatal(err)
	}
	if owned, err := service.ownsSession(ctx, window); err != nil || !owned {
		t.Fatalf("attachment cancellation changed the tmux session: owned=%v err=%v", owned, err)
	}

	refused, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID})
	if err != nil {
		t.Fatal(err)
	}
	if refused.Deleted || !refused.Forceable || !strings.Contains(refused.Reason, "live process") {
		t.Fatalf("expected live deletion refusal, got %+v", refused)
	}
	blockedWorkspace, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID})
	if err != nil || blockedWorkspace.Deleted || !blockedWorkspace.Forceable || !strings.Contains(blockedWorkspace.Reason, "1 live terminal window") {
		t.Fatalf("workspace-with-window deletion did not offer one clear force confirmation: %+v, %v", blockedWorkspace, err)
	}
	deletedWorkspace, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true})
	if err != nil || !deletedWorkspace.Deleted {
		t.Fatalf("force delete workspace and its window = %+v, %v", deletedWorkspace, err)
	}
	if _, err := os.Lstat(service.tmuxSocketPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unused editor tmux socket remains: %v", err)
	}
	if _, err := os.Stat(workspace.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists or has uncertain state: %v", err)
	}
	if out, err := exec.Command("git", "-C", project, "show-ref", "--verify", "refs/heads/"+workspace.Branch).CombinedOutput(); err == nil {
		t.Fatalf("owned branch still exists: %s", out)
	}
}

func TestGitWorkspaceLockAllowsBranchChangesAndPreservesExtraBranches(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Flexible branches"})
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.WorktreeLocked {
		t.Fatalf("new worktree is not recorded as locked: %+v", workspace)
	}
	locked, reason, err := service.gitWorktreeLock(ctx, workspace)
	if err != nil || !locked || reason != service.worktreeLockReason(workspace) {
		t.Fatalf("worktree lock = %v, %q, %v", locked, reason, err)
	}
	if out, err := exec.Command("git", "-C", project, "worktree", "remove", "--force", workspace.Path).CombinedOutput(); err == nil {
		t.Fatalf("external removal bypassed the editor lock: %s", out)
	}
	extraBranch := "feature/second-pr"
	runTestCommand(t, "git", "-C", workspace.Path, "checkout", "-b", extraBranch)
	if _, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID}); err != nil {
		t.Fatalf("create window after branch change: %v", err)
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil || len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].Unavailable {
		t.Fatalf("snapshot after branch change = %+v, %v", snapshot, err)
	}
	deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true})
	if err != nil || !deleted.Deleted {
		t.Fatalf("delete flexible workspace = %+v, %v", deleted, err)
	}
	if out, err := exec.Command("git", "-C", project, "show-ref", "--verify", "refs/heads/"+workspace.Branch).CombinedOutput(); err == nil {
		t.Fatalf("initial editor branch remains: %s", out)
	}
	if out, err := exec.Command("git", "-C", project, "show-ref", "--verify", "refs/heads/"+extraBranch).CombinedOutput(); err != nil {
		t.Fatalf("user-created branch was removed: %v: %s", err, out)
	}
}

func TestExistingOwnedWorktreeIsLockedOnSnapshot(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Lock migration"})
	if err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", project, "worktree", "unlock", workspace.Path)
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].WorktreeLocked = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.store.withReadLock(func(registry hostRegistry) error {
		if len(registry.Workspaces) != 1 || !registry.Workspaces[0].WorktreeLocked {
			t.Fatalf("migration did not record lock: %+v", registry.Workspaces)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	workspace.WorktreeLocked = true
	if locked, reason, err := service.gitWorktreeLock(ctx, workspace); err != nil || !locked || reason != service.worktreeLockReason(workspace) {
		t.Fatalf("migrated lock = %v, %q, %v", locked, reason, err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup migrated workspace: %+v, %v", result, err)
	}
}

func TestWorkspaceDeletionRefusesAChangedWorktreeLock(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Changed lock"})
	if err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", project, "worktree", "unlock", workspace.Path)
	runTestCommand(t, "git", "-C", project, "worktree", "lock", "--reason", "someone-else", workspace.Path)
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true})
	if err == nil || result.Deleted || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("changed lock did not block deletion: %+v, %v", result, err)
	}
	if _, statErr := os.Stat(workspace.Path); statErr != nil {
		t.Fatalf("changed-lock worktree was removed: %v", statErr)
	}
	runTestCommand(t, "git", "-C", project, "worktree", "unlock", workspace.Path)
	if err := service.ensureGitWorktreeLock(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup restored lock workspace: %+v, %v", result, err)
	}
}

func TestWorkspaceDeletionPreservesInitialBranchUsedElsewhere(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Shared initial branch"})
	if err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", workspace.Path, "checkout", "-b", "feature/current")
	other := filepath.Join(t.TempDir(), "other-worktree")
	runTestCommand(t, "git", "-C", project, "worktree", "add", other, workspace.Branch)
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true})
	if err == nil || result.Deleted || !strings.Contains(err.Error(), "checked out in another worktree") {
		t.Fatalf("branch used elsewhere was not preserved: %+v, %v", result, err)
	}
	if _, statErr := os.Stat(workspace.Path); statErr != nil {
		t.Fatalf("blocked deletion removed the owned worktree: %v", statErr)
	}
	runTestCommand(t, "git", "-C", project, "worktree", "remove", other)
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup workspace after branch release: %+v, %v", result, err)
	}
}

func TestAdoptedTmuxSessionIsPreservedThroughCleanupAndRelease(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	service.systemSocket = "mce-adopt-" + mustID(t)[:12]
	defer killTestSystemServer(service)
	session := "existing-session"
	secondSession := "existing-session-2"
	runTestCommand(t, "tmux", "-L", service.systemSocket, "new-session", "-d", "-s", session, "-c", project)
	runTestCommand(t, "tmux", "-L", service.systemSocket, "new-session", "-d", "-s", secondSession, "-c", project)
	pidBefore := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", session, "#{pane_pid}")
	secondPIDBefore := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", secondSession, "#{pane_pid}")
	projectID := mustID(t)
	candidates, err := service.ListTmuxSessions(ctx, ListTmuxSessionsRequest{ProjectID: projectID, ProjectPath: project})
	if err != nil || len(candidates) != 2 {
		t.Fatalf("adoption candidates = %+v, %v", candidates, err)
	}
	adopted, err := service.AdoptTmuxSession(ctx, AdoptTmuxSessionRequest{ProjectID: projectID, ProjectPath: project, WorkspaceName: "Shared checkout", Session: session})
	if err != nil {
		t.Fatal(err)
	}
	if !adopted.Workspace.External || !adopted.Workspace.Git || !adopted.Window.Adopted || adopted.Window.Session != session {
		t.Fatalf("unexpected adoption result: %+v", adopted)
	}
	if pidAfter := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", adopted.Window.TmuxSessionID, "#{pane_pid}"); pidAfter != pidBefore {
		t.Fatalf("adoption restarted pane: %q -> %q", pidBefore, pidAfter)
	}
	if candidates, err := service.ListTmuxSessions(ctx, ListTmuxSessionsRequest{ProjectID: projectID, ProjectPath: project}); err != nil || len(candidates) != 1 || candidates[0].Name != secondSession {
		t.Fatalf("adopted session remained eligible: %+v, %v", candidates, err)
	}
	second, err := service.AdoptTmuxSession(ctx, AdoptTmuxSessionRequest{ProjectID: projectID, ProjectPath: project, WorkspaceName: "Shared checkout", Session: secondSession})
	if err != nil || second.Workspace.ID != adopted.Workspace.ID || second.Window.Name != "Terminal 2" {
		t.Fatalf("second adoption did not reuse its workspace: %+v, %v", second, err)
	}
	if secondPIDAfter := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", second.Window.TmuxSessionID, "#{pane_pid}"); secondPIDAfter != secondPIDBefore {
		t.Fatalf("second adoption restarted pane: %q -> %q", secondPIDBefore, secondPIDAfter)
	}
	service.now = func() time.Time { return adopted.Window.LastUsedAt.Add(cleanupAfter + time.Hour) }
	cleanup, err := service.Cleanup(ctx)
	if err != nil || cleanup.WindowsDeleted != 0 || cleanup.WorkspacesDeleted != 0 {
		t.Fatalf("cleanup changed adopted resources: %+v, %v", cleanup, err)
	}
	if state, alive, err := service.inspectSession(ctx, adopted.Window); err != nil || state != sessionOwned || !alive {
		t.Fatalf("adopted session after cleanup = %v, %v, %v", state, alive, err)
	}
	runTestCommand(t, "tmux", "-L", service.systemSocket, "set-environment", "-t", adopted.Window.TmuxSessionID, "MCE_WINDOW", mustID(t))
	if deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: adopted.Workspace.ID}); err != nil || deleted.Deleted || !strings.Contains(deleted.Reason, "changed or uncertain ownership") {
		t.Fatalf("changed adopted marker did not block release: %+v, %v", deleted, err)
	}
	if pidAfter := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", adopted.Window.TmuxSessionID, "#{pane_pid}"); pidAfter != pidBefore {
		t.Fatalf("failed release changed original pane: %q -> %q", pidBefore, pidAfter)
	}
	runTestCommand(t, "tmux", "-L", service.systemSocket, "set-environment", "-t", adopted.Window.TmuxSessionID, "MCE_WINDOW", adopted.Window.ID)
	deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: adopted.Workspace.ID})
	if err != nil || !deleted.Deleted {
		t.Fatalf("delete preserved workspace = %+v, %v", deleted, err)
	}
	if pidAfter := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", session, "#{pane_pid}"); pidAfter != pidBefore {
		t.Fatalf("release changed original pane: %q -> %q", pidBefore, pidAfter)
	}
	if pidAfter := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", secondSession, "#{pane_pid}"); pidAfter != secondPIDBefore {
		t.Fatalf("release changed second pane: %q -> %q", secondPIDBefore, pidAfter)
	}
	if markers := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", session, "#{MCE_INSTANCE}|#{MCE_WINDOW}|#{MCE_WORKSPACE}"); markers != "||" {
		t.Fatalf("release retained editor markers: %q", markers)
	}
	if markers := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", secondSession, "#{MCE_INSTANCE}|#{MCE_WINDOW}|#{MCE_WORKSPACE}"); markers != "||" {
		t.Fatalf("release retained second editor markers: %q", markers)
	}
	if info, err := os.Stat(project); err != nil || !info.IsDir() {
		t.Fatalf("release removed preserved project: %v", err)
	}
	runTestCommand(t, "tmux", "-L", service.systemSocket, "kill-server")
	if candidates, err := service.ListTmuxSessions(ctx, ListTmuxSessionsRequest{ProjectID: projectID, ProjectPath: project}); err != nil || len(candidates) != 0 {
		t.Fatalf("inactive default tmux server did not produce an empty list: %+v, %v", candidates, err)
	}
}

func TestCleanupRollsBackCompatiblePartialAdoptionMarkers(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	service.systemSocket = "mce-partial-" + mustID(t)[:12]
	defer killTestSystemServer(service)
	session := "partial-adoption"
	runTestCommand(t, "tmux", "-L", service.systemSocket, "new-session", "-d", "-s", session, "-c", project)
	adopted, err := service.AdoptTmuxSession(ctx, AdoptTmuxSessionRequest{
		ProjectID: mustID(t), ProjectPath: project, WorkspaceName: "Shared checkout", Session: session,
	})
	if err != nil {
		t.Fatal(err)
	}
	panePID := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", adopted.Window.TmuxSessionID, "#{pane_pid}")
	runTestCommand(t, "tmux", "-L", service.systemSocket, "set-environment", "-u", "-t", adopted.Window.TmuxSessionID, "MCE_WORKSPACE")
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Windows[0].CreatePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := service.Cleanup(ctx); err != nil || result.WindowsDeleted != 0 {
		t.Fatalf("partial adoption recovery = %+v, %v", result, err)
	}
	if err := service.store.withReadLock(func(registry hostRegistry) error {
		if len(registry.Windows) != 0 || len(registry.Workspaces) != 1 {
			t.Fatalf("partial adoption records = %+v", registry)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if markers := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", adopted.Window.TmuxSessionID, "#{MCE_INSTANCE}|#{MCE_WINDOW}|#{MCE_WORKSPACE}|#{MCE_PROJECT}"); markers != "|||" {
		t.Fatalf("partial adoption markers remain: %q", markers)
	}
	if got := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", adopted.Window.TmuxSessionID, "#{pane_pid}"); got != panePID {
		t.Fatalf("partial adoption recovery changed pane %q to %q", panePID, got)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: adopted.Workspace.ID}); err != nil || !result.Deleted {
		t.Fatalf("remove recovered preserved workspace = %+v, %v", result, err)
	}
}

func TestCleanupCannotRaceTmuxAdoption(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	service.systemSocket = "mce-serial-" + mustID(t)[:12]
	defer killTestSystemServer(service)
	session := "serialized-adoption"
	runTestCommand(t, "tmux", "-L", service.systemSocket, "new-session", "-d", "-s", session, "-c", project)
	started := make(chan struct{})
	release := make(chan struct{})
	blocked := false
	delegate := execRunner{}
	service.runner = runnerFunc(func(commandContext context.Context, name string, args ...string) ([]byte, error) {
		isMarkerWrite := false
		for _, arg := range args {
			if arg == "set-environment" {
				isMarkerWrite = true
				break
			}
		}
		if !blocked && name == "tmux" && isMarkerWrite {
			blocked = true
			close(started)
			select {
			case <-release:
			case <-commandContext.Done():
				return nil, commandContext.Err()
			}
		}
		return delegate.run(commandContext, name, args...)
	})
	type adoptionResult struct {
		adopted AdoptedTmuxSession
		err     error
	}
	result := make(chan adoptionResult, 1)
	projectID := mustID(t)
	go func() {
		adopted, err := service.AdoptTmuxSession(ctx, AdoptTmuxSessionRequest{
			ProjectID: projectID, ProjectPath: project, WorkspaceName: "Shared checkout", Session: session,
		})
		result <- adoptionResult{adopted: adopted, err: err}
	}()
	select {
	case <-started:
	case adoption := <-result:
		t.Fatalf("adoption ended before its marker write: %v", adoption.err)
	case <-time.After(5 * time.Second):
		t.Fatal("adoption did not reach its marker write")
	}
	cleanupContext, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := service.Cleanup(cleanupContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup entered an active adoption: %v", err)
	}
	close(release)
	adoption := <-result
	if adoption.err != nil || adoption.adopted.Window.CreatePending {
		t.Fatalf("serialized adoption = %+v, %v", adoption.adopted, adoption.err)
	}
	if deleted, err := service.DeleteWindow(ctx, DeleteRequest{ID: adoption.adopted.Window.ID}); err != nil || !deleted.Deleted {
		t.Fatalf("release serialized adoption = %+v, %v", deleted, err)
	}
	if deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: adoption.adopted.Workspace.ID}); err != nil || !deleted.Deleted {
		t.Fatalf("remove serialized workspace = %+v, %v", deleted, err)
	}
}

func TestReleaseDoesNotTouchAReusedTmuxSessionName(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	service.systemSocket = "mce-reuse-" + mustID(t)[:12]
	defer killTestSystemServer(service)
	session := "reused-name"
	runTestCommand(t, "tmux", "-L", service.systemSocket, "new-session", "-d", "-s", session, "-c", project)
	projectID := mustID(t)
	adopted, err := service.AdoptTmuxSession(ctx, AdoptTmuxSessionRequest{ProjectID: projectID, ProjectPath: project, WorkspaceName: "Shared checkout", Session: session})
	if err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "tmux", "-L", service.systemSocket, "kill-session", "-t", adopted.Window.TmuxSessionID)
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot after adopted session exit: %v", err)
	}
	if len(snapshot.Windows) != 1 || snapshot.Windows[0].ID != adopted.Window.ID || snapshot.Windows[0].Alive {
		t.Fatalf("ended adopted session snapshot = %+v", snapshot.Windows)
	}
	runTestCommand(t, "tmux", "-L", service.systemSocket, "new-session", "-d", "-s", session, "-c", project)
	newPID := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", session, "#{pane_pid}")
	if result, err := service.DeleteWindow(ctx, DeleteRequest{ID: adopted.Window.ID}); err != nil || !result.Deleted {
		t.Fatalf("release absent adopted session: %+v, %v", result, err)
	}
	if got := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", session, "#{pane_pid}"); got != newPID {
		t.Fatalf("release touched reused session name: %q -> %q", newPID, got)
	}
	if markers := commandOutput(t, "tmux", "-L", service.systemSocket, "display-message", "-p", "-t", session, "#{MCE_INSTANCE}|#{MCE_WINDOW}|#{MCE_WORKSPACE}"); markers != "||" {
		t.Fatalf("release marked reused session: %q", markers)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].DeletePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if cleanup, err := service.Cleanup(ctx); err != nil || cleanup.WorkspacesDeleted != 1 {
		t.Fatalf("recover preserved workspace removal: %+v, %v", cleanup, err)
	}
	if info, err := os.Stat(project); err != nil || !info.IsDir() {
		t.Fatalf("recovery removed preserved project: %v", err)
	}
}

func TestSnapshotKeepsAWindowVisibleWhenItsWorkspaceDirectoryIsMissing(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Missing directory"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", project, "worktree", "unlock", workspace.Path)
	runTestCommand(t, "git", "-C", project, "worktree", "remove", "--force", workspace.Path)

	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workspaces) != 1 || !snapshot.Workspaces[0].Unavailable {
		t.Fatalf("missing workspace was not reported as unavailable: %+v", snapshot.Workspaces)
	}
	if len(snapshot.Windows) != 1 || snapshot.Windows[0].ID != window.ID || !snapshot.Windows[0].Alive || snapshot.Windows[0].PaneHash == "" {
		t.Fatalf("live terminal was not retained for recovery: %+v", snapshot.Windows)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup missing workspace: %+v, %v", result, err)
	}
}

func TestGitWorkspaceUsesCachedRemoteBranchWhenFetchIsUnavailable(t *testing.T) {
	requireCommands(t, "git")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	project := syntheticGitProject(t)
	runTestCommand(t, "git", "-C", project, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "offline.git"))
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{
		ProjectID: mustID(t), ProjectPath: project, Name: "Offline work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.BaseRef != "refs/remotes/origin/main" {
		t.Fatalf("base ref = %q, want cached origin/main", workspace.BaseRef)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID}); err != nil || !result.Deleted {
		t.Fatalf("delete offline workspace = %+v, %v", result, err)
	}
	if _, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{
		ProjectID: mustID(t), ProjectPath: project, Name: "Uncached work", BaseBranch: "uncached",
	}); err == nil {
		t.Fatal("workspace used an unavailable uncached remote branch")
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workspaces) != 0 {
		t.Fatalf("failed uncached workspace retained state: %+v", snapshot.Workspaces)
	}
}

func TestCleanupPreservesGitWorkspaceFilesForExplicitDeletion(t *testing.T) {
	requireCommands(t, "git")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{
		ProjectID: mustID(t), ProjectPath: project, Name: "Ignored data",
	})
	if err != nil {
		t.Fatal(err)
	}
	excludePath := commandOutput(t, "git", "-C", workspace.Path, "rev-parse", "--git-path", "info/exclude")
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(workspace.Path, excludePath)
	}
	existing, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excludePath, append(existing, []byte("\n.local-data\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	ignoredPath := filepath.Join(workspace.Path, ".local-data")
	if err := os.WriteFile(ignoredPath, []byte("must survive automatic cleanup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", workspace.Path, "check-ignore", ".local-data")
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].LastUsedAt = service.now().UTC().Add(-cleanupAfter - time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Cleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspacesDeleted != 0 || !strings.Contains(strings.Join(result.Skipped, " "), "explicit deletion") {
		t.Fatalf("cleanup result = %+v", result)
	}
	if _, err := os.Stat(ignoredPath); err != nil {
		t.Fatalf("ignored file was removed: %v", err)
	}
	if deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !deleted.Deleted {
		t.Fatalf("force cleanup workspace = %+v, %v", deleted, err)
	}
}

func TestTerminalPassesRequestedExtendedKeyThroughTmux(t *testing.T) {
	requireCommands(t, "git", "tmux", "od")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home := privateTestHome(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{
		ProjectID: mustID(t), ProjectPath: syntheticGitProject(t), Name: "Extended keys",
	})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := attachWindowPTY(ctx, Host{ID: localHostID, Name: localHostName}, "", service.store.instanceID, window, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer attachment.Close()
	waitUntil(t, 5*time.Second, func() bool { return strings.TrimSpace(attachment.Render(100, 30)) != "" })
	command := "stty -echo; printf '\\033[>4;1m\\115\\103\\105\\137\\105\\130\\124\\113\\105\\131\\137\\122\\105\\101\\104\\131\\n'; IFS= read -r line; stty echo; printf '%s' \"$line\" | od -An -t x1; printf '\\nMCE_EXTKEY_DONE\\n'"
	if err := attachment.SendKey(tea.KeyPressMsg{Code: 's', Text: command}); err != nil {
		t.Fatal(err)
	}
	if err := attachment.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	waitForRender(t, attachment, "MCE_EXTKEY_READY", 15*time.Second)
	if err := attachment.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}); err != nil {
		t.Fatal(err)
	}
	if err := attachment.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	var rendered string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rendered = ansi.Strip(attachment.Render(100, 30))
		compact := strings.Join(strings.Fields(rendered), " ")
		if strings.Contains(compact, "1b 5b 31 33 3b 32 75") || strings.Contains(compact, "1b 5b 32 37 3b 32 3b 31 33 7e") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	compact := strings.Join(strings.Fields(rendered), " ")
	if !strings.Contains(compact, "1b 5b 31 33 3b 32 75") && !strings.Contains(compact, "1b 5b 32 37 3b 32 3b 31 33 7e") {
		t.Fatalf("modified key did not pass through tmux: %q", rendered)
	}
}

func TestCleanupRemovesOnlyExpiredOwnedResources(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return createdAt }
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Expired"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.tmux(ctx, "send-keys", "-t", window.Session, "exit", "Enter"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		out, err := service.tmux(ctx, "display-message", "-p", "-t", window.Session, "#{pane_dead}")
		return err == nil && strings.TrimSpace(string(out)) == "1"
	})
	if snapshot, err := service.Snapshot(ctx); err != nil || len(snapshot.Windows) != 1 || snapshot.Windows[0].Alive {
		t.Fatalf("stopped window snapshot = %+v, %v", snapshot, err)
	}
	if _, err := service.tmux(ctx, "new-session", "-d", "-s", "unowned", "-x", "20", "-y", "10"); err != nil {
		t.Fatal(err)
	}

	service.now = func() time.Time { return createdAt.Add(8 * 24 * time.Hour) }
	result, err := service.Cleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.WindowsDeleted != 1 || result.WorkspacesDeleted != 0 || !strings.Contains(strings.Join(result.Skipped, " "), "explicit deletion") {
		t.Fatalf("unexpected cleanup: %+v", result)
	}
	if _, err := os.Stat(workspace.Path); err != nil {
		t.Fatalf("automatic cleanup removed a Git worktree: %v", err)
	}
	if _, err := service.tmux(ctx, "has-session", "-t", "unowned"); err != nil {
		t.Fatal("cleanup removed an unowned tmux session")
	}
	if deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID}); err != nil || !deleted.Deleted {
		t.Fatalf("explicit Git workspace deletion = %+v, %v", deleted, err)
	}
}

func TestExitedTerminalCanBeRemovedImmediatelyWithoutDeletingItsWorkspace(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{
		ProjectID: mustID(t), ProjectPath: syntheticGitProject(t), Name: "Keep workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.tmux(ctx, "send-keys", "-t", window.Session, "exit", "Enter"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		out, err := service.tmux(ctx, "display-message", "-p", "-t", window.Session, "#{pane_dead}")
		return err == nil && strings.TrimSpace(string(out)) == "1"
	})
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Windows) != 1 || snapshot.Windows[0].Alive || !snapshot.Windows[0].Exited {
		t.Fatalf("exited terminal snapshot = %+v", snapshot.Windows)
	}
	deleted, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID})
	if err != nil || !deleted.Deleted || deleted.Forceable {
		t.Fatalf("automatic terminal removal = %+v, %v", deleted, err)
	}
	snapshot, err = service.Snapshot(ctx)
	if err != nil || len(snapshot.Windows) != 0 || len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].ID != workspace.ID {
		t.Fatalf("workspace after terminal removal = %+v, %v", snapshot, err)
	}
	if _, err := os.Stat(workspace.Path); err != nil {
		t.Fatalf("terminal removal changed its workspace: %v", err)
	}
	if deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID}); err != nil || !deleted.Deleted {
		t.Fatalf("cleanup workspace = %+v, %v", deleted, err)
	}
}

func TestMissingTerminalDoesNotTakeItsHostOfflineOrSelectAnotherSession(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{
		ProjectID: mustID(t), ProjectPath: syntheticGitProject(t), Name: "Keep host online",
	})
	if err != nil {
		t.Fatal(err)
	}
	missing, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	live, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.tmux(ctx, "kill-session", "-t", "="+missing.Session); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot with one missing session took its host offline: %v", err)
	}
	if len(snapshot.Windows) != 2 {
		t.Fatalf("windows after one session disappeared = %+v", snapshot.Windows)
	}
	states := map[string]Window{}
	for _, window := range snapshot.Windows {
		states[window.ID] = window
	}
	if states[missing.ID].Alive || states[missing.ID].Exited || !states[live.ID].Alive {
		t.Fatalf("window states after one session disappeared = %+v", snapshot.Windows)
	}
	if deleted, err := service.DeleteWindow(ctx, DeleteRequest{ID: missing.ID}); err != nil || !deleted.Deleted || deleted.Forceable {
		t.Fatalf("delete missing window = %+v, %v", deleted, err)
	}
	if deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !deleted.Deleted {
		t.Fatalf("cleanup workspace = %+v, %v", deleted, err)
	}
}

func TestTmuxSocketCleanupPreservesUnexpectedPath(t *testing.T) {
	requireCommands(t, "tmux")
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	path := service.tmuxSocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() {
			_ = os.Remove(path)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.cleanupUnusedTmuxSocket(ctx); err == nil {
		t.Fatal("expected a non-socket path to be rejected")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("unexpected tmux path was changed: %q, %v", data, err)
	}
}

func TestTmuxSocketCleanupPreservesListeningSocketOnQueryFailure(t *testing.T) {
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	path := service.tmuxSocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(path)
	})
	service.runner = runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, commandFailure{exitCode: 1}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.cleanupUnusedTmuxSocket(ctx); err == nil {
		t.Fatal("expected a listening socket with an uncertain query to be preserved")
	}
	if info, err := os.Lstat(path); err != nil || info.Mode().Type() != os.ModeSocket {
		t.Fatalf("listening tmux socket was changed: %v", err)
	}
}

func TestDeleteWindowPreservesSessionAndRegistryWhenOwnershipChanged(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	project := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.tmux(ctx, "set-environment", "-t", window.Session, "MCE_WINDOW", strings.Repeat("f", 24)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID, Force: true}); err == nil || !strings.Contains(err.Error(), "ownership changed") {
		t.Fatalf("altered ownership deletion = %v", err)
	}
	if _, err := service.tmux(ctx, "has-session", "-t", window.Session); err != nil {
		t.Fatal("altered tmux session was removed")
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil || len(snapshot.Windows) != 1 || snapshot.Windows[0].ID != window.ID {
		t.Fatalf("altered window registry record was removed: %+v, %v", snapshot, err)
	}
	if _, err := service.tmux(ctx, "set-environment", "-t", window.Session, "MCE_WINDOW", window.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("delete after ownership restore = %+v, %v", result, err)
	}
}

func TestDeleteWorkspacePreservesEveryWindowWhenOneOwnershipMarkerChanged(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	project := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.tmux(ctx, "set-environment", "-t", second.Session, "MCE_WINDOW", strings.Repeat("f", 24)); err != nil {
		t.Fatal(err)
	}
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true})
	if err == nil || result.Deleted || !strings.Contains(err.Error(), "ownership") {
		t.Fatalf("altered workspace deletion = %+v, %v", result, err)
	}
	for _, window := range []Window{first, second} {
		if _, err := service.tmux(ctx, "has-session", "-t", window.Session); err != nil {
			t.Fatalf("workspace deletion removed preserved session %s", window.Name)
		}
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil || len(snapshot.Workspaces) != 1 || len(snapshot.Windows) != 2 {
		t.Fatalf("workspace deletion changed preserved registry: %+v, %v", snapshot, err)
	}
	if _, err := service.tmux(ctx, "set-environment", "-t", second.Session, "MCE_WINDOW", second.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("delete after ownership restore = %+v, %v", result, err)
	}
}

func TestDecliningInterruptedWorkspaceDeleteClearsCascadeIntent(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	project := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Retained"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].DeletePending = true
		registry.Windows[0].DeletePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID})
	if err != nil || result.Deleted || !result.Forceable {
		t.Fatalf("retained workspace deletion = %+v, %v", result, err)
	}
	if err := service.store.withReadLock(func(registry hostRegistry) error {
		if registry.Workspaces[0].DeletePending || registry.Windows[0].DeletePending {
			return errors.New("retained workspace or window still has a delete intent")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RenameWindow(ctx, RenameRequest{ID: window.ID, Name: "Still usable"}); err != nil {
		t.Fatalf("retained window is not usable: %v", err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup retained workspace = %+v, %v", result, err)
	}
}

func TestNonGitWorkspaceIsInPlaceAndNeverDeletesProjectDirectory(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	home := privateTestHome(t)
	project := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Git || workspace.Path != project {
		t.Fatalf("unexpected non-Git workspace: %+v", workspace)
	}
	var imageData bytes.Buffer
	fixture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	fixture.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageData, fixture); err != nil {
		t.Fatal(err)
	}
	attachment, err := service.PutAttachment(ctx, PutAttachmentRequest{WorkspaceID: workspace.ID, Extension: ".txt", Data: imageData.Bytes(), Image: true})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(attachment.Path) != ".png" {
		t.Fatalf("image extension = %q", filepath.Ext(attachment.Path))
	}
	if info, err := os.Stat(attachment.Path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("attachment is not a private file: %v, %v", info, err)
	}
	if _, err := service.PutAttachment(ctx, PutAttachmentRequest{WorkspaceID: workspace.ID, Data: []byte("not an image"), Image: true}); err == nil {
		t.Fatal("expected invalid clipboard image rejection")
	}
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID})
	if err != nil || !result.Deleted {
		t.Fatalf("delete non-Git workspace = %+v, %v", result, err)
	}
	if info, err := os.Stat(project); err != nil || !info.IsDir() {
		t.Fatalf("non-Git project directory was deleted: %v", err)
	}
	if _, err := os.Stat(attachment.Path); !os.IsNotExist(err) {
		t.Fatalf("owned attachment was not deleted with workspace: %v", err)
	}
}

func TestCleanupRemovesInterruptedPendingAttachment(t *testing.T) {
	ctx := context.Background()
	project := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := service.PutAttachment(ctx, PutAttachmentRequest{WorkspaceID: workspace.ID, Data: []byte("pending")})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Attachments[0].CreatePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attachment.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending attachment file remains: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(attachment.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending attachment directory remains: %v", err)
	}
	if err := service.store.withReadLock(func(registry hostRegistry) error {
		if len(registry.Attachments) != 0 {
			t.Fatalf("pending attachment record remains: %+v", registry.Attachments)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHostRegistryRejectsExpandedDeletionTargets(t *testing.T) {
	home := privateTestHome(t)
	store, err := newHostStore(home, testInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := mustID(t)
	projectID := mustID(t)
	registry := hostRegistry{Version: stateVersion, InstanceID: testInstanceID, Workspaces: []Workspace{{
		ID: workspaceID, ProjectID: projectID, ProjectPath: "/tmp/project", Name: "Owned", Git: true,
		Path: "/tmp/not-owned", Branch: "multicodex/owned-" + workspaceID[:8], BaseRef: "HEAD",
	}}}
	if err := store.save(registry); err == nil {
		t.Fatal("expected a Git workspace outside the owned root to be rejected")
	}
	registry.Workspaces = []Workspace{{ID: workspaceID, ProjectID: projectID, ProjectPath: "/tmp/project", Path: "/tmp/project", Name: "Owned"}}
	registry.Attachments = []AttachmentFile{{ID: mustID(t), WorkspaceID: workspaceID, Path: "/tmp/not-owned.txt"}}
	if err := store.save(registry); err == nil {
		t.Fatal("expected an attachment outside the owned root to be rejected")
	}
}

func TestCleanupRecoversPendingOwnedResources(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Recovered"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].CreatePending = true
		registry.Windows[0].CreatePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cleanupResult, err := service.Cleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workspaces) != 1 || len(snapshot.Windows) != 1 || snapshot.Workspaces[0].CreatePending || snapshot.Windows[0].CreatePending {
		t.Fatalf("pending resources were not recovered: snapshot=%+v cleanup=%+v", snapshot, cleanupResult)
	}
	if result, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup window: %+v, %v", result, err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup workspace: %+v, %v", result, err)
	}
}

func TestRecentAttachmentKeepsWorkspaceOutOfCleanup(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return created }
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Attachment"})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return created.Add(8 * 24 * time.Hour) }
	if _, err := service.PutAttachment(ctx, PutAttachmentRequest{WorkspaceID: workspace.ID, Extension: ".txt", Data: []byte("recent")}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Cleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspacesDeleted != 0 {
		t.Fatalf("cleanup removed a workspace with a recent attachment: %+v", result)
	}
	if deleted, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !deleted.Deleted {
		t.Fatalf("cleanup fixture: %+v, %v", deleted, err)
	}
}

func TestGitWorkspaceRefusesChangedProjectIdentity(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Identity"})
	if err != nil {
		t.Fatal(err)
	}
	original := project + "-original"
	if err := os.Rename(project, original); err != nil {
		t.Fatal(err)
	}
	replacement := syntheticGitProject(t)
	if err := os.Rename(replacement, project); err != nil {
		t.Fatal(err)
	}
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true})
	if err == nil || result.Deleted {
		t.Fatalf("changed project identity was not rejected: %+v, %v", result, err)
	}
	if err := os.Rename(project, project+"-replacement"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, project); err != nil {
		t.Fatal(err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup restored project workspace: %+v, %v", result, err)
	}
}

func TestGitWorkspaceRequiresConfirmationForDetachedCheckout(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Altered worktree"})
	if err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", workspace.Path, "checkout", "--detach")
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID})
	if err != nil || result.Deleted || !result.Forceable || !strings.Contains(result.Reason, "detached checkout") {
		t.Fatalf("detached worktree did not require confirmation: %+v, %v", result, err)
	}
	runTestCommand(t, "git", "-C", workspace.Path, "checkout", workspace.Branch)
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup restored worktree: %+v, %v", result, err)
	}
}

func TestGitWorkspacePreservesUniqueCommitsWithoutForce(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Unique commit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "change"), []byte("unique\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", workspace.Path, "add", "change")
	runTestCommand(t, "git", "-C", workspace.Path, "commit", "-m", "unique")
	if err := os.WriteFile(filepath.Join(workspace.Path, "untracked"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID})
	if err != nil || result.Deleted || !result.Forceable || !strings.Contains(result.Reason, "live terminal") || !strings.Contains(result.Reason, "uncommitted") || !strings.Contains(result.Reason, "commits") {
		t.Fatalf("unique commit was not preserved: %+v, %v", result, err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("forced cleanup of unique commit: %+v, %v", result, err)
	}
}

func TestDeleteWorkspaceRepairsRegisteredWorktreeAfterDirectoryLoss(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	projectID := mustID(t)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: projectID, ProjectPath: project, Name: "Interrupted metadata"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(workspace.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(workspace.Path)); err != nil {
		t.Fatal(err)
	}
	before := commandOutput(t, "git", "-C", project, "worktree", "list", "--porcelain")
	if !strings.Contains(before, workspace.Path) || !strings.Contains(before, "refs/heads/"+workspace.Branch) {
		t.Fatalf("fixture has no registered missing worktree:\n%s", before)
	}
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID})
	if err != nil || !result.Deleted {
		t.Fatalf("registered missing worktree cleanup: %+v, %v\n%s", result, err, before)
	}
	after := commandOutput(t, "git", "-C", project, "worktree", "list", "--porcelain")
	if strings.Contains(after, workspace.Path) || strings.Contains(after, "refs/heads/"+workspace.Branch) {
		t.Fatalf("registered worktree metadata remains:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(service.store.worktreeRoot, projectID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty owned worktree directory remains: %v", err)
	}
}

func TestMissingWorktreeWithUniqueCommitStillOffersForceDelete(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Missing unique"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "change"), []byte("unique\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", workspace.Path, "add", "change")
	runTestCommand(t, "git", "-C", workspace.Path, "commit", "-m", "unique before directory loss")
	if err := os.RemoveAll(workspace.Path); err != nil {
		t.Fatal(err)
	}
	result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID})
	if err != nil || result.Deleted || !result.Forceable || !strings.Contains(result.Reason, "commits") {
		t.Fatalf("missing unique worktree did not offer force deletion: %+v, %v", result, err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("forced cleanup of missing unique worktree: %+v, %v", result, err)
	}
}

func TestInterruptedWorkspaceDeleteRechecksNewCommits(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Interrupted delete"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].DeletePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "change"), []byte("unique\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", workspace.Path, "add", "change")
	runTestCommand(t, "git", "-C", workspace.Path, "commit", "-m", "unique after delete intent")
	result, err := service.Cleanup(ctx)
	if err != nil || result.WorkspacesDeleted != 0 || len(result.Skipped) == 0 {
		t.Fatalf("interrupted delete did not preserve new work: %+v, %v", result, err)
	}
	if _, err := os.Stat(workspace.Path); err != nil {
		t.Fatalf("worktree with new commit was removed: %v", err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("forced cleanup after preservation: %+v, %v", result, err)
	}
}

func TestInterruptedWorkspaceDeleteRestoresItsExactLock(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Interrupted unlock"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].DeletePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", project, "worktree", "unlock", workspace.Path)
	if result, err := service.Cleanup(ctx); err != nil || result.WorkspacesDeleted != 1 {
		t.Fatalf("interrupted unlock recovery = %+v, %v", result, err)
	}
	if _, err := os.Stat(workspace.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered deletion left worktree: %v", err)
	}
}

func TestFailedWorktreeRemovalRelocksWithFreshContext(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx, cancel := context.WithCancel(context.Background())
	project := syntheticGitProject(t)
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Removal failure"})
	if err != nil {
		t.Fatal(err)
	}
	delegate := execRunner{}
	failed := false
	service.runner = runnerFunc(func(commandContext context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if !failed && name == "git" && strings.Contains(joined, "worktree remove") {
			failed = true
			cancel()
			return nil, errors.New("synthetic removal failure")
		}
		return delegate.run(commandContext, name, args...)
	})
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID}); err == nil || result.Deleted {
		t.Fatalf("synthetic removal failure = %+v, %v", result, err)
	}
	if locked, reason, err := service.gitWorktreeLock(context.Background(), workspace); err != nil || !locked || reason != service.worktreeLockReason(workspace) {
		t.Fatalf("worktree was not relocked after canceled removal: %v, %q, %v", locked, reason, err)
	}
	service.runner = execRunner{}
	if result, err := service.Cleanup(context.Background()); err != nil || result.WorkspacesDeleted != 1 {
		t.Fatalf("recovered removal = %+v, %v", result, err)
	}
}

func TestDecliningPendingWorkspaceForceRestoresWorkspaceUse(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	project := syntheticGitProject(t)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Retained pending"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "unique"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", workspace.Path, "add", "unique")
	runTestCommand(t, "git", "-C", workspace.Path, "commit", "-m", "keep pending work")
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].DeletePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refused, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID})
	if err != nil || refused.Deleted || !refused.Forceable {
		t.Fatalf("pending workspace did not offer safe retention: %+v, %v", refused, err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatalf("declined deletion did not restore workspace use: %v", err)
	}
	if _, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID, Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupCompletesInterruptedOwnedDeletes(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Delete recovery"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Windows[0].DeletePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Cleanup(ctx)
	if err != nil || result.WindowsDeleted != 0 || len(result.Skipped) == 0 {
		t.Fatalf("cleanup replayed stale force consent against a live window: %+v, %v", result, err)
	}
	if err := service.TouchWindow(ctx, window.ID); err != nil {
		t.Fatalf("live pending window could not be resumed safely: %v", err)
	}
	if err := service.killOwnedSession(ctx, window); err != nil {
		t.Fatal(err)
	}
	result, err = service.Cleanup(ctx)
	if err != nil || result.WindowsDeleted != 1 {
		t.Fatalf("window delete recovery: %+v, %v", result, err)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Workspaces[0].DeletePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.removeGitWorktree(ctx, workspace, true); err != nil {
		t.Fatal(err)
	}
	result, err = service.Cleanup(ctx)
	if err != nil || result.WorkspacesDeleted != 1 {
		t.Fatalf("workspace delete recovery: %+v, %v", result, err)
	}
}

func TestClearWindowDeletePendingRestoresNormalCleanupGuard(t *testing.T) {
	requireCommands(t, "git", "tmux")
	ctx := context.Background()
	service, err := NewHostService(privateTestHome(t), mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	project := syntheticGitProject(t)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Pending guard"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.withLock(func(registry *hostRegistry) error {
		registry.Windows[0].DeletePending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refused, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID})
	if err != nil || refused.Deleted || !refused.Forceable {
		t.Fatalf("live pending window did not return a force choice: %+v, %v", refused, err)
	}
	if err := service.store.withReadLock(func(registry hostRegistry) error {
		if registry.Windows[0].DeletePending {
			t.Fatal("declined deletion left a persistent delete intent")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.TouchWindow(ctx, window.ID); err != nil {
		t.Fatal(err)
	}
	result, err := service.Cleanup(ctx)
	if err != nil || result.WindowsDeleted != 0 {
		t.Fatalf("normal cleanup removed a live window after declined deletion: %+v, %v", result, err)
	}
	if result, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup window: %+v, %v", result, err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup workspace: %+v, %v", result, err)
	}
}

func TestTerminalHandlesLargePasteAndOutputFlood(t *testing.T) {
	requireCommands(t, "git", "tmux", "dd", "tr")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	home := privateTestHome(t)
	project := syntheticGitProject(t)
	service, err := NewHostService(home, mustID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	workspace, err := service.CreateWorkspace(ctx, CreateWorkspaceRequest{ProjectID: mustID(t), ProjectPath: project, Name: "Terminal stress"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := service.CreateWindow(ctx, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := attachWindowPTY(ctx, Host{ID: localHostID, Name: localHostName}, "", service.store.instanceID, window, 100, 24)
	if err != nil {
		t.Fatal(err)
	}
	countPath := filepath.Join(workspace.Path, "paste.bin")
	donePath := filepath.Join(workspace.Path, "paste.done")
	command := "stty -echo; printf PASTE_\\READY; wc -c >" + quotePOSIX(countPath) + "; stty echo; touch " + quotePOSIX(donePath)
	if err := attachment.SendKey(tea.KeyPressMsg{Code: 'p', Text: command}); err != nil {
		t.Fatal(err)
	}
	if err := attachment.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	// Race instrumentation and concurrent public CI packages can delay the
	// initial tmux render without changing the terminal contract.
	readyDeadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(attachment.Render(100, 24), "PASTE_READY") && time.Now().Before(readyDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rendered := attachment.Render(100, 24); !strings.Contains(rendered, "PASTE_READY") {
		t.Fatalf("terminal did not become ready: input=%v render=%q", attachment.inputError(), rendered)
	}
	select {
	case <-attachment.responsesDone:
		t.Fatal("terminal input writer stopped before the large paste")
	default:
	}
	paste := strings.Repeat(strings.Repeat("x", 1000)+"\n", 1024)
	if err := attachment.Paste(paste); err != nil {
		t.Fatal(err)
	}
	if err := attachment.SendKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(donePath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(donePath); err != nil {
		info, countErr := os.Stat(countPath)
		t.Fatalf("large paste did not finish: count=%v err=%v input=%v render=%q", info, countErr, attachment.inputError(), attachment.Render(100, 24))
	}
	count, err := os.ReadFile(countPath)
	if err != nil || strings.TrimSpace(string(count)) != strconv.Itoa(len(paste)) {
		t.Fatalf("large paste count: %q, %v", count, err)
	}
	floodPath := filepath.Join(workspace.Path, "flood.done")
	command = "head -c 1048576 /dev/zero | tr '\\0' x; touch " + quotePOSIX(floodPath)
	if err := attachment.SendKey(tea.KeyPressMsg{Code: 'h', Text: command}); err != nil {
		t.Fatal(err)
	}
	if err := attachment.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool {
		_, err := os.Stat(floodPath)
		return err == nil
	})
	if lines := attachment.terminal.ScrollbackLen(); lines > 1 {
		t.Fatalf("terminal renderer retained %d scrollback lines, want at most 1", lines)
	}
	if err := attachment.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attachment.Done():
	default:
		t.Fatal("terminal reader did not stop")
	}
	select {
	case <-attachment.responsesDone:
	default:
		t.Fatal("terminal response writer did not stop")
	}
	if result, err := service.DeleteWindow(ctx, DeleteRequest{ID: window.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup window: %+v, %v", result, err)
	}
	if result, err := service.DeleteWorkspace(ctx, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("cleanup workspace: %+v, %v", result, err)
	}
}

func privateTestHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "multicodex")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func syntheticGitProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runTestCommand(t, "git", "init", "--bare", "--initial-branch=main", remote)
	project := filepath.Join(root, "project")
	runTestCommand(t, "git", "clone", remote, project)
	runTestCommand(t, "git", "-C", project, "config", "user.name", "Multicodex Test")
	runTestCommand(t, "git", "-C", project, "config", "user.email", "test@invalid.example")
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, "git", "-C", project, "add", "README.md")
	runTestCommand(t, "git", "-C", project, "commit", "-m", "fixture")
	runTestCommand(t, "git", "-C", project, "push", "-u", "origin", "main")
	runTestCommand(t, "git", "-C", project, "remote", "set-head", "origin", "main")
	return project
}

func mustID(t *testing.T) string {
	t.Helper()
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func requireCommands(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("required integration command %s is not installed", name)
			}
			t.Skipf("%s is not installed", name)
		}
	}
}

func runTestCommand(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := isolatedTestCommand(name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s failed: %v: %s", name, err, output)
	}
}

func commandOutput(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := isolatedTestCommand(name, args...).Output()
	if err != nil {
		t.Fatalf("%s failed: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

func isolatedTestCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	if name != "tmux" {
		return cmd
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "MCE_INSTANCE", "MCE_WINDOW", "MCE_WORKSPACE", "MCE_PROJECT":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	return cmd
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func waitForRender(t *testing.T, attachment *Attachment, needle string, timeout time.Duration) {
	t.Helper()
	waitUntil(t, timeout, func() bool { return strings.Contains(attachment.Render(80, 20), needle) })
}

func waitUntil(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true before timeout")
}

func killTestServer(service *HostService) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = service.tmux(ctx, "kill-server")
	_ = service.cleanupUnusedTmuxSocket(ctx)
}

func killTestSystemServer(service *HostService) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = service.systemTmux(ctx, "kill-server")
	_ = os.Remove(service.systemTmuxSocketPath())
}
