package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"multicodex/internal/monitor/usage"
)

type FetchFunc func(context.Context) (*usage.Summary, error)

type Options struct {
	Interval        time.Duration
	Timeout         time.Duration
	NoColor         bool
	AltScreen       bool
	DisplayLocation *time.Location
	Fetch           FetchFunc
}

type Model struct {
	interval        time.Duration
	timeout         time.Duration
	fetch           FetchFunc
	displayLocation *time.Location

	width  int
	height int

	now time.Time

	fetching          bool
	lastAttemptAt     time.Time
	lastSuccessAt     time.Time
	lastFetchDuration time.Duration
	lastError         string
	nextFetchAt       time.Time

	summary *usage.Summary
	styles  styles
}

type styles struct {
	title   lipgloss.Style
	dim     lipgloss.Style
	panel   lipgloss.Style
	label   lipgloss.Style
	value   lipgloss.Style
	ok      lipgloss.Style
	warn    lipgloss.Style
	bad     lipgloss.Style
	accent  lipgloss.Style
	error   lipgloss.Style
	help    lipgloss.Style
	mono    lipgloss.Style
	loading lipgloss.Style
}

type pollTickMsg struct {
	at time.Time
}

type clockTickMsg struct {
	at time.Time
}

type fetchResultMsg struct {
	at       time.Time
	duration time.Duration
	summary  *usage.Summary
	err      error
}

const (
	defaultInterval = 60 * time.Second
	defaultTimeout  = 20 * time.Second
)

func NewModel(opts Options) Model {
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	fetch := opts.Fetch
	if fetch == nil {
		fetch = func(context.Context) (*usage.Summary, error) {
			return nil, errors.New("missing fetch function")
		}
	}
	displayLocation := opts.DisplayLocation
	if displayLocation == nil {
		displayLocation = time.Local
	}
	now := time.Now().UTC()

	return Model{
		interval:        interval,
		timeout:         timeout,
		fetch:           fetch,
		displayLocation: displayLocation,
		now:             now,
		fetching:        true,
		nextFetchAt:     now.Add(interval),
		styles:          defaultStyles(opts.NoColor),
	}
}

func defaultStyles(noColor bool) styles {
	basePanel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if noColor {
		return styles{
			title:   lipgloss.NewStyle().Bold(true),
			dim:     lipgloss.NewStyle(),
			panel:   basePanel,
			label:   lipgloss.NewStyle().Bold(true),
			value:   lipgloss.NewStyle(),
			ok:      lipgloss.NewStyle().Bold(true),
			warn:    lipgloss.NewStyle().Bold(true),
			bad:     lipgloss.NewStyle().Bold(true),
			accent:  lipgloss.NewStyle().Bold(true),
			error:   lipgloss.NewStyle().Bold(true),
			help:    lipgloss.NewStyle(),
			mono:    lipgloss.NewStyle(),
			loading: lipgloss.NewStyle(),
		}
	}
	return styles{
		title:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Padding(0, 1),
		dim:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		panel:   basePanel.BorderForeground(lipgloss.Color("61")),
		label:   lipgloss.NewStyle().Foreground(lipgloss.Color("109")),
		value:   lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		ok:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")),
		warn:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")),
		bad:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")),
		accent:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")),
		error:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")),
		help:    lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		mono:    lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		loading: lipgloss.NewStyle().Foreground(lipgloss.Color("117")),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchCmd(m.fetch, m.timeout), pollCmd(m.interval), clockCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+c":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
	case pollTickMsg:
		m.nextFetchAt = v.at.UTC().Add(m.interval)
		cmds := []tea.Cmd{pollCmd(m.interval)}
		if !m.fetching {
			m.fetching = true
			cmds = append(cmds, fetchCmd(m.fetch, m.timeout))
		}
		return m, tea.Batch(cmds...)
	case clockTickMsg:
		m.now = v.at.UTC()
		return m, clockCmd()
	case fetchResultMsg:
		m.fetching = false
		m.lastAttemptAt = v.at.UTC()
		m.lastFetchDuration = v.duration
		if v.err != nil {
			m.lastError = v.err.Error()
			return m, nil
		}
		m.lastError = ""
		m.lastSuccessAt = v.at.UTC()
		m.summary = v.summary
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "initializing..."
	}

	header := m.renderHeader()
	body := m.renderBody()
	exitHint := m.styles.dim.Render("Ctrl+C to exit")

	top := lipgloss.JoinVertical(lipgloss.Left, header, body, "")
	combined := pinFooterToBottom(top, exitHint, m.height)
	return clipToViewport(combined, m.width, m.height)
}

func (m Model) renderHeader() string {
	title := m.styles.title.Render(" multicodex monitor ")

	stateText := "idle"
	stateStyle := m.styles.dim
	if m.fetching {
		stateText = "refreshing"
		stateStyle = m.styles.loading
	} else if m.lastError != "" {
		stateText = "error"
		stateStyle = m.styles.bad
	} else if m.summary != nil {
		stateText = "healthy"
		stateStyle = m.styles.ok
	}

	left := title + "  " + m.styles.label.Render("state: ") + stateStyle.Render(stateText)
	if !m.nextFetchAt.IsZero() {
		refreshText := "[next refresh in " + humanDuration(m.nextFetchAt.Sub(m.now)) + "]"
		left += " " + m.styles.dim.Render(refreshText)
	}
	right := m.styles.dim.Render("local " + m.formatDisplayTimestamp(m.now))
	line1 := joinWithPaddingKeepRight(left, right, m.width)
	return line1
}

func (m Model) renderBody() string {
	if m.summary == nil {
		if m.lastError != "" {
			msg := m.styles.error.Render("last error: " + m.lastError)
			return m.styles.panel.Width(max(20, m.width-4)).Render(msg)
		}
		return m.styles.panel.Width(max(20, m.width-4)).Render(m.styles.loading.Render("loading usage data..."))
	}

	contentWidth := max(20, m.width-4)
	fiveHourTitle := "five-hour window"
	weeklyTitle := "weekly window"
	if accountName := summaryAccountDisplayName(m.summary); accountName != "" {
		fiveHourTitle += " [" + accountName + "]"
		weeklyTitle += " [" + accountName + "]"
	} else if !m.summary.WindowDataAvailable {
		fiveHourTitle += " [unavailable]"
		weeklyTitle += " [unavailable]"
	}

	windowRows := []string{
		m.renderWindowRow(
			contentWidth,
			windowPanelSpec{title: fiveHourTitle, window: m.summary.PrimaryWindow, available: m.summary.WindowDataAvailable},
			windowPanelSpec{title: weeklyTitle, window: m.summary.SecondaryWindow, available: m.summary.WindowDataAvailable},
		),
	}
	for _, account := range m.additionalAccountWindowRows() {
		available := accountWindowAvailable(account)
		windowRows = append(windowRows, m.renderWindowRow(
			contentWidth,
			windowPanelSpec{title: windowPanelTitle("five-hour window", account), window: account.PrimaryWindow, available: available},
			windowPanelSpec{title: windowPanelTitle("weekly window", account), window: account.SecondaryWindow, available: available},
		))
	}
	panelVerticalOverhead := verticalOverhead(m.styles.panel)
	windowRows = fitWindowRowsToViewport(windowRows, m.height, panelVerticalOverhead)
	windowsBlock := lipgloss.JoinVertical(lipgloss.Left, windowRows...)

	metaLines := []string{}
	maxMetaWidth := max(8, contentWidth-4)
	windowsHeight := lipgloss.Height(windowsBlock)
	statusRows := statusRowsForLayout(m.height, windowsHeight, panelVerticalOverhead)
	visibleStatusRows := min(4, statusRows)

	metaLines = append(metaLines, m.renderAccountsLine(maxMetaWidth))
	metaLines = append(metaLines, m.renderObservedHeaderLine("five-hour token estimate", m.summary.ObservedWindow5h, m.summary.ObservedTokens5h))
	metaLines = append(metaLines, m.renderObservedBreakdownLinesFixed(m.summary.ObservedWindow5h, m.summary.ObservedTokens5h)...)
	metaLines = append(metaLines, m.renderObservedHeaderLine("weekly token estimate", m.summary.ObservedWindowWeekly, m.summary.ObservedTokensWeekly))
	metaLines = append(metaLines, m.renderObservedBreakdownLinesFixed(m.summary.ObservedWindowWeekly, m.summary.ObservedTokensWeekly)...)
	metaLines = append(metaLines, m.renderStatusLinesFixed(visibleStatusRows)...)
	for i := 0; i < statusRows-visibleStatusRows; i++ {
		metaLines = append(metaLines, "")
	}
	for i := range metaLines {
		metaLines[i] = ansi.Truncate(metaLines[i], maxMetaWidth, "...")
	}

	metaPanel := m.styles.panel.Width(contentWidth).Render(strings.Join(metaLines, "\n"))
	return lipgloss.JoinVertical(lipgloss.Left, windowsBlock, metaPanel)
}

func (m Model) renderWindowRow(contentWidth int, left, right windowPanelSpec) string {
	leftPanelWidth := contentWidth
	rightPanelWidth := contentWidth
	if contentWidth >= 94 {
		panelOverhead := horizontalOverhead(m.styles.panel)
		panelWidth, spacerWidth := splitEqualPanelContentWidths(contentWidth, panelOverhead)
		spacer := strings.Repeat(" ", spacerWidth)
		leftPanelWidth = panelWidth
		rightPanelWidth = panelWidth
		leftPanel := m.renderWindowPanel(left.title, left.window, leftPanelWidth, left.available)
		rightPanel := m.renderWindowPanel(right.title, right.window, rightPanelWidth, right.available)
		return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, spacer, rightPanel)
	}
	leftPanel := m.renderWindowPanel(left.title, left.window, leftPanelWidth, left.available)
	rightPanel := m.renderWindowPanel(right.title, right.window, rightPanelWidth, right.available)
	return lipgloss.JoinVertical(lipgloss.Left, leftPanel, "", rightPanel)
}

func (m Model) renderWindowPanel(title string, win usage.WindowSummary, maxWidth int, available bool) string {
	if !available {
		lines := []string{
			m.styles.accent.Render(title),
			m.styles.label.Render("used: ") + m.styles.bad.Render("unavailable"),
			m.renderResetLine("unavailable", "unavailable"),
		}
		for i := range lines {
			lines[i] = ansi.Truncate(lines[i], max(4, maxWidth), "...")
		}
		return m.styles.panel.Width(max(20, maxWidth)).Render(strings.Join(lines, "\n"))
	}

	statusStyle := percentStyle(win.UsedPercent, m.styles)

	reset := "unknown"
	if win.ResetsAt != nil {
		reset = m.formatDisplayTimestamp(*win.ResetsAt)
	}
	remaining := "unknown"
	if win.SecondsUntilReset != nil {
		if *win.SecondsUntilReset <= 0 {
			remaining = "resetting"
		} else {
			remaining = humanDuration(time.Duration(*win.SecondsUntilReset) * time.Second)
		}
	}

	lines := []string{
		m.styles.accent.Render(title),
		m.styles.label.Render("used: ") + statusStyle.Render(fmt.Sprintf("%d%%", win.UsedPercent)),
		m.renderResetLine(reset, remaining),
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(4, maxWidth), "...")
	}
	return m.styles.panel.Width(max(20, maxWidth)).Render(strings.Join(lines, "\n"))
}

func (m Model) renderResetLine(reset, remaining string) string {
	return m.styles.label.Render("resets at: ") +
		m.styles.value.Render(reset) +
		m.styles.dim.Render(" ["+remaining+"]")
}

func (m Model) renderAccountsLine(maxWidth int) string {
	detected := m.summary.TotalAccounts
	if detected <= 0 {
		detected = len(m.summary.Accounts)
	}
	identities := summarizeAccountIdentities(m.summary.Accounts)
	value := fmt.Sprintf("%d detected [%s]", detected, strings.Join(identities, ", "))
	line := m.styles.label.Render("accounts: ") + m.styles.value.Render(value)
	return ansi.Truncate(line, maxWidth, "...")
}

func (m Model) renderObservedHeaderLine(windowLabel string, win *usage.ObservedTokenBreakdown, fallbackTotal *int64) string {
	state, style := m.observedHeaderState(win, fallbackTotal)
	return m.styles.label.Render(windowLabel+" ") + style.Render("["+state+"]") + m.styles.label.Render(" (sum across accounts):")
}

func (m Model) observedHeaderState(win *usage.ObservedTokenBreakdown, fallbackTotal *int64) (string, lipgloss.Style) {
	state := "n/a"
	style := m.styles.warn
	observedStatus := strings.ToLower(strings.TrimSpace(m.summary.ObservedTokensStatus))
	warming := m.summary.ObservedTokensWarming
	hasData := win != nil || fallbackTotal != nil
	if m.fetching && !hasData {
		state = "loading"
		style = m.styles.loading
	} else if warming && !hasData {
		state = "loading"
		style = m.styles.loading
	} else if observedStatus == "partial" {
		state = "partial"
		style = m.styles.warn
	} else if observedStatus == "unavailable" && !hasData {
		state = "unavailable"
		style = m.styles.warn
	} else if hasData {
		if m.fetching {
			state = "refreshing"
			style = m.styles.loading
		} else {
			state = "ready"
			style = m.styles.ok
		}
	}
	return state, style
}

func summarizeAccountIdentities(accounts []usage.AccountSummary) []string {
	if len(accounts) == 0 {
		return []string{"none"}
	}
	out := make([]string, 0, len(accounts))
	seen := map[string]struct{}{}
	for _, account := range accounts {
		identityKey := accountIdentityKey(account.AccountEmail, account.AccountID, account.UserID)
		if identityKey == "" {
			if label := strings.TrimSpace(account.Label); label != "" {
				identityKey = "label:" + strings.ToLower(label)
			} else {
				identityKey = "unidentified"
			}
		}
		if _, ok := seen[identityKey]; ok {
			continue
		}
		seen[identityKey] = struct{}{}

		identity := strings.TrimSpace(account.Label)
		if identity == "" {
			if accountID := strings.TrimSpace(account.AccountID); accountID != "" {
				identity = "account_id:" + accountID
			} else if userID := strings.TrimSpace(account.UserID); userID != "" {
				identity = "user_id:" + userID
			} else {
				identity = "unidentified"
			}
		}
		out = append(out, identity)
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

func summaryAccountDisplayName(summary *usage.Summary) string {
	if summary == nil {
		return ""
	}
	return displayNameFromParts(summary.WindowAccountLabel, summary.AccountID, summary.UserID)
}

func (m Model) formatDisplayTimestamp(ts time.Time) string {
	return ts.In(m.displayLocation).Format("2006-01-02 15:04")
}

func accountDisplayName(account usage.AccountSummary) string {
	return displayNameFromParts(account.Label, account.AccountID, account.UserID)
}

func displayNameFromParts(label, accountID, userID string) string {
	if label := strings.TrimSpace(label); label != "" {
		return label
	}
	if accountID := strings.TrimSpace(accountID); accountID != "" {
		return "account_id:" + accountID
	}
	if userID := strings.TrimSpace(userID); userID != "" {
		return "user_id:" + userID
	}
	return ""
}

func (m Model) additionalAccountWindowRows() []usage.AccountSummary {
	if m.summary == nil || len(m.summary.Accounts) <= 1 {
		return nil
	}
	activeIndex := activeAccountIndex(m.summary)
	out := make([]usage.AccountSummary, 0, len(m.summary.Accounts)-1)
	for i, account := range m.summary.Accounts {
		if i == activeIndex {
			continue
		}
		out = append(out, account)
	}
	return out
}

func activeAccountIndex(summary *usage.Summary) int {
	if summary == nil || len(summary.Accounts) == 0 {
		return -1
	}
	activeIdentity := accountIdentityKey(summary.AccountEmail, summary.AccountID, summary.UserID)
	activeLabel := strings.TrimSpace(summary.WindowAccountLabel)
	for i, account := range summary.Accounts {
		if activeIdentity != "" && accountIdentityKey(account.AccountEmail, account.AccountID, account.UserID) == activeIdentity {
			return i
		}
		if activeLabel != "" && strings.TrimSpace(account.Label) == activeLabel {
			return i
		}
	}
	return -1
}

func accountIdentityKey(email, accountID, userID string) string {
	if trimmed := strings.ToLower(strings.TrimSpace(email)); trimmed != "" {
		return "email:" + trimmed
	}
	if trimmed := strings.ToLower(strings.TrimSpace(accountID)); trimmed != "" {
		return "account_id:" + trimmed
	}
	if trimmed := strings.ToLower(strings.TrimSpace(userID)); trimmed != "" {
		return "user_id:" + trimmed
	}
	return ""
}

func accountWindowAvailable(account usage.AccountSummary) bool {
	return strings.TrimSpace(account.Error) == "" && account.FetchedAt != nil
}

func windowPanelTitle(base string, account usage.AccountSummary) string {
	if name := accountDisplayName(account); name != "" {
		return base + " [" + name + "]"
	}
	return base
}

func fitWindowRowsToViewport(rows []string, viewportHeight, panelVerticalOverhead int) []string {
	if len(rows) <= 1 {
		return rows
	}
	bodyTargetHeight := max(1, viewportHeight-3) // header + spacer + exit hint
	minMetaHeight := panelVerticalOverhead + observedMetaBaseLineCount() + 1
	usedHeight := 0
	keep := 0
	for i, row := range rows {
		rowHeight := lipgloss.Height(row)
		if i == 0 {
			usedHeight += rowHeight
			keep = 1
			continue
		}
		if usedHeight+rowHeight+minMetaHeight > bodyTargetHeight {
			break
		}
		usedHeight += rowHeight
		keep++
	}
	return rows[:keep]
}

func (m Model) renderObservedBreakdownLinesFixed(win *usage.ObservedTokenBreakdown, fallbackTotal *int64) []string {
	total := "n/a"
	input := "n/a"
	cachedInput := "n/a"
	output := "n/a"
	reasoningOutput := "n/a"

	if win != nil {
		total = compactCount(win.Total)
		if win.HasSplit {
			input = compactCount(win.Input)
			cachedInput = compactCount(win.CachedInput)
			output = compactCount(win.Output)
			reasoningOutput = compactCount(win.ReasoningOutput)
		}
	} else if fallbackTotal != nil {
		total = compactCount(*fallbackTotal)
	}

	lines := []string{
		m.styles.dim.Render("- total: " + total),
		m.styles.dim.Render("- input: " + input),
		m.styles.dim.Render("- input (cached): " + cachedInput),
		m.styles.dim.Render("- output: " + output),
		m.styles.dim.Render("- output (reasoning): " + reasoningOutput),
	}
	return lines
}

type statusLine struct {
	level string
	name  string
	value string
}

type windowPanelSpec struct {
	title     string
	window    usage.WindowSummary
	available bool
}

func (m Model) renderStatusLinesFixed(rows int) []string {
	if rows < 1 {
		rows = 1
	}
	checks := []statusLine{
		m.activeWindowsStatusLine(),
		m.observedStatusLine("five-hour token estimate", m.summary.ObservedWindow5h, m.summary.ObservedTokens5h),
		m.observedStatusLine("weekly token estimate", m.summary.ObservedWindowWeekly, m.summary.ObservedTokensWeekly),
		m.diagnosticsStatusLine(),
	}

	selected := checks
	if rows < len(checks) {
		if rows == 1 {
			selected = []statusLine{{
				level: "warning",
				name:  "more checks",
				value: fmt.Sprintf("+%d hidden", len(checks)-1),
			}}
		} else {
			selected = append([]statusLine{}, checks[:rows-1]...)
			selected = append(selected, statusLine{
				level: "warning",
				name:  "more checks",
				value: fmt.Sprintf("+%d hidden", len(checks)-(rows-1)),
			})
		}
	}

	out := make([]string, 0, len(selected))
	for _, line := range selected {
		rendered := fmt.Sprintf("%s [%s]: %s", line.level, line.name, line.value)
		switch line.level {
		case "error":
			out = append(out, m.styles.error.Render(rendered))
		case "warning":
			out = append(out, m.styles.warn.Render(rendered))
		default:
			out = append(out, m.styles.ok.Render(rendered))
		}
	}
	return out
}

func (m Model) activeWindowsStatusLine() statusLine {
	if !m.summary.WindowDataAvailable {
		if m.fetching {
			return statusLine{level: "status", name: "active windows", value: "loading"}
		}
		return statusLine{level: "warning", name: "active windows", value: "unavailable"}
	}
	if m.fetching {
		return statusLine{level: "status", name: "active windows", value: "refreshing"}
	}
	return statusLine{level: "status", name: "active windows", value: "ok"}
}

func (m Model) observedStatusLine(name string, win *usage.ObservedTokenBreakdown, fallbackTotal *int64) statusLine {
	state, _ := m.observedHeaderState(win, fallbackTotal)
	if state == "loading" || state == "refreshing" {
		return statusLine{level: "status", name: name, value: state}
	}
	if state == "n/a" || state == "unavailable" {
		return statusLine{level: "warning", name: name, value: "unavailable"}
	}
	if state == "partial" || strings.EqualFold(strings.TrimSpace(m.summary.ObservedTokensStatus), "partial") {
		return statusLine{level: "warning", name: name, value: "partial"}
	}
	return statusLine{level: "status", name: name, value: state}
}

func (m Model) diagnosticsStatusLine() statusLine {
	if trimmed := strings.TrimSpace(m.lastError); trimmed != "" {
		return statusLine{level: "error", name: "source + diagnostics", value: trimmed}
	}

	warnings := dedupeWarnings(m.summary.Warnings)
	if len(warnings) > 0 {
		value := preferredDiagnosticWarning(warnings)
		if len(warnings) > 1 {
			value = fmt.Sprintf("%s (+%d more)", value, len(warnings)-1)
		}
		return statusLine{level: "warning", name: "source + diagnostics", value: value}
	}

	source := strings.TrimSpace(m.summary.Source)
	if source == "" {
		source = "ok"
	}
	return statusLine{level: "status", name: "source + diagnostics", value: source}
}

func dedupeWarnings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, warning := range in {
		trimmed := strings.TrimSpace(warning)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func preferredDiagnosticWarning(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	for _, warning := range warnings {
		if strings.Contains(strings.ToLower(warning), "window cards are unavailable") {
			return warning
		}
	}
	for _, warning := range warnings {
		if strings.Contains(strings.ToLower(warning), "active account") {
			return warning
		}
	}
	return warnings[0]
}

func statusRowsForLayout(viewportHeight, windowsBlockHeight, panelVerticalOverhead int) int {
	bodyTargetHeight := max(1, viewportHeight-3) // header + spacer + exit hint
	metaTargetHeight := bodyTargetHeight - windowsBlockHeight
	if metaTargetHeight < panelVerticalOverhead+1 {
		metaTargetHeight = panelVerticalOverhead + 1
	}
	rows := metaTargetHeight - panelVerticalOverhead - observedMetaBaseLineCount()
	if rows < 1 {
		return 1
	}
	return rows
}

func observedMetaBaseLineCount() int {
	// accounts line + two observed headers + two fixed 5-line breakdown blocks.
	return 1 + 1 + 5 + 1 + 5
}

func percentStyle(percent int, styles styles) lipgloss.Style {
	switch {
	case percent >= 90:
		return styles.bad
	case percent >= 70:
		return styles.warn
	default:
		return styles.ok
	}
}

func compactCount(v int64) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	if v < 1000 {
		return fmt.Sprintf("%s%d", sign, v)
	}
	units := []string{"", "k", "m", "b", "t"}
	value := float64(v)
	unitIndex := 0
	for value >= 1000 && unitIndex < len(units)-1 {
		value /= 1000
		unitIndex++
	}
	decimals := 0
	switch {
	case value >= 100:
		decimals = 0
	case value >= 10:
		decimals = 1
	default:
		decimals = 2
	}
	formatted := fmt.Sprintf("%.*f", decimals, value)
	if decimals > 0 {
		formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	}
	return fmt.Sprintf("%s%s%s", sign, formatted, units[unitIndex])
}

func pollCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return pollTickMsg{at: t}
	})
}

func clockCmd() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return clockTickMsg{at: t}
	})
}

func fetchCmd(fetch FetchFunc, timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		summary, err := fetch(ctx)
		return fetchResultMsg{
			at:       time.Now(),
			duration: time.Since(start),
			summary:  summary,
			err:      err,
		}
	}
}

func Run(opts Options) error {
	model := NewModel(opts)
	progOpts := []tea.ProgramOption{}
	if opts.AltScreen {
		progOpts = append(progOpts, tea.WithAltScreen())
	}
	prog := tea.NewProgram(model, progOpts...)
	_, err := prog.Run()
	return err
}

func joinWithPaddingKeepRight(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	rightWidth := lipgloss.Width(right)
	if rightWidth >= width {
		return truncateRunes(right, width)
	}
	maxLeftWidth := width - rightWidth - 1
	if maxLeftWidth < 0 {
		maxLeftWidth = 0
	}
	left = truncateRunes(left, maxLeftWidth)
	leftWidth := lipgloss.Width(left)
	padding := width - leftWidth - rightWidth
	if padding < 1 {
		padding = 1
	}
	return left + strings.Repeat(" ", padding) + right
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	return ansi.Truncate(s, maxRunes, "")
}

func clipToViewport(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = truncateRunes(lines[i], width)
		pad := width - lipgloss.Width(lines[i])
		if pad > 0 {
			lines[i] += strings.Repeat(" ", pad)
		}
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func pinFooterToBottom(top, footer string, height int) string {
	if height <= 0 {
		return ""
	}
	footerLines := []string{}
	if footer != "" {
		footerLines = strings.Split(footer, "\n")
	}
	topLines := []string{}
	if top != "" {
		topLines = strings.Split(top, "\n")
	}

	maxTopLines := height - len(footerLines)
	if maxTopLines < 0 {
		maxTopLines = 0
	}
	if len(topLines) > maxTopLines {
		topLines = topLines[:maxTopLines]
	}
	for len(topLines) < maxTopLines {
		topLines = append(topLines, "")
	}

	all := append(topLines, footerLines...)
	if len(all) == 0 {
		return ""
	}
	return strings.Join(all, "\n")
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return d.String()
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func splitEqualPanelContentWidths(contentWidth, panelOverhead int) (panelWidth int, spacerWidth int) {
	if contentWidth <= 0 {
		return 0, 0
	}
	// Keep panel content widths equal while ensuring:
	// 2*(panel content + panel overhead) + spacer == bottom panel outer width.
	usable := contentWidth - panelOverhead
	if usable < 3 {
		return 1, 1
	}
	if usable%2 == 0 {
		spacerWidth = 2
	} else {
		spacerWidth = 1
	}
	panelWidth = (usable - spacerWidth) / 2
	if panelWidth < 1 {
		panelWidth = 1
	}
	return panelWidth, spacerWidth
}

func horizontalOverhead(style lipgloss.Style) int {
	// Probe with a stable non-trivial width to avoid edge-case minimum sizing.
	const probeWidth = 40
	overhead := lipgloss.Width(style.Width(probeWidth).Render("")) - probeWidth
	if overhead < 0 {
		return 0
	}
	return overhead
}

func verticalOverhead(style lipgloss.Style) int {
	// Probe with a stable non-trivial height to avoid edge-case minimum sizing.
	const probeHeight = 20
	overhead := lipgloss.Height(style.Height(probeHeight).Render("")) - probeHeight
	if overhead < 0 {
		return 0
	}
	return overhead
}
