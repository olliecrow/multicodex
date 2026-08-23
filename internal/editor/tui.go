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
	accounts []accountUsage
	err      string
}

type refreshTickMsg time.Time
type cleanupTickMsg time.Time
type usageTickMsg time.Time

const (
	headerTitleText     = " multicodex editor "
	actionsButtonLabel  = "[ Actions ]"
	helpButtonLabel     = "[ Help ]"
	cancelButtonLabel   = "[ Cancel ]"
	deleteButtonLabel   = "[ Delete ]"
	closeButtonLabel    = "[ Close ]"
	confirmationWarning = "This cannot be undone. Cancel is selected by default."
	modalInsetX         = 1
	modalInsetY         = 1
	contextInsetX       = 1
	contextInsetY       = 1
	contextActionRow    = 4
	recentOutputWindow  = 5 * time.Second
)

var (
	infoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	accentStyle  = infoStyle.Bold(true)
	borderStyle  = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack)
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
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

type exitedWindowRemovalMsg struct {
	hostID   string
	windowID string
	result   DeleteResult
	err      error
}

type windowRef struct {
	hostID   string
	windowID string
}

type actionResultMsg struct {
	action    string
	hostID    string
	targetID  string
	windowIDs []string
	value     any
	form      *modal
	err       error
}

type createdWorkspace struct {
	workspace Workspace
	window    Window
}

type renamedResource struct {
	kind string
	name string
}

type sidebarRow struct {
	kind      string
	host      Host
	project   Project
	workspace Workspace
	window    Window
	slot      int
	offline   bool
	hostError string
	signal    sidebarSignal
}

type sidebarSignal uint8

const (
	sidebarEmpty sidebarSignal = iota
	sidebarQuiet
	sidebarActive
	sidebarStopped
	sidebarOffline
	sidebarUnavailable
)

type formField struct {
	label string
	value string
	limit int
}

type accountUsage struct {
	label        string
	usedPercent  int
	resetSeconds int64
	resetKnown   bool
	available    bool
	loading      bool
	stale        bool
}

type accountUsageState struct {
	accounts []accountUsage
	err      string
}

type screenLayout struct {
	width, height            int
	bodyContent              int
	sidebarWidth             int
	terminalX, terminalWidth int
	bodyHeight               int
	sidebarHeight            int
	usageHeight              int
	requiredHeight           int
}

type choice struct {
	label     string
	action    string
	host      Host
	project   Project
	workspace Workspace
	window    Window
	session   TmuxSessionCandidate
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
	session   TmuxSessionCandidate
	windowIDs []string
	delete    DeleteRequest
	reason    string
	confirm   string
	warning   string
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
	refreshPulse      bool
	actionBusy        bool
	cleanupBusy       bool
	exitRemovalBusy   bool
	usageBusy         bool
	modal             *modal
	attachment        *Attachment
	attachedHost      string
	attachedID        string
	attachingID       string
	keepSidebarAttach string
	queuedAttach      *sidebarRow
	selectOnRefreshID string
	resizePending     bool
	pendingPastes     map[string]string
	exitRemovalTried  map[windowRef]struct{}
	message           string
	usage             accountUsageState
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

	model := tuiModel{
		manager: manager, usageFetcher: fetcher, workers: workers,
		usage:       accountUsageState{accounts: loadingAccountUsage(fetcher.AccountLabels())},
		selectedRow: -1, refreshing: true, cleanupBusy: true, usageBusy: true,
	}
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
		if m.attachment != nil {
			m.resizePending = !m.layout().fits()
			if !m.resizePending {
				_ = m.attachment.Resize(m.terminalWidth(), m.bodyHeight())
			}
		}
	case refreshMsg:
		m.refreshing = false
		m.refreshPulse = !m.refreshPulse
		m.mergeStatuses(msg.statuses)
		m.rebuildRows()
		m.flushPendingPaste(m.attachedID)
		exitRemovalCmd := m.startExitedWindowRemoval(msg.statuses)
		if m.layout().fits() && m.attachedID == "" && m.attachingID == "" {
			if row, ok := m.preferredWindow(); ok {
				m.selectedRow = m.rowIndexForWindow(row.window.ID)
				return m, tea.Batch(exitRemovalCmd, m.requestAttach(row))
			}
			if row, ok := m.selectedSidebarRow(); ok && row.window.ID != "" && !row.offline && !row.window.Alive {
				if row.window.Exited {
					m.message = "terminal exited · removing…"
				} else {
					m.message = "terminal stopped · use Actions → Delete selected to replace it"
				}
			}
		}
		return m, exitRemovalCmd
	case usageMsg:
		m.usageBusy = false
		m.applyUsage(msg)
		m.ensureSelectionVisible()
		if m.attachment != nil && m.resizePending && m.layout().fits() {
			_ = m.attachment.Resize(m.terminalWidth(), m.bodyHeight())
			m.resizePending = false
		}
	case refreshTickMsg:
		return m, tea.Batch(m.startRefresh(), m.refreshTick())
	case cleanupTickMsg:
		if m.cleanupBusy {
			return m, m.cleanupTick()
		}
		m.cleanupBusy = true
		return m, tea.Batch(m.track(cleanupCmd(m.manager, "background_cleanup")), m.cleanupTick())
	case usageTickMsg:
		if m.usageBusy {
			return m, m.usageTick()
		}
		m.usageBusy = true
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
				if queued.window.ID == m.keepSidebarAttach {
					m.keepSidebarAttach = ""
				}
				m.message = "kept current window"
				return m, nil
			}
			return m, m.requestAttach(queued)
		}
		if msg.err != nil {
			if msg.window.ID == m.keepSidebarAttach {
				m.keepSidebarAttach = ""
			}
			m.message = msg.err.Error()
			return m, nil
		}
		if m.attachment != nil {
			_ = m.attachment.Close()
		}
		m.attachment = msg.attachment
		m.attachedHost, m.attachedID = msg.host.ID, msg.window.ID
		if m.layout().fits() {
			_ = m.attachment.Resize(m.terminalWidth(), m.bodyHeight())
			m.resizePending = false
		} else {
			m.resizePending = true
		}
		m.controlMode = msg.window.ID == m.keepSidebarAttach
		if msg.window.ID == m.keepSidebarAttach {
			m.keepSidebarAttach = ""
		}
		m.message = ""
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
		ref := windowRef{hostID: m.attachedHost, windowID: msg.windowID}
		_ = m.attachment.Close()
		m.attachment = nil
		m.resizePending = false
		m.attachedHost = ""
		m.attachedID = ""
		if _, removing := m.exitRemovalTried[ref]; removing {
			m.message = "terminal exited · removing…"
		} else {
			m.message = "terminal disconnected; reconnecting…"
		}
		return m, m.startRefresh()
	case exitedWindowRemovalMsg:
		m.exitRemovalBusy = false
		if msg.result.Deleted || msg.result.Reason == "window no longer exists" {
			if msg.windowID == m.attachedID && m.attachment != nil {
				_ = m.attachment.Close()
				m.attachment, m.attachedID, m.attachedHost = nil, "", ""
			}
			m.message = "terminal exited and was removed"
			if msg.err != nil {
				m.message = "terminal was removed, but reconnect selection was not saved"
			}
			return m, m.startRefresh()
		}
		if msg.err != nil {
			m.message = "terminal exited, but automatic removal did not complete"
			return m, m.startRefresh()
		}
		if msg.result.Forceable {
			delete(m.exitRemovalTried, windowRef{hostID: msg.hostID, windowID: msg.windowID})
		}
		return m, m.startRefresh()
	case actionResultMsg:
		return m.handleActionResult(msg)
	case tea.PasteMsg:
		if !m.layout().fits() {
			return m, nil
		}
		if m.modal != nil {
			if m.modal.kind == "form" {
				field := &m.modal.fields[m.modal.field]
				plain := plainDisplayText(msg.Content)
				field.value = appendBounded(field.value, plain, field.limit)
				if plain != msg.Content {
					m.message = "paste omitted terminal control characters"
				}
			} else {
				m.message = "close the dialog before pasting"
			}
			return m, nil
		}
		if m.attachment == nil {
			m.message = "open a terminal before pasting"
			return m, nil
		}
		wasControlMode := m.controlMode
		m.controlMode = false
		if err := m.attachment.Paste(msg.Content); err != nil {
			m.message = err.Error()
		} else if wasControlMode {
			m.message = "pasted into terminal; terminal focused"
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
	case tea.MouseMsg:
		// Direct dispatch preserves mouse and keyboard input order. View.OnMouse commands run asynchronously.
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m tuiModel) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.layout().fits() {
		if key.Keystroke() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.modal != nil {
		return m.handleModalKey(key)
	}
	if isGlobalHelpKey(key) {
		m.modal = &modal{kind: "help", title: "Controls"}
		return m, nil
	}
	if slot := windowSlotKey(key); slot > 0 {
		return m.selectWindowSlot(slot)
	}
	if !m.controlMode {
		if isSidebarFocusKey(key) {
			m.controlMode = true
			m.message = ""
			return m, nil
		}
		if m.attachment != nil {
			if err := m.attachment.SendKey(key); err != nil {
				m.message = err.Error()
			}
		}
		return m, nil
	}
	if isSidebarFocusKey(key) {
		m.controlMode = false
		m.message = ""
		return m, nil
	}
	if isMacListEdgeKey(key, tea.KeyUp) {
		m.selectSidebarEdge(false)
		return m.openSelectedWindowFromSidebar()
	}
	if isMacListEdgeKey(key, tea.KeyDown) {
		m.selectSidebarEdge(true)
		return m.openSelectedWindowFromSidebar()
	}
	if isMacOptionArrowKey(key, tea.KeyUp) {
		return m.selectAdjacentWindow(-1)
	}
	if isMacOptionArrowKey(key, tea.KeyDown) {
		return m.selectAdjacentWindow(1)
	}
	if isCreateKey(key) {
		return m.createForSelection()
	}
	if isRenameKey(key) {
		m.openRename()
		return m, nil
	}
	if isDeleteKey(key) {
		m.openDeleteConfirmation()
		return m, nil
	}
	if isSidebarHelpKey(key) {
		m.modal = &modal{kind: "help", title: "Controls"}
		return m, nil
	}

	switch key.Keystroke() {
	case "esc":
		m.controlMode = false
		m.message = ""
	case "ctrl+c":
		return m, tea.Quit
	case "up":
		m.moveSelection(-1)
		return m.openSelectedWindowFromSidebar()
	case "down":
		m.moveSelection(1)
		return m.openSelectedWindowFromSidebar()
	case "home":
		m.selectSidebarEdge(false)
		return m.openSelectedWindowFromSidebar()
	case "end":
		m.selectSidebarEdge(true)
		return m.openSelectedWindowFromSidebar()
	case "pgup":
		m.moveSelectionPage(-1)
		return m.openSelectedWindowFromSidebar()
	case "pgdown":
		m.moveSelectionPage(1)
		return m.openSelectedWindowFromSidebar()
	case "enter":
		return m.selectCurrentRow()
	case "tab", "shift+tab", "right":
		m.openActionMenu()
	}
	return m, nil
}

func (m tuiModel) handleModalKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCancelKey(key) {
		m.modal = nil
		return m, nil
	}
	switch m.modal.kind {
	case "help":
		if key.Keystroke() == "enter" || isGlobalHelpKey(key) || isSidebarHelpKey(key) {
			m.modal = nil
		}
	case "choice", "actions":
		if isMacListEdgeKey(key, tea.KeyUp) {
			m.modal.choice = 0
			return m, nil
		}
		if isMacListEdgeKey(key, tea.KeyDown) {
			m.modal.choice = max(0, len(m.modal.choices)-1)
			return m, nil
		}
		if isMacOptionArrowKey(key, tea.KeyUp) {
			m.moveModalChoicePage(-1)
			return m, nil
		}
		if isMacOptionArrowKey(key, tea.KeyDown) {
			m.moveModalChoicePage(1)
			return m, nil
		}
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
	layout := m.layout()
	if !layout.fits() || mouse.X < 0 || mouse.X >= m.width || mouse.Y < 0 || mouse.Y >= m.height {
		return m, nil
	}
	if m.modal != nil {
		return m.handleModalMouse(event)
	}
	if _, ok := event.(tea.MouseWheelMsg); ok {
		if mouse.Y >= layout.bodyContent && mouse.Y < layout.bodyContent+layout.bodyHeight {
			if mouse.X > 0 && mouse.X <= layout.sidebarWidth && mouse.Y < layout.bodyContent+layout.sidebarHeight {
				delta, vertical := verticalWheelDelta(mouse.Button, 3)
				if !vertical {
					return m, nil
				}
				m.moveSelectionClamped(delta)
				return m, nil
			}
			if mouse.X >= layout.terminalX && mouse.X < layout.terminalX+layout.terminalWidth {
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
		if click.Y >= layout.bodyContent && click.Y < layout.bodyContent+layout.sidebarHeight && click.X > 0 && click.X <= layout.sidebarWidth {
			index := m.sidebarOffset + click.Y - layout.bodyContent
			if index < 0 || index >= len(m.rows) {
				return m, nil
			}
			m.selectedRow = index
			m.controlMode = true
			if m.rows[index].kind == "window" || m.rows[index].kind == "project" {
				return m.selectCurrentRow()
			}
			return m, nil
		}
		if _, choices, ok := m.selectedContextActions(); ok {
			x := click.X - layout.terminalX - contextInsetX
			y := click.Y - layout.bodyContent - contextInsetY
			index := y - contextActionRow
			if index >= 0 && index < len(choices) {
				button := contextActionButton(choices[index])
				if hitLabel(button, button, x) {
					return m.activateEditorChoice(choices[index])
				}
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
	layout := m.layout()
	if mouse.X < layout.terminalX || mouse.X >= layout.terminalX+layout.terminalWidth || mouse.Y < layout.bodyContent || mouse.Y >= layout.bodyContent+layout.bodyHeight {
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
	x, y := click.X-layout.terminalX-modalInsetX, click.Y-layout.bodyContent-modalInsetY
	if x < 0 || y < 0 {
		return m, nil
	}
	switch m.modal.kind {
	case "help":
		content := helpModalContent()
		if y == 2+len(content)-1 && hitLabel(content[len(content)-1], closeButtonLabel, x) {
			m.modal = nil
		}
	case "choice", "actions":
		start, end := modalChoiceWindow(*m.modal, modalContentHeight(m.bodyHeight()))
		if y >= 2 && y < 2+end-start {
			m.modal.choice = start + y - 2
			return m.activateModalChoice()
		}
		if y == 3+end-start && hitLabel(modalChoiceButtonLine(), cancelButtonLabel, x) {
			m.modal = nil
		}
	case "form":
		if y >= 2 && y < 2+len(m.modal.fields) {
			m.modal.field = y - 2
			return m, nil
		}
		primary := modalPrimaryButton(*m.modal)
		buttons := primary + "   " + cancelButtonLabel
		if y == 3+len(m.modal.fields) {
			switch {
			case hitLabel(buttons, primary, x):
				return m.submitModalForm()
			case hitLabel(buttons, cancelButtonLabel, x):
				m.modal = nil
			}
		}
	case "confirm":
		confirm := modalConfirmLabel(*m.modal)
		buttons := cancelButtonLabel + "   " + confirm
		if y == confirmButtonRow(*m.modal, max(1, m.terminalWidth()-modalInsetX)) {
			switch {
			case hitLabel(buttons, cancelButtonLabel, x):
				m.modal.choice = 0
				return m.activateConfirmation()
			case hitLabel(buttons, confirm, x):
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
	return m.acceptChoice()
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
	layout := m.layout()
	_, _, contextOpen := m.selectedContextActions()
	return m.attachment != nil && !contextOpen && layout.fits() && x >= layout.terminalX && x < layout.terminalX+layout.terminalWidth && y >= layout.bodyContent && y < layout.bodyContent+layout.bodyHeight
}

func (m tuiModel) forwardTerminalMouse(event tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := event.Mouse()
	layout := m.layout()
	x, y := mouse.X-layout.terminalX, mouse.Y-layout.bodyContent
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
	layout := m.layout()
	if !layout.fits() {
		message := fmt.Sprintf(
			"This terminal is too small to show every account.\n\nCurrent size: %d×%d\nRequired height: %d\n\nEnlarge the terminal to continue.\nAccount usage is never hidden or collapsed.\nCtrl+C quits the editor.",
			m.width, m.height, layout.requiredHeight,
		)
		view := tea.NewView(padBlock(message, m.width, m.height))
		view.AltScreen = true
		return view
	}
	buttonStyle := lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(lipgloss.Cyan)
	headerLeft := accentStyle.Render(headerTitleText) + " " + buttonStyle.Render(actionsButtonLabel) + " " + buttonStyle.Render(helpButtonLabel)
	header := joinKeepLeft(headerLeft, accentStyle.Render(m.focusLabel()), m.width)
	sidebar := m.renderSidebar()
	main := m.renderMain()
	sidebarLines, mainLines := strings.Split(sidebar, "\n"), strings.Split(main, "\n")
	usageLines := m.renderUsageLines(layout.sidebarWidth)
	bodyLines := []string{
		frame("┌") + styledTitledSegment(m.sidebarTitle(), layout.sidebarWidth) + frame("┬") + styledTitledSegment(m.mainTitle(), layout.terminalWidth) + frame("┐"),
	}
	for i := 0; i < layout.bodyHeight; i++ {
		switch {
		case i < layout.sidebarHeight:
			bodyLines = append(bodyLines, frame("│")+sidebarLines[i]+frame("│")+mainLines[i]+frame("│"))
		case i == layout.sidebarHeight:
			bodyLines = append(bodyLines, frame("├")+styledTitledSegment(m.usageTitle(), layout.sidebarWidth)+frame("┤")+mainLines[i]+frame("│"))
		default:
			bodyLines = append(bodyLines, frame("│")+usageLines[i-layout.sidebarHeight-1]+frame("│")+mainLines[i]+frame("│"))
		}
	}
	bodyLines = append(bodyLines, frame("└"+strings.Repeat("─", layout.sidebarWidth)+"┴"+strings.Repeat("─", layout.terminalWidth)+"┘"))
	footerLeft := terminalFooter()
	if m.controlMode {
		footerLeft = m.sidebarFooter()
	} else if m.loadingProjects() {
		footerLeft = "Loading projects…"
	} else if len(m.rows) == 0 {
		footerLeft = "No projects · Click Actions, or ⌘B/Ctrl+G then Tab"
	} else if m.attachment == nil {
		footerLeft = "No terminal · Select a project, workspace, or terminal, then Enter"
	}
	footer := joinKeepRight(footerLeft, m.message, m.width)
	content := header + "\n" + strings.Join(bodyLines, "\n") + "\n" + footer
	view := tea.NewView(content)
	view.AltScreen = true
	view.ReportFocus = true
	view.MouseMode = tea.MouseModeCellMotion
	if m.attachment != nil && !m.controlMode && m.modal == nil {
		x, y := m.attachment.CursorPosition()
		view.Cursor = tea.NewCursor(layout.terminalX+x, layout.bodyContent+y)
	}
	return view
}

func terminalFooter() string {
	if os.Getenv("TERM_PROGRAM") == "iTerm.app" {
		return "Terminal · ⌥-drag: select · ⌘C/⌘V: copy/paste · Ctrl+G: sidebar"
	}
	return "Terminal · ⌘B/Ctrl+G: sidebar · ⌘?: Help"
}

func (m tuiModel) renderSidebar() string {
	width, height := m.sidebarWidth(), m.sidebarHeight()
	lines := []string{}
	for index, row := range m.rows {
		var label string
		switch row.kind {
		case "project":
			marker, slot := "", ""
			marker = sidebarSignalMarker(row.signal) + " "
			if row.slot > 0 && row.slot <= 9 {
				slot = fmt.Sprintf("%d ", row.slot)
			}
			label = slot + marker + row.project.Name + " · " + row.host.Name
			if row.offline && row.window.ID == "" {
				label += " · offline"
			}
		case "workspace":
			marker := sidebarSignalMarker(row.signal)
			label = "  " + marker + " " + row.workspace.Name
		case "window":
			marker := sidebarSignalMarker(row.signal)
			slot := "  "
			if row.slot > 0 && row.slot <= 9 {
				slot = fmt.Sprintf("%d ", row.slot)
			}
			label = "    " + slot + marker + " " + row.window.Name
		}
		prefix := " "
		if index == m.selectedRow && !m.controlMode {
			prefix = "›"
		}
		label = fitPlain(prefix+label, width)
		style := sidebarRowStyle(row, index == m.selectedRow, m.controlMode, width)
		lines = append(lines, style.Render(label))
	}
	if len(lines) == 0 {
		if m.loadingProjects() {
			lines = append(lines, fitPlain(" Loading projects…", width), fitPlain(" Connecting to hosts", width))
		} else {
			lines = append(lines, fitPlain(" No projects", width), fitPlain(" Click Actions to begin", width))
		}
	}
	start := min(m.sidebarOffset, len(lines))
	end := min(len(lines), start+height)
	lines = lines[start:end]
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func sidebarRowStyle(row sidebarRow, selected, controlMode bool, width int) lipgloss.Style {
	style := lipgloss.NewStyle().Width(width)
	if selected && controlMode {
		return style.Reverse(true).Bold(true)
	}
	if selected {
		return style.Foreground(lipgloss.Cyan).Bold(true)
	}
	switch row.signal {
	case sidebarActive:
		style = style.Foreground(lipgloss.Cyan)
	case sidebarQuiet:
		style = style.Foreground(lipgloss.Green)
	case sidebarEmpty:
		style = style.Faint(true)
	case sidebarStopped, sidebarOffline, sidebarUnavailable:
		style = style.Foreground(lipgloss.Yellow)
	}
	if row.kind == "project" && row.signal != sidebarEmpty {
		style = style.Bold(true)
	}
	return style
}

func (m tuiModel) renderMain() string {
	width, height := m.terminalWidth(), m.bodyHeight()
	if m.modal != nil {
		return renderModal(*m.modal, width, height)
	}
	if row, choices, ok := m.selectedContextActions(); ok {
		return renderContextPanel(row, choices, width, height)
	}
	if m.attachment == nil {
		text := "Set up your first terminal\n\n1. Open Actions: click [ Actions ].\n   Keyboard: ⌘B or Ctrl+G, then Tab.\n2. Add a project.\n3. Click it or press Enter.\n   Its terminal opens in the original directory.\n4. Press Ctrl+N when you need a named workspace."
		if m.loadingProjects() {
			text = "Loading projects\n\nConnecting to configured hosts…"
		}
		if row, ok := m.selectedSidebarRow(); ok {
			if row.offline {
				reason := safeClientText(row.hostError, 160)
				if reason == "" {
					reason = "connection unavailable"
				}
				if hostNeedsManualRecovery(reason) {
					text = "Editor update required\n\nUpdate multicodex on every machine.\nThen quit and reopen only the outer editor.\nManaged tmux sessions keep running."
				} else {
					text = "Host offline\n\n" + row.host.Name + " is not reachable: " + reason + ".\n\nMulticodex editor will reconnect automatically. It does not stop existing tmux sessions."
				}
				return padInsetBlock(text, width, height, 1, 1)
			}
			switch row.kind {
			case "project":
				if row.window.ID != "" && !row.window.Alive {
					text = "Project terminal stopped\n\nThis tmux session no longer exists.\nUse Actions → Delete selected.\nThen press Enter to create a replacement."
				} else {
					text = "Project selected\n\nPress Enter or click the project to open its terminal in the original project directory.\nPress Ctrl+N to create a named workspace."
				}
			case "workspace":
				if row.workspace.Unavailable {
					text = "Workspace directory is unavailable\n\nIts terminals remain available for recovery, but commands that use the missing directory can fail.\nUse Actions → Delete selected when you no longer need it."
				} else {
					text = "Workspace selected\n\nPress Enter to create and open a new terminal.\nChoose Rename from Actions to change its name."
				}
			case "window":
				if !row.window.Alive {
					text = "Terminal stopped\n\nThis tmux session no longer exists.\nUse Actions → Delete selected.\nThen select the workspace to create a replacement."
				} else if row.workspace.Unavailable {
					text = "Workspace directory is unavailable\n\nOpen this terminal to recover its live session. Commands that use the missing directory can fail.\nUse Actions → Delete selected when finished."
				} else {
					text = "No terminal is open\n\nPress Enter or click the selected window to open it.\nPress Ctrl+N for another terminal."
				}
			}
		}
		return padInsetBlock(text, width, height, 1, 1)
	}
	return m.attachment.Render(width, height)
}

func (m tuiModel) selectedContextActions() (sidebarRow, []choice, bool) {
	row, ok := m.selectedSidebarRow()
	if !ok || !m.controlMode || (row.kind != "project" && row.kind != "workspace") {
		return sidebarRow{}, nil, false
	}
	choices := m.primaryActions(row)
	if item, ok := m.deleteAction(row); ok {
		choices = append(choices, item)
	}
	return row, choices, true
}

func renderContextPanel(row sidebarRow, choices []choice, width, height int) string {
	title := "Project selected"
	detail := row.project.Name + " · " + row.host.Name
	hint := "Enter: open project terminal · Ctrl+N: new workspace · Tab: all actions"
	manualRecovery := false
	if row.kind == "project" && row.window.ID != "" && !row.window.Alive && !row.offline {
		title = "Project terminal stopped"
		hint = "Delete the stopped terminal before creating a replacement."
	}
	if row.kind == "workspace" {
		title = "Workspace selected"
		detail = row.workspace.Name + " · " + row.project.Name + " · " + row.host.Name
		hint = "Enter: new terminal · ⌘R: rename · Tab: all actions"
		if row.workspace.Unavailable {
			hint = "Directory unavailable · use Delete only when recovery is complete"
		}
	}
	if row.offline && row.window.ID == "" {
		if hostNeedsManualRecovery(row.hostError) {
			title = "Editor update required"
			manualRecovery = true
		} else {
			hint = "Reconnect is automatic · existing tmux sessions are left unchanged"
		}
	}
	lines := []string{accentStyle.Render(title), plainDisplayText(detail), "", "Choose an action"}
	buttonStyle := lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(lipgloss.Cyan)
	for _, item := range choices {
		lines = append(lines, buttonStyle.Render(contextActionButton(item)))
	}
	if len(choices) == 0 {
		lines = append(lines, "No action is available while this host is offline.")
	}
	if manualRecovery {
		lines = append(lines, "", "Update multicodex on every machine.", "Then quit and reopen only the outer editor.", "Managed tmux sessions keep running.")
	} else {
		lines = append(lines, "", hint, "Click an option, or use the shown keyboard shortcut.")
	}
	return padInsetBlock(strings.Join(lines, "\n"), width, height, contextInsetX, contextInsetY)
}

func contextActionButton(item choice) string {
	label := "Action"
	switch item.action {
	case "open_project_window":
		label = "Open project terminal"
	case "new_workspace_selected":
		label = "New workspace…"
	case "list_tmux_sessions":
		label = "Adopt tmux session…"
	case "new_window_selected":
		label = "New terminal"
	case "rename_selected":
		label = "Rename workspace…"
	case "delete":
		switch {
		case item.window.Adopted:
			label = "Release tmux session…"
		case item.window.ID != "" && item.workspace.ID == "":
			label = "Delete project terminal…"
		case item.window.ID != "":
			label = "Delete terminal…"
		case item.workspace.External:
			label = "Remove preserved workspace…"
		default:
			label = "Delete workspace…"
		}
	}
	return "[ " + label + " ]"
}

func (m tuiModel) loadingProjects() bool {
	return m.refreshing && len(m.statuses) == 0 && len(m.rows) == 0
}

func (m tuiModel) sidebarFooter() string {
	row, ok := m.selectedSidebarRow()
	if !ok {
		return "Sidebar · ↑/↓: select · Tab: Actions · Esc: terminal"
	}
	if row.offline && row.window.ID == "" {
		reason := safeClientText(row.hostError, 100)
		if reason == "" {
			reason = "connection unavailable"
		}
		return row.host.Name + " offline · " + reason
	}
	if row.window.ID != "" && !row.offline && !row.window.Alive {
		return "Stopped · Actions → Delete selected to create a replacement"
	}
	switch row.kind {
	case "project":
		return "Project · Enter/click: open project terminal · Ctrl+N: new workspace"
	case "workspace":
		return "Workspace · Choose an option on the right · ⌘R: rename"
	case "window":
		return "Window · " + m.windowStatusLabel(row) + " · Enter: focus · ⌘N/Ctrl+N: new"
	default:
		return "Sidebar · ↑/↓: select · Tab: Actions · Esc: terminal"
	}
}

func (m tuiModel) focusLabel() string {
	if m.modal != nil {
		return "Dialog: " + plainDisplayText(m.modal.title)
	}
	if m.controlMode {
		return "Editor controls"
	}
	if row, ok := m.currentAttachedRow(); ok {
		return "Terminal: " + plainDisplayText(row.window.Name) + " · " + plainDisplayText(row.host.Name)
	}
	return "No terminal"
}

func (m tuiModel) mainTitle() string {
	if m.modal != nil {
		return "Dialog"
	}
	if row, _, ok := m.selectedContextActions(); ok {
		if row.kind == "project" {
			return "Project options"
		}
		return "Workspace options"
	}
	if row, ok := m.currentAttachedRow(); ok {
		if row.workspace.Unavailable {
			return "Terminal · workspace unavailable"
		}
		return "Terminal · " + row.window.Name
	}
	return "Terminal"
}

func (m tuiModel) sidebarTitle() string {
	if len(m.statuses) == 0 {
		if m.refreshing {
			return "Projects · connecting"
		}
		return "Projects"
	}
	offline := 0
	for _, status := range m.statuses {
		if status.Error != "" {
			offline++
		}
	}
	if offline > 0 {
		pulse := "·"
		if m.refreshPulse {
			pulse = "•"
		}
		return fmt.Sprintf("Projects · %d offline %s", offline, pulse)
	}
	if m.refreshPulse {
		return "Projects · live •"
	}
	return "Projects · live ·"
}

func sidebarSignalMarker(signal sidebarSignal) string {
	switch signal {
	case sidebarQuiet:
		return "○"
	case sidebarActive:
		return "●"
	case sidebarStopped:
		return "×"
	case sidebarOffline:
		return "?"
	case sidebarUnavailable:
		return "!"
	default:
		return "◇"
	}
}

func (m tuiModel) windowStatusLabel(row sidebarRow) string {
	switch row.signal {
	case sidebarActive:
		return "output changing"
	case sidebarQuiet:
		return "live, quiet"
	case sidebarStopped:
		return "stopped"
	case sidebarOffline:
		return "host offline"
	case sidebarUnavailable:
		return "directory unavailable"
	default:
		return "no terminal"
	}
}

func renderModal(modal modal, width, height int) string {
	contentWidth := max(1, width-modalInsetX)
	contentHeight := modalContentHeight(height)
	lines := []string{lipgloss.NewStyle().Bold(true).Render(modal.title), ""}
	switch modal.kind {
	case "help":
		lines = append(lines, helpModalContent()...)
	case "choice", "actions":
		start, end := modalChoiceWindow(modal, contentHeight)
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
		lines = append(lines, "", modalChoiceButtonLine(), "↑/↓ or Tab: choose · Enter: "+verb+" · Esc: cancel")
	case "form":
		for i, field := range modal.fields {
			marker := "  "
			if i == modal.field {
				marker = "› "
			}
			lines = append(lines, renderFormField(field, marker, contentWidth))
		}
		help := "Type a value · Enter: " + strings.ToLower(modalPrimaryLabel(modal))
		if len(modal.fields) > 1 {
			help = "Type values · Tab/↑/↓: next field"
		}
		lines = append(lines, "", modalPrimaryButton(modal)+"   "+cancelButtonLabel, help)
		if len(modal.fields) > 1 {
			lines = append(lines, "Enter: next; last field: "+strings.ToLower(modalPrimaryLabel(modal)))
		}
		lines = append(lines, "Ctrl+U: clear this field · Esc: cancel")
	case "confirm":
		cancel, remove := cancelButtonLabel, modalConfirmLabel(modal)
		if modal.choice == 0 {
			cancel = lipgloss.NewStyle().Reverse(true).Render(cancel)
		} else {
			remove = lipgloss.NewStyle().Reverse(true).Render(remove)
		}
		lines = append(lines, wrappedPlainLines(modal.reason, contentWidth)...)
		lines = append(lines, "")
		lines = append(lines, wrappedPlainLines(modalWarning(modal), contentWidth)...)
		lines = append(lines, "", cancel+"   "+remove)
		lines = append(lines, wrappedPlainLines("←/→ or Tab: choose · Enter: confirm · Esc: cancel", contentWidth)...)
	}
	return padInsetBlock(strings.Join(lines, "\n"), width, height, modalInsetX, modalInsetY)
}

func helpModalContent() []string {
	return []string{
		"Mouse",
		"  Click project/window: open · workspace: options",
		"  Wheel: move lists or scroll terminal history",
		"  Copy: iTerm2 ⌥-drag · others Shift-drag · ⌘C",
		"  Paste: ⌘V · focuses the attached terminal",
		"Keyboard · MacBook",
		"  ⌘B or Ctrl+G: focus the sidebar",
		"  ↑/↓: one row · ⌥↑/⌥↓: previous/next terminal",
		"  ⌘↑/⌘↓: first/last · Enter: open/focus/create",
		"  Tab: Actions · ⌘N/Ctrl+N: create · ?: Help",
		"  ⌘R: rename · ⌘⌫: delete · Esc: terminal",
		"  ⌘1–9 or ⌥1–9: open the numbered terminal",
		"  In the sidebar, Ctrl+C: quit",
		"Signals: ● changing   ○ live, quiet   ◇ empty",
		"         × stopped · ? offline · ! unavailable",
		"Need terminal Ctrl+G? Use Actions → Send Ctrl+G.",
		closeButtonLabel + " · Enter, ?, or Esc: close",
	}
}

func modalContentHeight(height int) int {
	return max(1, height-modalInsetY)
}

func modalPrimaryButton(current modal) string {
	return "[ " + modalPrimaryLabel(current) + " ]"
}

func modalPrimaryLabel(current modal) string {
	label := "Save"
	switch current.action {
	case "add_host":
		label = "Add host"
	case "add_project":
		label = "Add project"
	case "create_workspace":
		label = "Create workspace"
	case "rename_workspace", "rename_window":
		label = "Rename"
	case "put_file":
		label = "Attach file"
	case "adopt_tmux_session":
		label = "Adopt session"
	}
	return label
}

func modalChoiceWindow(current modal, height int) (int, int) {
	visible := max(1, height-5)
	start := max(0, current.choice-visible/2)
	start = min(start, max(0, len(current.choices)-visible))
	return start, min(len(current.choices), start+visible)
}

func modalChoiceButtonLine() string {
	return cancelButtonLabel
}

func (m *tuiModel) rebuildRows() {
	state := m.manager.State()
	locations := sortedProjectsByActivity(state, m.statuses)
	activities := make(map[string]time.Time, len(state.Activities))
	for _, activity := range state.Activities {
		activities[activity.HostID+"/"+activity.WindowID] = activity.ChangedAt
	}
	selectedID := ""
	if m.selectedRow >= 0 && m.selectedRow < len(m.rows) {
		selectedID = rowIdentity(m.rows[m.selectedRow])
	}
	rows := []sidebarRow{}
	slot := 0
	now := time.Now()
	for _, location := range locations {
		projectWindows := append([]Window(nil), location.Windows...)
		if location.ProjectWindow.ID != "" {
			projectWindows = append(projectWindows, location.ProjectWindow)
		}
		projectRow := sidebarRow{kind: "project", host: location.Host, project: location.Project, window: location.ProjectWindow, offline: location.HostError != "", hostError: location.HostError}
		projectRow.signal = summarizeSidebarWindows(projectRow.offline, false, location.Host.ID, projectWindows, activities, now)
		if projectRow.window.ID != "" {
			slot++
			projectRow.slot = slot
		}
		rows = append(rows, projectRow)
		for _, workspace := range location.Workspaces {
			workspaceWindows := []Window{}
			for _, window := range location.Windows {
				if window.WorkspaceID == workspace.ID {
					workspaceWindows = append(workspaceWindows, window)
				}
			}
			workspaceRow := sidebarRow{kind: "workspace", host: location.Host, project: location.Project, workspace: workspace, offline: location.HostError != "", hostError: location.HostError}
			workspaceRow.signal = summarizeSidebarWindows(workspaceRow.offline, workspace.Unavailable, location.Host.ID, workspaceWindows, activities, now)
			rows = append(rows, workspaceRow)
			for _, window := range workspaceWindows {
				slot++
				windowRow := sidebarRow{kind: "window", host: location.Host, project: location.Project, workspace: workspace, window: window, slot: slot, offline: location.HostError != "", hostError: location.HostError}
				windowRow.signal = summarizeSidebarWindows(windowRow.offline, false, location.Host.ID, []Window{window}, activities, now)
				rows = append(rows, windowRow)
			}
		}
	}
	m.rows = rows
	m.selectedRow = -1
	if m.selectOnRefreshID != "" {
		for i, row := range rows {
			if rowIdentity(row) == m.selectOnRefreshID {
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

func summarizeSidebarWindows(offline, unavailable bool, hostID string, windows []Window, activities map[string]time.Time, now time.Time) sidebarSignal {
	if offline {
		return sidebarOffline
	}
	if unavailable {
		return sidebarUnavailable
	}
	if len(windows) == 0 {
		return sidebarEmpty
	}
	quiet := false
	for _, window := range windows {
		if !window.Alive {
			continue
		}
		quiet = true
		changed := activities[hostID+"/"+window.ID]
		age := now.Sub(changed)
		if !changed.IsZero() && age >= 0 && age <= recentOutputWindow {
			return sidebarActive
		}
	}
	if quiet {
		return sidebarQuiet
	}
	return sidebarStopped
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
		if row.window.ID != "" && row.window.Alive && row.window.ID == state.SelectedWindowID {
			return row, true
		}
	}
	for _, row := range m.rows {
		if row.window.ID != "" && row.window.Alive {
			return row, true
		}
	}
	return sidebarRow{}, false
}

func (m tuiModel) selectWindowSlot(slot int) (tea.Model, tea.Cmd) {
	for i, row := range m.rows {
		if row.window.ID != "" && row.slot == slot {
			m.selectedRow = i
			m.ensureSelectionVisible()
			return m.selectCurrentRow()
		}
	}
	m.message = fmt.Sprintf("numbered window %d is not visible", slot)
	return m, nil
}

func (m tuiModel) selectCurrentRow() (tea.Model, tea.Cmd) {
	if m.selectedRow < 0 || m.selectedRow >= len(m.rows) {
		return m, nil
	}
	row := m.rows[m.selectedRow]
	if row.offline && row.window.ID == "" {
		m.message = offlineStatusMessage(row)
		return m, nil
	}
	if row.window.ID != "" && !row.offline && !row.window.Alive {
		m.controlMode = true
		m.message = "terminal stopped · use Actions → Delete selected to replace it"
		return m, nil
	}
	switch row.kind {
	case "project":
		if row.window.ID == "" {
			if !m.beginAction("opening project terminal…") {
				return m, nil
			}
			m.controlMode = false
			return m, m.track(openProjectWindowCmd(m.manager, row.host, row.project))
		}
	case "workspace":
		return m.startCreateWindow(row)
	case "window":
	default:
		return m, nil
	}
	if row.window.ID == m.attachedID {
		m.keepSidebarAttach = ""
		m.controlMode = false
		if m.attachingID != "" {
			m.queuedAttach = &row
			m.message = "current window queued after the active connection attempt"
		}
		return m, nil
	}
	if row.window.ID == m.attachingID {
		m.queuedAttach = nil
		m.keepSidebarAttach = ""
		m.controlMode = false
		return m, nil
	}
	m.controlMode = false
	m.keepSidebarAttach = ""
	return m, m.requestAttach(row)
}

func (m tuiModel) openSelectedWindowFromSidebar() (tea.Model, tea.Cmd) {
	row, ok := m.selectedSidebarRow()
	if !ok || row.window.ID == "" || row.window.ID == m.attachedID {
		return m, nil
	}
	m.keepSidebarAttach = row.window.ID
	return m, m.requestAttach(row)
}

func (m tuiModel) createForSelection() (tea.Model, tea.Cmd) {
	row, ok := m.selectedSidebarRow()
	if !ok {
		m.openActionMenu()
		return m, nil
	}
	if row.offline {
		m.message = offlineStatusMessage(row)
		return m, nil
	}
	if row.kind == "project" {
		m.openWorkspaceName(row.host, row.project)
		return m, nil
	}
	return m.startCreateWindow(row)
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
		m.selectedRow = max(0, min(len(m.rows)-1, m.selectedRow+direction*max(1, m.sidebarHeight()-1)))
	}
	m.ensureSelectionVisible()
}

func (m tuiModel) selectAdjacentWindow(direction int) (tea.Model, tea.Cmd) {
	var windowRows []int
	current := -1
	original := m.selectedRow
	for index, row := range m.rows {
		if row.window.ID == "" {
			continue
		}
		if index == m.selectedRow {
			current = len(windowRows)
		}
		windowRows = append(windowRows, index)
	}
	if len(windowRows) == 0 {
		m.message = "no terminal windows are available"
		return m, nil
	}
	if current >= 0 {
		current = (current + direction + len(windowRows)) % len(windowRows)
		m.selectedRow = windowRows[current]
	} else if direction > 0 {
		m.selectedRow = windowRows[0]
		for _, index := range windowRows {
			if index > original {
				m.selectedRow = index
				break
			}
		}
	} else {
		m.selectedRow = windowRows[len(windowRows)-1]
		for index := len(windowRows) - 1; index >= 0; index-- {
			if windowRows[index] < original {
				m.selectedRow = windowRows[index]
				break
			}
		}
	}
	m.ensureSelectionVisible()
	return m.openSelectedWindowFromSidebar()
}

func (m *tuiModel) moveModalChoicePage(direction int) {
	if m.modal == nil || len(m.modal.choices) == 0 {
		return
	}
	page := max(1, modalContentHeight(m.bodyHeight())-5)
	m.modal.choice = max(0, min(len(m.modal.choices)-1, m.modal.choice+direction*page))
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
	height := m.sidebarHeight()
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
	choices := []choice{}
	hasProject := false
	hasWorkspace := false
	for _, row := range m.rows {
		switch row.kind {
		case "project":
			hasProject = hasProject || !row.offline
		case "workspace":
			hasWorkspace = hasWorkspace || !row.offline && !row.workspace.Unavailable
		}
	}
	selectedRow, hasSelection := m.selectedSidebarRow()
	if hasSelection {
		choices = append(choices, m.primaryActions(selectedRow)...)
	}
	selectedProjectCanCreateWorkspace := hasSelection && !selectedRow.offline && selectedRow.kind == "project"
	if hasProject && !selectedProjectCanCreateWorkspace {
		choices = append(choices, choice{label: "New workspace…", action: "new_workspace"})
	}
	selectedWorkspaceCanCreateWindow := hasSelection && !selectedRow.offline && (selectedRow.kind == "workspace" || selectedRow.kind == "window") && !selectedRow.workspace.Unavailable
	if hasWorkspace && !selectedWorkspaceCanCreateWindow {
		choices = append(choices, choice{label: "New window…", action: "new_window"})
	}
	choices = append(choices,
		choice{label: "Add project…", action: "add_project"},
		choice{label: "Add SSH host…", action: "add_host"},
	)
	if _, ok := m.currentAttachedRow(); ok {
		choices = append(choices,
			choice{label: "Attach file…", action: "attach_file"},
			choice{label: "Attach clipboard image", action: "attach_clipboard"},
			choice{label: "Open terminal history", action: "scrollback"},
		)
	}
	if hasSelection {
		if item, ok := m.deleteAction(selectedRow); ok {
			choices = append(choices, item)
		}
	}
	choices = append(choices, choice{label: "Run safe cleanup", action: "cleanup"})
	if m.attachment != nil {
		choices = append(choices, choice{label: "Send Ctrl+G to terminal", action: "send_control_g"})
	}
	choices = append(choices,
		choice{label: "Help", action: "help"},
		choice{label: "Quit multicodex editor", action: "quit"},
	)
	m.modal = &modal{kind: "actions", title: "Editor actions", choices: choices}
}

func (m tuiModel) primaryActions(row sidebarRow) []choice {
	choices := []choice{}
	if row.offline {
		return choices
	}
	switch row.kind {
	case "project":
		if row.window.ID == "" || row.window.Alive {
			choices = append(choices, choice{label: "Open project terminal", action: "open_project_window", host: row.host, project: row.project, window: row.window})
		}
		choices = append(choices, choice{label: "New workspace in " + row.project.Name + "…", action: "new_workspace_selected", host: row.host, project: row.project})
		if !row.offline {
			choices = append(choices, choice{label: "Adopt existing tmux session…", action: "list_tmux_sessions", host: row.host, project: row.project})
		}
	case "workspace":
		if !row.workspace.Unavailable {
			choices = append(choices, choice{label: "New window in " + row.workspace.Name, action: "new_window_selected", host: row.host, project: row.project, workspace: row.workspace})
		}
		choices = append(choices, choice{label: "Rename workspace…", action: "rename_selected", host: row.host, project: row.project, workspace: row.workspace})
	case "window":
		if !row.workspace.Unavailable {
			choices = append(choices, choice{label: "New window in " + row.workspace.Name, action: "new_window_selected", host: row.host, project: row.project, workspace: row.workspace, window: row.window})
		}
		choices = append(choices, choice{label: "Rename window…", action: "rename_selected", host: row.host, project: row.project, workspace: row.workspace, window: row.window})
	}
	return choices
}

func (m tuiModel) deleteAction(row sidebarRow) (choice, bool) {
	if row.offline || row.kind == "project" && row.window.ID == "" || row.kind != "project" && row.kind != "workspace" && row.kind != "window" {
		return choice{}, false
	}
	label := "Delete selected window or workspace…"
	if row.kind == "project" {
		label = "Delete project terminal…"
	} else if row.window.Adopted {
		label = "Release selected tmux session…"
	} else if row.workspace.External {
		label = "Remove preserved workspace…"
	}
	return choice{label: label, action: "delete", host: row.host, project: row.project, workspace: row.workspace, window: row.window}, true
}

func (m tuiModel) activateEditorAction() (tea.Model, tea.Cmd) {
	if m.modal == nil || len(m.modal.choices) == 0 || m.modal.choice < 0 || m.modal.choice >= len(m.modal.choices) {
		return m, nil
	}
	selected := m.modal.choices[m.modal.choice]
	m.modal = nil
	return m.activateEditorChoice(selected)
}

func (m tuiModel) activateEditorChoice(selected choice) (tea.Model, tea.Cmd) {
	action := selected.action
	switch action {
	case "new_window_selected":
		return m.startCreateWindow(sidebarRow{kind: "workspace", host: selected.host, project: selected.project, workspace: selected.workspace})
	case "new_workspace_selected":
		m.openWorkspaceName(selected.host, selected.project)
	case "open_project_window":
		row := sidebarRow{kind: "project", host: selected.host, project: selected.project, window: selected.window}
		if row.window.ID != "" {
			for i := range m.rows {
				if m.rows[i].kind == "project" && m.rows[i].project.ID == row.project.ID && m.rows[i].host.ID == row.host.ID {
					m.selectedRow = i
					return m.selectCurrentRow()
				}
			}
		}
		if !m.beginAction("opening project terminal…") {
			return m, nil
		}
		return m, m.track(openProjectWindowCmd(m.manager, selected.host, selected.project))
	case "rename_selected":
		kind := "workspace"
		if selected.window.ID != "" {
			kind = "window"
		}
		m.openRenameRow(sidebarRow{kind: kind, host: selected.host, project: selected.project, workspace: selected.workspace, window: selected.window})
	case "new_window":
		m.openWorkspaceChoice()
	case "new_workspace":
		m.openProjectChoice()
	case "add_project":
		m.openHostChoice("add_project", "Choose a host for the new project")
	case "add_host":
		m.modal = &modal{kind: "form", action: "add_host", title: "Add SSH host", fields: []formField{{label: "Display name", limit: 80}, {label: "SSH alias from ~/.ssh/config", limit: 128}}}
	case "list_tmux_sessions":
		if !m.beginAction("looking for tmux sessions in " + selected.project.Name + "…") {
			return m, nil
		}
		return m, m.track(listTmuxSessionsCmd(m.manager, selected.host, selected.project))
	case "attach_file":
		if row, ok := m.currentAttachedRow(); ok {
			if _, pending := m.pendingPastes[row.window.ID]; pending {
				m.message = "this window already has an attachment waiting to paste"
				return m, nil
			}
			m.modal = &modal{kind: "form", action: "put_file", title: "Attach file to " + row.window.Name, host: row.host, project: row.project, workspace: row.workspace, window: row.window,
				fields: []formField{{label: "Absolute client file path (16 MiB max)", limit: 4096}}}
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
	choices := []choice{}
	for _, row := range m.rows {
		if row.kind == "project" && !row.offline {
			choices = append(choices, choice{label: row.project.Name + " · " + row.host.Name, host: row.host, project: row.project})
		}
	}
	if len(choices) == 0 {
		m.message = "add a project first from the Actions menu"
		return
	}
	m.modal = &modal{kind: "choice", action: "create_workspace", title: "Choose a project for the new workspace", choices: choices}
}

func (m *tuiModel) openWorkspaceChoice() {
	choices := []choice{}
	for _, status := range m.statuses {
		if status.Error != "" {
			continue
		}
		projects := map[string]Project{}
		for _, project := range status.Host.Projects {
			projects[project.ID] = project
		}
		for _, workspace := range status.Snapshot.Workspaces {
			if workspace.Unavailable {
				continue
			}
			project := projects[workspace.ProjectID]
			choices = append(choices, choice{label: workspace.Name + " · " + project.Name + " · " + status.Host.Name, host: status.Host, project: project, workspace: workspace})
		}
	}
	if len(choices) == 0 {
		m.message = "create a workspace first from the Actions menu"
		return
	}
	m.modal = &modal{kind: "choice", action: "create_window", title: "Choose a workspace for the new window", choices: choices}
}

func (m tuiModel) selectedSidebarRow() (sidebarRow, bool) {
	if m.selectedRow < 0 || m.selectedRow >= len(m.rows) {
		return sidebarRow{}, false
	}
	return m.rows[m.selectedRow], true
}

func (m *tuiModel) openWorkspaceName(host Host, project Project) {
	m.modal = &modal{kind: "form", action: "create_workspace", title: "Create workspace — " + project.Name, host: host, project: project,
		fields: []formField{{label: "Workspace name", limit: 80}}}
}

func (m *tuiModel) openRename() {
	row, ok := m.selectedSidebarRow()
	if !ok {
		m.message = "select a workspace or window to rename"
		return
	}
	if row.offline {
		m.message = offlineStatusMessage(row)
		return
	}
	m.openRenameRow(row)
}

func (m *tuiModel) openRenameRow(row sidebarRow) {
	switch row.kind {
	case "workspace":
		m.modal = &modal{kind: "form", action: "rename_workspace", title: "Rename workspace", host: row.host, workspace: row.workspace,
			fields: []formField{{label: "Workspace name", value: row.workspace.Name, limit: 80}}}
	case "window":
		m.modal = &modal{kind: "form", action: "rename_window", title: "Rename window", host: row.host, workspace: row.workspace, window: row.window,
			fields: []formField{{label: "Window name", value: row.window.Name, limit: 80}}}
	default:
		m.message = "select a workspace or window to rename"
	}
}

func (m tuiModel) startCreateWindow(row sidebarRow) (tea.Model, tea.Cmd) {
	if row.workspace.ID == "" {
		m.message = "select a workspace before creating a window"
		return m, nil
	}
	if row.offline {
		m.message = offlineStatusMessage(row)
		return m, nil
	}
	if row.workspace.Unavailable {
		m.message = "workspace directory is unavailable; remove it or select another workspace"
		return m, nil
	}
	if !m.beginAction("creating a terminal in " + row.workspace.Name + "…") {
		return m, nil
	}
	m.modal = nil
	return m, m.track(createWindowCmd(m.manager, row.host.ID, row.workspace.ID))
}

func (m tuiModel) acceptChoice() (tea.Model, tea.Cmd) {
	if len(m.modal.choices) == 0 {
		m.modal = nil
		return m, nil
	}
	selected := m.modal.choices[m.modal.choice]
	action := m.modal.action
	switch action {
	case "add_project":
		m.modal = &modal{kind: "form", action: action, title: "Add project — " + selected.host.Name, host: selected.host,
			fields: []formField{{label: "Project name", limit: 80}, {label: "Absolute host directory path", limit: 4096}}}
	case "create_workspace":
		m.openWorkspaceName(selected.host, selected.project)
	case "create_window":
		return m.startCreateWindow(sidebarRow{kind: "workspace", host: selected.host, project: selected.project, workspace: selected.workspace})
	case "adopt_tmux_session":
		if workspace, ok := m.externalWorkspace(selected.host.ID, selected.project.ID); ok {
			if !m.beginAction("adopting " + selected.session.Name + "…") {
				return m, nil
			}
			m.modal = nil
			return m, m.track(adoptTmuxSessionCmd(m.manager, selected.host, selected.project, workspace.Name, selected.session))
		}
		m.modal = &modal{kind: "form", action: "adopt_tmux_session", title: "Adopt " + selected.session.Name, host: selected.host, project: selected.project, session: selected.session,
			fields: []formField{{label: "Workspace name", limit: 80}}}
	}
	return m, nil
}

func (m tuiModel) externalWorkspace(hostID, projectID string) (Workspace, bool) {
	for _, status := range m.statuses {
		if status.Host.ID != hostID {
			continue
		}
		for _, workspace := range status.Snapshot.Workspaces {
			if workspace.ProjectID == projectID && workspace.External {
				return workspace, true
			}
		}
	}
	return Workspace{}, false
}

func (m *tuiModel) openDeleteConfirmation() {
	if m.selectedRow < 0 || m.selectedRow >= len(m.rows) {
		return
	}
	row := m.rows[m.selectedRow]
	if row.offline {
		m.message = offlineStatusMessage(row)
		return
	}
	current := &modal{kind: "confirm", host: row.host, project: row.project, workspace: row.workspace, delete: DeleteRequest{Force: false}}
	switch row.kind {
	case "project":
		if row.window.ID == "" {
			m.message = "this project has no project terminal to delete"
			return
		}
		current.action, current.title = "delete_window", "Delete project terminal?"
		current.reason = "Delete the project terminal and its tmux session? The project directory stays unchanged."
		current.delete.ID = row.window.ID
	case "window":
		current.action, current.title, current.reason = "delete_window", "Delete window?", "Delete “"+row.window.Name+"” and its tmux session?"
		if row.window.Adopted {
			current.title = "Release session?"
			current.reason = "Stop managing “" + row.window.Name + "” in multicodex editor?"
			current.confirm = "[ Release ]"
			current.warning = "The tmux session and its process will keep running. You can adopt it again."
		}
		current.delete.ID = row.window.ID
	case "workspace":
		current.action, current.title = "delete_workspace", "Delete workspace?"
		if row.workspace.External {
			current.title = "Remove preserved workspace?"
			current.reason = "Stop managing workspace “" + row.workspace.Name + "” and its windows?"
			current.confirm = "[ Remove ]"
			current.warning = "The project directory, Git branches, and adopted sessions will remain. Editor-created windows will stop."
		} else if row.workspace.Git {
			current.reason = "Delete workspace “" + row.workspace.Name + "”, all its terminal windows, and its owned Git worktree and branch?"
		} else {
			current.reason = "Delete workspace “" + row.workspace.Name + "” and all its terminal windows? Its project directory will remain."
		}
		current.delete.ID = row.workspace.ID
		for _, candidate := range m.rows {
			if candidate.workspace.ID == row.workspace.ID && candidate.window.ID != "" {
				current.windowIDs = append(current.windowIDs, candidate.window.ID)
			}
		}
	default:
		m.message = "select a window or workspace to delete"
		return
	}
	m.modal = current
}

func offlineStatusMessage(row sidebarRow) string {
	if hostNeedsManualRecovery(row.hostError) {
		return row.host.Name + " needs an editor update and restart; tmux sessions keep running"
	}
	return row.host.Name + " is offline; reconnect is automatic"
}

func hostNeedsManualRecovery(reason string) bool {
	return reason == incompatibleEditorHostMessage || reason == unverifiableEditorHostBuildMessage
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
		if msg.form != nil {
			form := *msg.form
			m.modal = &form
		}
		return m, m.startRefresh()
	}
	if msg.action == "copy_mode" {
		m.message = "terminal history: arrows or Option+↑/↓, q to leave"
	}
	switch value := msg.value.(type) {
	case Host:
		m.message = "added host " + value.Name
	case Project:
		m.message = "added project " + value.Name
		m.selectOnRefreshID = "p/" + value.ID
		m.controlMode = true
	case createdWorkspace:
		m.message = "created workspace " + value.workspace.Name + " with " + value.window.Name
		m.selectOnRefreshID = "w/" + value.window.ID
		if host, ok := m.manager.findHost(msg.hostID); ok {
			cmd := m.requestAttach(sidebarRow{kind: "window", host: host, workspace: value.workspace, window: value.window})
			return m, tea.Batch(m.startRefresh(), cmd)
		}
	case Window:
		m.message = "created window " + value.Name
		m.selectOnRefreshID = "w/" + value.ID
		if host, ok := m.manager.findHost(msg.hostID); ok {
			cmd := m.requestAttach(sidebarRow{kind: "window", host: host, window: value})
			return m, tea.Batch(m.startRefresh(), cmd)
		}
	case OpenProjectWindowResult:
		if value.Created {
			m.message = "created project terminal"
		} else {
			m.message = "opened project terminal"
		}
		m.selectOnRefreshID = "p/" + value.Window.ProjectID
		if host, ok := m.manager.findHost(msg.hostID); ok {
			project, _ := findProject(host, value.Window.ProjectID)
			cmd := m.requestAttach(sidebarRow{kind: "project", host: host, project: project, window: value.Window})
			return m, tea.Batch(m.startRefresh(), cmd)
		}
	case []TmuxSessionCandidate:
		if len(value) == 0 {
			m.message = "no eligible tmux sessions are running in this project"
			break
		}
		if msg.form == nil {
			m.message = "tmux session discovery lost its project context"
			break
		}
		choices := make([]choice, 0, len(value))
		for _, session := range value {
			choices = append(choices, choice{label: session.Name + " · " + session.Command, host: msg.form.host, project: msg.form.project, session: session})
		}
		m.modal = &modal{kind: "choice", action: "adopt_tmux_session", title: "Choose a tmux session to adopt", choices: choices}
		return m, nil
	case AdoptedTmuxSession:
		m.message = "adopted " + value.Window.Session + " without restarting it"
		m.selectOnRefreshID = "w/" + value.Window.ID
		if host, ok := m.manager.findHost(msg.hostID); ok {
			cmd := m.requestAttach(sidebarRow{kind: "window", host: host, workspace: value.Workspace, window: value.Window})
			return m, tea.Batch(m.startRefresh(), cmd)
		}
	case renamedResource:
		m.message = "renamed " + value.kind + " to " + value.name
	case DeleteResult:
		if value.Deleted {
			m.message = "deleted"
			if msg.action == "delete_window" {
				for _, row := range m.rows {
					if row.window.ID == msg.targetID && row.window.Adopted {
						m.message = "released; the original tmux session is still running"
						break
					}
				}
			}
			if msg.action == "delete_workspace" {
				for _, row := range m.rows {
					if row.workspace.ID == msg.targetID && row.workspace.External {
						m.message = "removed; the project and adopted tmux sessions are preserved"
						break
					}
				}
			}
			if msg.action == "delete_window" && msg.targetID == m.attachedID && m.attachment != nil {
				_ = m.attachment.Close()
				m.attachment, m.attachedID, m.attachedHost = nil, "", ""
			}
			if msg.action == "delete_workspace" {
				if err := m.forgetDeletedWorkspace(msg.windowIDs); err != nil {
					m.message = "deleted, but client reconnect state was not saved: " + err.Error()
				}
			}
		} else if value.Reason != "" && value.Forceable {
			m.modal = &modal{kind: "confirm", action: msg.action, title: "Delete permanently?", host: hostForID(m.manager.State(), msg.hostID), windowIDs: msg.windowIDs, delete: DeleteRequest{ID: msg.targetID, Force: true}, reason: value.Reason}
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
		if msg.action != "background_cleanup" || cleanupResultHasNews(value) {
			m.message = cleanupSummary(value)
		}
	}
	return m, m.startRefresh()
}

func (m *tuiModel) forgetDeletedWorkspace(windowIDs []string) error {
	selectedWindowID := m.manager.State().SelectedWindowID
	selectedWindowDeleted, attachedWindowDeleted := false, false
	for _, windowID := range windowIDs {
		selectedWindowDeleted = selectedWindowDeleted || windowID == selectedWindowID
		attachedWindowDeleted = attachedWindowDeleted || windowID == m.attachedID
	}
	if attachedWindowDeleted && m.attachment != nil {
		_ = m.attachment.Close()
		m.attachment, m.attachedID, m.attachedHost = nil, "", ""
	}
	if selectedWindowDeleted {
		return m.manager.clearSelectedWindow(selectedWindowID)
	}
	return nil
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

func cleanupResultHasNews(results map[string]CleanupResult) bool {
	for _, result := range results {
		if result.WindowsDeleted != 0 || result.WorkspacesDeleted != 0 || result.AttachmentsDeleted != 0 || len(result.Skipped) != 0 {
			return true
		}
	}
	return false
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
	if m.attachedID == "" {
		return sidebarRow{}, false
	}
	for _, row := range m.rows {
		if row.window.ID == m.attachedID {
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
	return m, m.track(clipboardAttachmentCmd(m.manager, row.host.ID, row.workspace.ID, row.project.ID, row.window.ID))
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
func (m tuiModel) terminalWidth() int  { return m.layout().terminalWidth }
func (m tuiModel) bodyHeight() int     { return m.layout().bodyHeight }
func (m tuiModel) sidebarHeight() int  { return m.layout().sidebarHeight }

func (m tuiModel) layout() screenLayout {
	const minimumSidebarHeight = 3
	sidebarWidth := m.sidebarWidth()
	usageHeight := max(1, len(m.usage.accounts))
	bodyHeight := max(1, m.height-4)
	sidebarHeight := max(1, bodyHeight-usageHeight-1)
	return screenLayout{
		width:         m.width,
		height:        m.height,
		bodyContent:   2,
		sidebarWidth:  sidebarWidth,
		terminalX:     sidebarWidth + 2,
		terminalWidth: max(1, m.width-sidebarWidth-3),
		bodyHeight:    bodyHeight,
		sidebarHeight: sidebarHeight,
		usageHeight:   usageHeight,
		requiredHeight: max(
			minimumHeight,
			4+usageHeight+1+minimumSidebarHeight,
		),
	}
}

func (l screenLayout) fits() bool {
	return l.width >= minimumWidth && l.height >= l.requiredHeight
}

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

func isSidebarFocusKey(key tea.KeyPressMsg) bool {
	return key.Keystroke() == "ctrl+g" || isCommandRune(key, 'b')
}

func isGlobalHelpKey(key tea.KeyPressMsg) bool {
	if key.Keystroke() == "f1" {
		return true
	}
	return hasCommandModifier(key) && (key.Code == '?' || key.BaseCode == '/' || key.Text == "?")
}

func isSidebarHelpKey(key tea.KeyPressMsg) bool {
	return key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModHyper|tea.ModSuper) == 0 && (key.Code == '?' || key.Text == "?")
}

func isCreateKey(key tea.KeyPressMsg) bool {
	return key.Keystroke() == "ctrl+n" || isCommandRune(key, 'n')
}

func isRenameKey(key tea.KeyPressMsg) bool {
	return key.Keystroke() == "f2" || isCommandRune(key, 'r')
}

func isDeleteKey(key tea.KeyPressMsg) bool {
	return hasCommandModifier(key) && (key.Code == tea.KeyBackspace || key.Code == tea.KeyDelete)
}

func isCancelKey(key tea.KeyPressMsg) bool {
	return key.Keystroke() == "esc" || isCommandRune(key, '.')
}

func isMacOptionArrowKey(key tea.KeyPressMsg, code rune) bool {
	return key.Code == code && key.Mod&tea.ModAlt != 0 && key.Mod&(tea.ModCtrl|tea.ModMeta|tea.ModHyper|tea.ModSuper) == 0
}

func isMacListEdgeKey(key tea.KeyPressMsg, code rune) bool {
	return key.Code == code && hasCommandModifier(key)
}

func isCommandRune(key tea.KeyPressMsg, want rune) bool {
	if !hasCommandModifier(key) {
		return false
	}
	code := key.Code
	if key.BaseCode != 0 {
		code = key.BaseCode
	}
	return unicode.ToLower(code) == unicode.ToLower(want)
}

func hasCommandModifier(key tea.KeyPressMsg) bool {
	return key.Mod&(tea.ModSuper|tea.ModMeta) != 0 && key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModHyper) == 0
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

func (m *tuiModel) startExitedWindowRemoval(statuses []HostStatus) tea.Cmd {
	if m.exitRemovalTried == nil {
		m.exitRemovalTried = make(map[windowRef]struct{})
	}
	reachable := make(map[string]bool, len(statuses))
	present := make(map[windowRef]bool)
	for _, status := range statuses {
		if status.Error != "" {
			for ref := range m.exitRemovalTried {
				if ref.hostID == status.Host.ID {
					delete(m.exitRemovalTried, ref)
				}
			}
			continue
		}
		reachable[status.Host.ID] = true
		for _, window := range status.Snapshot.Windows {
			ref := windowRef{hostID: status.Host.ID, windowID: window.ID}
			present[ref] = true
			if !window.Exited {
				delete(m.exitRemovalTried, ref)
			}
		}
	}
	for ref := range m.exitRemovalTried {
		if reachable[ref.hostID] && !present[ref] {
			delete(m.exitRemovalTried, ref)
		}
	}
	if m.exitRemovalBusy || m.actionBusy || m.cleanupBusy {
		return nil
	}
	for _, status := range statuses {
		if status.Error != "" {
			continue
		}
		for _, window := range status.Snapshot.Windows {
			if window.Alive || !window.Exited {
				continue
			}
			ref := windowRef{hostID: status.Host.ID, windowID: window.ID}
			if _, tried := m.exitRemovalTried[ref]; tried {
				continue
			}
			m.exitRemovalTried[ref] = struct{}{}
			m.exitRemovalBusy = true
			m.message = "terminal exited · removing…"
			return m.track(removeExitedWindowCmd(m.manager, ref))
		}
	}
	return nil
}

func removeExitedWindowCmd(manager *Manager, ref windowRef) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 30*time.Second)
		defer cancel()
		result, err := manager.DeleteWindow(ctx, ref.hostID, DeleteRequest{ID: ref.windowID})
		return exitedWindowRemovalMsg{hostID: ref.hostID, windowID: ref.windowID, result: result, err: err}
	}
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
		message := usageMsg{accounts: accountUsageRows(summary)}
		if err != nil {
			message.err = "usage refresh failed"
		}
		return message
	}
}

func loadingAccountUsage(labels []string) []accountUsage {
	rows := make([]accountUsage, 0, len(labels))
	for _, label := range labels {
		rows = append(rows, accountUsage{label: accountUsageLabel(label), loading: true})
	}
	return rows
}

func accountUsageRows(summary *usage.Summary) []accountUsage {
	if summary == nil {
		return nil
	}
	rows := make([]accountUsage, 0, len(summary.Accounts))
	for _, account := range summary.Accounts {
		reference := summary.FetchedAt
		if account.FetchedAt != nil {
			reference = *account.FetchedAt
		}
		resetSeconds, resetKnown := accountResetSeconds(account.WeeklyWindow, reference)
		rows = append(rows, accountUsage{
			label:        accountUsageLabel(account.Label),
			usedPercent:  account.WeeklyWindow.UsedPercent,
			resetSeconds: resetSeconds,
			resetKnown:   resetKnown,
			available:    strings.TrimSpace(account.Error) == "" && account.WeeklyWindow.UsedPercent >= 0,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].label < rows[j].label })
	return rows
}

func accountResetSeconds(window usage.WindowSummary, reference time.Time) (int64, bool) {
	if window.SecondsUntilReset != nil {
		return max(0, *window.SecondsUntilReset), true
	}
	if window.ResetsAt == nil {
		return 0, false
	}
	if reference.IsZero() {
		reference = time.Now()
	}
	return max(0, int64(window.ResetsAt.Sub(reference).Seconds())), true
}

func accountUsageLabel(label string) string {
	label = strings.TrimSpace(plainDisplayText(label))
	if label == "" {
		return "account"
	}
	return label
}

func (m *tuiModel) applyUsage(message usageMsg) {
	if message.err == "" {
		m.usage = accountUsageState{accounts: retainFailedAccountUsage(m.usage.accounts, message.accounts)}
		return
	}
	if hasAvailableAccountUsage(m.usage.accounts) {
		for i := range m.usage.accounts {
			if m.usage.accounts[i].available {
				m.usage.accounts[i].stale = true
			}
		}
		m.usage.accounts = mergeAccountUsage(m.usage.accounts, message.accounts)
		m.usage.err = message.err
		return
	}
	if len(message.accounts) > 0 {
		m.usage.accounts = message.accounts
	} else {
		for i := range m.usage.accounts {
			m.usage.accounts[i].loading = false
			m.usage.accounts[i].available = false
		}
	}
	m.usage.err = message.err
}

func retainFailedAccountUsage(previous, incoming []accountUsage) []accountUsage {
	previousByLabel := make(map[string][]accountUsage, len(previous))
	for _, account := range previous {
		previousByLabel[account.label] = append(previousByLabel[account.label], account)
	}
	used := make(map[string]int, len(incoming))
	rows := make([]accountUsage, 0, len(incoming))
	for _, account := range incoming {
		index := used[account.label]
		used[account.label]++
		matches := previousByLabel[account.label]
		if !account.available && index < len(matches) && matches[index].available {
			account = matches[index]
			account.loading = false
			account.stale = true
		}
		rows = append(rows, account)
	}
	return rows
}

func hasAvailableAccountUsage(accounts []accountUsage) bool {
	for _, account := range accounts {
		if account.available {
			return true
		}
	}
	return false
}

func mergeAccountUsage(previous, incoming []accountUsage) []accountUsage {
	rows := append([]accountUsage(nil), previous...)
	previousCount := make(map[string]int, len(previous))
	for _, account := range previous {
		previousCount[account.label]++
	}
	incomingCount := make(map[string]int, len(incoming))
	for _, account := range incoming {
		incomingCount[account.label]++
		if incomingCount[account.label] > previousCount[account.label] {
			rows = append(rows, account)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].label < rows[j].label })
	return rows
}

func (m tuiModel) usageLines(width int) []string {
	if len(m.usage.accounts) == 0 {
		if m.usage.err != "" {
			return []string{"Usage unavailable"}
		}
		return []string{"No accounts configured"}
	}
	lines := make([]string, 0, len(m.usage.accounts))
	for _, account := range m.usage.accounts {
		lines = append(lines, accountUsageLine(account, width))
	}
	return lines
}

func accountUsageLine(account accountUsage, width int) string {
	width = max(1, width)
	state := accountUsageStateText(account)
	stateWidth := lipgloss.Width(state)
	if stateWidth >= width {
		return ansi.Truncate(state, width, "…")
	}
	labelWidth := width - stateWidth - 1
	label := fitMiddlePlain(account.label, labelWidth)
	return label + " " + state
}

func accountUsageStateText(account accountUsage) string {
	switch {
	case account.loading:
		return "loading…"
	case account.available && account.stale:
		return fmt.Sprintf("%d%% stale · %s", account.usedPercent, accountResetText(account))
	case account.available:
		return fmt.Sprintf("%d%% used · %s", account.usedPercent, accountResetText(account))
	default:
		return "unavailable"
	}
}

func accountResetText(account accountUsage) string {
	if !account.resetKnown {
		return "?"
	}
	seconds := max(0, account.resetSeconds)
	duration := time.Duration(seconds) * time.Second
	switch {
	case duration <= 0:
		return "now"
	case duration < time.Minute:
		return "<1m"
	case duration < time.Hour:
		return fmt.Sprintf("%dm", int(duration/time.Minute))
	case duration < 24*time.Hour:
		hours, minutes := int(duration/time.Hour), int(duration%time.Hour/time.Minute)
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, minutes)
	default:
		days, hours := int(duration/(24*time.Hour)), int(duration%(24*time.Hour)/time.Hour)
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, hours)
	}
}

func (m tuiModel) usageTitle() string {
	if m.usage.err != "" {
		if hasAvailableAccountUsage(m.usage.accounts) {
			return "Codex use · stale"
		}
		return "Codex use · error"
	}
	full := "Codex weekly use · resets in"
	if lipgloss.Width(full) <= max(1, m.sidebarWidth()-3) {
		return full
	}
	return "Codex use · resets in"

}

func (m tuiModel) renderUsageLines(width int) []string {
	innerWidth := max(1, width-2)
	plain := m.usageLines(innerWidth)
	lines := make([]string, 0, len(plain))
	for index, line := range plain {
		content := fitPlain(line, innerWidth)
		if len(m.usage.accounts) > index {
			account := m.usage.accounts[index]
			switch {
			case account.stale || !account.available && !account.loading:
				content = warningStyle.Render(content)
			case account.available:
				content = infoStyle.Render(content)
			}
		} else if m.usage.err != "" {
			content = warningStyle.Render(content)
		}
		lines = append(lines, " "+content+" ")
	}
	return lines
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
		ctx, cancel := context.WithTimeout(manager.Context(), 2*time.Minute+45*time.Second)
		defer cancel()
		result := actionResultMsg{action: form.action, hostID: form.host.ID, form: &form}
		switch form.action {
		case "add_host":
			result.value, result.err = manager.AddHost(ctx, form.fields[0].value, form.fields[1].value)
		case "add_project":
			result.value, result.err = manager.AddProject(ctx, form.host.ID, form.fields[0].value, form.fields[1].value)
		case "create_workspace":
			workspace, window, err := manager.CreateWorkspaceWithWindow(ctx, form.host.ID, CreateWorkspaceRequest{ProjectID: form.project.ID, ProjectPath: form.project.Path, Name: form.fields[0].value})
			result.value, result.err = createdWorkspace{workspace: workspace, window: window}, err
			if err != nil && workspace.ID != "" {
				result.form = nil
			}
		case "rename_workspace":
			result.targetID = form.workspace.ID
			result.err = manager.RenameWorkspace(ctx, form.host.ID, RenameRequest{ID: form.workspace.ID, Name: form.fields[0].value})
			if result.err == nil {
				result.value = renamedResource{kind: "workspace", name: form.fields[0].value}
			}
		case "rename_window":
			result.targetID = form.window.ID
			result.err = manager.RenameWindow(ctx, form.host.ID, RenameRequest{ID: form.window.ID, Name: form.fields[0].value})
			if result.err == nil {
				result.value = renamedResource{kind: "window", name: form.fields[0].value}
			}
		case "put_file":
			data, extension, err := ReadAttachment(form.fields[0].value)
			if err != nil {
				result.err = err
				break
			}
			result.targetID = form.window.ID
			request := PutAttachmentRequest{WorkspaceID: form.workspace.ID, Extension: extension, Data: data}
			if request.WorkspaceID == "" {
				request.ProjectID = form.project.ID
			}
			result.value, result.err = manager.PutAttachment(ctx, form.host.ID, request)
		case "adopt_tmux_session":
			result.value, result.err = manager.AdoptTmuxSession(ctx, form.host.ID, AdoptTmuxSessionRequest{ProjectID: form.project.ID, ProjectPath: form.project.Path, WorkspaceName: form.fields[0].value, Session: form.session.Name})
		}
		return result
	}
}

func listTmuxSessionsCmd(manager *Manager, host Host, project Project) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 15*time.Second)
		defer cancel()
		value, err := manager.ListTmuxSessions(ctx, host.ID, ListTmuxSessionsRequest{ProjectID: project.ID, ProjectPath: project.Path})
		return actionResultMsg{action: "list_tmux_sessions", hostID: host.ID, value: value, form: &modal{host: host, project: project}, err: err}
	}
}

func adoptTmuxSessionCmd(manager *Manager, host Host, project Project, workspaceName string, session TmuxSessionCandidate) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 30*time.Second)
		defer cancel()
		value, err := manager.AdoptTmuxSession(ctx, host.ID, AdoptTmuxSessionRequest{ProjectID: project.ID, ProjectPath: project.Path, WorkspaceName: workspaceName, Session: session.Name})
		return actionResultMsg{action: "adopt_tmux_session", hostID: host.ID, value: value, err: err}
	}
}

func createWindowCmd(manager *Manager, hostID, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 30*time.Second)
		defer cancel()
		window, err := manager.CreateWindow(ctx, hostID, CreateWindowRequest{WorkspaceID: workspaceID})
		return actionResultMsg{action: "create_window", hostID: hostID, value: window, err: err}
	}
}

func openProjectWindowCmd(manager *Manager, host Host, project Project) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 30*time.Second)
		defer cancel()
		value, err := manager.OpenProjectWindow(ctx, host.ID, OpenProjectWindowRequest{ProjectID: project.ID, ProjectPath: project.Path})
		return actionResultMsg{action: "open_project_window", hostID: host.ID, value: value, err: err}
	}
}

func clipboardAttachmentCmd(manager *Manager, hostID, workspaceID, projectID, windowID string) tea.Cmd {
	return func() tea.Msg {
		captureContext, cancelCapture := context.WithTimeout(manager.Context(), 30*time.Second)
		data, extension, err := CaptureClipboardImage(captureContext)
		cancelCapture()
		result := actionResultMsg{action: "put_clipboard", hostID: hostID, targetID: windowID, err: err}
		if err == nil {
			uploadContext, cancelUpload := context.WithTimeout(manager.Context(), 35*time.Second)
			request := PutAttachmentRequest{WorkspaceID: workspaceID, Extension: extension, Data: data, Image: true}
			if request.WorkspaceID == "" {
				request.ProjectID = projectID
			}
			result.value, result.err = manager.PutAttachment(uploadContext, hostID, request)
			cancelUpload()
		}
		return result
	}
}

func deleteCmd(manager *Manager, confirmation modal) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(manager.Context(), 2*time.Minute)
		defer cancel()
		result := actionResultMsg{action: confirmation.action, hostID: confirmation.host.ID, targetID: confirmation.delete.ID, windowIDs: confirmation.windowIDs}
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
	if right != "" && left != "" {
		rightLimit := max(width/3, width-lipgloss.Width(left)-1)
		right = ansi.Truncate(right, max(1, rightLimit), "…")
	} else {
		right = ansi.Truncate(right, width, "…")
	}
	rightWidth := lipgloss.Width(right)
	leftLimit := width - rightWidth
	if left != "" && right != "" && leftLimit > 0 {
		leftLimit--
	}
	left = ansi.Truncate(left, max(0, leftLimit), "…")
	padding := max(0, width-lipgloss.Width(left)-rightWidth)
	return left + strings.Repeat(" ", padding) + right
}

func joinKeepLeft(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	left = ansi.Truncate(left, width, "")
	leftWidth := lipgloss.Width(left)
	rightLimit := width - leftWidth
	if left != "" && right != "" && rightLimit > 0 {
		rightLimit--
	}
	right = ansi.Truncate(right, max(0, rightLimit), "")
	padding := max(0, width-leftWidth-lipgloss.Width(right))
	return left + strings.Repeat(" ", padding) + right
}

func styledTitledSegment(title string, width int) string {
	if width <= 0 {
		return ""
	}
	title = strings.TrimSpace(plainDisplayText(title))
	if title == "" || width < 4 {
		return frame(strings.Repeat("─", width))
	}
	label := " " + ansi.Truncate(title, max(1, width-3), "…") + " "
	label = ansi.Truncate(label, width, "")
	return frame("─") + accentStyle.Render(label) + frame(strings.Repeat("─", max(0, width-1-lipgloss.Width(label))))
}

func frame(value string) string {
	return borderStyle.Render(value)
}

func fitPlain(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = ansi.Truncate(value, width, "")
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func fitMiddlePlain(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = plainDisplayText(value)
	valueWidth := lipgloss.Width(value)
	if valueWidth <= width {
		return fitPlain(value, width)
	}
	if width == 1 {
		return "…"
	}
	remaining := width - 1
	leftWidth := (remaining + 1) / 2
	rightWidth := remaining - leftWidth
	result := ansi.Cut(value, 0, leftWidth) + "…"
	if rightWidth > 0 {
		result += ansi.Cut(value, valueWidth-rightWidth, valueWidth)
	}
	return fitPlain(result, width)
}

func renderFormField(field formField, marker string, width int) string {
	prefix := marker + plainDisplayText(field.label) + ": "
	valueWidth := max(1, width-lipgloss.Width(prefix))
	value := plainDisplayText(field.value)
	if lipgloss.Width(value) > valueWidth {
		total := lipgloss.Width(value)
		value = "…" + ansi.Cut(value, total-valueWidth+1, total)
	}
	return ansi.Truncate(prefix, max(0, width-valueWidth), "…") + value
}

func plainDisplayText(value string) string {
	return strings.Map(func(r rune) rune {
		if unsafeDisplayRune(r) {
			return -1
		}
		return r
	}, value)
}

func wrappedPlainLines(value string, width int) []string {
	width = max(1, width)
	wrapped := ansi.Wordwrap(plainDisplayText(value), width, " ")
	lines := []string{}
	for _, line := range strings.Split(wrapped, "\n") {
		lines = append(lines, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func confirmButtonRow(current modal, width int) int {
	return 4 + len(wrappedPlainLines(current.reason, width)) + len(wrappedPlainLines(modalWarning(current), width))
}

func modalConfirmLabel(current modal) string {
	if current.confirm != "" {
		return current.confirm
	}
	return deleteButtonLabel
}

func modalWarning(current modal) string {
	if current.warning != "" {
		return current.warning
	}
	return confirmationWarning
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

func padInsetBlock(value string, width, height, left, top int) string {
	left = max(0, min(left, max(0, width-1)))
	top = max(0, min(top, max(0, height-1)))
	prefix := strings.Repeat(" ", left)
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}
	if top > 0 {
		lines = append(make([]string, top), lines...)
	}
	return padBlock(strings.Join(lines, "\n"), width, height)
}
