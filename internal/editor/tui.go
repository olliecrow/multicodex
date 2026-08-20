package editor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/olliecrow/multicodex/internal/monitor/usage"
	"golang.org/x/term"
)

type Options struct {
	MulticodexHome string
}

type refreshMsg struct {
	statuses []HostStatus
}

type usageMsg struct {
	text string
}

type refreshTickMsg time.Time
type cleanupTickMsg time.Time
type usageTickMsg time.Time

type editorMouseMsg struct {
	event tea.MouseMsg
}

const (
	headerTitleText    = " multicodex editor "
	actionsButtonLabel = "[ Actions ]"
	helpButtonLabel    = "[ Help ]"
	cancelButtonLabel  = "[ Cancel ]"
	saveButtonLabel    = "[ Save ]"
	deleteButtonLabel  = "[ Delete ]"
	closeButtonLabel   = "[ Close ]"
)

type attachResultMsg struct {
	host       Host
	window     Window
	attachment *Attachment
	err        error
}

type attachmentDoneMsg struct {
	windowID   string
	attachment *Attachment
}

type attachmentUpdateMsg struct {
	windowID   string
	attachment *Attachment
	closed     bool
}

type actionResultMsg struct {
	action   string
	hostID   string
	targetID string
	value    any
	err      error
}

type sidebarRow struct {
	kind      string
	host      Host
	project   Project
	workspace Workspace
	window    Window
	slot      int
	offline   bool
}

type formField struct {
	label string
	value string
	limit int
}

type choice struct {
	label     string
	action    string
	host      Host
	project   Project
	workspace Workspace
}

type modal struct {
	kind      string
	action    string
	title     string
	fields    []formField
	field     int
	choices   []choice
	choice    int
	host      Host
	project   Project
	workspace Workspace
	window    Window
	delete    DeleteRequest
	reason    string
}

type tuiModel struct {
	manager           *Manager
	usageFetcher      *usage.Fetcher
	workers           *uiWorkers
	width             int
	height            int
	statuses          []HostStatus
	rows              []sidebarRow
	selectedRow       int
	sidebarOffset     int
	controlMode       bool
	refreshing        bool
	actionBusy        bool
	cleanupBusy       bool
	modal             *modal
	attachment        *Attachment
	attachedHost      string
	attachedID        string
	attachingID       string
	queuedAttach      *sidebarRow
	selectOnRefreshID string
	pendingPastes     map[string]string
	message           string
	usageText         string
}

type uiWorkers struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

func newUIWorkers(parent context.Context) *uiWorkers {
	ctx, cancel := context.WithCancel(parent)
	return &uiWorkers{ctx: ctx, cancel: cancel}
}

func (w *uiWorkers) track(command tea.Cmd) tea.Cmd {
	if command == nil {
		return nil
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.wg.Add(1)
	w.mu.Unlock()
	return func() tea.Msg {
		defer w.wg.Done()
		return command()
	}
}

func (w *uiWorkers) stop() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		w.cancel()
	}
	w.mu.Unlock()
}

func (w *uiWorkers) wait() {
	w.wg.Wait()
}

func Run(opts Options) (runErr error) {
	if os.Getenv("TMUX") != "" {
		return errors.New("multicodex editor never runs inside tmux; detach from tmux and start it in a normal terminal")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("multicodex editor requires an interactive terminal")
	}
	manager, err := NewManager(opts.MulticodexHome)
	if err != nil {
		return err
	}
	var workers *uiWorkers
	var fetcher *usage.Fetcher
	defer func() {
		if workers != nil {
			workers.stop()
		}
		if err := manager.Close(); runErr == nil {
			runErr = err
		}
		if workers != nil {
			workers.wait()
		}
		if fetcher != nil {
			if err := fetcher.Close(); runErr == nil {
				runErr = err
			}
		}
	}()
	checkContext, cancelCheck := context.WithTimeout(manager.Context(), 10*time.Second)
	err = manager.CheckLocal(checkContext)
	cancelCheck()
	if err != nil {
		return err
	}
	fetcher = usage.NewDefaultFetcherWithAccountOptions(usage.MonitorAccountOptions{IncludeDefault: true})
	workers = newUIWorkers(manager.Context())

	model := tuiModel{manager: manager, usageFetcher: fetcher, workers: workers, usageText: "usage …", selectedRow: -1, refreshing: true, cleanupBusy: true}
	final, err := tea.NewProgram(model).Run()
	if finished, ok := final.(tuiModel); ok && finished.attachment != nil {
		_ = finished.attachment.Close()
	}
	return err
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		m.track(refreshCmd(m.manager)), m.track(usageCmd(m.usageFetcher.Fetch, m.workerContext())),
		m.refreshTick(), m.track(cleanupCmd(m.manager, "background_cleanup")), m.cleanupTick(), m.usageTick(),
	)
}

func (m tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ensureSelectionVisible()
		if m.attachment != nil && m.hasUsableSize() {
			_ = m.attachment.Resize(m.terminalWidth(), m.bodyHeight())
		}
	case refreshMsg:
		m.refreshing = false
		m.mergeStatuses(msg.statuses)
		m.rebuildRows()
		m.flushPendingPaste(m.attachedID)
		if m.hasUsableSize() && m.attachedID == "" && m.attachingID == "" {
			if row, ok := m.preferredWindow(); ok {
				m.selectedRow = m.rowIndexForWindow(row.window.ID)
				return m, m.requestAttach(row)
			}
		}
	case usageMsg:
		m.usageText = msg.text
	case refreshTickMsg:
		return m, tea.Batch(m.startRefresh(), m.refreshTick())
	case cleanupTickMsg:
		if m.cleanupBusy {
			return m, m.cleanupTick()
		}
		m.cleanupBusy = true
		return m, tea.Batch(m.track(cleanupCmd(m.manager, "background_cleanup")), m.cleanupTick())
	case usageTickMsg:
		return m, tea.Batch(m.track(usageCmd(m.usageFetcher.Fetch, m.workerContext())), m.usageTick())
	case attachResultMsg:
		if msg.window.ID != m.attachingID {
			if msg.attachment != nil {
				_ = msg.attachment.Close()
			}
			return m, nil
		}
		m.attachingID = ""
		if m.queuedAttach != nil {
			queued := *m.queuedAttach
			m.queuedAttach = nil
			if msg.attachment != nil {
				_ = msg.attachment.Close()
			}
			if queued.window.ID == m.attachedID {
				m.message = "kept current window"
				return m, nil
			}
			return m, m.requestAttach(queued)
		}
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
		}
		if m.attachment != nil {
			_ = m.attachment.Close()
		}
		m.attachment = msg.attachment
		m.attachedHost, m.attachedID = msg.host.ID, msg.window.ID
		if m.hasUsableSize() {
			_ = m.attachment.Resize(m.terminalWidth(), m.bodyHeight())
		}
		m.controlMode = false
		m.message = "connected to " + msg.window.Name
		if err := m.manager.SetSelectedWindow(msg.window.ID); err != nil {
			m.message = "connected, but reconnect selection was not saved: " + err.Error()
		}
		m.flushPendingPaste(msg.window.ID)
		return m, tea.Batch(m.track(attachmentDoneCmd(msg.window.ID, msg.attachment)), m.track(attachmentUpdateCmd(msg.window.ID, msg.attachment)))
	case attachmentUpdateMsg:
		if msg.windowID != m.attachedID || msg.attachment != m.attachment {
			return m, nil
		}
		if msg.closed {
			return m, nil
		}
		m.flushPendingPaste(msg.windowID)
		return m, m.track(attachmentUpdateCmd(msg.windowID, msg.attachment))
	case attachmentDoneMsg:
		if msg.windowID != m.attachedID || msg.attachment != m.attachment {
			return m, nil
		}
		_ = m.attachment.Close()
		m.attachment = nil
		m.attachedHost = ""
		m.attachedID = ""
		m.message = "terminal disconnected; reconnecting…"
		return m, m.startRefresh()
	case actionResultMsg:
		return m.handleActionResult(msg)
	case tea.PasteMsg:
		if m.modal != nil && m.modal.kind == "form" {
			field := &m.modal.fields[m.modal.field]
			plain := plainDisplayText(msg.Content)
			field.value = appendBounded(field.value, plain, field.limit)
			if plain != msg.Content {
				m.message = "paste omitted terminal control characters"
			}
			return m, nil
		}
		if !m.controlMode && m.attachment != nil {
			if err := m.attachment.Paste(msg.Content); err != nil {
				m.message = err.Error()
			}
		}
	case tea.FocusMsg:
		if m.attachment != nil {
			m.attachment.SendFocus(true)
		}
	case tea.BlurMsg:
		if m.attachment != nil {
			m.attachment.SendFocus(false)
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case editorMouseMsg:
		return m.handleMouse(msg.event)
	}
	return m, nil
}

func (m tuiModel) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		return m.handleModalKey(key)
	}
	if key.Keystroke() == "super+v" || key.Keystroke() == "shift+super+v" {
		return m.startClipboardAttachment()
	}
	if slot := windowSlotKey(key); slot > 0 {
		return m.selectWindowSlot(slot)
	}
	if !m.controlMode {
		if key.Keystroke() == "ctrl+g" {
			m.controlMode = true
			m.message = "editor controls active"
			return m, nil
		}
		if m.attachment != nil {
			if err := m.attachment.SendKey(key); err != nil {
				m.message = err.Error()
			}
		}
		return m, nil
	}

	switch key.Keystroke() {
	case "ctrl+g", "esc":
		m.controlMode = false
		m.message = "terminal input active"
	case "ctrl+c":
		return m, tea.Quit
	case "up":
		m.moveSelection(-1)
	case "down":
		m.moveSelection(1)
	case "home":
		m.selectSidebarEdge(false)
	case "end":
		m.selectSidebarEdge(true)
	case "pgup":
		m.moveSelectionPage(-1)
	case "pgdown":
		m.moveSelectionPage(1)
	case "enter":
		return m.selectCurrentRow()
	case "tab", "shift+tab", "right":
		m.openActionMenu()
	case "f1":
		m.modal = &modal{kind: "help", title: "Controls"}
	}
	return m, nil
}

func (m tuiModel) handleModalKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Keystroke() == "esc" {
		m.modal = nil
		return m, nil
	}
	switch m.modal.kind {
	case "help":
		if key.Keystroke() == "enter" || key.Keystroke() == "f1" {
			m.modal = nil
		}
	case "choice", "actions":
		switch key.Keystroke() {
		case "up":
			m.modal.choice = max(0, m.modal.choice-1)
		case "down":
			m.modal.choice = min(len(m.modal.choices)-1, m.modal.choice+1)
		case "shift+tab":
			if len(m.modal.choices) > 0 {
				m.modal.choice = (m.modal.choice - 1 + len(m.modal.choices)) % len(m.modal.choices)
			}
		case "tab":
			if len(m.modal.choices) > 0 {
				m.modal.choice = (m.modal.choice + 1) % len(m.modal.choices)
			}
		case "home":
			m.modal.choice = 0
		case "end":
			m.modal.choice = max(0, len(m.modal.choices)-1)
		case "enter":
			return m.activateModalChoice()
		}
	case "confirm":
		switch key.Keystroke() {
		case "left", "shift+tab":
			m.modal.choice = 0
		case "right":
			m.modal.choice = 1
		case "tab":
			m.modal.choice = 1 - m.modal.choice
		case "enter":
			return m.activateConfirmation()
		}
	case "form":
		switch key.Keystroke() {
		case "enter":
			if m.modal.field+1 < len(m.modal.fields) {
				m.modal.field++
				return m, nil
			}
			return m.submitModalForm()
		case "tab", "down":
			m.modal.field = (m.modal.field + 1) % len(m.modal.fields)
		case "shift+tab", "up":
			m.modal.field = (m.modal.field - 1 + len(m.modal.fields)) % len(m.modal.fields)
		case "backspace":
			value := m.modal.fields[m.modal.field].value
			if value != "" {
				_, size := utf8.DecodeLastRuneInString(value)
				m.modal.fields[m.modal.field].value = value[:len(value)-size]
			}
		case "ctrl+u":
			m.modal.fields[m.modal.field].value = ""
		default:
			if isDirectTextKey(key) {
				field := &m.modal.fields[m.modal.field]
				field.value = appendBounded(field.value, key.Text, field.limit)
			}
		}
	}
	return m, nil
}

func (m tuiModel) handleMouse(event tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := event.Mouse()
	if !m.hasUsableSize() || mouse.X < 0 || mouse.X >= m.width || mouse.Y < 0 || mouse.Y >= m.height {
		return m, nil
	}
	if m.modal != nil {
		return m.handleModalMouse(event)
	}
	if _, ok := event.(tea.MouseWheelMsg); ok {
		if mouse.Y >= 1 && mouse.Y <= m.bodyHeight() {
			if mouse.X < m.sidebarWidth() {
				delta, vertical := verticalWheelDelta(mouse.Button, 3)
				if !vertical {
					return m, nil
				}
				m.moveSelectionClamped(delta)
				m.controlMode = true
				return m, nil
			}
			if mouse.X > m.sidebarWidth() {
				return m.forwardTerminalMouse(event)
			}
		}
		return m, nil
	}
	if click, ok := event.(tea.MouseClickMsg); ok {
		if click.Button != tea.MouseLeft {
			if m.isTerminalMousePosition(click.X, click.Y) {
				return m.forwardTerminalMouse(event)
			}
			return m, nil
		}
		if click.Y == 0 {
			switch headerButtonAt(click.X) {
			case "actions":
				m.openActionMenu()
			case "help":
				m.modal = &modal{kind: "help", title: "Controls"}
			}
			return m, nil
		}
		if click.Y >= 1 && click.Y <= m.bodyHeight() && click.X < m.sidebarWidth() {
			index := m.sidebarOffset + click.Y - 1
			if index < 0 || index >= len(m.rows) {
				return m, nil
			}
			m.selectedRow = index
			m.controlMode = true
			if m.rows[index].kind == "window" {
				return m.selectCurrentRow()
			}
			return m, nil
		}
		if m.isTerminalMousePosition(click.X, click.Y) {
			return m.forwardTerminalMouse(event)
		}
		return m, nil
	}
	if release, ok := event.(tea.MouseReleaseMsg); ok && m.isTerminalMousePosition(release.X, release.Y) {
		return m.forwardTerminalMouse(event)
	}
	return m, nil
}

func (m tuiModel) handleModalMouse(event tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := event.Mouse()
	if mouse.X <= m.sidebarWidth() || mouse.X >= m.width || mouse.Y < 1 || mouse.Y > m.bodyHeight() {
		return m, nil
	}
	if _, ok := event.(tea.MouseWheelMsg); ok {
		delta, vertical := verticalWheelDelta(mouse.Button, 3)
		if !vertical {
			return m, nil
		}
		switch m.modal.kind {
		case "choice", "actions":
			m.modal.choice = max(0, min(len(m.modal.choices)-1, m.modal.choice+delta))
		case "form":
			if len(m.modal.fields) > 0 {
				m.modal.field = max(0, min(len(m.modal.fields)-1, m.modal.field+delta))
			}
		}
		return m, nil
	}
	click, ok := event.(tea.MouseClickMsg)
	if !ok || click.Button != tea.MouseLeft {
		return m, nil
	}
	x, y := click.X-m.sidebarWidth()-1, click.Y-1
	switch m.modal.kind {
	case "help":
		content := helpModalContent()
		if y == 2+len(content)-1 && hitLabel(content[len(content)-1], closeButtonLabel, x) {
			m.modal = nil
		}
	case "choice", "actions":
		start, end := modalChoiceWindow(*m.modal, m.bodyHeight())
		if y >= 2 && y < 2+end-start {
			m.modal.choice = start + y - 2
			return m.activateModalChoice()
		}
		verb := "continue"
		if m.modal.kind == "actions" {
			verb = "run"
		}
		if y == 3+end-start && hitLabel(modalChoiceButtonLine(verb), cancelButtonLabel, x) {
			m.modal = nil
		}
	case "form":
		if y >= 2 && y < 2+len(m.modal.fields) {
			m.modal.field = y - 2
			return m, nil
		}
		buttons := saveButtonLabel + "   " + cancelButtonLabel
		if y == 3+len(m.modal.fields) {
			switch {
			case hitLabel(buttons, saveButtonLabel, x):
				return m.submitModalForm()
			case hitLabel(buttons, cancelButtonLabel, x):
				m.modal = nil
			}
		}
	case "confirm":
		buttons := cancelButtonLabel + "   " + deleteButtonLabel
		if y == 6 {
			switch {
			case hitLabel(buttons, cancelButtonLabel, x):
				m.modal.choice = 0
				return m.activateConfirmation()
			case hitLabel(buttons, deleteButtonLabel, x):
				m.modal.choice = 1
				return m.activateConfirmation()
			}
		}
	}
	return m, nil
}

func (m tuiModel) activateModalChoice() (tea.Model, tea.Cmd) {
	if m.modal == nil {
		return m, nil
	}
	if m.modal.kind == "actions" {
		return m.activateEditorAction()
	}
	m.acceptChoice()
	return m, nil
}

func (m tuiModel) activateConfirmation() (tea.Model, tea.Cmd) {
	if m.modal == nil || m.modal.kind != "confirm" || m.modal.choice == 0 {
		m.modal = nil
		return m, nil
	}
	if !m.beginAction("deleting owned resources…") {
		return m, nil
	}
	current := *m.modal
	m.modal = nil
	return m, m.track(deleteCmd(m.manager, current))
}

func (m tuiModel) submitModalForm() (tea.Model, tea.Cmd) {
	if m.modal == nil || m.modal.kind != "form" || !m.beginAction("working…") {
		return m, nil
	}
	current := *m.modal
	m.modal = nil
	return m, m.track(submitFormCmd(m.manager, current))
}

func (m tuiModel) isTerminalMousePosition(x, y int) bool {
	return m.attachment != nil && x > m.sidebarWidth() && x < m.width && y >= 1 && y <= m.bodyHeight()
}

func (m tuiModel) forwardTerminalMouse(event tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := event.Mouse()
	x, y := mouse.X-m.sidebarWidth()-1, mouse.Y-1
	if !m.isTerminalMousePosition(mouse.X, mouse.Y) || x < 0 || x >= m.terminalWidth() || y < 0 || y >= m.bodyHeight() {
		return m, nil
	}
	m.controlMode = false
	if err := m.attachment.SendMouse(event, x, y); err != nil {
		m.message = err.Error()
	}
	return m, nil
}

func headerButtonAt(x int) string {
	actionsStart := lipgloss.Width(headerTitleText) + 1
	if x >= actionsStart && x < actionsStart+lipgloss.Width(actionsButtonLabel) {
		return "actions"
	}
	helpStart := actionsStart + lipgloss.Width(actionsButtonLabel) + 1
	if x >= helpStart && x < helpStart+lipgloss.Width(helpButtonLabel) {
		return "help"
	}
	return ""
}

func hitLabel(line, label string, x int) bool {
	index := strings.Index(line, label)
	if index < 0 {
		return false
	}
	start := lipgloss.Width(line[:index])
	return x >= start && x < start+lipgloss.Width(label)
}

func verticalWheelDelta(button tea.MouseButton, step int) (int, bool) {
	switch button {
	case tea.MouseWheelUp:
		return -step, true
	case tea.MouseWheelDown:
		return step, true
	default:
		return 0, false
	}
}

func (m tuiModel) View() tea.View {
	if !m.hasUsableSize() {
		view := tea.NewView(fmt.Sprintf("multicodex editor needs at least %d×%d; current size is %d×%d", minimumWidth, minimumHeight, m.width, m.height))
		view.AltScreen = true
		return view
	}
	buttonStyle := lipgloss.NewStyle().Reverse(true)
	headerLeft := lipgloss.NewStyle().Bold(true).Render(headerTitleText) + " " + buttonStyle.Render(actionsButtonLabel) + " " + buttonStyle.Render(helpButtonLabel)
	header := joinKeepRight(headerLeft, m.usageText, m.width)
	sidebar := m.renderSidebar()
	main := m.renderMain()
	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, "│", main)
	footerLeft := "Click windows or Actions · Ctrl+G keyboard controls"
	if m.controlMode {
		footerLeft = "↑↓ select · Enter open · Tab actions · F1 help · Esc terminal"
	}
	footer := joinKeepRight(footerLeft, m.message, m.width)
	view := tea.NewView(header + "\n" + body + "\n" + footer)
	view.AltScreen = true
	view.ReportFocus = true
	view.MouseMode = tea.MouseModeCellMotion
	view.OnMouse = func(event tea.MouseMsg) tea.Cmd {
		return func() tea.Msg { return editorMouseMsg{event: event} }
	}
	if m.attachment != nil && !m.controlMode && m.modal == nil {
		x, y := m.attachment.CursorPosition()
		view.Cursor = tea.NewCursor(m.sidebarWidth()+1+x, 1+y)
	}
	return view
}

func (m tuiModel) renderSidebar() string {
	width, height := m.sidebarWidth(), m.bodyHeight()
	lines := []string{}
	for index, row := range m.rows {
		var label string
		switch row.kind {
		case "project":
			label = row.project.Name + " · " + row.host.Name
			if row.offline {
				label += " · offline"
			}
		case "workspace":
			label = "  ◇ " + row.workspace.Name
		case "window":
			marker := "●"
			if !row.window.Alive {
				marker = "○"
			}
			slot := "  "
			if row.slot > 0 && row.slot <= 9 {
				slot = fmt.Sprintf("%d ", row.slot)
			}
			label = "    " + slot + marker + " " + row.window.Name
		}
		label = fitPlain(label, width)
		style := lipgloss.NewStyle().Width(width)
		if index == m.selectedRow {
			style = style.Reverse(true)
		} else if row.kind == "project" {
			style = style.Bold(true)
		}
		lines = append(lines, style.Render(label))
	}
	if len(lines) == 0 {
		lines = append(lines, fitPlain("No workspaces", width), fitPlain("Ctrl+G · Tab actions", width))
	}
	start := min(m.sidebarOffset, len(lines))
	end := min(len(lines), start+height)
	lines = lines[start:end]
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderMain() string {
	width, height := m.terminalWidth(), m.bodyHeight()
	if m.modal != nil {
		return renderModal(*m.modal, width, height)
	}
	if m.attachment == nil {
		text := "No terminal selected.\n\nClick Actions above, or press Ctrl+G then Tab.\nChoose an action with the mouse, arrows, or Tab."
		return padBlock(text, width, height)
	}
	return m.attachment.Render(width, height)
}

func renderModal(modal modal, width, height int) string {
	lines := []string{lipgloss.NewStyle().Bold(true).Render(modal.title), ""}
	switch modal.kind {
	case "help":
		lines = append(lines, helpModalContent()...)
	case "choice", "actions":
		start, end := modalChoiceWindow(modal, height)
		for i := start; i < end; i++ {
			item := modal.choices[i]
			line := "  " + item.label
			if i == modal.choice {
				line = lipgloss.NewStyle().Reverse(true).Render(line)
			}
			lines = append(lines, line)
		}
		verb := "continue"
		if modal.kind == "actions" {
			verb = "run"
		}
		lines = append(lines, "", modalChoiceButtonLine(verb), "↑/↓ or Tab choose · Esc cancel")
	case "form":
		for i, field := range modal.fields {
			marker := "  "
			if i == modal.field {
				marker = "› "
			}
			lines = append(lines, marker+plainDisplayText(field.label)+": "+plainDisplayText(field.value))
		}
		lines = append(lines, "", saveButtonLabel+"   "+cancelButtonLabel, "Enter next/save · Tab change field · Esc cancel")
	case "confirm":
		cancel, remove := cancelButtonLabel, deleteButtonLabel
		if modal.choice == 0 {
			cancel = lipgloss.NewStyle().Reverse(true).Render(cancel)
		} else {
			remove = lipgloss.NewStyle().Reverse(true).Render(remove)
		}
		lines = append(lines, modal.reason, "", "This action cannot be undone.", "", cancel+"   "+remove, "←/→ or Tab choose · Enter continue · Esc cancel")
	}
	return padBlock(strings.Join(lines, "\n"), width, height)
}

func helpModalContent() []string {
	return []string{
		"Mouse         Click windows, buttons, fields, and menus",
		"Wheel         Scroll lists and terminal history",
		"Click terminal Return keyboard focus to the terminal",
		"Ctrl+G        Focus the editor sidebar",
		"↑/↓, Enter    Select and open a window",
		"Home/End      Move to the first/last sidebar item",
		"Tab / →       Open the editor action menu",
		"Alt/⌘+1–9    Select a window slot (⌘ when supported)",
		"F1            Show or close this help",
		"Esc           Return to terminal input",
		"Ctrl+C        Quit while editor controls are active",
		"", "Actions include create, attach, delete, and cleanup.",
		"", closeButtonLabel + " · Esc or Enter",
	}
}

func modalChoiceWindow(current modal, height int) (int, int) {
	visible := max(1, height-5)
	start := max(0, current.choice-visible/2)
	start = min(start, max(0, len(current.choices)-visible))
	return start, min(len(current.choices), start+visible)
}

func modalChoiceButtonLine(verb string) string {
	return cancelButtonLabel + " · Click or Enter to " + verb
}

func (m *tuiModel) rebuildRows() {
	state := m.manager.State()
	locations := sortedProjectsByActivity(state, m.statuses)
	selectedID := ""
	if m.selectedRow >= 0 && m.selectedRow < len(m.rows) {
		selectedID = rowIdentity(m.rows[m.selectedRow])
	}
	rows := []sidebarRow{}
	slot := 0
	for _, location := range locations {
		rows = append(rows, sidebarRow{kind: "project", host: location.Host, project: location.Project, offline: location.HostError != ""})
		for _, workspace := range location.Workspaces {
			rows = append(rows, sidebarRow{kind: "workspace", host: location.Host, project: location.Project, workspace: workspace})
			for _, window := range location.Windows {
				if window.WorkspaceID != workspace.ID {
					continue
				}
				slot++
				rows = append(rows, sidebarRow{kind: "window", host: location.Host, project: location.Project, workspace: workspace, window: window, slot: slot})
			}
		}
	}
	m.rows = rows
	m.selectedRow = -1
	if m.selectOnRefreshID != "" {
		for i, row := range rows {
			if row.window.ID == m.selectOnRefreshID {
				m.selectedRow = i
				m.selectOnRefreshID = ""
				break
			}
		}
	}
	if m.selectedRow < 0 {
		for i, row := range rows {
			if rowIdentity(row) == selectedID || selectedID == "" && state.SelectedWindowID != "" && row.window.ID == state.SelectedWindowID {
				m.selectedRow = i
				break
			}
		}
	}
	if m.selectedRow < 0 && len(rows) > 0 {
		m.selectedRow = 0
	}
	m.ensureSelectionVisible()
}

func (m *tuiModel) mergeStatuses(incoming []HostStatus) {
	previous := map[string]HostStatus{}
	for _, status := range m.statuses {
		previous[status.Host.ID] = status
	}
	for i := range incoming {
		if incoming[i].Busy {
			if old, ok := previous[incoming[i].Host.ID]; ok {
				incoming[i].Snapshot = old.Snapshot
				incoming[i].Error = old.Error
			}
			continue
		}
		if incoming[i].Error != "" && len(incoming[i].Snapshot.Workspaces) == 0 {
			if old, ok := previous[incoming[i].Host.ID]; ok {
				incoming[i].Snapshot = old.Snapshot
			}
		}
	}
	m.statuses = incoming
}

func (m tuiModel) preferredWindow() (sidebarRow, bool) {
	state := m.manager.State()
	for _, row := range m.rows {
		if row.kind == "window" && row.window.ID == state.SelectedWindowID {
			return row, true
		}
	}
	for _, row := range m.rows {
		if row.kind == "window" {
			return row, true
		}
	}
	return sidebarRow{}, false
}

func (m tuiModel) selectWindowSlot(slot int) (tea.Model, tea.Cmd) {
	for i, row := range m.rows {
		if row.kind == "window" && row.slot == slot {
			m.selectedRow = i
			m.ensureSelectionVisible()
			return m.selectCurrentRow()
		}
	}
	m.message = fmt.Sprintf("window slot %d is not visible", slot)
	return m, nil
}

func (m tuiModel) selectCurrentRow() (tea.Model, tea.Cmd) {
	if m.selectedRow < 0 || m.selectedRow >= len(m.rows) || m.rows[m.selectedRow].kind != "window" {
		return m, nil
	}
	row := m.rows[m.selectedRow]
	if row.window.ID == m.attachedID {
		m.controlMode = false
		if m.attachingID != "" {
			m.queuedAttach = &row
			m.message = "current window queued after the active connection attempt"
		}
		return m, nil
	}
	if row.window.ID == m.attachingID {
		m.queuedAttach = nil
		m.controlMode = false
		return m, nil
	}
	m.controlMode = false
	return m, m.requestAttach(row)
}

func (m *tuiModel) requestAttach(row sidebarRow) tea.Cmd {
	if m.attachingID != "" {
		queued := row
		m.queuedAttach = &queued
		m.message = "window switch queued after the active connection attempt"
		return nil
	}
	m.attachingID = row.window.ID
	m.message = "connecting to " + row.host.Name + "…"
	return m.track(attachCmd(m.manager, row.host, row.window, m.terminalWidth(), m.bodyHeight()))
}

func (m *tuiModel) moveSelection(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.selectedRow = (m.selectedRow + delta + len(m.rows)) % len(m.rows)
	m.ensureSelectionVisible()
}

func (m *tuiModel) moveSelectionClamped(delta int) {
	if len(m.rows) == 0 {
		return
	}
	if m.selectedRow < 0 {
		m.selectedRow = 0
	} else {
		m.selectedRow = max(0, min(len(m.rows)-1, m.selectedRow+delta))
	}
	m.ensureSelectionVisible()
}

func (m *tuiModel) moveSelectionPage(direction int) {
	if len(m.rows) == 0 {
		return
	}
	if m.selectedRow < 0 {
		m.selectedRow = 0
	} else {
		m.selectedRow = max(0, min(len(m.rows)-1, m.selectedRow+direction*max(1, m.bodyHeight()-1)))
	}
	m.ensureSelectionVisible()
}

func (m *tuiModel) selectSidebarEdge(last bool) {
	if len(m.rows) == 0 {
		return
	}
	m.selectedRow = 0
	if last {
		m.selectedRow = len(m.rows) - 1
	}
	m.ensureSelectionVisible()
}

func (m *tuiModel) ensureSelectionVisible() {
	height := m.bodyHeight()
	if len(m.rows) <= height {
		m.sidebarOffset = 0
		return
	}
	if m.selectedRow < m.sidebarOffset {
		m.sidebarOffset = max(0, m.selectedRow)
	}
	if m.selectedRow >= m.sidebarOffset+height {
		m.sidebarOffset = m.selectedRow - height + 1
	}
	m.sidebarOffset = min(m.sidebarOffset, max(0, len(m.rows)-height))
}

func (m *tuiModel) openActionMenu() {
	m.modal = &modal{kind: "actions", title: "Editor actions", choices: []choice{
		{label: "New window", action: "new_window"},
		{label: "New workspace", action: "new_workspace"},
		{label: "Add project", action: "add_project"},
		{label: "Add SSH host", action: "add_host"},
		{label: "Attach a client file", action: "attach_file"},
		{label: "Attach a clipboard image", action: "attach_clipboard"},
		{label: "Open 50,000-line scrollback", action: "scrollback"},
		{label: "Delete selected window or workspace", action: "delete"},
		{label: "Run safe cleanup", action: "cleanup"},
		{label: "Send Ctrl+G to the terminal", action: "send_control_g"},
		{label: "Help and controls", action: "help"},
		{label: "Quit multicodex editor", action: "quit"},
	}}
}

func (m tuiModel) activateEditorAction() (tea.Model, tea.Cmd) {
	if m.modal == nil || len(m.modal.choices) == 0 || m.modal.choice < 0 || m.modal.choice >= len(m.modal.choices) {
		return m, nil
	}
	action := m.modal.choices[m.modal.choice].action
	m.modal = nil
	switch action {
	case "new_window":
		m.openWorkspaceChoice()
	case "new_workspace":
		m.openProjectChoice()
	case "add_project":
		m.openHostChoice("add_project", "Choose the project host")
	case "add_host":
		m.modal = &modal{kind: "form", action: "add_host", title: "Add SSH host", fields: []formField{{label: "Name", limit: 80}, {label: "SSH alias", limit: 128}}}
	case "attach_file":
		if row, ok := m.currentAttachedRow(); ok {
			if _, pending := m.pendingPastes[row.window.ID]; pending {
				m.message = "this window already has an attachment waiting to paste"
				return m, nil
			}
			m.modal = &modal{kind: "form", action: "put_file", title: "Attach a client file", host: row.host, workspace: row.workspace, window: row.window,
				fields: []formField{{label: "Absolute client file path", limit: 4096}}}
		} else {
			m.message = "select a window before attaching a file"
		}
	case "attach_clipboard":
		return m.startClipboardAttachment()
	case "scrollback":
		if m.attachedID == "" {
			m.message = "select a connected window before opening scrollback"
			return m, nil
		}
		if !m.beginAction("opening tmux scrollback…") {
			return m, nil
		}
		m.controlMode = false
		return m, m.track(copyModeCmd(m.manager, m.attachedHost, m.attachedID))
	case "delete":
		m.openDeleteConfirmation()
	case "cleanup":
		if !m.beginAction("running safe cleanup…") {
			return m, nil
		}
		return m, m.track(cleanupCmd(m.manager, "cleanup"))
	case "send_control_g":
		if m.attachment == nil {
			m.message = "no terminal is connected"
			return m, nil
		}
		if err := m.attachment.SendKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}); err != nil {
			m.message = err.Error()
			return m, nil
		}
		m.controlMode = false
		m.message = "sent Ctrl+G to terminal"
	case "help":
		m.modal = &modal{kind: "help", title: "Controls"}
	case "quit":
		return m, tea.Quit
	}
	return m, nil
}

func (m *tuiModel) openHostChoice(action, title string) {
	state := m.manager.State()
	choices := make([]choice, 0, len(state.Hosts))
	for _, host := range state.Hosts {
		choices = append(choices, choice{label: host.Name, host: host})
	}
	m.modal = &modal{kind: "choice", action: action, title: title, choices: choices}
}

func (m *tuiModel) openProjectChoice() {
	state := m.manager.State()
	choices := []choice{}
	for _, host := range state.Hosts {
		for _, project := range host.Projects {
			choices = append(choices, choice{label: project.Name + " · " + host.Name, host: host, project: project})
		}
	}
	if len(choices) == 0 {
		m.message = "add a project first from the Actions menu"
		return
	}
	m.modal = &modal{kind: "choice", action: "create_workspace", title: "Choose the project", choices: choices}
}

func (m *tuiModel) openWorkspaceChoice() {
	choices := []choice{}
	for _, status := range m.statuses {
		projects := map[string]Project{}
		for _, project := range status.Host.Projects {
			projects[project.ID] = project
		}
		for _, workspace := range status.Snapshot.Workspaces {
			project := projects[workspace.ProjectID]
			choices = append(choices, choice{label: workspace.Name + " · " + project.Name + " · " + status.Host.Name, host: status.Host, project: project, workspace: workspace})
		}
	}
	if len(choices) == 0 {
		m.message = "create a workspace first from the Actions menu"
		return
	}
	m.modal = &modal{kind: "choice", action: "create_window", title: "Choose the workspace", choices: choices}
}

func (m *tuiModel) acceptChoice() {
	if len(m.modal.choices) == 0 {
		m.modal = nil
		return
	}
	selected := m.modal.choices[m.modal.choice]
	action := m.modal.action
	switch action {
	case "add_project":
		m.modal = &modal{kind: "form", action: action, title: "Add project on " + selected.host.Name, host: selected.host,
			fields: []formField{{label: "Name", limit: 80}, {label: "Absolute directory path", limit: 4096}}}
	case "create_workspace":
		m.modal = &modal{kind: "form", action: action, title: "New workspace in " + selected.project.Name, host: selected.host, project: selected.project,
			fields: []formField{{label: "Name", limit: 80}}}
	case "create_window":
		m.modal = &modal{kind: "form", action: action, title: "New window in " + selected.workspace.Name, host: selected.host, project: selected.project, workspace: selected.workspace,
			fields: []formField{{label: "Name", value: defaultShellWin, limit: 80}, {label: "Type: shell or codex", value: "shell", limit: 5}}}
	}
}

func (m *tuiModel) openDeleteConfirmation() {
	if m.selectedRow < 0 || m.selectedRow >= len(m.rows) {
		return
	}
	row := m.rows[m.selectedRow]
	current := &modal{kind: "confirm", host: row.host, project: row.project, workspace: row.workspace, delete: DeleteRequest{Force: false}}
	switch row.kind {
	case "window":
		current.action, current.title, current.reason = "delete_window", "Delete window", "Delete window “"+row.window.Name+"” and its tmux session?"
		current.delete.ID = row.window.ID
	case "workspace":
		current.action, current.title = "delete_workspace", "Delete workspace"
		if row.workspace.Git {
			current.reason = "Delete workspace “" + row.workspace.Name + "” and its owned Git worktree and branch?"
		} else {
			current.reason = "Remove workspace “" + row.workspace.Name + "” from the editor? Its project directory will remain."
		}
		current.delete.ID = row.workspace.ID
	default:
		m.message = "select a window or workspace to delete"
		return
	}
	m.modal = current
}

func (m tuiModel) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	if msg.action == "background_cleanup" {
		m.cleanupBusy = false
	} else {
		m.actionBusy = false
	}
	if msg.err != nil {
		if value, ok := msg.value.(DeleteResult); ok && value.Deleted {
			if msg.action == "delete_window" && msg.targetID == m.attachedID && m.attachment != nil {
				_ = m.attachment.Close()
				m.attachment, m.attachedID, m.attachedHost = nil, "", ""
			}
			m.message = "deleted, but client reconnect state was not saved: " + msg.err.Error()
			return m, m.startRefresh()
		}
		m.message = msg.err.Error()
		return m, m.startRefresh()
	}
	if msg.action == "copy_mode" {
		m.message = "tmux scrollback: arrows/Page Up, q to leave"
	}
	switch value := msg.value.(type) {
	case Host:
		m.message = "added host " + value.Name
	case Project:
		m.message = "added project " + value.Name
	case Workspace:
		m.message = "created workspace " + value.Name
	case Window:
		m.message = "created window " + value.Name
		m.selectOnRefreshID = value.ID
		if host, ok := m.manager.findHost(msg.hostID); ok {
			cmd := m.requestAttach(sidebarRow{kind: "window", host: host, window: value})
			return m, tea.Batch(m.startRefresh(), cmd)
		}
	case DeleteResult:
		if value.Deleted {
			m.message = "deleted"
			if msg.action == "delete_window" && msg.targetID == m.attachedID && m.attachment != nil {
				_ = m.attachment.Close()
				m.attachment, m.attachedID, m.attachedHost = nil, "", ""
			}
		} else if value.Reason != "" && value.Forceable {
			m.modal = &modal{kind: "confirm", action: msg.action, title: "Confirm permanent deletion", host: hostForID(m.manager.State(), msg.hostID), delete: DeleteRequest{ID: msg.targetID, Force: true}, reason: value.Reason}
			return m, nil
		} else if value.Reason != "" {
			m.message = value.Reason
		}
	case AttachmentFile:
		prefix := "Please inspect this attachment: "
		if msg.action == "put_clipboard" {
			prefix = "Please inspect this image: "
		}
		if m.pendingPastes == nil {
			m.pendingPastes = make(map[string]string)
		}
		m.pendingPastes[msg.targetID] = prefix + value.Path + " "
		if !m.flushPendingPaste(msg.targetID) {
			m.message = "attachment uploaded; it will paste when its window opens"
		}
	case map[string]CleanupResult:
		m.message = cleanupSummary(value)
	}
	return m, m.startRefresh()
}

func cleanupSummary(results map[string]CleanupResult) string {
	windows, workspaces, attachments, skipped := 0, 0, 0, 0
	var notes []string
	for _, result := range results {
		windows += result.WindowsDeleted
		workspaces += result.WorkspacesDeleted
		attachments += result.AttachmentsDeleted
		skipped += len(result.Skipped)
		notes = append(notes, result.Skipped...)
	}
	message := fmt.Sprintf("cleanup: %d windows, %d workspaces, %d attachments removed", windows, workspaces, attachments)
	if skipped != 0 {
		message += fmt.Sprintf(" · %d skipped", skipped)
		sort.Strings(notes)
		message += ": " + safeClientText(notes[0], 120)
	}
	return message
}

func hostForID(state ClientState, id string) Host {
	for _, host := range state.Hosts {
		if host.ID == id {
			return host
		}
	}
	return Host{}
}

func (m tuiModel) rowIndexForWindow(id string) int {
	for i, row := range m.rows {
		if row.window.ID == id {
			return i
		}
	}
	return m.selectedRow
}

func (m tuiModel) currentAttachedRow() (sidebarRow, bool) {
	for _, row := range m.rows {
		if row.kind == "window" && row.window.ID == m.attachedID {
			return row, true
		}
	}
	return sidebarRow{}, false
}

func (m tuiModel) startClipboardAttachment() (tea.Model, tea.Cmd) {
	row, ok := m.currentAttachedRow()
	if !ok {
		m.message = "select a window before pasting an image"
		return m, nil
	}
	if _, pending := m.pendingPastes[row.window.ID]; pending {
		m.message = "this window already has an attachment waiting to paste"
		return m, nil
	}
	if !m.beginAction("reading the client clipboard…") {
		return m, nil
	}
	return m, m.track(clipboardAttachmentCmd(m.manager, row.host.ID, row.workspace.ID, row.window.ID))
}

func (m *tuiModel) flushPendingPaste(windowID string) bool {
	text, ok := m.pendingPastes[windowID]
	if !ok || windowID != m.attachedID || m.attachment == nil {
		return false
	}
	if err := m.attachment.Paste(text); err != nil {
		return false
	}
	delete(m.pendingPastes, windowID)
	m.controlMode = false
	m.message = "attachment pasted into the draft; press Enter when ready"
	return true
}

func (m *tuiModel) beginAction(message string) bool {
	if m.actionBusy {
		m.message = "another editor action is still running"
		return false
	}
	m.actionBusy = true
	m.message = message
	return true
}

func (m tuiModel) hasUsableSize() bool { return m.width >= minimumWidth && m.height >= minimumHeight }
func (m tuiModel) sidebarWidth() int   { return min(34, max(24, m.width/4)) }
func (m tuiModel) terminalWidth() int  { return max(1, m.width-m.sidebarWidth()-1) }
func (m tuiModel) bodyHeight() int     { return max(1, m.height-2) }

func rowIdentity(row sidebarRow) string {
	switch row.kind {
	case "project":
		return "p/" + row.project.ID
	case "workspace":
		return "s/" + row.workspace.ID
	default:
		return "w/" + row.window.ID
	}
}

func windowSlotKey(key tea.KeyPressMsg) int {
	if key.Code < '1' || key.Code > '9' {
		return 0
	}
	if key.Mod&(tea.ModAlt|tea.ModSuper|tea.ModMeta) != 0 {
		return int(key.Code - '0')
	}
	return 0
}

func appendBounded(current, added string, limit int) string {
	if limit <= 0 {
		limit = 4096
	}
	currentRunes := []rune(current)
	if len(currentRunes) >= limit {
		return current
	}
	addedRunes := []rune(added)
	remaining := limit - len(currentRunes)
	if len(addedRunes) > remaining {
		addedRunes = addedRunes[:remaining]
	}
	return current + string(addedRunes)
}

func refreshCmd(manager *Manager) tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{statuses: manager.Refresh(manager.Context())}
	}
}

func (m *tuiModel) startRefresh() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	return m.track(refreshCmd(m.manager))
}

func attachmentDoneCmd(windowID string, attachment *Attachment) tea.Cmd {
	return func() tea.Msg {
		<-attachment.Done()
		return attachmentDoneMsg{windowID: windowID, attachment: attachment}
	}
}

func attachmentUpdateCmd(windowID string, attachment *Attachment) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-attachment.Updates()
		return attachmentUpdateMsg{windowID: windowID, attachment: attachment, closed: !ok}
	}
}

func usageCmd(fetch func(context.Context) (*usage.Summary, error), parent context.Context) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 45*time.Second)
		defer cancel()
		summary, err := fetch(ctx)
		if err != nil || summary == nil {
			return usageMsg{text: "usage unavailable"}
		}
		return usageMsg{text: compactUsage(summary)}
	}
}

func compactUsage(summary *usage.Summary) string {
	type item struct {
		label string
		used  int
	}
	items := []item{}
	for _, account := range summary.Accounts {
		if account.WeeklyWindow.UsedPercent >= 0 {
			label := strings.TrimSpace(plainDisplayText(account.Label))
			if label == "" {
				label = "account"
			}
			items = append(items, item{label: label, used: account.WeeklyWindow.UsedPercent})
		}
	}
	if len(items) == 0 && summary.WeeklyWindow.UsedPercent >= 0 {
		label := strings.TrimSpace(plainDisplayText(summary.WindowAccountLabel))
		if label == "" {
			label = "account"
		}
		items = append(items, item{label: label, used: summary.WeeklyWindow.UsedPercent})
	}
	if len(items) == 0 {
		return "usage unavailable"
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].label < items[j].label })
	parts := []string{}
	for i, item := range items {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("+%d", len(items)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d%%", item.label, item.used))
	}
	return "usage " + strings.Join(parts, " · ")
}

func (m tuiModel) track(command tea.Cmd) tea.Cmd {
	if m.workers == nil {
		return command
	}
	return m.workers.track(command)
}

func (m tuiModel) workerContext() context.Context {
	if m.workers != nil {
		return m.workers.ctx
	}
	if m.manager != nil {
		return m.manager.Context()
	}
	return context.Background()
}

func (m tuiModel) after(duration time.Duration, message func(time.Time) tea.Msg) tea.Cmd {
	ctx := m.workerContext()
	return m.track(func() tea.Msg {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case now := <-timer.C:
			return message(now)
		case <-ctx.Done():
			return nil
		}
	})
}

func (m tuiModel) refreshTick() tea.Cmd {
	return m.after(2*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

func (m tuiModel) cleanupTick() tea.Cmd {
	return m.after(time.Hour, func(t time.Time) tea.Msg { return cleanupTickMsg(t) })
}

func (m tuiModel) usageTick() tea.Cmd {
	return m.after(time.Minute, func(t time.Time) tea.Msg { return usageTickMsg(t) })
}

func cleanupCmd(manager *Manager, action string) tea.Cmd {
	return func() tea.Msg {
		return actionResultMsg{action: action, value: manager.CleanupAll(manager.Context())}
	}
}

func attachCmd(manager *Manager, host Host, window Window, width, height int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 10*time.Second)
		defer cancel()
		attachment, err := manager.AttachWindow(ctx, host.ID, window, max(1, width), max(1, height))
		return attachResultMsg{host: host, window: window, attachment: attachment, err: err}
	}
}

func copyModeCmd(manager *Manager, hostID, windowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 10*time.Second)
		defer cancel()
		return actionResultMsg{action: "copy_mode", err: manager.CopyMode(ctx, hostID, windowID)}
	}
}

func submitFormCmd(manager *Manager, form modal) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 2*time.Minute+15*time.Second)
		defer cancel()
		result := actionResultMsg{action: form.action, hostID: form.host.ID}
		switch form.action {
		case "add_host":
			result.value, result.err = manager.AddHost(ctx, form.fields[0].value, form.fields[1].value)
		case "add_project":
			result.value, result.err = manager.AddProject(ctx, form.host.ID, form.fields[0].value, form.fields[1].value)
		case "create_workspace":
			result.value, result.err = manager.CreateWorkspace(ctx, form.host.ID, CreateWorkspaceRequest{ProjectID: form.project.ID, ProjectPath: form.project.Path, Name: form.fields[0].value})
		case "create_window":
			result.value, result.err = manager.CreateWindow(ctx, form.host.ID, CreateWindowRequest{WorkspaceID: form.workspace.ID, Name: form.fields[0].value, Launch: strings.ToLower(form.fields[1].value)})
		case "put_file":
			data, extension, err := ReadAttachment(form.fields[0].value)
			if err != nil {
				result.err = err
				break
			}
			result.targetID = form.window.ID
			result.value, result.err = manager.PutAttachment(ctx, form.host.ID, PutAttachmentRequest{WorkspaceID: form.workspace.ID, Extension: extension, Data: data})
		}
		return result
	}
}

func clipboardAttachmentCmd(manager *Manager, hostID, workspaceID, windowID string) tea.Cmd {
	return func() tea.Msg {
		captureContext, cancelCapture := context.WithTimeout(manager.Context(), 30*time.Second)
		data, extension, err := CaptureClipboardImage(captureContext)
		cancelCapture()
		result := actionResultMsg{action: "put_clipboard", hostID: hostID, targetID: windowID, err: err}
		if err == nil {
			uploadContext, cancelUpload := context.WithTimeout(manager.Context(), 35*time.Second)
			result.value, result.err = manager.PutAttachment(uploadContext, hostID, PutAttachmentRequest{WorkspaceID: workspaceID, Extension: extension, Data: data, Image: true})
			cancelUpload()
		}
		return result
	}
}

func deleteCmd(manager *Manager, confirmation modal) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 2*time.Minute)
		defer cancel()
		result := actionResultMsg{action: confirmation.action, hostID: confirmation.host.ID, targetID: confirmation.delete.ID}
		if confirmation.action == "delete_window" {
			result.value, result.err = manager.DeleteWindow(ctx, confirmation.host.ID, confirmation.delete)
		} else {
			result.value, result.err = manager.DeleteWorkspace(ctx, confirmation.host.ID, confirmation.delete)
		}
		return result
	}
}

func joinKeepRight(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	right = ansi.Truncate(right, width, "")
	rightWidth := lipgloss.Width(right)
	leftLimit := width - rightWidth
	if left != "" && right != "" && leftLimit > 0 {
		leftLimit--
	}
	left = ansi.Truncate(left, max(0, leftLimit), "")
	padding := max(0, width-lipgloss.Width(left)-rightWidth)
	return left + strings.Repeat(" ", padding) + right
}

func fitPlain(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = ansi.Truncate(value, width, "")
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func plainDisplayText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\x1b' {
			return -1
		}
		return r
	}, value)
}

func padBlock(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	for i := range lines {
		lines[i] = fitPlain(lines[i], width)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines[:height], "\n")
}
