package editor

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestActivityOrderingKeepsPopulatedProjectsFirst(t *testing.T) {
	hostID := "111111111111111111111111"
	oldProjectID := "222222222222222222222222"
	newProjectID := "333333333333333333333333"
	emptyProjectID := "444444444444444444444444"
	oldWorkspaceID := "555555555555555555555555"
	newWorkspaceID := "666666666666666666666666"
	oldWindowID := "777777777777777777777777"
	newWindowID := "888888888888888888888888"
	oldActivity := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newActivity := oldActivity.Add(time.Hour)
	host := Host{ID: hostID, Name: "Build", Projects: []Project{
		{ID: emptyProjectID, Name: "A empty", Path: "/srv/empty"},
		{ID: oldProjectID, Name: "B older", Path: "/srv/older"},
		{ID: newProjectID, Name: "Z newer", Path: "/srv/newer"},
	}}
	status := HostStatus{Host: host, Snapshot: HostSnapshot{
		Workspaces: []Workspace{
			{ID: oldWorkspaceID, ProjectID: oldProjectID, Name: "Old work", LastUsedAt: oldActivity},
			{ID: newWorkspaceID, ProjectID: newProjectID, Name: "New work", LastUsedAt: newActivity},
		},
		Windows: []Window{
			{ID: oldWindowID, WorkspaceID: oldWorkspaceID, Name: "Old terminal", LastUsedAt: oldActivity},
			{ID: newWindowID, WorkspaceID: newWorkspaceID, Name: "New terminal", LastUsedAt: newActivity},
		},
	}}
	state := ClientState{Activities: []Activity{
		{HostID: hostID, WindowID: oldWindowID, ChangedAt: oldActivity},
		{HostID: hostID, WindowID: newWindowID, ChangedAt: newActivity},
	}}

	locations := sortedProjectsByActivity(state, []HostStatus{status})
	got := []string{locations[0].Project.Name, locations[1].Project.Name, locations[2].Project.Name}
	want := []string{"Z newer", "B older", "A empty"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("project order = %v, want %v", got, want)
	}
}

func TestActivityOrderingSortsWorkspacesAndWindowsWithoutMutatingSnapshot(t *testing.T) {
	hostID := "111111111111111111111111"
	projectID := "222222222222222222222222"
	oldWorkspaceID := "333333333333333333333333"
	newWorkspaceID := "444444444444444444444444"
	emptyWorkspaceID := "555555555555555555555555"
	oldWindowID := "666666666666666666666666"
	newWindowID := "777777777777777777777777"
	newestWindowID := "888888888888888888888888"
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	status := HostStatus{
		Host: Host{ID: hostID, Name: "Build", Projects: []Project{{ID: projectID, Name: "Project", Path: "/srv/project"}}},
		Snapshot: HostSnapshot{
			Workspaces: []Workspace{
				{ID: oldWorkspaceID, ProjectID: projectID, Name: "Old work", LastUsedAt: base},
				{ID: emptyWorkspaceID, ProjectID: projectID, Name: "Empty work", LastUsedAt: base.Add(4 * time.Hour)},
				{ID: newWorkspaceID, ProjectID: projectID, Name: "New work", LastUsedAt: base.Add(time.Hour)},
			},
			Windows: []Window{
				{ID: oldWindowID, WorkspaceID: oldWorkspaceID, Name: "Old terminal", LastUsedAt: base},
				{ID: newWindowID, WorkspaceID: newWorkspaceID, Name: "Second terminal", LastUsedAt: base.Add(time.Hour)},
				{ID: newestWindowID, WorkspaceID: newWorkspaceID, Name: "First terminal", LastUsedAt: base.Add(2 * time.Hour)},
			},
		},
	}
	originalWorkspaces := append([]Workspace(nil), status.Snapshot.Workspaces...)
	originalWindows := append([]Window(nil), status.Snapshot.Windows...)
	state := ClientState{Activities: []Activity{
		{HostID: hostID, WindowID: oldWindowID, ChangedAt: base},
		{HostID: hostID, WindowID: newWindowID, ChangedAt: base.Add(time.Hour)},
		{HostID: hostID, WindowID: newestWindowID, ChangedAt: base.Add(2 * time.Hour)},
	}}

	locations := sortedProjectsByActivity(state, []HostStatus{status})
	if len(locations) != 1 {
		t.Fatalf("locations = %+v", locations)
	}
	gotWorkspaces := []string{locations[0].Workspaces[0].ID, locations[0].Workspaces[1].ID, locations[0].Workspaces[2].ID}
	wantWorkspaces := []string{emptyWorkspaceID, newWorkspaceID, oldWorkspaceID}
	if !reflect.DeepEqual(gotWorkspaces, wantWorkspaces) {
		t.Fatalf("workspace order = %v, want %v", gotWorkspaces, wantWorkspaces)
	}
	gotWindows := []string{locations[0].Windows[0].ID, locations[0].Windows[1].ID, locations[0].Windows[2].ID}
	wantWindows := []string{newestWindowID, newWindowID, oldWindowID}
	if !reflect.DeepEqual(gotWindows, wantWindows) {
		t.Fatalf("window order = %v, want %v", gotWindows, wantWindows)
	}
	if !reflect.DeepEqual(status.Snapshot.Workspaces, originalWorkspaces) || !reflect.DeepEqual(status.Snapshot.Windows, originalWindows) {
		t.Fatal("activity ordering mutated the host snapshot")
	}
}

func TestActivityMonitoringUpdatesEveryWindowWithoutOpeningIt(t *testing.T) {
	hostID := "111111111111111111111111"
	changingID := "222222222222222222222222"
	quietID := "333333333333333333333333"
	newID := "444444444444444444444444"
	old := time.Now().UTC().Add(-time.Hour)
	manager := &Manager{
		lastSave: time.Now(),
		state: ClientState{Activities: []Activity{
			{HostID: hostID, WindowID: changingID, PaneHash: strings.Repeat("a", 64), ChangedAt: old},
			{HostID: hostID, WindowID: quietID, PaneHash: strings.Repeat("b", 64), ChangedAt: old},
		}},
	}
	manager.updateActivities([]HostStatus{{
		Host: Host{ID: hostID},
		Snapshot: HostSnapshot{Windows: []Window{
			{ID: changingID, PaneHash: strings.Repeat("c", 64)},
			{ID: quietID, PaneHash: strings.Repeat("b", 64)},
			{ID: newID, PaneHash: strings.Repeat("d", 64)},
		}},
	}})

	activities := map[string]Activity{}
	for _, activity := range manager.state.Activities {
		activities[activity.WindowID] = activity
	}
	if len(activities) != 3 {
		t.Fatalf("monitored activities = %+v", manager.state.Activities)
	}
	if !activities[changingID].ChangedAt.After(old) || activities[changingID].PaneHash != strings.Repeat("c", 64) {
		t.Fatalf("changing window was not updated: %+v", activities[changingID])
	}
	if !activities[quietID].ChangedAt.Equal(old) {
		t.Fatalf("quiet window was reported as changing: %+v", activities[quietID])
	}
	if activities[newID].ChangedAt.IsZero() || activities[newID].PaneHash != strings.Repeat("d", 64) {
		t.Fatalf("new unselected window was not monitored: %+v", activities[newID])
	}
}

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

func TestEveryChangedReconnectSelectionIsSavedImmediately(t *testing.T) {
	home := privateTestHome(t)
	manager, err := NewManager(home)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first, second := mustID(t), mustID(t)
	if err := manager.SetSelectedWindow(first); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetSelectedWindow(second); err != nil {
		t.Fatal(err)
	}
	persisted, err := NewStateStore(home).Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.SelectedWindowID != second {
		t.Fatalf("rapid selection persisted as %q, want %q", persisted.SelectedWindowID, second)
	}
	if err := manager.SetSelectedWindow(second); err != nil {
		t.Fatal(err)
	}
	if manager.dirty {
		t.Fatal("selecting the current window dirtied client state")
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
			ID: windowID, WorkspaceID: workspaceID, Name: "Terminal", Session: "mce-" + windowID, PaneHash: strings.Repeat("a", 64),
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

func TestSnapshotValidationAllowsOnlyUnavailableLegacyWorktreesWithoutARecordedLock(t *testing.T) {
	projectID := "111111111111111111111111"
	workspaceID := "222222222222222222222222"
	host := Host{ID: localHostID, Name: localHostName, Projects: []Project{{ID: projectID, Name: "Project", Path: "/tmp/project"}}}
	snapshot := HostSnapshot{Protocol: hostProtocol, Workspaces: []Workspace{{
		ID: workspaceID, ProjectID: projectID, ProjectPath: "/tmp/project", Name: "Missing", Path: "/tmp/worktree", Git: true,
		GitCommonDir: "/tmp/project/.git", Branch: "multicodex/missing-" + workspaceID[:8], BaseRef: "refs/remotes/origin/main", Unavailable: true,
	}}}
	if err := validateHostSnapshot(host, snapshot); err != nil {
		t.Fatalf("unavailable legacy worktree was hidden: %v", err)
	}
	snapshot.Workspaces[0].Unavailable = false
	if err := validateHostSnapshot(host, snapshot); err == nil {
		t.Fatal("available unlocked worktree was accepted")
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

func TestDoctorResultValidationRejectsRemoteTerminalData(t *testing.T) {
	valid := DoctorResult{OK: true, Checks: []string{"tmux 3.4", "git version 2.43.0", "editor host path policy is valid"}}
	if err := validateDoctorResult(valid); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []DoctorResult{
		{OK: false},
		{OK: true, Issues: []string{"unexpected"}},
		{OK: false, Issues: []string{"\x1b]52;c;unsafe\a"}},
		{OK: false, Issues: []string{strings.Repeat("x", 201)}},
		{OK: false, Issues: make([]string, 9)},
	} {
		if err := validateDoctorResult(unsafe); err == nil {
			t.Fatalf("accepted unsafe doctor result: %+v", unsafe)
		}
	}
}

func TestDoctorIssueSummaryIsBoundedPlainText(t *testing.T) {
	value := doctorIssueSummary(DoctorResult{Issues: []string{strings.Repeat("x", 180), strings.Repeat("y", 180)}})
	if len(value) != 300 || strings.ContainsAny(value, "\x1b\a") {
		t.Fatalf("unsafe doctor summary: %q", value)
	}
}
