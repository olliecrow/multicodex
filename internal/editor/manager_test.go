package editor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCleanupResultValidationRejectsUnsafeHostData(t *testing.T) {
	if err := validateCleanupResult(CleanupResult{Skipped: []string{"busy but safe"}}); err != nil {
		t.Fatal(err)
	}
	for _, result := range []CleanupResult{
		{WindowsDeleted: -1},
		{Skipped: []string{"unsafe\ntext"}},
		{Skipped: []string{strings.Repeat("x", 513)}},
	} {
		if err := validateCleanupResult(result); err == nil {
			t.Fatalf("unsafe cleanup result accepted: %+v", result)
		}
	}
}

func TestClearSelectedWindowPersistsOnlyTheDeletedSelection(t *testing.T) {
	home := privateTestHome(t)
	manager, err := NewManager(home)
	if err != nil {
		t.Fatal(err)
	}
	selected := mustID(t)
	if err := manager.SetSelectedWindow(selected); err != nil {
		t.Fatal(err)
	}
	if err := manager.clearSelectedWindow(mustID(t)); err != nil {
		t.Fatal(err)
	}
	if manager.State().SelectedWindowID != selected {
		t.Fatal("unrelated window deletion cleared the reconnect selection")
	}
	if err := manager.clearSelectedWindow(selected); err != nil {
		t.Fatal(err)
	}
	state, err := NewStateStore(home).Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedWindowID != "" {
		t.Fatalf("deleted reconnect selection persisted as %q", state.SelectedWindowID)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshQueueCancellationPreservesHealthyClient(t *testing.T) {
	manager, err := NewManager(privateTestHome(t))
	if err != nil {
		t.Fatal(err)
	}
	callGate := make(chan struct{}, 1)
	client := &HostClient{callGate: callGate}
	manager.mu.Lock()
	manager.clients[localHostID] = client
	manager.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	statuses := manager.Refresh(ctx)
	if len(statuses) != 1 || !strings.Contains(statuses[0].Error, "request canceled") {
		t.Fatalf("unexpected canceled refresh status: %+v", statuses)
	}
	manager.mu.Lock()
	retained := manager.clients[localHostID]
	manager.mu.Unlock()
	if retained != client {
		t.Fatal("queue cancellation dropped a healthy host client")
	}
	callGate <- struct{}{}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotValidationRejectsHostControlData(t *testing.T) {
	projectID := "111111111111111111111111"
	workspaceID := "222222222222222222222222"
	windowID := "333333333333333333333333"
	host := Host{ID: localHostID, Name: localHostName, Projects: []Project{{ID: projectID, Name: "Project", Path: "/tmp/project"}}}
	snapshot := HostSnapshot{
		Protocol: hostProtocol,
		Workspaces: []Workspace{{
			ID: workspaceID, ProjectID: projectID, ProjectPath: "/tmp/project", Name: "Work", Path: "/tmp/project",
		}},
		Windows: []Window{{
			ID: windowID, WorkspaceID: workspaceID, Name: "Terminal", Session: "mce-" + windowID, Launch: "shell", PaneHash: strings.Repeat("a", 64),
		}},
	}
	if err := validateHostSnapshot(host, snapshot); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	snapshot.Windows[0].PaneHash = "safe\x1b]52;c;payload\a"
	if err := validateHostSnapshot(host, snapshot); err == nil {
		t.Fatal("expected unsafe pane hash to be rejected")
	}
	snapshot.Windows[0].PaneHash = strings.Repeat("a", 64)
	snapshot.Workspaces[0].Path = "/tmp/project\nforged"
	if err := validateHostSnapshot(host, snapshot); err == nil {
		t.Fatal("expected remote control character to be rejected")
	}
}

func TestSafeClientTextIsBoundedControlFreeUTF8(t *testing.T) {
	got := safeClientText("hello\n\x1b]52;c;secret\a界界界", 17)
	if len(got) > 17 || !strings.Contains(got, "hello") || strings.ContainsAny(got, "\n\r\x1b\a") || !utf8.ValidString(got) {
		t.Fatalf("unsafe client text: %q", got)
	}
}

func TestRefreshGivesQueuedHostsTheirOwnDeadline(t *testing.T) {
	hosts := make([]Host, 9)
	for i := range hosts {
		hosts[i] = Host{ID: mustID(t), Name: fmt.Sprintf("Host %d", i)}
	}
	statuses := refreshHosts(context.Background(), hosts, 20*time.Millisecond, func(ctx context.Context, host Host) HostStatus {
		if host.ID == hosts[8].ID {
			return HostStatus{Host: host}
		}
		<-ctx.Done()
		return HostStatus{Host: host, Error: "timeout"}
	})
	if len(statuses) != 9 || statuses[8].Error != "" {
		t.Fatalf("queued healthy host was starved: %+v", statuses)
	}
}
