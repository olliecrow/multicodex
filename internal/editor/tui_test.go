package editor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/olliecrow/multicodex/internal/monitor/usage"
)

func TestSidebarHidesEmptyProjectsGroupsWorkspacesAndUsesDynamicSlots(t *testing.T) {
	hostID := "111111111111111111111111"
	projectAID := "222222222222222222222222"
	projectBID := "333333333333333333333333"
	windowAID := "444444444444444444444444"
	windowBID := "555555555555555555555555"
	state := ClientState{
		Version: stateVersion, InstanceID: testInstanceID,
		Hosts: []Host{
			{ID: localHostID, Name: localHostName},
			{ID: hostID, Name: "Build", SSHAlias: "build", Projects: []Project{
				{ID: projectAID, Name: "Alpha", Path: "/srv/alpha"},
				{ID: projectBID, Name: "Beta", Path: "/srv/beta"},
				{ID: "666666666666666666666666", Name: "Empty", Path: "/srv/empty"},
			}},
		},
		Activities: []Activity{
			{HostID: hostID, WindowID: windowAID, ChangedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{HostID: hostID, WindowID: windowBID, ChangedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		},
	}
	host := state.Hosts[1]
	status := HostStatus{Host: host, Snapshot: HostSnapshot{
		Workspaces: []Workspace{
			{ID: "777777777777777777777777", ProjectID: projectAID, Name: "Alpha work"},
			{ID: "888888888888888888888888", ProjectID: projectBID, Name: "Beta work"},
		},
		Windows: []Window{
			{ID: windowAID, WorkspaceID: "777777777777777777777777", Name: "Alpha terminal", Alive: true},
			{ID: windowBID, WorkspaceID: "888888888888888888888888", Name: "Beta terminal", Alive: true},
		},
	}}
	manager := &Manager{state: state}
	model := tuiModel{manager: manager, statuses: []HostStatus{status}, selectedRow: -1}
	model.rebuildRows()
	if len(model.rows) != 6 {
		t.Fatalf("rows = %+v", model.rows)
	}
	if model.rows[0].project.Name != "Beta" || model.rows[2].slot != 1 {
		t.Fatalf("newest project and first dynamic slot mismatch: %+v", model.rows)
	}
	if model.rows[3].project.Name != "Alpha" || model.rows[5].slot != 2 {
		t.Fatalf("second project and slot mismatch: %+v", model.rows)
	}
	for _, row := range model.rows {
		if row.project.Name == "Empty" {
			t.Fatal("project without a workspace must remain hidden")
		}
	}
}

func TestWindowSlotShortcutsAreDynamicAndDoNotStealPlainTerminalDigits(t *testing.T) {
	if got := windowSlotKey(tea.KeyPressMsg{Code: '3'}, false); got != 0 {
		t.Fatalf("plain terminal digit selected slot %d", got)
	}
	if got := windowSlotKey(tea.KeyPressMsg{Code: '3'}, true); got != 3 {
		t.Fatalf("control-mode digit selected slot %d", got)
	}
	if got := windowSlotKey(tea.KeyPressMsg{Code: '3', Mod: tea.ModSuper}, false); got != 3 {
		t.Fatalf("Command+3 selected slot %d", got)
	}
	if got := windowSlotKey(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt}, false); got != 3 {
		t.Fatalf("Alt+3 selected slot %d", got)
	}
}

func TestCompactUsageIsAlwaysClearAndBounded(t *testing.T) {
	summary := &usage.Summary{Accounts: []usage.AccountSummary{
		{Label: "delta", WeeklyWindow: usage.WindowSummary{UsedPercent: 40}},
		{Label: "alpha", WeeklyWindow: usage.WindowSummary{UsedPercent: 10}},
		{Label: "charlie", WeeklyWindow: usage.WindowSummary{UsedPercent: 30}},
		{Label: "bravo", WeeklyWindow: usage.WindowSummary{UsedPercent: 20}},
	}}
	got := compactUsage(summary)
	if got != "usage alpha 10% · bravo 20% · charlie 30% · +1" {
		t.Fatalf("compact usage = %q", got)
	}
	if got := compactUsage(&usage.Summary{WeeklyWindow: usage.WindowSummary{UsedPercent: -1}}); got != "usage unavailable" {
		t.Fatalf("unavailable usage = %q", got)
	}
}

func TestCompactUsageNeverRendersAccountControlSequences(t *testing.T) {
	summary := &usage.Summary{Accounts: []usage.AccountSummary{{
		Label: "safe\x1b]52;c;clipboard\a\x1b[31m", WeeklyWindow: usage.WindowSummary{UsedPercent: 12},
	}}}
	got := compactUsage(summary)
	for _, forbidden := range []string{"\x1b]52", "\x1b[31m", "\a"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("usage rendered terminal control sequence %q: %q", forbidden, got)
		}
	}
}

func TestMinimumViewportShowsTitleUsageSidebarAndFooter(t *testing.T) {
	state := ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{{ID: localHostID, Name: localHostName}}}
	model := tuiModel{manager: &Manager{state: state}, width: minimumWidth, height: minimumHeight, usageText: "usage alpha 42%", message: "ready"}
	view := ansi.Strip(model.View().Content)
	for _, want := range []string{"multicodex editor", "usage alpha 42%", "No workspaces", "Ctrl+G controls", "ready"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view:\n%s", want, view)
		}
	}
	lines := strings.Split(view, "\n")
	if len(lines) != minimumHeight {
		t.Fatalf("view has %d lines, want %d", len(lines), minimumHeight)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width != minimumWidth {
			t.Fatalf("line %d width = %d, want %d: %q", i, width, minimumWidth, line)
		}
	}
}

func TestFormAcceptsShiftedText(t *testing.T) {
	model := tuiModel{modal: &modal{kind: "form", fields: []formField{{label: "Name"}}}}
	updated, _ := model.handleModalKey(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	got := updated.(tuiModel).modal.fields[0].value
	if got != "N" {
		t.Fatalf("shifted form input = %q", got)
	}
}

func TestFormPasteNeverRendersTerminalControlSequences(t *testing.T) {
	model := tuiModel{modal: &modal{kind: "form", title: "Add", fields: []formField{{label: "Name", limit: 80}}}}
	updated, _ := model.Update(tea.PasteMsg{Content: "safe\x1b]52;c;clipboard\a\x1b[31m\ntext"})
	got := updated.(tuiModel)
	rendered := renderModal(*got.modal, 80, 24)
	for _, forbidden := range []string{"\x1b]52", "\x1b[31m", "\a"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("form rendered terminal control sequence %q: %q", forbidden, rendered)
		}
	}
	if strings.ContainsAny(got.modal.fields[0].value, "\x1b\a\n\r") {
		t.Fatalf("form retained pasted control characters: %q", got.modal.fields[0].value)
	}
	if !strings.Contains(got.message, "omitted") {
		t.Fatalf("form did not report sanitized paste: %q", got.message)
	}
}

func TestOfflineHostRemainsVisibleWithLastSnapshot(t *testing.T) {
	host := Host{ID: "111111111111111111111111", Name: "Remote", SSHAlias: "remote", Projects: []Project{{ID: "222222222222222222222222", Name: "Repo", Path: "/srv/repo"}}}
	state := ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{{ID: localHostID, Name: localHostName}, host}}
	model := tuiModel{manager: &Manager{state: state}, statuses: []HostStatus{{Host: host, Error: "offline", Snapshot: HostSnapshot{Workspaces: []Workspace{{ID: "333333333333333333333333", ProjectID: host.Projects[0].ID, Name: "Work"}}}}}}
	model.rebuildRows()
	if len(model.rows) < 1 || !model.rows[0].offline {
		t.Fatalf("offline project not retained: %+v", model.rows)
	}
}

func TestOfflineHostKeepsSavedActivity(t *testing.T) {
	hostID := "111111111111111111111111"
	windowID := "222222222222222222222222"
	changedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager := &Manager{state: ClientState{Activities: []Activity{{HostID: hostID, WindowID: windowID, PaneHash: "hash", ChangedAt: changedAt}}}}
	manager.updateActivities([]HostStatus{{Host: Host{ID: hostID}, Error: "offline"}})
	if len(manager.state.Activities) != 1 || !manager.state.Activities[0].ChangedAt.Equal(changedAt) {
		t.Fatalf("offline activity was discarded: %+v", manager.state.Activities)
	}
}

func TestSidebarScrollKeepsSelectionVisible(t *testing.T) {
	rows := make([]sidebarRow, 40)
	for i := range rows {
		rows[i] = sidebarRow{kind: "window", window: Window{ID: mustID(t), Name: "Window"}}
	}
	model := tuiModel{width: 100, height: 24, rows: rows, selectedRow: 35}
	model.ensureSelectionVisible()
	if model.sidebarOffset == 0 || model.selectedRow < model.sidebarOffset || model.selectedRow >= model.sidebarOffset+model.bodyHeight() {
		t.Fatalf("selection %d is outside offset %d and height %d", model.selectedRow, model.sidebarOffset, model.bodyHeight())
	}
	view := ansi.Strip(model.renderSidebar())
	if strings.Count(view, "\n")+1 != model.bodyHeight() {
		t.Fatalf("sidebar has the wrong visible height")
	}
}

func TestJoinKeepRightNeverExceedsWidth(t *testing.T) {
	for _, test := range []struct{ left, right string }{{"left", strings.Repeat("r", 20)}, {"left", "right"}, {"", "right"}} {
		got := joinKeepRight(test.left, test.right, 20)
		if width := lipgloss.Width(got); width != 20 {
			t.Fatalf("join width = %d, want 20: %q", width, got)
		}
	}
}

func TestFormInputIsBoundedWithoutSplittingUnicode(t *testing.T) {
	if got := appendBounded("ab", "界界界", 4); got != "ab界界" {
		t.Fatalf("bounded input = %q", got)
	}
	if got := appendBounded("full", "ignored", 4); got != "full" {
		t.Fatalf("full input changed to %q", got)
	}
}

func TestRefreshIsSingleFlightAndStaleAttachResultIsIgnored(t *testing.T) {
	model := tuiModel{refreshing: true, attachingID: "222222222222222222222222", message: "waiting"}
	if cmd := model.startRefresh(); cmd != nil {
		t.Fatal("expected an overlapping refresh to be suppressed")
	}
	updated, _ := model.Update(attachResultMsg{window: Window{ID: "111111111111111111111111"}, err: errors.New("stale")})
	got := updated.(tuiModel)
	if got.attachingID != model.attachingID || got.message != model.message {
		t.Fatalf("stale attach result changed current selection: %+v", got)
	}
}

func TestSelectingAttachedWindowQueuesLatestChoiceWithoutAnotherAttach(t *testing.T) {
	windowA := Window{ID: "111111111111111111111111"}
	model := tuiModel{
		rows:        []sidebarRow{{kind: "window", window: windowA}},
		selectedRow: 0,
		attachedID:  windowA.ID,
		attachingID: "222222222222222222222222",
	}
	updated, cmd := model.selectCurrentRow()
	got := updated.(tuiModel)
	if cmd != nil || got.attachingID == "" || got.queuedAttach == nil || got.queuedAttach.window.ID != windowA.ID {
		t.Fatalf("attached window was not queued behind the one active attempt: %+v", got)
	}
}

func TestRapidWindowSelectionKeepsOnlyLatestQueuedAttach(t *testing.T) {
	windowB := Window{ID: "222222222222222222222222"}
	windowC := Window{ID: "333333333333333333333333"}
	windowD := Window{ID: "444444444444444444444444"}
	model := tuiModel{
		manager:     &Manager{},
		rows:        []sidebarRow{{kind: "window", window: windowC}, {kind: "window", window: windowD}},
		selectedRow: 0,
		attachingID: windowB.ID,
	}
	updated, cmd := model.selectCurrentRow()
	got := updated.(tuiModel)
	if cmd != nil || got.queuedAttach == nil || got.queuedAttach.window.ID != windowC.ID {
		t.Fatalf("third window was not queued: %+v", got)
	}
	got.selectedRow = 1
	updated, cmd = got.selectCurrentRow()
	got = updated.(tuiModel)
	if cmd != nil || got.queuedAttach == nil || got.queuedAttach.window.ID != windowD.ID {
		t.Fatalf("latest window did not replace queued choice: %+v", got)
	}
	updated, cmd = got.Update(attachResultMsg{window: windowB})
	got = updated.(tuiModel)
	if cmd == nil || got.attachingID != windowD.ID || got.queuedAttach != nil {
		t.Fatalf("queued latest window did not start after active attempt: %+v", got)
	}
}

func TestCleanupSummaryReportsSkippedResources(t *testing.T) {
	results := map[string]CleanupResult{
		"local": {WindowsDeleted: 1, WorkspacesDeleted: 2, AttachmentsDeleted: 3, Skipped: []string{"busy", "offline"}},
	}
	got := cleanupSummary(results)
	if got != "cleanup: 1 windows, 2 workspaces, 3 attachments removed · 2 skipped" {
		t.Fatalf("cleanup summary = %q", got)
	}
}

func TestChoiceModalKeepsLargeSelectionVisible(t *testing.T) {
	choices := make([]choice, 50)
	for i := range choices {
		choices[i].label = fmt.Sprintf("Project %02d", i)
	}
	rendered := ansi.Strip(renderModal(modal{kind: "choice", title: "Choose", choices: choices, choice: 45}, 40, 10))
	if !strings.Contains(rendered, "Project 45") || strings.Contains(rendered, "Project 00") {
		t.Fatalf("choice modal did not scroll to its selection:\n%s", rendered)
	}
}

func TestRepeatedActionsStaySingleFlight(t *testing.T) {
	model := tuiModel{manager: &Manager{}, controlMode: true}
	updated, cmd := model.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	first := updated.(tuiModel)
	if cmd == nil || !first.actionBusy {
		t.Fatalf("first cleanup did not start: %+v", first)
	}
	updated, cmd = first.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	second := updated.(tuiModel)
	if cmd != nil || !second.actionBusy || !strings.Contains(second.message, "still running") {
		t.Fatalf("repeated cleanup was not suppressed: %+v", second)
	}
}

func TestUIWorkersCancelAndJoinFetchesAndLongTimers(t *testing.T) {
	workers := newUIWorkers(context.Background())
	started := make(chan struct{})
	fetchStopped := make(chan struct{})
	fetch := workers.track(usageCmd(func(ctx context.Context) (*usage.Summary, error) {
		close(started)
		<-ctx.Done()
		close(fetchStopped)
		return nil, ctx.Err()
	}, workers.ctx))
	timer := (tuiModel{workers: workers}).cleanupTick()
	commandsDone := make(chan struct{})
	go func() {
		defer close(commandsDone)
		var running sync.WaitGroup
		for _, command := range []tea.Cmd{fetch, timer} {
			running.Add(1)
			go func(command tea.Cmd) {
				defer running.Done()
				_ = command()
			}(command)
		}
		running.Wait()
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("usage fetch did not start")
	}
	workers.stop()
	workers.wait()
	select {
	case <-fetchStopped:
	case <-time.After(time.Second):
		t.Fatal("usage fetch did not observe UI shutdown")
	}
	select {
	case <-commandsDone:
	case <-time.After(time.Second):
		t.Fatal("UI commands remained after shutdown")
	}
	if command := workers.track(func() tea.Msg { return nil }); command != nil {
		t.Fatal("UI accepted a command after shutdown")
	}
}
