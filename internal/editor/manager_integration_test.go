package editor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "__editor-host" && os.Args[2] == "--instance" {
		service, err := NewHostService(os.Getenv("MULTICODEX_HOME"), os.Args[3])
		if err != nil {
			os.Exit(2)
		}
		if err := RunHostProtocol(context.Background(), service, os.Stdin, os.Stdout); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestManagerUsesLongLivedLocalProtocolAndRecoversSnapshot(t *testing.T) {
	requireCommands(t, "git", "tmux")
	home := privateTestHome(t)
	t.Setenv("MULTICODEX_HOME", home)
	projectPath := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(projectPath, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(home)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.CheckLocal(ctx); err != nil {
		t.Fatal(err)
	}
	client := manager.clients[localHostID]
	for range 100 {
		callContext, cancelCall := context.WithCancel(ctx)
		var hello helloResult
		if err := client.Call(callContext, "hello", nil, &hello); err != nil {
			cancelCall()
			t.Fatalf("fast host call failed: %v", err)
		}
		cancelCall()
	}
	project, err := manager.AddProject(ctx, localHostID, "Notes", projectPath)
	if err != nil {
		t.Fatal(err)
	}
	workspace, window, err := manager.CreateWorkspaceWithWindow(ctx, localHostID, CreateWorkspaceRequest{ProjectID: project.ID, ProjectPath: project.Path, Name: "Desk"})
	if err != nil {
		t.Fatal(err)
	}
	if window.Name != defaultWindowName {
		t.Fatalf("automatic first window = %+v", window)
	}
	if err := manager.RenameWorkspace(ctx, localHostID, RenameRequest{ID: workspace.ID, Name: "Desk work"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.RenameWindow(ctx, localHostID, RenameRequest{ID: window.ID, Name: "Main"}); err != nil {
		t.Fatal(err)
	}
	socket := "mce-" + manager.State().InstanceID[:12]
	runTestCommand(t, "tmux", "-L", socket, "set-environment", "-t", window.Session, "MCE_WINDOW", "ffffffffffffffffffffffff")
	if attachment, err := manager.AttachWindow(ctx, localHostID, window, 80, 20); err == nil {
		_ = attachment.Close()
		t.Fatal("manager attached to a tmux session with altered ownership")
	}
	runTestCommand(t, "tmux", "-L", socket, "set-environment", "-t", window.Session, "MCE_WINDOW", window.ID)
	attachment, err := manager.AttachWindow(ctx, localHostID, window, 80, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := attachment.Close(); err != nil {
		t.Fatal(err)
	}
	statuses := manager.Refresh(ctx)
	if len(statuses) != 1 || statuses[0].Error != "" || len(statuses[0].Snapshot.Workspaces) != 1 || statuses[0].Snapshot.Workspaces[0].Name != "Desk work" || len(statuses[0].Snapshot.Windows) != 1 || statuses[0].Snapshot.Windows[0].Name != "Main" {
		t.Fatalf("unexpected manager snapshot: %+v", statuses)
	}
	if manager.clients[localHostID] == nil {
		t.Fatal("expected a warm local host protocol connection")
	}
	if result, err := manager.DeleteWindow(ctx, localHostID, DeleteRequest{ID: window.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("delete window = %+v, %v", result, err)
	}
	if result, err := manager.DeleteWorkspace(ctx, localHostID, DeleteRequest{ID: workspace.ID}); err != nil || !result.Deleted {
		t.Fatalf("delete workspace = %+v, %v", result, err)
	}
}

func TestManagerRestartKeepsSelectionAndRunningTerminal(t *testing.T) {
	requireCommands(t, "git", "tmux")
	home := privateTestHome(t)
	t.Setenv("MULTICODEX_HOME", home)
	projectPath := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(projectPath, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(home)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	instanceID := manager.State().InstanceID
	service, err := NewHostService(home, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	defer killTestServer(service)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.CheckLocal(ctx); err != nil {
		t.Fatal(err)
	}
	project, err := manager.AddProject(ctx, localHostID, "Notes", projectPath)
	if err != nil {
		t.Fatal(err)
	}
	workspace, window, err := manager.CreateWorkspaceWithWindow(ctx, localHostID, CreateWorkspaceRequest{ProjectID: project.ID, ProjectPath: project.Path, Name: "Upgrade check"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetSelectedWindow(window.ID); err != nil {
		t.Fatal(err)
	}
	panePID := commandOutput(t, "tmux", "-L", service.socketName(), "display-message", "-p", "-t", window.Session, "#{pane_pid}")
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewManager(home)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.CheckLocal(ctx); err != nil {
		t.Fatal(err)
	}
	statuses := restarted.Refresh(ctx)
	if len(statuses) != 1 || statuses[0].Error != "" || len(statuses[0].Snapshot.Windows) != 1 || statuses[0].Snapshot.Windows[0].ID != window.ID {
		t.Fatalf("restart snapshot = %+v", statuses)
	}
	if restarted.State().SelectedWindowID != window.ID {
		t.Fatalf("restart selection = %q, want %q", restarted.State().SelectedWindowID, window.ID)
	}
	if got := commandOutput(t, "tmux", "-L", service.socketName(), "display-message", "-p", "-t", window.Session, "#{pane_pid}"); got != panePID {
		t.Fatalf("terminal process changed across restart: %s -> %s", panePID, got)
	}
	attachment, err := restarted.AttachWindow(ctx, localHostID, statuses[0].Snapshot.Windows[0], 80, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := attachment.Close(); err != nil {
		t.Fatal(err)
	}
	if result, err := restarted.DeleteWorkspace(ctx, localHostID, DeleteRequest{ID: workspace.ID, Force: true}); err != nil || !result.Deleted {
		t.Fatalf("delete restarted workspace = %+v, %v", result, err)
	}
}

func TestBackgroundCleanupDoesNotQueueBehindInteractiveHostConnection(t *testing.T) {
	requireCommands(t, "git", "tmux")
	home := privateTestHome(t)
	t.Setenv("MULTICODEX_HOME", home)
	manager, err := NewManager(home)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.CheckLocal(ctx); err != nil {
		t.Fatal(err)
	}
	interactive := manager.clients[localHostID]
	<-interactive.callGate
	defer func() { interactive.callGate <- struct{}{} }()

	result := manager.cleanupHost(ctx, manager.State().Hosts[0])
	if len(result.Skipped) != 0 {
		t.Fatalf("isolated cleanup was blocked by the interactive connection: %+v", result)
	}
	if manager.clients[localHostID] != interactive {
		t.Fatal("isolated cleanup replaced the interactive host connection")
	}
}

func TestManagerCloseStopsConcurrentRefreshAndReleasesState(t *testing.T) {
	requireCommands(t, "git", "tmux")
	home := privateTestHome(t)
	t.Setenv("MULTICODEX_HOME", home)
	manager, err := NewManager(home)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = manager.Refresh(manager.Context())
		close(done)
	}()
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh remained active after manager close")
	}
	manager.mu.Lock()
	clientCount := len(manager.clients)
	manager.mu.Unlock()
	if clientCount != 0 {
		t.Fatalf("manager retained %d host clients after close", clientCount)
	}
	lock, err := NewStateStore(home).AcquireInstanceLock()
	if err != nil {
		t.Fatalf("manager did not release client state: %v", err)
	}
	releaseInstanceLock(lock)
}
