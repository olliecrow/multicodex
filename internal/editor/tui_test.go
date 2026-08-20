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
	if cmd != nil || got.modal != nil {
		t.Fatalf("legacy help key opened an action: %+v", got)
	}
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
	rendered := ansi.Strip(renderModal(*got.modal, 60, 24))
	for _, want := range []string{"Editor actions", "New window…", "Add SSH host…", "Run safe cleanup", "[ Cancel ]", "Enter: run", "Esc: cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("action menu is missing %q:\n%s", want, rendered)
		}
	}
	helpWidth := (tuiModel{width: minimumWidth, height: minimumHeight}).terminalWidth()
	for _, line := range helpModalContent() {
		if lipgloss.Width(line) > helpWidth {
			t.Fatalf("minimum-width help line is clipped: %q", line)
		}
	}
	help := ansi.Strip(renderModal(modal{kind: "help", title: "Controls"}, helpWidth, minimumHeight-7))
	for _, want := range []string{"Click windows, actions, fields, choices, buttons", "scroll terminal history", "Need terminal Ctrl+G?", "[ Close ]"} {
		if !strings.Contains(help, want) {
			t.Fatalf("minimum-width help truncated %q:\n%s", want, help)
		}
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
	for _, want := range []string{"alpha 10%", "bravo 20%", "charlie unavailable", "delta 40%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage is missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "+") || strings.Contains(got, "charlie 0%") {
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
	for _, want := range []string{"alpha 42% (stale)", "bravo unavailable"} {
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
	for _, want := range []string{"alpha 50%", "bravo 15% (stale)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("partial usage refresh is missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "alpha 42%") || strings.Contains(got, "bravo unavailable") {
		t.Fatalf("partial usage refresh kept the wrong rows: %q", got)
	}
}

func TestAccountUsageWrapsWithoutHidingLongLabels(t *testing.T) {
	label := strings.Repeat("wide-account-", 8)
	model := tuiModel{usage: accountUsageState{accounts: []accountUsage{
		{label: label, usedPercent: 10, available: true},
		{label: "second", usedPercent: 20, available: true},
	}}}
	got := strings.Join(model.usageLines(24), "")
	if !strings.Contains(got, label) || !strings.Contains(got, "second 20%") || strings.Contains(got, "+") {
		t.Fatalf("wrapped usage hid an account: %q", got)
	}
}

func TestAccountUsageOverflowAsksForMoreHeightInsteadOfHidingRows(t *testing.T) {
	accounts := make([]accountUsage, 20)
	for i := range accounts {
		accounts[i] = accountUsage{label: fmt.Sprintf("account-%02d-with-a-readable-name", i), usedPercent: i, available: true}
	}
	model := tuiModel{width: minimumWidth, height: minimumHeight, usage: accountUsageState{accounts: accounts}}
	view := ansi.Strip(model.View().Content)
	for _, want := range []string{"too small to show every account usage", "Enlarge the terminal", "never hidden or collapsed", "Ctrl+C quits"} {
		if !strings.Contains(view, want) {
			t.Fatalf("size blocker is missing %q:\n%s", want, view)
		}
	}
}

func TestMultilineUsageGeometryKeepsSidebarMouseMappingExact(t *testing.T) {
	accounts := make([]accountUsage, 10)
	for i := range accounts {
		accounts[i] = accountUsage{label: fmt.Sprintf("account-%02d", i), usedPercent: i, available: true}
	}
	model := tuiModel{
		width: 100, height: 30, usage: accountUsageState{accounts: accounts}, selectedRow: 0,
		rows: []sidebarRow{{kind: "workspace"}, {kind: "workspace"}, {kind: "workspace"}},
	}
	usageLines := model.usageLines(model.width - 2)
	if len(usageLines) < 2 {
		t.Fatalf("fixture did not create multiline usage: %q", usageLines)
	}
	layout := model.layout()
	if layout.bodyContent != 4+len(usageLines) || layout.bodyHeight != model.height-len(usageLines)-6 {
		t.Fatalf("multiline layout = %+v, usage lines = %d", layout, len(usageLines))
	}
	updated, cmd := model.handleMouse(tea.MouseClickMsg{X: 2, Y: layout.bodyContent + 1, Button: tea.MouseLeft})
	got := updated.(tuiModel)
	if cmd != nil || got.selectedRow != 1 {
		t.Fatalf("multiline usage shifted sidebar mouse mapping: %+v", got)
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
	for _, want := range []string{"multicodex editor", "[ Actions ]", "[ Help ]", "Weekly account usage", "alpha 42%", "Workspaces", "No workspaces", "Use Actions to create or open a window", "ready", "┌", "┬", "┴"} {
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
	updated, cmd := model.handleKey(tea.KeyPressMsg{Code: tea.KeyF1})
	got := updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.kind != "help" {
		t.Fatalf("global F1 did not open help: %+v", got)
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
	mainX := layout.terminalX
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: mainX, Y: layout.bodyContent + 2 + helpIndex, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd != nil || got.modal == nil || got.modal.kind != "help" {
		t.Fatalf("Help menu click = %+v", got)
	}
	layout = got.layout()
	closeY := layout.bodyContent + 2 + len(helpModalContent()) - 1
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: layout.terminalX, Y: closeY, Button: tea.MouseLeft})
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
	if cmd != nil || current.modal == nil || current.modal.kind != "form" || current.modal.action != "add_host" {
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
	if got.selectedRow != 4 || !got.controlMode {
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
}

func TestMouseUsesFormAndConfirmationButtons(t *testing.T) {
	model := tuiModel{manager: &Manager{}, width: 100, height: 30, modal: &modal{kind: "form", action: "add_host", title: "Add", fields: []formField{{label: "Name"}, {label: "SSH alias"}}}}
	layout := model.layout()
	mainX := layout.terminalX
	updated, cmd := model.handleMouse(tea.MouseClickMsg{X: mainX, Y: layout.bodyContent + 3, Button: tea.MouseLeft})
	got := updated.(tuiModel)
	if cmd != nil || got.modal.field != 1 {
		t.Fatalf("form field click = %+v", got)
	}
	layout = got.layout()
	updated, cmd = got.handleMouse(tea.MouseClickMsg{X: mainX, Y: layout.bodyContent + 5, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd == nil || got.modal != nil || !got.actionBusy {
		t.Fatalf("Save button click = %+v", got)
	}

	model = tuiModel{manager: &Manager{}, width: 100, height: 30, modal: &modal{kind: "confirm", action: "delete_window", delete: DeleteRequest{ID: testInstanceID}}}
	layout = model.layout()
	updated, cmd = model.handleMouse(tea.MouseClickMsg{X: layout.terminalX, Y: layout.bodyContent + 6, Button: tea.MouseLeft})
	got = updated.(tuiModel)
	if cmd != nil || got.modal != nil || got.actionBusy {
		t.Fatalf("Cancel button click = %+v", got)
	}

	model.modal = &modal{kind: "confirm", action: "delete_window", delete: DeleteRequest{ID: testInstanceID}}
	layout = model.layout()
	deleteX := layout.terminalX + lipgloss.Width(cancelButtonLabel) + 3
	updated, cmd = model.handleMouse(tea.MouseClickMsg{X: deleteX, Y: layout.bodyContent + 6, Button: tea.MouseLeft})
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

func TestCreatedWindowBecomesSidebarSelectionAfterRefresh(t *testing.T) {
	projectID := "111111111111111111111111"
	workspaceID := "222222222222222222222222"
	oldWindowID := "333333333333333333333333"
	newWindowID := "444444444444444444444444"
	project := Project{ID: projectID, Name: "Project", Path: "/tmp/project"}
	host := Host{ID: localHostID, Name: localHostName, Projects: []Project{project}}
	manager := &Manager{state: ClientState{Version: stateVersion, InstanceID: testInstanceID, Hosts: []Host{host}}}
	model := tuiModel{
		manager: manager, selectedRow: 2, selectOnRefreshID: newWindowID,
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
		value: DeleteResult{Reason: "delete the workspace windows first"},
	})
	got := updated.(tuiModel)
	if got.modal != nil || got.message != "delete the workspace windows first" {
		t.Fatalf("non-forceable refusal opened confirmation: %+v", got)
	}
	got.actionBusy = true
	updated, _ = got.handleActionResult(actionResultMsg{
		action: "delete_workspace", hostID: localHostID, targetID: "111111111111111111111111",
		value: DeleteResult{Reason: "worktree has uncommitted changes", Forceable: true},
	})
	got = updated.(tuiModel)
	if got.modal == nil || !got.modal.delete.Force {
		t.Fatalf("forceable risk did not open confirmation: %+v", got)
	}
}

func TestBackgroundCleanupDoesNotBlockOrClearUserAction(t *testing.T) {
	model := tuiModel{cleanupBusy: true}
	if !model.beginAction("creating workspace…") || !model.actionBusy {
		t.Fatal("background cleanup blocked an independent user action")
	}
	updated, _ := model.handleActionResult(actionResultMsg{
		action: "background_cleanup", value: map[string]CleanupResult{},
	})
	got := updated.(tuiModel)
	if got.cleanupBusy || !got.actionBusy {
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
