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
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/olliecrow/multicodex/internal/monitor/usage"
)

func TestSidebarShowsProjectsGroupsWorkspacesAndUsesDynamicSlots(t *testing.T) {
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
	if len(model.rows) != 7 {
		t.Fatalf("rows = %+v", model.rows)
	}
	if model.rows[0].project.Name != "Beta" || model.rows[2].slot != 1 {
		t.Fatalf("newest project and first dynamic slot mismatch: %+v", model.rows)
	}
	if model.rows[3].project.Name != "Alpha" || model.rows[5].slot != 2 {
		t.Fatalf("second project and slot mismatch: %+v", model.rows)
	}
	if model.rows[6].kind != "project" || model.rows[6].project.Name != "Empty" {
		t.Fatalf("empty project is not directly selectable: %+v", model.rows)
	}
}

func TestSidebarHasNoManagedWindowLimit(t *testing.T) {
	hostID := "111111111111111111111111"
	projectID := "222222222222222222222222"
	workspaceID := "333333333333333333333333"
	project := Project{ID: projectID, Name: "Project", Path: "/srv/project"}
	host := Host{ID: hostID, Name: "Host", Projects: []Project{project}}
	windows := make([]Window, 12)
	for i := range windows {
		windows[i] = Window{ID: fmt.Sprintf("%024d", i+1), WorkspaceID: workspaceID, Name: fmt.Sprintf("Terminal %d", i+1), Alive: true}
	}
	model := tuiModel{
		manager: &Manager{state: ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{host}}},
		statuses: []HostStatus{{Host: host, Snapshot: HostSnapshot{
			Workspaces: []Workspace{{ID: workspaceID, ProjectID: projectID, Name: "Work"}},
			Windows:    windows,
		}}},
		selectedRow: -1,
	}
	model.rebuildRows()
	if len(model.rows) != 14 {
		t.Fatalf("sidebar has %d rows for 12 windows, want 14: %+v", len(model.rows), model.rows)
	}
	if got := model.rows[len(model.rows)-1].slot; got != 12 {
		t.Fatalf("last dynamic slot = %d, want 12", got)
	}
}

func TestSidebarStatusShowsLiveOutputRunningStoppedAndFailures(t *testing.T) {
	now := time.Now()
	model := tuiModel{}
	tests := []struct {
		row  sidebarRow
		want string
	}{
		{sidebarRow{window: Window{Alive: true}, changedAt: now}, "◉"},
		{sidebarRow{window: Window{Alive: true}}, "●"},
		{sidebarRow{window: Window{}}, "○"},
		{sidebarRow{offline: true, window: Window{Alive: true}}, "?"},
		{sidebarRow{workspace: Workspace{Unavailable: true}, window: Window{Alive: true}}, "●"},
	}
	for _, test := range tests {
		if got := model.windowStatusMarker(test.row); got != test.want {
			t.Fatalf("window status marker = %q, want %q for %+v", got, test.want, test.row)
		}
	}
}

func TestSidebarLivePulseAdvancesAfterEachRefresh(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	model := tuiModel{manager: &Manager{state: ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{host}}}, refreshing: true}
	updated, _ := model.Update(refreshMsg{statuses: []HostStatus{{Host: host}}})
	first := updated.(tuiModel)
	if title := first.sidebarTitle(); title != "Projects · live •" {
		t.Fatalf("first completed refresh title = %q", title)
	}
	updated, _ = first.Update(refreshMsg{statuses: []HostStatus{{Host: host}}})
	second := updated.(tuiModel)
	if title := second.sidebarTitle(); title != "Projects · live ·" {
		t.Fatalf("second completed refresh title = %q", title)
	}
	second.statuses[0].Error = "offline"
	if title := second.sidebarTitle(); title != "Projects · 1 offline ·" {
		t.Fatalf("offline refresh title = %q", title)
	}
}

func TestWindowSlotShortcutsAreDynamicAndDoNotStealPlainTerminalDigits(t *testing.T) {
	if got := windowSlotKey(tea.KeyPressMsg{Code: '3'}); got != 0 {
		t.Fatalf("plain terminal digit selected slot %d", got)
	}
	if got := windowSlotKey(tea.KeyPressMsg{Code: '3', Mod: tea.ModSuper}); got != 3 {
		t.Fatalf("Command+3 selected slot %d", got)
	}
	if got := windowSlotKey(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt}); got != 3 {
		t.Fatalf("Alt+3 selected slot %d", got)
	}
}

func TestControlModeCanSendLiteralControlG(t *testing.T) {
	attachment := &Attachment{inputQueue: make(chan terminalInput, 1)}
	model := tuiModel{attachment: attachment, controlMode: true}
	model.openActionMenu()
	selectEditorAction(t, &model, "send_control_g")
	updated, cmd := model.handleModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(tuiModel)
	if cmd != nil || got.controlMode || !strings.Contains(got.message, "Ctrl+G") {
		t.Fatalf("literal Ctrl+G result = %+v", got)
	}
	input := <-attachment.inputQueue
	if input.kind != "key" || input.key.Code != 'g' || input.key.Mod != tea.ModCtrl {
		t.Fatalf("literal Ctrl+G input = %+v", input)
	}
}

func TestEditorControlsUseNavigationAndAVisibleActionMenu(t *testing.T) {
	model := tuiModel{width: minimumWidth, height: minimumHeight, controlMode: true}
	updated, cmd := model.handleKey(tea.KeyPressMsg{Code: 'h', Text: "h"})
	got := updated.(tuiModel)
	if cmd != nil || got.modal != nil {
		t.Fatalf("legacy mnemonic key opened an action: %+v", got)
	}
	updated, cmd = got.handleKey(tea.KeyPressMsg{Code: '?', Text: "?"})
	got = updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.kind != "help" {
		t.Fatalf("sidebar ? did not open Help: %+v", got)
	}
	got.modal = nil
	updated, cmd = got.handleKey(tea.KeyPressMsg{Code: '3', Text: "3"})
	got = updated.(tuiModel)
	if cmd != nil || got.modal != nil {
		t.Fatalf("plain digit opened an action: %+v", got)
	}
	updated, cmd = got.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	got = updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.kind != "actions" {
		t.Fatalf("Tab did not open the action menu: %+v", got)
	}
	if got.modal.choices[0].action != "add_project" {
		t.Fatalf("empty editor first action = %q, want add_project", got.modal.choices[0].action)
	}
	rendered := ansi.Strip(renderModal(*got.modal, 60, 24))
	for _, want := range []string{"Editor actions", "Add project…", "Add SSH host…", "Run safe cleanup", "[ Cancel ]", "Enter: run", "Esc: cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("action menu is missing %q:\n%s", want, rendered)
		}
	}
	for _, unavailable := range []string{"New workspace…", "New window…", "Attach file…", "Open terminal history", "Delete selected", "Send Ctrl+G"} {
		if strings.Contains(rendered, unavailable) {
			t.Fatalf("empty editor shows unavailable action %q:\n%s", unavailable, rendered)
		}
	}
	helpWidth := (tuiModel{width: minimumWidth, height: minimumHeight}).terminalWidth()
	for _, line := range helpModalContent() {
		if lipgloss.Width(line) > helpWidth {
			t.Fatalf("minimum-width help line is clipped: %q", line)
		}
	}
	help := ansi.Strip(renderModal(modal{kind: "help", title: "Controls"}, helpWidth, (tuiModel{width: minimumWidth, height: minimumHeight}).bodyHeight()))
	for _, want := range []string{"Click project/workspace: options · window: open", "⌘B or Ctrl+G: focus the sidebar", "⌥↑/⌥↓: one screen", "scroll terminal history", "In the sidebar, Ctrl+C: quit", "● running · ○ stopped", "Need terminal Ctrl+G?", "[ Close ]"} {
		if !strings.Contains(help, want) {
			t.Fatalf("minimum-width help truncated %q:\n%s", want, help)
		}
	}
}

func TestProjectActionsOfferSafeTmuxAdoption(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	project := Project{ID: "111111111111111111111111", Name: "Project", Path: "/tmp/project"}
	model := tuiModel{rows: []sidebarRow{{kind: "project", host: host, project: project}}, selectedRow: 0}
	model.openActionMenu()
	if !modalHasAction(model.modal, "list_tmux_sessions") {
		t.Fatalf("project actions omit tmux adoption: %+v", model.modal.choices)
	}
	model.rows[0].offline = true
	model.openActionMenu()
	if modalHasAction(model.modal, "list_tmux_sessions") {
		t.Fatalf("offline project offered tmux adoption: %+v", model.modal.choices)
	}
}

func TestUnavailableWorkspaceCannotCreateAWindow(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	workspace := Workspace{ID: "111111111111111111111111", Name: "Missing", Unavailable: true}
	model := tuiModel{rows: []sidebarRow{{kind: "workspace", host: host, workspace: workspace}}, selectedRow: 0}
	model.openActionMenu()
	if modalHasAction(model.modal, "new_window_selected") || modalHasAction(model.modal, "new_window") {
		t.Fatalf("unavailable workspace offered a new window: %+v", model.modal.choices)
	}
	updated, cmd := model.startCreateWindow(model.rows[0])
	got := updated.(tuiModel)
	if cmd != nil || !strings.Contains(got.message, "unavailable") {
		t.Fatalf("direct unavailable-window guard = %q, %v", got.message, cmd)
	}
}

func TestAdoptedSessionUsesReleaseLanguage(t *testing.T) {
	window := Window{ID: "111111111111111111111111", Name: "Imported", Session: "existing", TmuxSessionID: "$1", Adopted: true}
	workspace := Workspace{ID: "222222222222222222222222", Name: "Shared checkout", External: true}
	model := tuiModel{rows: []sidebarRow{{kind: "window", workspace: workspace, window: window}}, selectedRow: 0}
	model.openDeleteConfirmation()
	if model.modal == nil || model.modal.title != "Release session?" || modalConfirmLabel(*model.modal) != "[ Release ]" || !strings.Contains(model.modal.warning, "keep running") {
		t.Fatalf("adopted delete dialog = %+v", model.modal)
	}
	rendered := ansi.Strip(renderModal(*model.modal, 70, 18))
	for _, want := range []string{"Release session?", "keep running", "[ Release ]"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("release dialog omits %q:\n%s", want, rendered)
		}
	}
}

func TestAdoptionReusesThePreservedWorkspaceName(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	project := Project{ID: "111111111111111111111111", Name: "Project", Path: "/tmp/project"}
	workspace := Workspace{ID: "222222222222222222222222", ProjectID: project.ID, Name: "Shared checkout", External: true}
	manager := &Manager{state: ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{host}}, ctx: context.Background()}
	model := tuiModel{manager: manager, statuses: []HostStatus{{Host: host, Snapshot: HostSnapshot{Workspaces: []Workspace{workspace}}}}, modal: &modal{
		kind: "choice", action: "adopt_tmux_session", choices: []choice{{host: host, project: project, session: TmuxSessionCandidate{Name: "existing", Command: "codex"}}},
	}}
	updated, cmd := model.acceptChoice()
	got := updated.(tuiModel)
	if cmd == nil || got.modal != nil || !got.actionBusy || !strings.Contains(got.message, "adopting existing") {
		t.Fatalf("preserved workspace adoption flow = modal=%+v busy=%v message=%q cmd=%v", got.modal, got.actionBusy, got.message, cmd)
	}
}

func modalHasAction(current *modal, action string) bool {
	if current == nil {
		return false
	}
	for _, item := range current.choices {
		if item.action == action {
			return true
		}
	}
	return false
}

func TestMacBookSidebarNavigationNeedsNoFunctionOrNavigationKeys(t *testing.T) {
	rows := make([]sidebarRow, 30)
	for i := range rows {
		rows[i] = sidebarRow{kind: "project", project: Project{ID: fmt.Sprintf("%024d", i+1), Name: fmt.Sprintf("Project %d", i+1)}}
	}
	model := tuiModel{width: 100, height: 30, controlMode: true, rows: rows, selectedRow: 10}
	page := max(1, model.sidebarHeight()-1)

	updated, cmd := model.handleKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	got := updated.(tuiModel)
	want := min(len(rows)-1, 10+page)
	if cmd != nil || got.selectedRow != want {
		t.Fatalf("Option+Down selected row %d, want %d", got.selectedRow, want)
	}

	updated, cmd = got.handleKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModSuper})
	got = updated.(tuiModel)
	if cmd != nil || got.selectedRow != len(rows)-1 {
		t.Fatalf("Command+Down selected row %d", got.selectedRow)
	}

	updated, cmd = got.handleKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModSuper})
	got = updated.(tuiModel)
	if cmd != nil || got.selectedRow != 0 {
		t.Fatalf("Command+Up selected row %d", got.selectedRow)
	}
}

func TestMacBookEditorShortcutsHaveVisibleKeyFallbacks(t *testing.T) {
	model := tuiModel{width: minimumWidth, height: minimumHeight}
	updated, cmd := model.handleKey(tea.KeyPressMsg{Code: 'b', Text: "b", Mod: tea.ModSuper})
	got := updated.(tuiModel)
	if cmd != nil || !got.controlMode {
		t.Fatalf("Command+B did not focus sidebar: %+v", got)
	}

	updated, cmd = got.handleKey(tea.KeyPressMsg{Code: '?', BaseCode: '/', Text: "?", Mod: tea.ModShift})
	got = updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.kind != "help" {
		t.Fatalf("sidebar ? did not open Help: %+v", got)
	}

	updated, cmd = got.handleModalKey(tea.KeyPressMsg{Code: '.', Text: ".", Mod: tea.ModSuper})
	got = updated.(tuiModel)
	if cmd != nil || got.modal != nil {
		t.Fatalf("Command+Period did not close dialog: %+v", got)
	}

	workspace := Workspace{ID: "222222222222222222222222", Name: "Work"}
	got.rows = []sidebarRow{{kind: "workspace", workspace: workspace}}
	got.selectedRow = 0
	updated, cmd = got.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r", Mod: tea.ModSuper})
	got = updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.action != "rename_workspace" {
		t.Fatalf("Command+R did not open Rename: %+v", got)
	}

	terminal := &Attachment{inputQueue: make(chan terminalInput, 1)}
	model = tuiModel{width: minimumWidth, height: minimumHeight, attachment: terminal}
	updated, cmd = model.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n", Mod: tea.ModSuper})
	got = updated.(tuiModel)
	if cmd != nil || len(terminal.inputQueue) != 1 || got.controlMode {
		t.Fatalf("terminal Command+N was stolen by editor controls: %+v", got)
	}
}

func TestWorkspaceRequiresANameAndWindowCreationIsDirect(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	project := Project{ID: "111111111111111111111111", Name: "Project"}
	workspace := Workspace{ID: "222222222222222222222222", Name: "Workspace"}

	model := tuiModel{modal: &modal{kind: "choice", action: "create_workspace", choices: []choice{{host: host, project: project}}}}
	updated, cmd := model.acceptChoice()
	model = updated.(tuiModel)
	if cmd != nil || model.modal == nil || model.modal.kind != "form" || model.modal.action != "create_workspace" || len(model.modal.fields) != 1 || model.modal.fields[0].label != "Workspace name" {
		t.Fatalf("workspace name form = %+v, cmd=%v", model.modal, cmd)
	}

	model = tuiModel{manager: &Manager{}, modal: &modal{kind: "choice", action: "create_window", choices: []choice{{host: host, project: project, workspace: workspace}}}}
	updated, cmd = model.acceptChoice()
	model = updated.(tuiModel)
	if cmd == nil || model.modal != nil || !model.actionBusy {
		t.Fatalf("direct window creation = %+v, cmd=%v", model, cmd)
	}
}

func TestSidebarSelectionProvidesContextualCreateAndRename(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	project := Project{ID: "111111111111111111111111", Name: "Project"}
	workspace := Workspace{ID: "222222222222222222222222", ProjectID: project.ID, Name: "Workspace"}
	window := Window{ID: "333333333333333333333333", WorkspaceID: workspace.ID, Name: "Terminal"}

	model := tuiModel{manager: &Manager{}, controlMode: true, rows: []sidebarRow{
		{kind: "project", host: host, project: project},
		{kind: "workspace", host: host, project: project, workspace: workspace},
		{kind: "window", host: host, project: project, workspace: workspace, window: window},
	}}
	updated, cmd := model.selectCurrentRow()
	model = updated.(tuiModel)
	if cmd != nil || model.modal == nil || model.modal.action != "create_workspace" || len(model.modal.fields) != 1 || model.modal.fields[0].label != "Workspace name" {
		t.Fatalf("project Enter did not request the required workspace name: %+v", model.modal)
	}

	model.modal = nil
	model.selectedRow = 1
	updated, cmd = model.selectCurrentRow()
	model = updated.(tuiModel)
	if cmd == nil || model.modal != nil || !model.actionBusy {
		t.Fatalf("workspace Enter did not start direct terminal creation: %+v", model)
	}

	model.actionBusy = false
	model.selectedRow = 2
	model.openRename()
	if model.modal == nil || model.modal.action != "rename_window" || len(model.modal.fields) != 1 || model.modal.fields[0].value != "Terminal" {
		t.Fatalf("rename was not prefilled from the selected window: %+v", model.modal)
	}

	model.modal = nil
	model.attachedID = window.ID
	model.attachment = &Attachment{}
	model.openActionMenu()
	if len(model.modal.choices) < 2 || model.modal.choices[0].action != "new_window_selected" || model.modal.choices[1].action != "rename_selected" {
		t.Fatalf("selected-window actions are not contextual: %+v", model.modal.choices)
	}
	available := map[string]bool{}
	for _, item := range model.modal.choices {
		available[item.action] = true
	}
	for _, action := range []string{"attach_file", "attach_clipboard", "scrollback", "delete", "send_control_g"} {
		if !available[action] {
			t.Fatalf("connected-window action %q is missing: %+v", action, model.modal.choices)
		}
	}
	model.selectedRow = 0 // A refresh may move selection while this menu stays open.
	selectEditorAction(t, &model, "new_window_selected")
	updated, cmd = model.activateEditorAction()
	model = updated.(tuiModel)
	if cmd == nil || !model.actionBusy || !strings.Contains(model.message, workspace.Name) {
		t.Fatalf("contextual create did not keep its captured workspace: %+v", model)
	}

	model.actionBusy = false
	model.selectedRow = 2
	model.openActionMenu()
	model.selectedRow = 0
	selectEditorAction(t, &model, "rename_selected")
	updated, cmd = model.activateEditorAction()
	model = updated.(tuiModel)
	if cmd != nil || model.modal == nil || model.modal.action != "rename_window" || model.modal.window.ID != window.ID {
		t.Fatalf("contextual rename did not keep its captured window: %+v", model.modal)
	}
}

func TestProjectAndWorkspaceSelectionShowClickableOptions(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	project := Project{ID: "111111111111111111111111", Name: "Project"}
	workspace := Workspace{ID: "222222222222222222222222", ProjectID: project.ID, Name: "Workspace"}
	rows := []sidebarRow{
		{kind: "project", host: host, project: project},
		{kind: "workspace", host: host, project: project, workspace: workspace},
	}
	model := tuiModel{manager: &Manager{}, width: minimumWidth, height: minimumHeight, controlMode: true, rows: rows, selectedRow: 0}
	inputQueue := make(chan terminalInput, 1)
	model.attachment = &Attachment{inputQueue: inputQueue}

	projectView := ansi.Strip(model.View().Content)
	for _, want := range []string{"Project options", "Project selected", "[ New workspace… ]", "[ Adopt tmux session… ]"} {
		if !strings.Contains(projectView, want) {
			t.Fatalf("project context omitted %q:\n%s", want, projectView)
		}
	}
	if model.isTerminalMousePosition(model.layout().terminalX+10, model.layout().bodyContent+10) {
		t.Fatal("hidden terminal accepted mouse input through the project options panel")
	}
	layout := model.layout()
	updated, cmd := model.handleMouse(tea.MouseClickMsg{
		X:      layout.terminalX + contextInsetX,
		Y:      layout.bodyContent + contextInsetY + contextActionRow,
		Button: tea.MouseLeft,
	})
	got := updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.action != "create_workspace" {
		t.Fatalf("project context click did not open workspace form: %+v", got.modal)
	}

	model.selectedRow = 1
	workspaceView := ansi.Strip(model.View().Content)
	for _, want := range []string{"Workspace options", "Workspace selected", "[ New terminal ]", "[ Rename workspace… ]", "[ Delete workspace… ]"} {
		if !strings.Contains(workspaceView, want) {
			t.Fatalf("workspace context omitted %q:\n%s", want, workspaceView)
		}
	}
	layout = model.layout()
	updated, cmd = model.handleMouse(tea.MouseClickMsg{
		X:      layout.terminalX + contextInsetX,
		Y:      layout.bodyContent + contextInsetY + contextActionRow + 1,
		Button: tea.MouseLeft,
	})
	got = updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.action != "rename_workspace" || got.modal.fields[0].value != workspace.Name {
		t.Fatalf("workspace context click did not open rename form: %+v", got.modal)
	}
}

func TestKeyboardSelectionOpensAWindowAndKeepsSidebarNavigation(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	project := Project{ID: "111111111111111111111111", Name: "Project"}
	workspace := Workspace{ID: "222222222222222222222222", ProjectID: project.ID, Name: "Workspace"}
	window := Window{ID: "333333333333333333333333", WorkspaceID: workspace.ID, Name: "Terminal"}
	model := tuiModel{manager: &Manager{}, width: minimumWidth, height: minimumHeight, controlMode: true, selectedRow: 0, rows: []sidebarRow{
		{kind: "project", host: host, project: project},
		{kind: "window", host: host, project: project, workspace: workspace, window: window},
	}}

	updated, cmd := model.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	got := updated.(tuiModel)
	if cmd == nil || got.selectedRow != 1 || got.attachingID != window.ID || got.keepSidebarAttach != window.ID || !got.controlMode {
		t.Fatalf("keyboard window selection did not start a sidebar-preserving attach: %+v", got)
	}
}

func TestCreatedWorkspaceSelectsAndOpensItsAutomaticWindow(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	workspace := Workspace{ID: "111111111111111111111111", ProjectID: "222222222222222222222222", Name: "Work"}
	window := Window{ID: "333333333333333333333333", WorkspaceID: workspace.ID, Name: "Terminal", Session: "mce-333333333333333333333333"}
	manager := &Manager{ctx: context.Background(), state: ClientState{Hosts: []Host{host}}}
	model := tuiModel{manager: manager, width: minimumWidth, height: minimumHeight, actionBusy: true}
	updated, cmd := model.handleActionResult(actionResultMsg{action: "create_workspace", hostID: host.ID, value: createdWorkspace{workspace: workspace, window: window}})
	got := updated.(tuiModel)
	if cmd == nil || got.actionBusy || got.selectOnRefreshID != "w/"+window.ID || got.attachingID != window.ID || !strings.Contains(got.message, host.Name) {
		t.Fatalf("created workspace did not select and open its automatic window: %+v", got)
	}
}

func TestAddedProjectSelectsItAndFocusesSidebarAfterRefresh(t *testing.T) {
	oldProject := Project{ID: "111111111111111111111111", Name: "Old", Path: "/tmp/old"}
	newProject := Project{ID: "222222222222222222222222", Name: "New", Path: "/tmp/new"}
	host := Host{ID: localHostID, Name: localHostName, Projects: []Project{oldProject, newProject}}
	manager := &Manager{ctx: context.Background(), state: ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{host}}}
	model := tuiModel{
		manager: manager, width: minimumWidth, height: minimumHeight, actionBusy: true,
		statuses: []HostStatus{{Host: host}},
		rows:     []sidebarRow{{kind: "project", host: host, project: oldProject}},
	}

	updated, cmd := model.handleActionResult(actionResultMsg{action: "add_project", hostID: host.ID, value: newProject})
	got := updated.(tuiModel)
	if cmd == nil || got.actionBusy || !got.controlMode || got.selectOnRefreshID != "p/"+newProject.ID {
		t.Fatalf("added project did not request focused sidebar selection: %+v", got)
	}
	got.rebuildRows()
	if got.selectedRow < 0 || got.rows[got.selectedRow].project.ID != newProject.ID || got.selectOnRefreshID != "" {
		t.Fatalf("added project selection = %+v", got)
	}
}

func TestDeleteConfirmationDefaultsToCancelAndUsesDialogControls(t *testing.T) {
	model := tuiModel{manager: &Manager{}, controlMode: true, modal: &modal{kind: "confirm", action: "delete_window", delete: DeleteRequest{ID: testInstanceID}}}
	updated, cmd := model.handleModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(tuiModel)
	if cmd != nil || got.modal != nil || got.actionBusy {
		t.Fatalf("default confirmation did not cancel safely: %+v", got)
	}

	model.modal = &modal{kind: "confirm", action: "delete_window", delete: DeleteRequest{ID: testInstanceID}}
	updated, _ = model.handleModalKey(tea.KeyPressMsg{Code: tea.KeyRight})
	selected := updated.(tuiModel)
	updated, cmd = selected.handleModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = updated.(tuiModel)
	if cmd == nil || got.modal != nil || !got.actionBusy {
		t.Fatalf("selected Delete did not start: %+v", got)
	}
}

func TestAccountUsageShowsEveryAccountAndFailureState(t *testing.T) {
	summary := &usage.Summary{Accounts: []usage.AccountSummary{
		{Label: "delta", WeeklyWindow: usage.WindowSummary{UsedPercent: 40}},
		{Label: "alpha", WeeklyWindow: usage.WindowSummary{UsedPercent: 10}},
		{Label: "charlie", Error: "signed out", WeeklyWindow: usage.WindowSummary{UsedPercent: 0}},
		{Label: "bravo", WeeklyWindow: usage.WindowSummary{UsedPercent: 20}},
	}}
	rows := accountUsageRows(summary)
	if len(rows) != 4 || rows[2].label != "charlie" || rows[2].available {
		t.Fatalf("account rows = %+v", rows)
	}
	model := tuiModel{usage: accountUsageState{accounts: rows}}
	got := strings.Join(model.usageLines(38), "\n")
	for _, want := range []string{"alpha", "10% used", "bravo", "20% used", "charlie", "unavailable", "delta", "40% used"} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage is missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "+") || strings.Contains(got, "charlie 0%") || len(strings.Split(got, "\n")) != 4 {
		t.Fatalf("usage collapsed or misreported a failed account: %q", got)
	}
}

func TestAccountUsageNeverRendersAccountControlSequences(t *testing.T) {
	summary := &usage.Summary{Accounts: []usage.AccountSummary{{
		Label: "safe\x1b]52;c;clipboard\a\x1b[31m", WeeklyWindow: usage.WindowSummary{UsedPercent: 12},
	}}}
	got := strings.Join((tuiModel{usage: accountUsageState{accounts: accountUsageRows(summary)}}).usageLines(78), "\n")
	for _, forbidden := range []string{"\x1b]52", "\x1b[31m", "\a"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("usage rendered terminal control sequence %q: %q", forbidden, got)
		}
	}
}

func TestAccountUsageRetainsLastResultAfterRefreshFailure(t *testing.T) {
	model := tuiModel{usage: accountUsageState{accounts: []accountUsage{{label: "alpha", usedPercent: 42, available: true}}}}
	model.applyUsage(usageMsg{accounts: []accountUsage{{label: "alpha"}, {label: "bravo"}}, err: "usage refresh failed"})
	got := strings.Join(model.usageLines(78), "\n")
	for _, want := range []string{"alpha", "42% stale", "bravo", "unavailable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stale usage is missing %q: %q", want, got)
		}
	}
}

func TestAccountUsageRetainsOnlyFailedRowsAfterPartialRefresh(t *testing.T) {
	model := tuiModel{usage: accountUsageState{accounts: []accountUsage{
		{label: "alpha", usedPercent: 42, available: true},
		{label: "bravo", usedPercent: 15, available: true},
	}}}
	model.applyUsage(usageMsg{accounts: []accountUsage{
		{label: "alpha", usedPercent: 50, available: true},
		{label: "bravo"},
	}})
	got := strings.Join(model.usageLines(78), "\n")
	for _, want := range []string{"alpha", "50% used", "bravo", "15% stale"} {
		if !strings.Contains(got, want) {
			t.Fatalf("partial usage refresh is missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "42% used") || strings.Contains(got, "bravo unavailable") {
		t.Fatalf("partial usage refresh kept the wrong rows: %q", got)
	}
}

func TestAccountUsageKeepsOneIdentifiableRowPerLongLabel(t *testing.T) {
	label := strings.Repeat("wide-account-", 8) + "one"
	model := tuiModel{usage: accountUsageState{accounts: []accountUsage{
		{label: label, usedPercent: 10, available: true},
		{label: strings.Repeat("wide-account-", 8) + "two", usedPercent: 20, available: true},
	}}}
	lines := model.usageLines(22)
	if len(lines) != 2 || lines[0] == lines[1] || !strings.Contains(lines[0], "one") || !strings.Contains(lines[1], "two") {
		t.Fatalf("long usage labels are not identifiable: %q", lines)
	}
	for _, line := range lines {
		if lipgloss.Width(line) != 22 || !strings.Contains(line, "% used") {
			t.Fatalf("usage row is not compact: %q", line)
		}
	}
}

func TestAccountUsageOverflowAsksForMoreHeightInsteadOfHidingRows(t *testing.T) {
	accounts := make([]accountUsage, 20)
	for i := range accounts {
		accounts[i] = accountUsage{label: fmt.Sprintf("account-%02d-with-a-readable-name", i), usedPercent: i, available: true}
	}
	model := tuiModel{width: minimumWidth, height: minimumHeight, usage: accountUsageState{accounts: accounts}}
	view := ansi.Strip(model.View().Content)
	for _, want := range []string{"too small to show every account", "Enlarge the terminal", "never hidden or collapsed", "Ctrl+C quits"} {
		if !strings.Contains(view, want) {
			t.Fatalf("size blocker is missing %q:\n%s", want, view)
		}
	}
}

func TestSidebarUsageGeometryKeepsTerminalSizeAndMouseMappingExact(t *testing.T) {
	accounts := make([]accountUsage, 10)
	for i := range accounts {
		accounts[i] = accountUsage{label: fmt.Sprintf("account-%02d", i), usedPercent: i, available: true}
	}
	model := tuiModel{
		width: 100, height: 30, usage: accountUsageState{accounts: accounts}, selectedRow: 0,
		rows: []sidebarRow{{kind: "workspace"}, {kind: "workspace"}, {kind: "workspace"}},
	}
	layout := model.layout()
	if layout.bodyContent != 2 || layout.bodyHeight != model.height-4 || layout.usageHeight != len(accounts) || layout.sidebarHeight != layout.bodyHeight-layout.usageHeight-1 {
		t.Fatalf("sidebar usage layout = %+v", layout)
	}
	updated, cmd := model.handleMouse(tea.MouseClickMsg{X: 2, Y: layout.bodyContent + 1, Button: tea.MouseLeft})
	got := updated.(tuiModel)
	if cmd != nil || got.selectedRow != 1 {
		t.Fatalf("sidebar usage shifted mouse mapping: %+v", got)
	}
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: 2, Y: layout.bodyContent + layout.sidebarHeight + 1, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd != nil || got.selectedRow != 1 {
		t.Fatalf("usage row click changed sidebar selection: %+v", got)
	}
	updated, cmd = got.handleMouse(tea.MouseWheelMsg{X: 2, Y: layout.bodyContent + layout.sidebarHeight + 1, Button: tea.MouseWheelDown})
	got = updated.(tuiModel)
	if cmd != nil || got.selectedRow != 1 {
		t.Fatalf("usage row wheel changed sidebar selection: %+v", got)
	}
}

func TestAccountCountNeverChangesTerminalDimensions(t *testing.T) {
	base := tuiModel{width: minimumWidth, height: minimumHeight}.layout()
	for _, count := range []int{1, 5, 15} {
		accounts := make([]accountUsage, count)
		for index := range accounts {
			accounts[index] = accountUsage{label: fmt.Sprintf("account-%02d", index), usedPercent: index, available: true}
		}
		layout := (tuiModel{width: minimumWidth, height: minimumHeight, usage: accountUsageState{accounts: accounts}}).layout()
		if !layout.fits() || layout.terminalWidth != base.terminalWidth || layout.bodyHeight != base.bodyHeight || layout.bodyContent != base.bodyContent {
			t.Fatalf("%d accounts changed terminal geometry: base=%+v current=%+v", count, base, layout)
		}
	}
}

func TestAttachmentResizesAfterAccountBlockerClears(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	accounts := make([]accountUsage, 17)
	for index := range accounts {
		accounts[index] = accountUsage{label: fmt.Sprintf("account-%02d", index), loading: true}
	}
	attachment := &Attachment{pty: master, terminal: vt.NewSafeEmulator(10, 10)}
	model := tuiModel{width: minimumWidth, height: minimumHeight, attachment: attachment, usage: accountUsageState{accounts: accounts}}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: minimumWidth, Height: minimumHeight})
	got := updated.(tuiModel)
	if !got.resizePending {
		t.Fatal("blocked resize was not retained")
	}
	updated, _ = got.Update(usageMsg{accounts: accounts[:16]})
	got = updated.(tuiModel)
	if got.resizePending || attachment.terminal.Width() != got.terminalWidth() || attachment.terminal.Height() != got.bodyHeight() {
		t.Fatalf("attachment was not resized after blocker cleared: pending=%v terminal=%dx%d layout=%dx%d", got.resizePending, attachment.terminal.Width(), attachment.terminal.Height(), got.terminalWidth(), got.bodyHeight())
	}
}

func TestUnfocusedSidebarSelectionHasANonColorMarker(t *testing.T) {
	model := tuiModel{width: minimumWidth, height: minimumHeight, selectedRow: 0, rows: []sidebarRow{{kind: "project", project: Project{Name: "Project"}, host: Host{Name: "Host"}}}}
	line := strings.Split(ansi.Strip(model.renderSidebar()), "\n")[0]
	if !strings.HasPrefix(line, "›Project · Host") {
		t.Fatalf("unfocused selection has no text marker: %q", line)
	}
}

func TestUsageBoxStaysAtTheBottomOfTheSidebar(t *testing.T) {
	accounts := []accountUsage{
		{label: "alpha", usedPercent: 10, available: true},
		{label: "bravo", loading: true},
		{label: "charlie", available: false},
	}
	model := tuiModel{width: minimumWidth, height: minimumHeight, usage: accountUsageState{accounts: accounts}}
	lines := strings.Split(ansi.Strip(model.View().Content), "\n")
	layout := model.layout()
	bottomBorder := len(lines) - 2
	firstUsage := bottomBorder - layout.usageHeight
	separator := firstUsage - 1
	if !strings.HasPrefix(lines[separator], "├") || !strings.Contains(lines[separator], "Codex weekly use") || !strings.HasPrefix(lines[bottomBorder], "└") {
		t.Fatalf("usage box boundaries are misplaced:\n%s", strings.Join(lines, "\n"))
	}
	for index, account := range accounts {
		if !strings.Contains(lines[firstUsage+index], fitMiddlePlain(account.label, max(1, layout.sidebarWidth-2-lipgloss.Width(accountUsageStateText(account))-1))) {
			t.Fatalf("account %q is not in its bottom-sidebar row: %q", account.label, lines[firstUsage+index])
		}
	}
}

func TestMinimumHeightFitsSixteenAccountsAndBlocksSeventeen(t *testing.T) {
	for _, test := range []struct {
		count int
		fits  bool
	}{{16, true}, {17, false}} {
		accounts := make([]accountUsage, test.count)
		for index := range accounts {
			accounts[index] = accountUsage{label: fmt.Sprintf("a-%02d", index), usedPercent: index, available: true}
		}
		layout := (tuiModel{width: minimumWidth, height: minimumHeight, usage: accountUsageState{accounts: accounts}}).layout()
		if layout.fits() != test.fits {
			t.Fatalf("%d-account minimum-height fit = %v, want %v: %+v", test.count, layout.fits(), test.fits, layout)
		}
	}
}

func TestUsageRefreshIsSingleFlight(t *testing.T) {
	model := tuiModel{usageBusy: true}
	updated, cmd := model.Update(usageTickMsg(time.Now()))
	got := updated.(tuiModel)
	if cmd == nil || !got.usageBusy {
		t.Fatalf("busy usage refresh did not keep one timer and suppress overlap: %+v", got)
	}
}

func TestMinimumViewportShowsTitleUsageSidebarAndFooter(t *testing.T) {
	state := ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{{ID: localHostID, Name: localHostName}}}
	model := tuiModel{manager: &Manager{state: state}, width: minimumWidth, height: minimumHeight, usage: accountUsageState{accounts: []accountUsage{{label: "alpha", usedPercent: 42, available: true}}}, message: "ready"}
	view := ansi.Strip(model.View().Content)
	for _, want := range []string{"multicodex editor", "[ Actions ]", "[ Help ]", "Codex weekly use", "alpha", "42% used", "Projects", "No projects", "Set up your first terminal", "Click Actions, or ⌘B/Ctrl+G then Tab", "ready", "┌", "┬", "├", "┤", "┴"} {
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

func TestInitialRefreshDoesNotClaimConfiguredProjectsAreMissing(t *testing.T) {
	state := ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{{ID: localHostID, Name: localHostName}}}
	model := tuiModel{manager: &Manager{state: state}, width: minimumWidth, height: minimumHeight, refreshing: true}
	view := ansi.Strip(model.View().Content)
	for _, want := range []string{"Projects · connecting", "Loading projects…", "Connecting to hosts", "Connecting to configured hosts…"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in loading view:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"No projects", "Set up your first terminal", "Click Actions to begin"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("loading view contains premature empty-state text %q:\n%s", unwanted, view)
		}
	}
}

func TestFramedLayoutShowsEveryAccountAcrossViewportSizes(t *testing.T) {
	for _, width := range []int{80, 81, 100, 160} {
		for _, height := range []int{24, 30} {
			for _, count := range []int{0, 1, 5, 15} {
				accounts := make([]accountUsage, count)
				for i := range accounts {
					accounts[i] = accountUsage{label: fmt.Sprintf("acct-%02d", i), usedPercent: i, available: true}
				}
				model := tuiModel{width: width, height: height, usage: accountUsageState{accounts: accounts}}
				view := ansi.Strip(model.View().Content)
				lines := strings.Split(view, "\n")
				if len(lines) != height {
					t.Fatalf("%dx%d with %d accounts rendered %d lines", width, height, count, len(lines))
				}
				for line, text := range lines {
					if got := lipgloss.Width(text); got != width {
						t.Fatalf("%dx%d with %d accounts line %d width = %d", width, height, count, line, got)
					}
				}
				if !model.layout().fits() {
					if !strings.Contains(view, "Enlarge the terminal") {
						t.Fatalf("%dx%d with %d accounts did not show the size blocker", width, height, count)
					}
					continue
				}
				for i := range accounts {
					if !strings.Contains(view, accounts[i].label) {
						t.Fatalf("%dx%d hid account %q", width, height, accounts[i].label)
					}
				}
			}
		}
	}
}

func TestLongFocusLabelNeverHidesHeaderButtons(t *testing.T) {
	windowID := "111111111111111111111111"
	model := tuiModel{
		width: minimumWidth, height: minimumHeight, attachedID: windowID,
		rows: []sidebarRow{{kind: "window", host: Host{Name: strings.Repeat("host", 20)}, window: Window{ID: windowID, Name: strings.Repeat("window", 20)}}},
	}
	header := ansi.Strip(strings.Split(model.View().Content, "\n")[0])
	for _, want := range []string{"multicodex editor", actionsButtonLabel, helpButtonLabel} {
		if !strings.Contains(header, want) {
			t.Fatalf("long focus label hid %q: %q", want, header)
		}
	}
	if lipgloss.Width(header) != minimumWidth {
		t.Fatalf("header width = %d, want %d: %q", lipgloss.Width(header), minimumWidth, header)
	}
}

func TestHelpOpensFromTerminalInputWithoutEditorFocus(t *testing.T) {
	model := tuiModel{width: minimumWidth, height: minimumHeight}
	updated, cmd := model.handleKey(tea.KeyPressMsg{Code: '?', BaseCode: '/', Text: "?", Mod: tea.ModShift | tea.ModSuper})
	got := updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.kind != "help" {
		t.Fatalf("global Command+? did not open help: %+v", got)
	}
}

func TestHelpShowsEveryCoreControlAtMinimumSize(t *testing.T) {
	model := tuiModel{width: minimumWidth, height: minimumHeight, modal: &modal{kind: "help", title: "Controls"}}
	view := ansi.Strip(model.View().Content)
	for _, want := range []string{
		"Click project/workspace: options · window: open",
		"⌘B or Ctrl+G: focus the sidebar",
		"⌥↑/⌥↓: one screen",
		"⌘↑/⌘↓: first/last",
		"⌘N or Ctrl+N: create",
		"⌘R: rename",
		"⌘1–9 or ⌥1–9: open the numbered window",
		"Copy: iTerm2 ⌥-drag",
		"Paste: ⌘V",
		"[ Close ]",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("minimum-size Help omitted %q:\n%s", want, view)
		}
	}
}

func TestCommandVRemainsTerminalInput(t *testing.T) {
	attachment := &Attachment{inputQueue: make(chan terminalInput, 1)}
	model := tuiModel{width: minimumWidth, height: minimumHeight, attachment: attachment}
	updated, cmd := model.handleKey(tea.KeyPressMsg{Code: 'v', Text: "v", Mod: tea.ModSuper})
	got := updated.(tuiModel)
	if cmd != nil || got.actionBusy || got.modal != nil || len(attachment.inputQueue) != 1 {
		t.Fatalf("Command-V was treated as an editor action: %+v", got)
	}
	input := <-attachment.inputQueue
	if input.kind != "raw" || input.text == "" {
		t.Fatalf("Command-V terminal input = %+v", input)
	}
}

func TestOrdinaryPasteGoesToTerminal(t *testing.T) {
	attachment := &Attachment{inputQueue: make(chan terminalInput, 1)}
	model := tuiModel{width: minimumWidth, height: minimumHeight, attachment: attachment, controlMode: true}
	updated, cmd := model.Update(tea.PasteMsg{Content: "normal paste"})
	got := updated.(tuiModel)
	if cmd != nil || got.actionBusy || got.controlMode || got.message != "pasted into terminal; terminal focused" || len(attachment.inputQueue) != 1 {
		t.Fatalf("ordinary paste was not sent to the terminal: %+v", got)
	}
	input := <-attachment.inputQueue
	if input.kind != "paste" || input.text != "normal paste" {
		t.Fatalf("terminal paste = %+v", input)
	}
}

func TestPasteDoesNotPassThroughADialog(t *testing.T) {
	attachment := &Attachment{inputQueue: make(chan terminalInput, 1)}
	model := tuiModel{width: minimumWidth, height: minimumHeight, attachment: attachment, modal: &modal{kind: "help"}}
	updated, cmd := model.Update(tea.PasteMsg{Content: "hidden paste"})
	got := updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.message != "close the dialog before pasting" || len(attachment.inputQueue) != 0 {
		t.Fatalf("dialog paste was not blocked clearly: %+v", got)
	}
}

func TestPasteWithoutATerminalExplainsWhatToDo(t *testing.T) {
	model := tuiModel{width: minimumWidth, height: minimumHeight, controlMode: true}
	updated, cmd := model.Update(tea.PasteMsg{Content: "unused paste"})
	got := updated.(tuiModel)
	if cmd != nil || got.message != "open a terminal before pasting" {
		t.Fatalf("missing-terminal paste guidance = %+v", got)
	}
}

func TestITermFooterShowsNativeCopyAndPasteControls(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if got := terminalFooter(); !strings.Contains(got, "⌥-drag: select") || !strings.Contains(got, "⌘C/⌘V: copy/paste") {
		t.Fatalf("iTerm footer = %q", got)
	}
	t.Setenv("TERM_PROGRAM", "unknown")
	if got := terminalFooter(); strings.Contains(got, "⌥-drag") || !strings.Contains(got, "Ctrl+G: sidebar") {
		t.Fatalf("generic terminal footer = %q", got)
	}
}

func TestSizeBlockerNeverForwardsHiddenTerminalInput(t *testing.T) {
	attachment := &Attachment{inputQueue: make(chan terminalInput, 2)}
	model := tuiModel{width: minimumWidth - 1, height: minimumHeight, attachment: attachment}
	updated, cmd := model.Update(tea.PasteMsg{Content: "hidden paste"})
	got := updated.(tuiModel)
	if cmd != nil || got.attachment != attachment || len(attachment.inputQueue) != 0 {
		t.Fatalf("size blocker forwarded a hidden paste: %+v", got)
	}
	updated, cmd = got.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd != nil || len(attachment.inputQueue) != 0 {
		t.Fatalf("size blocker forwarded a hidden key")
	}
}

func TestMouseModeRoutesClicksToVisibleEditorControls(t *testing.T) {
	model := tuiModel{width: 100, height: 30, selectedRow: -1}
	view := model.View()
	if view.MouseMode != tea.MouseModeCellMotion || view.OnMouse != nil {
		t.Fatalf("mouse view configuration = %+v", view)
	}

	actionsX := lipgloss.Width(headerTitleText) + 2
	updated, cmd := model.Update(tea.MouseClickMsg{X: actionsX, Y: 0, Button: tea.MouseLeft})
	got := updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.kind != "actions" {
		t.Fatalf("Actions click = %+v", got)
	}

	helpIndex := -1
	for i, item := range got.modal.choices {
		if item.action == "help" {
			helpIndex = i
			break
		}
	}
	if helpIndex < 0 {
		t.Fatal("help action is missing")
	}
	layout := got.layout()
	mainX := layout.terminalX + modalInsetX
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: mainX, Y: layout.bodyContent + modalInsetY + 2 + helpIndex, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.kind != "help" {
		t.Fatalf("Help menu click = %+v", got)
	}
	layout = got.layout()
	closeY := layout.bodyContent + modalInsetY + 2 + len(helpModalContent()) - 1
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: layout.terminalX + modalInsetX, Y: closeY, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd != nil || got.modal != nil {
		t.Fatalf("Close button click = %+v", got)
	}
}

func TestDirectMouseUpdatePreservesInputOrder(t *testing.T) {
	model := tuiModel{width: 100, height: 30, selectedRow: -1}
	updated, cmd := model.Update(tea.MouseClickMsg{X: lipgloss.Width(headerTitleText) + 2, Y: 0, Button: tea.MouseLeft})
	if cmd != nil {
		t.Fatal("header mouse update returned an asynchronous command")
	}
	current := updated.(tuiModel)
	layout := current.layout()
	updated, cmd = current.Update(tea.MouseWheelMsg{X: layout.terminalX, Y: layout.bodyContent + 2, Button: tea.MouseWheelDown})
	if cmd != nil {
		t.Fatal("wheel update returned an asynchronous command")
	}
	current = updated.(tuiModel)
	updated, cmd = current.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	current = updated.(tuiModel)
	if cmd != nil || current.modal == nil || current.modal.kind != "help" {
		t.Fatalf("wheel then Enter chose the wrong action: %+v", current)
	}
}

func TestMouseSelectsSidebarRowsAndForwardsTerminalEvents(t *testing.T) {
	windowID := "111111111111111111111111"
	rows := []sidebarRow{
		{kind: "workspace", workspace: Workspace{ID: "222222222222222222222222", Name: "Work"}},
		{kind: "window", window: Window{ID: windowID, Name: "Terminal"}},
		{kind: "workspace", workspace: Workspace{ID: "333333333333333333333333", Name: "Other"}},
		{kind: "workspace", workspace: Workspace{ID: "444444444444444444444444", Name: "More"}},
		{kind: "workspace", workspace: Workspace{ID: "555555555555555555555555", Name: "Last"}},
	}
	attachment := &Attachment{inputQueue: make(chan terminalInput, 4)}
	model := tuiModel{width: 100, height: 30, rows: rows, selectedRow: 0, attachedID: windowID, attachment: attachment, controlMode: true}
	layout := model.layout()
	updated, cmd := model.handleMouse(tea.MouseClickMsg{X: 2, Y: layout.bodyContent + 1, Button: tea.MouseLeft})
	got := updated.(tuiModel)
	if cmd != nil || got.selectedRow != 1 || got.controlMode {
		t.Fatalf("window row click = %+v", got)
	}
	updated, _ = got.handleMouse(tea.MouseWheelMsg{X: 2, Y: layout.bodyContent + 1, Button: tea.MouseWheelDown})
	got = updated.(tuiModel)
	if got.selectedRow != 4 || got.controlMode {
		t.Fatalf("sidebar wheel = %+v", got)
	}

	layout = got.layout()
	terminalX, terminalY := layout.terminalX+4, layout.bodyContent+3
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: terminalX, Y: terminalY, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd != nil || got.controlMode {
		t.Fatalf("terminal click focus = %+v", got)
	}
	input := <-attachment.inputQueue
	if input.kind != "raw" || input.text != "\x1b[<0;5;4M" {
		t.Fatalf("forwarded terminal click = %+v", input)
	}
	bottomY := layout.bodyContent + layout.bodyHeight - 1
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: terminalX, Y: bottomY, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd != nil {
		t.Fatal("bottom terminal click returned an asynchronous command")
	}
	input = <-attachment.inputQueue
	want := fmt.Sprintf("\x1b[<0;5;%dM", layout.bodyHeight)
	if input.kind != "raw" || input.text != want {
		t.Fatalf("bottom terminal click = %+v, want %q", input, want)
	}
}

func TestMouseUsesFormAndConfirmationButtons(t *testing.T) {
	model := tuiModel{manager: &Manager{}, width: 100, height: 30, modal: &modal{kind: "form", action: "add_host", title: "Add", fields: []formField{{label: "Name"}, {label: "SSH alias"}}}}
	form := ansi.Strip(renderModal(*model.modal, model.terminalWidth(), model.bodyHeight()))
	for _, want := range []string{"Type values", "Tab/↑/↓: next field", "last field: add host", "Ctrl+U: clear this field", "Esc: cancel"} {
		if !strings.Contains(form, want) {
			t.Fatalf("form is missing %q:\n%s", want, form)
		}
	}
	layout := model.layout()
	mainX := layout.terminalX + modalInsetX
	updated, cmd := model.handleMouse(tea.MouseClickMsg{X: mainX, Y: layout.bodyContent + modalInsetY + 3, Button: tea.MouseLeft})
	got := updated.(tuiModel)
	if cmd != nil || got.modal.field != 1 {
		t.Fatalf("form field click = %+v", got)
	}
	layout = got.layout()
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: mainX, Y: layout.bodyContent + modalInsetY + 5, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd == nil || got.modal != nil || !got.actionBusy {
		t.Fatalf("Save button click = %+v", got)
	}

	model = tuiModel{manager: &Manager{}, width: 100, height: 30, modal: &modal{kind: "confirm", action: "delete_window", delete: DeleteRequest{ID: testInstanceID}}}
	layout = model.layout()
	buttonY := layout.bodyContent + modalInsetY + confirmButtonRow(*model.modal, model.terminalWidth()-modalInsetX)
	updated, cmd = model.handleMouse(tea.MouseClickMsg{X: layout.terminalX + modalInsetX, Y: buttonY, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd != nil || got.modal != nil || got.actionBusy {
		t.Fatalf("Cancel button click = %+v", got)
	}

	model.modal = &modal{kind: "confirm", action: "delete_window", delete: DeleteRequest{ID: testInstanceID}}
	layout = model.layout()
	buttonY = layout.bodyContent + modalInsetY + confirmButtonRow(*model.modal, model.terminalWidth()-modalInsetX)
	deleteX := layout.terminalX + modalInsetX + lipgloss.Width(cancelButtonLabel) + 3
	updated, cmd = model.handleMouse(tea.MouseClickMsg{X: deleteX, Y: buttonY, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd == nil || got.modal != nil || !got.actionBusy {
		t.Fatalf("Delete button click = %+v", got)
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

func TestFailedFormKeepsUserInputForCorrection(t *testing.T) {
	form := modal{kind: "form", action: "add_host", title: "Add SSH host", fields: []formField{
		{label: "Display name", value: "Build box"},
		{label: "SSH alias from ~/.ssh/config", value: "bad alias"},
	}}
	model := tuiModel{manager: &Manager{}, refreshing: true, actionBusy: true}
	updated, cmd := model.Update(actionResultMsg{action: "add_host", form: &form, err: errors.New("invalid SSH alias")})
	got := updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.fields[1].value != "bad alias" || got.actionBusy {
		t.Fatalf("failed form did not preserve its input: %+v", got)
	}
}

func TestFormPasteNeverRendersTerminalControlSequences(t *testing.T) {
	model := tuiModel{width: minimumWidth, height: minimumHeight, modal: &modal{kind: "form", title: "Add", fields: []formField{{label: "Name", limit: 80}}}}
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
	if len(model.rows) != 2 || !model.rows[0].offline || !model.rows[1].offline {
		t.Fatalf("offline project and workspace were not retained and marked: %+v", model.rows)
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
	if model.sidebarOffset == 0 || model.selectedRow < model.sidebarOffset || model.selectedRow >= model.sidebarOffset+model.sidebarHeight() {
		t.Fatalf("selection %d is outside offset %d and height %d", model.selectedRow, model.sidebarOffset, model.sidebarHeight())
	}
	view := ansi.Strip(model.renderSidebar())
	if strings.Count(view, "\n")+1 != model.sidebarHeight() {
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
	got := joinKeepRight("Sidebar · ↑/↓ · Enter open · Tab Actions · Esc back", strings.Repeat("status", 20), 80)
	if !strings.HasPrefix(got, "Sidebar · ↑/↓ · Enter open · Tab Actions · Esc back") || !strings.Contains(got, "…") {
		t.Fatalf("long status hid footer instructions: %q", got)
	}
}

func TestConfirmationWrapsReasonAndKeepsMouseButtonAligned(t *testing.T) {
	reason := "Delete workspace “feature-with-a-very-long-identifiable-name”, all its terminal windows, and its owned Git worktree and branch?"
	model := tuiModel{manager: &Manager{}, width: minimumWidth, height: minimumHeight, modal: &modal{
		kind: "confirm", action: "delete_workspace", title: "Delete workspace?", reason: reason, delete: DeleteRequest{ID: testInstanceID},
	}}
	rendered := ansi.Strip(renderModal(*model.modal, model.terminalWidth(), model.bodyHeight()))
	for _, want := range []string{"Git worktree and", "branch?", "This cannot be undone.", "default.", deleteButtonLabel, "Esc: cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("confirmation is missing %q:\n%s", want, rendered)
		}
	}
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) != model.terminalWidth() {
			t.Fatalf("confirmation line width = %d, want %d: %q", lipgloss.Width(line), model.terminalWidth(), line)
		}
	}
	layout := model.layout()
	deleteX := layout.terminalX + modalInsetX + lipgloss.Width(cancelButtonLabel) + 3
	buttonY := layout.bodyContent + modalInsetY + confirmButtonRow(*model.modal, model.terminalWidth()-modalInsetX)
	updated, cmd := model.handleMouse(tea.MouseClickMsg{X: deleteX, Y: buttonY, Button: tea.MouseLeft})
	got := updated.(tuiModel)
	if cmd == nil || got.modal != nil || !got.actionBusy {
		t.Fatalf("wrapped confirmation Delete click = %+v", got)
	}
}

func TestWorkspaceDeleteConfirmationCapturesAllWindows(t *testing.T) {
	workspaceID := "111111111111111111111111"
	firstWindowID := "222222222222222222222222"
	secondWindowID := "333333333333333333333333"
	workspace := Workspace{ID: workspaceID, Name: "Review", Git: true}
	model := tuiModel{
		selectedRow: 0,
		rows: []sidebarRow{
			{kind: "workspace", workspace: workspace},
			{kind: "window", workspace: workspace, window: Window{ID: firstWindowID}},
			{kind: "window", workspace: workspace, window: Window{ID: secondWindowID}},
		},
	}
	model.openDeleteConfirmation()
	if model.modal == nil || !strings.Contains(model.modal.reason, "all its terminal windows") {
		t.Fatalf("workspace confirmation = %+v", model.modal)
	}
	if got := model.modal.windowIDs; len(got) != 2 || got[0] != firstWindowID || got[1] != secondWindowID {
		t.Fatalf("workspace confirmation window IDs = %v", got)
	}
}

func TestDeletedWorkspaceClearsItsReconnectSelection(t *testing.T) {
	home := privateTestHome(t)
	windowID := "222222222222222222222222"
	state := ClientState{
		Version: stateVersion, InstanceID: testInstanceID, SelectedWindowID: windowID,
		Hosts: []Host{{ID: localHostID, Name: localHostName}},
	}
	store := NewStateStore(home)
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: store, state: state}
	model := tuiModel{
		manager: manager,
	}
	if err := model.forgetDeletedWorkspace([]string{windowID}); err != nil {
		t.Fatal(err)
	}
	if got := manager.State().SelectedWindowID; got != "" {
		t.Fatalf("deleted workspace left reconnect selection %q", got)
	}
	persisted, err := store.Load()
	if err != nil || persisted.SelectedWindowID != "" {
		t.Fatalf("deleted workspace selection was not saved: %+v, %v", persisted, err)
	}
}

func TestFormFieldKeepsTheEditableTailVisible(t *testing.T) {
	field := formField{label: "Project path", value: "/a/very/long/path/to/the/project-directory"}
	got := ansi.Strip(renderFormField(field, "› ", 30))
	if lipgloss.Width(got) > 30 || !strings.HasSuffix(got, "ect-directory") || !strings.Contains(got, "…") {
		t.Fatalf("long form value did not show its editable end: %q", got)
	}
}

func TestMiddleEllipsisPreservesUnicodeLabelEnds(t *testing.T) {
	got := fitMiddlePlain("账户-primary-long-label-αω", 16)
	if lipgloss.Width(got) != 16 || !strings.HasPrefix(got, "账户") || !strings.HasSuffix(got, "αω") || !strings.Contains(got, "…") {
		t.Fatalf("Unicode middle ellipsis = %q (%d cells)", got, lipgloss.Width(got))
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

func TestCreatedWindowBecomesSidebarSelectionAfterRefresh(t *testing.T) {
	projectID := "111111111111111111111111"
	workspaceID := "222222222222222222222222"
	oldWindowID := "333333333333333333333333"
	newWindowID := "444444444444444444444444"
	project := Project{ID: projectID, Name: "Project", Path: "/tmp/project"}
	host := Host{ID: localHostID, Name: localHostName, Projects: []Project{project}}
	manager := &Manager{state: ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{host}}}
	model := tuiModel{
		manager: manager, selectedRow: 2, selectOnRefreshID: "w/" + newWindowID,
		rows: []sidebarRow{
			{kind: "project", project: project},
			{kind: "workspace", workspace: Workspace{ID: workspaceID}},
			{kind: "window", window: Window{ID: oldWindowID}},
		},
		statuses: []HostStatus{{Host: host, Snapshot: HostSnapshot{
			Workspaces: []Workspace{{ID: workspaceID, ProjectID: projectID, Name: "Work"}},
			Windows: []Window{
				{ID: oldWindowID, WorkspaceID: workspaceID, Name: "Old"},
				{ID: newWindowID, WorkspaceID: workspaceID, Name: "New"},
			},
		}}},
	}
	model.rebuildRows()
	if model.rows[model.selectedRow].window.ID != newWindowID || model.selectOnRefreshID != "" {
		t.Fatalf("new window selection = %+v", model.rows[model.selectedRow])
	}
}

func TestDeleteResultOnlyConfirmsForceableRisk(t *testing.T) {
	manager := &Manager{state: ClientState{Hosts: []Host{{ID: localHostID, Name: localHostName}}}}
	model := tuiModel{manager: manager, actionBusy: true}
	updated, _ := model.handleActionResult(actionResultMsg{
		action: "delete_workspace", hostID: localHostID, targetID: "111111111111111111111111",
		value: DeleteResult{Reason: "terminal window ownership is uncertain"},
	})
	got := updated.(tuiModel)
	if got.modal != nil || got.message != "terminal window ownership is uncertain" {
		t.Fatalf("non-forceable refusal opened confirmation: %+v", got)
	}
	got.actionBusy = true
	windowIDs := []string{"222222222222222222222222", "333333333333333333333333"}
	updated, _ = got.handleActionResult(actionResultMsg{
		action: "delete_workspace", hostID: localHostID, targetID: "111111111111111111111111",
		windowIDs: windowIDs, value: DeleteResult{Reason: "workspace has 2 live terminal windows", Forceable: true},
	})
	got = updated.(tuiModel)
	if got.modal == nil || !got.modal.delete.Force || len(got.modal.windowIDs) != 2 || got.modal.windowIDs[0] != windowIDs[0] || got.modal.windowIDs[1] != windowIDs[1] {
		t.Fatalf("forceable risk did not open confirmation: %+v", got)
	}
}

func TestBackgroundCleanupDoesNotBlockOrClearUserAction(t *testing.T) {
	model := tuiModel{cleanupBusy: true, message: "ready"}
	if !model.beginAction("creating workspace…") || !model.actionBusy {
		t.Fatal("background cleanup blocked an independent user action")
	}
	updated, _ := model.handleActionResult(actionResultMsg{
		action: "background_cleanup", value: map[string]CleanupResult{},
	})
	got := updated.(tuiModel)
	if got.cleanupBusy || !got.actionBusy || got.message != "creating workspace…" {
		t.Fatalf("background cleanup changed user-action state: %+v", got)
	}
}

func TestAttachmentPasteWaitsForItsTargetAndRetriesBusyInput(t *testing.T) {
	targetID := "111111111111111111111111"
	model := tuiModel{refreshing: true, actionBusy: true}
	updated, _ := model.handleActionResult(actionResultMsg{
		action: "put_file", targetID: targetID,
		value: AttachmentFile{Path: "/srv/.multicodex/editor/attachments/file.txt"},
	})
	model = updated.(tuiModel)
	if model.pendingPastes[targetID] == "" || !strings.Contains(model.message, "when its window opens") {
		t.Fatalf("switched-window attachment was lost: %+v", model)
	}
	attachment := &Attachment{inputQueue: make(chan terminalInput, 1)}
	attachment.inputQueue <- terminalInput{kind: "text", text: "busy"}
	model.attachment, model.attachedID = attachment, targetID
	if model.flushPendingPaste(targetID) || model.pendingPastes[targetID] == "" {
		t.Fatal("busy terminal input discarded the pending attachment")
	}
	<-attachment.inputQueue
	if !model.flushPendingPaste(targetID) || model.pendingPastes[targetID] != "" {
		t.Fatal("pending attachment was not retried after terminal input drained")
	}
	input := <-attachment.inputQueue
	if input.kind != "paste" || !strings.Contains(input.text, "file.txt") {
		t.Fatalf("retried attachment input = %+v", input)
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
	if got != "cleanup: 1 windows, 2 workspaces, 3 attachments removed · 2 skipped: busy" {
		t.Fatalf("cleanup summary = %q", got)
	}
	if !cleanupResultHasNews(results) || cleanupResultHasNews(map[string]CleanupResult{"local": {}}) {
		t.Fatal("cleanup result news detection is incorrect")
	}
}

func TestBusyRefreshKeepsTheLastReachableHostState(t *testing.T) {
	host := Host{ID: localHostID, Name: localHostName}
	workspace := Workspace{ID: "111111111111111111111111"}
	model := tuiModel{statuses: []HostStatus{{Host: host, Snapshot: HostSnapshot{Workspaces: []Workspace{workspace}}}}}
	model.mergeStatuses([]HostStatus{{Host: host, Error: "request timed out", Busy: true}})
	if len(model.statuses) != 1 || model.statuses[0].Error != "" || len(model.statuses[0].Snapshot.Workspaces) != 1 {
		t.Fatalf("busy refresh replaced reachable state: %+v", model.statuses)
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
	model.openActionMenu()
	selectEditorAction(t, &model, "cleanup")
	updated, cmd := model.handleModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	first := updated.(tuiModel)
	if cmd == nil || !first.actionBusy {
		t.Fatalf("first cleanup did not start: %+v", first)
	}
	first.openActionMenu()
	selectEditorAction(t, &first, "cleanup")
	updated, cmd = first.handleModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	second := updated.(tuiModel)
	if cmd != nil || !second.actionBusy || !strings.Contains(second.message, "still running") {
		t.Fatalf("repeated cleanup was not suppressed: %+v", second)
	}
}

func selectEditorAction(t *testing.T, model *tuiModel, action string) {
	t.Helper()
	if model.modal == nil || model.modal.kind != "actions" {
		t.Fatal("editor action menu is not open")
	}
	for i, item := range model.modal.choices {
		if item.action == action {
			model.modal.choice = i
			return
		}
	}
	t.Fatalf("editor action %q is missing", action)
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
